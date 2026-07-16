package core

import "math"

// Knobs (CONNECTION_ENGINE.md Open Questions — tuned via dogfood).
const (
	PriorAlpha = 1.0
	PriorBeta  = 1.0

	// HalfLifeMs: evidence halves toward the prior every 90 days (ADR-0002).
	HalfLifeMs = 90 * 24 * 3600 * 1000

	// ThetaTrigger: cumulative excess surprisal (nats) that marks a
	// connection Questioned and summons the judgment.
	ThetaTrigger = 2.0
	// ThetaSplit / ThetaMerge: hysteresis band on the corrected ln BF
	// (ADR-0002: Schmitt trigger — formation needs strong evidence,
	// persistence is cheap).
	ThetaSplit = 3.0
	ThetaMerge = 0.0

	// InheritM0: the fixed mass m₀ a split child's inherited prior carries
	// (ADR-0013 Decision 1 — 「新入りの謙虚さ」の量). The parent may be a
	// thousand-battle veteran; the child is born an m₀-baby whose first
	// impression is the parent's opinion.
	InheritM0 = 2.0
)

// DecayFactor returns 2^(-Δt/halflife); 1 when to <= from.
func DecayFactor(fromMs, toMs int64) float64 {
	if toMs <= fromMs {
		return 1
	}
	return math.Exp2(-float64(toMs-fromMs) / float64(HalfLifeMs))
}

// PosteriorAt applies lazy decay: pseudo-counts shrink toward the
// connection's own prior (ADR-0013: the sink is the family memory, not a
// fixed Beta(1,1)). The stored (alpha, beta) are raw values anchored at
// lastUpdate.
func (c *Connection) PosteriorAt(nowMs int64) (a, b float64) {
	pa, pb := c.Prior()
	f := DecayFactor(c.LastUpdate, nowMs)
	return pa + (c.Alpha-pa)*f, pb + (c.Beta-pb)*f
}

// Mean of the decayed posterior at nowMs.
func (c *Connection) Mean(nowMs int64) float64 {
	a, b := c.PosteriorAt(nowMs)
	return a / (a + b)
}

// Evidence is the decayed pseudo-count in excess of the connection's prior.
func (c *Connection) Evidence(nowMs int64) float64 {
	pa, pb := c.Prior()
	a, b := c.PosteriorAt(nowMs)
	return a + b - pa - pb
}

// Observe folds one weighted outcome into the posterior: decay to ts,
// then α += y, β += 1−y.
func (c *Connection) Observe(y float64, tsMs int64) {
	a, b := c.PosteriorAt(tsMs)
	c.Alpha = a + y
	c.Beta = b + (1 - y)
	// LastUpdate is the decay anchor and must stay monotonic: an out-of-order
	// observation (tsMs < LastUpdate) already adds undecayed via PosteriorAt,
	// but moving the anchor backward would re-decay the whole posterior on the
	// next in-order Observe. Never rewind it.
	if tsMs > c.LastUpdate {
		c.LastUpdate = tsMs
	}
}

// BernoulliEntropy H(p) in nats. p is strictly inside (0,1) because the
// prior keeps pseudo-counts positive.
func BernoulliEntropy(p float64) float64 {
	return -p*math.Log(p) - (1-p)*math.Log(1-p)
}

// ExcessSurprisal = cross-entropy of the (possibly weighted) outcome
// minus the predictive entropy: 驚き − 迷い (ADR-0002). Zero-mean under
// calibration; sinks on expected outcomes, rises on confident misses.
func ExcessSurprisal(p, y float64) float64 {
	s := -(y*math.Log(p) + (1-y)*math.Log(1-p))
	return s - BernoulliEntropy(p)
}

// lnBeta = ln B(a,b).
func lnBeta(a, b float64) float64 {
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	return la + lb - lab
}

// lnMarginal is the Beta-Binomial log marginal likelihood of k weighted
// successes in n weighted trials under the prior.
func lnMarginal(k, n float64) float64 {
	return lnBeta(PriorAlpha+k, PriorBeta+(n-k)) - lnBeta(PriorAlpha, PriorBeta)
}

// LnBF is the log Bayes factor of H1 (the attribute partitions the world)
// against H0 (one p for all) — ADR-0002. Accepts fractional (decayed)
// counts.
func LnBF(kWith, nWith, kWithout, nWithout float64) float64 {
	return lnMarginal(kWith, nWith) + lnMarginal(kWithout, nWithout) -
		lnMarginal(kWith+kWithout, nWith+nWithout)
}
