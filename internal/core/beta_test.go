package core

import (
	"math"
	"testing"
)

func almostEqual(t *testing.T, got, want, tol float64, msg string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s: got %v, want %v (±%v)", msg, got, want, tol)
	}
}

func TestDecayFactorIsOneWhenNotAdvancing(t *testing.T) {
	almostEqual(t, decayFactor(100, 100), 1, 1e-12, "to==from")
	almostEqual(t, decayFactor(200, 100), 1, 1e-12, "to<from")
}

func TestDecayFactorHalvesEveryHalfLife(t *testing.T) {
	almostEqual(t, decayFactor(0, HalfLifeMs), 0.5, 1e-12, "one half-life")
	almostEqual(t, decayFactor(0, 2*HalfLifeMs), 0.25, 1e-12, "two half-lives")
}

func TestPosteriorAtLeavesCountsWhenNowEqualsLastUpdate(t *testing.T) {
	a, b := PosteriorAt(11, 4, 500, 500)
	almostEqual(t, a, 11, 1e-12, "alpha")
	almostEqual(t, b, 4, 1e-12, "beta")
}

func TestPosteriorAtShrinksTowardPrior(t *testing.T) {
	a, b := PosteriorAt(11, 1, 0, HalfLifeMs)
	almostEqual(t, a, PriorAlpha+(11-PriorAlpha)*0.5, 1e-12, "alpha halved toward prior")
	almostEqual(t, b, PriorBeta+(1-PriorBeta)*0.5, 1e-12, "beta halved toward prior")
}

func TestPosteriorAtApproachesPriorAtInfinity(t *testing.T) {
	a, b := PosteriorAt(100, 50, 0, 100*HalfLifeMs)
	almostEqual(t, a, PriorAlpha, 1e-9, "alpha -> prior")
	almostEqual(t, b, PriorBeta, 1e-9, "beta -> prior")
}

func TestObserveDecaysThenFoldsWeightedOutcome(t *testing.T) {
	c := &Connection{Alpha: 11, Beta: 1, LastUpdate: 0}
	c.Observe(0, HalfLifeMs)
	almostEqual(t, c.Alpha, 6, 1e-12, "alpha after decay+y=0")
	almostEqual(t, c.Beta, 2, 1e-12, "beta after decay+(1-y)")
	if c.LastUpdate != HalfLifeMs {
		t.Errorf("LastUpdate: got %d, want %d", c.LastUpdate, HalfLifeMs)
	}
}

func TestObserveWithoutDecayAddsOutcomeDirectly(t *testing.T) {
	c := &Connection{Alpha: 1, Beta: 1, LastUpdate: 0}
	c.Observe(0.7, 0)
	almostEqual(t, c.Alpha, 1.7, 1e-12, "alpha += y")
	almostEqual(t, c.Beta, 1.3, 1e-12, "beta += 1-y")
}

func TestObserveKeepsLastUpdateMonotonicUnderReordering(t *testing.T) {
	c := &Connection{Alpha: 1, Beta: 1, LastUpdate: 1000}
	c.Observe(1, 500)
	if c.LastUpdate != 1000 {
		t.Errorf("LastUpdate must not rewind: got %d, want 1000", c.LastUpdate)
	}
	almostEqual(t, c.Alpha, 2, 1e-12, "alpha += y with no decay on out-of-order ts")
	almostEqual(t, c.Beta, 1, 1e-12, "beta += 1-y with no decay on out-of-order ts")
}

func TestMeanIsDecayedPosteriorMean(t *testing.T) {
	c := &Connection{Alpha: 2, Beta: 1, LastUpdate: 0}
	almostEqual(t, c.Mean(0), 2.0/3.0, 1e-12, "mean")
}

func TestEvidenceIsDecayedExcessPseudocount(t *testing.T) {
	c := &Connection{Alpha: 11, Beta: 1, LastUpdate: 0}
	almostEqual(t, c.Evidence(0), 10, 1e-12, "evidence at anchor")
	almostEqual(t, c.Evidence(HalfLifeMs), 5, 1e-12, "evidence after one half-life")
}

func TestBernoulliEntropyPeaksAtHalf(t *testing.T) {
	almostEqual(t, BernoulliEntropy(0.5), math.Ln2, 1e-12, "H(0.5)=ln2")
	almostEqual(t, BernoulliEntropy(0.9), BernoulliEntropy(0.1), 1e-12, "symmetry")
}

func TestExcessSurprisalIsZeroAtHalfRegardlessOfOutcome(t *testing.T) {
	almostEqual(t, ExcessSurprisal(0.5, 0), 0, 1e-12, "p=0.5, y=0")
	almostEqual(t, ExcessSurprisal(0.5, 1), 0, 1e-12, "p=0.5, y=1")
}

func TestExcessSurprisalRisesOnConfidentMiss(t *testing.T) {
	if got := ExcessSurprisal(0.9, 0); got <= 0 {
		t.Errorf("confident miss should be positive, got %v", got)
	}
}

func TestExcessSurprisalSinksOnExpectedOutcome(t *testing.T) {
	if got := ExcessSurprisal(0.9, 1); got >= 0 {
		t.Errorf("expected outcome should be negative, got %v", got)
	}
}

func TestLnBFPositiveWhenAttributeTrulyPartitions(t *testing.T) {
	if got := LnBF(5, 5, 0, 5); got <= 0 {
		t.Errorf("clean partition should be positive, got %v", got)
	}
}

func TestLnBFNegativeWhenPartitionIsIrrelevant(t *testing.T) {
	if got := LnBF(3, 6, 3, 6); got >= 0 {
		t.Errorf("same rate on both sides should be negative, got %v", got)
	}
}

func TestLnBFAcceptsFractionalCounts(t *testing.T) {
	got := LnBF(2.5, 5.0, 0.5, 5.0)
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("fractional counts should yield a finite value, got %v", got)
	}
}
