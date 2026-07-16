package core

// Beta-distribution machinery for the Decision Engine (ADR-0012): the
// pessimistic-quantile capability gate needs the inverse CDF, Thompson
// Sampling needs seeded draws. Both are pure — same inputs (and seed) give
// the same numbers, so decisions replay (ADR-0011 / ADR-0012 Decision 5).

import (
	"math"
	"math/rand"
)

// RegIncBeta is the regularized incomplete beta function I_x(a,b) — the
// Beta(a,b) CDF at x. Continued-fraction evaluation (Lentz), accurate to
// ~1e-12 for the posterior masses this engine sees.
func RegIncBeta(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	front := math.Exp(a*math.Log(x) + b*math.Log(1-x) - lnBeta(a, b))
	// <= (not <): at x exactly on the crossover with a==b the mirror call
	// lands back here and would recurse forever.
	if x <= (a+1)/(a+b+2) {
		return front * betaCF(x, a, b) / a
	}
	return 1 - RegIncBeta(1-x, b, a)
}

// betaCF is the continued fraction for RegIncBeta (modified Lentz method).
func betaCF(x, a, b float64) float64 {
	const (
		maxIter = 300
		eps     = 1e-14
		tiny    = 1e-300
	)
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		m2 := float64(2 * m)
		aa := float64(m) * (b - float64(m)) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		aa = -(a + float64(m)) * (qab + float64(m)) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// BetaQuantile inverts the Beta(a,b) CDF at probability q by bisection —
// slower than Newton but monotone, branchless and immune to the flat tails
// a decayed posterior can develop. 80 halvings pin the result far below any
// gate's tolerance.
func BetaQuantile(a, b, q float64) float64 {
	if q <= 0 {
		return 0
	}
	if q >= 1 {
		return 1
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 80; i++ {
		mid := (lo + hi) / 2
		if RegIncBeta(mid, a, b) < q {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// SampleBeta draws one Beta(a,b) variate from rng via two Gamma draws.
func SampleBeta(rng *rand.Rand, a, b float64) float64 {
	x := sampleGamma(rng, a)
	y := sampleGamma(rng, b)
	if x+y == 0 {
		return 0.5 // both shapes tiny and both draws underflowed — call it even
	}
	return x / (x + y)
}

// sampleGamma draws Gamma(shape, 1) via Marsaglia–Tsang, boosting shape<1
// with the U^(1/shape) trick. (1 - Float64()) keeps U in (0,1] so the boost
// never sees log/pow of zero.
func sampleGamma(rng *rand.Rand, shape float64) float64 {
	if shape < 1 {
		u := 1 - rng.Float64()
		return sampleGamma(rng, shape+1) * math.Pow(u, 1/shape)
	}
	d := shape - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := rng.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := 1 - rng.Float64()
		if u < 1-0.0331*x*x*x*x {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
			return d * v
		}
	}
}

// QuantileAt is the pessimistic quantile of the connection's decayed
// posterior — the capability gate's statistic (ADR-0012 Decision 2).
func (c *Connection) QuantileAt(nowMs int64, q float64) float64 {
	a, b := c.PosteriorAt(nowMs)
	return BetaQuantile(a, b, q)
}

// SampleAt draws once from the connection's decayed posterior — the
// Thompson Sampling primitive (ADR-0012 Decision 1).
func (c *Connection) SampleAt(rng *rand.Rand, nowMs int64) float64 {
	a, b := c.PosteriorAt(nowMs)
	return SampleBeta(rng, a, b)
}
