package core

import (
	"math"
	"math/rand"
	"testing"
)

func TestRegIncBetaUniformIsIdentity(t *testing.T) {
	for _, x := range []float64{0.1, 0.2, 0.5, 0.9} {
		almostEqual(t, RegIncBeta(x, 1, 1), x, 1e-10, "I_x(1,1)")
	}
}

func TestRegIncBetaSymmetry(t *testing.T) {
	// I_x(a,b) = 1 - I_{1-x}(b,a)
	for _, tc := range []struct{ x, a, b float64 }{
		{0.3, 2, 5}, {0.7, 0.5, 0.5}, {0.42, 11, 4},
	} {
		almostEqual(t, RegIncBeta(tc.x, tc.a, tc.b),
			1-RegIncBeta(1-tc.x, tc.b, tc.a), 1e-10, "symmetry")
	}
}

func TestBetaQuantileInvertsCDF(t *testing.T) {
	for _, tc := range []struct{ a, b, q float64 }{
		{1, 1, 0.2}, {2, 1, 0.2}, {1, 2, 0.2}, {5, 3, 0.5}, {0.4, 1.6, 0.1},
	} {
		x := BetaQuantile(tc.a, tc.b, tc.q)
		almostEqual(t, RegIncBeta(x, tc.a, tc.b), tc.q, 1e-9, "CDF(quantile)=q")
	}
}

// TestBetaQuantileBlankSlateSitsOnTheBar: for the uniform prior the
// q-quantile is exactly q — the fact the capability gate's self-referential
// bar leans on (ADR-0012: a provider nobody knows passes exactly at the bar).
func TestBetaQuantileBlankSlateSitsOnTheBar(t *testing.T) {
	almostEqual(t, BetaQuantile(1, 1, 0.2), 0.2, 1e-9, "uniform q20")
	// One failure closes the gate…
	if q := BetaQuantile(1, 2, 0.2); q >= 0.2 {
		t.Errorf("one failure should drop the pessimistic quantile below the bar, got %v", q)
	}
	// …one success clears it comfortably.
	if q := BetaQuantile(2, 1, 0.2); q <= 0.2 {
		t.Errorf("one success should lift the pessimistic quantile above the bar, got %v", q)
	}
}

func TestSampleBetaDeterministicPerSeed(t *testing.T) {
	a := SampleBeta(rand.New(rand.NewSource(42)), 3, 2)
	b := SampleBeta(rand.New(rand.NewSource(42)), 3, 2)
	if a != b {
		t.Errorf("same seed must give the same draw: %v vs %v", a, b)
	}
	c := SampleBeta(rand.New(rand.NewSource(43)), 3, 2)
	if a == c {
		t.Errorf("different seeds should (essentially always) differ")
	}
}

func TestSampleBetaMomentsRoughlyMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 20000
	for _, tc := range []struct{ a, b float64 }{{2, 5}, {0.5, 0.5}, {8, 2}} {
		sum := 0.0
		for i := 0; i < n; i++ {
			x := SampleBeta(rng, tc.a, tc.b)
			if x < 0 || x > 1 {
				t.Fatalf("sample out of range: %v", x)
			}
			sum += x
		}
		mean := tc.a / (tc.a + tc.b)
		almostEqual(t, sum/n, mean, 0.02, "sample mean near a/(a+b)")
	}
}

// TestQuantileAtUsesInheritedPrior: a fully decayed child is judged by its
// family memory (ADR-0013 × ADR-0012 Decision 3: decay alone re-opens or
// keeps shut the gate, depending on the inherited μ).
func TestQuantileAtUsesInheritedPrior(t *testing.T) {
	good := &Connection{Alpha: 1.6, Beta: 0.4, PriorA: 1.6, PriorB: 0.4, LastUpdate: 0}
	bad := &Connection{Alpha: 0.4, Beta: 1.6, PriorA: 0.4, PriorB: 1.6, LastUpdate: 0}
	far := int64(100 * HalfLifeMs)
	if q := good.QuantileAt(far, 0.2); q <= 0.2 {
		t.Errorf("good family memory should clear the bar, got %v", q)
	}
	if q := bad.QuantileAt(far, 0.2); q >= 0.2 {
		t.Errorf("bad family memory should stay below the bar, got %v", q)
	}
	if math.Abs(good.QuantileAt(far, 0.2)-BetaQuantile(1.6, 0.4, 0.2)) > 1e-9 {
		t.Error("fully decayed quantile should equal the prior's quantile")
	}
}
