package face

// Stage ladder tests (ADR-0017): quantity gates for infancy, calibration for
// S3, calibration+sharpness for S4, preference for S5 — and the regression
// story: a Tomo that starts missing walks back down.

import (
	"testing"

	"github.com/Rererr/tomobit/internal/core"
)

const now = int64(1_800_000_000_000)

// stageRepo is a minimal read-only core.Repo for stage derivation.
type stageRepo struct {
	conns  []*core.Connection
	ledger map[string][]*core.LedgerEntry // key: kind|scope|target
	exps   []*core.Experience
}

func ledgerKey(kind, scope, target string) string { return kind + "\x00" + scope + "\x00" + target }

func (r *stageRepo) AllConnections() ([]*core.Connection, error) { return r.conns, nil }
func (r *stageRepo) LedgerFor(kind, scopeKey, target string) ([]*core.LedgerEntry, error) {
	return r.ledger[ledgerKey(kind, scopeKey, target)], nil
}
func (r *stageRepo) CurrentExperiences() ([]*core.Experience, error) { return r.exps, nil }

func (r *stageRepo) GetConnection(kind, scopeKey, target string) (*core.Connection, error) {
	return nil, nil
}
func (r *stageRepo) UpsertConnection(*core.Connection) error           { return nil }
func (r *stageRepo) DeleteConnection(kind, scope, target string) error { return nil }
func (r *stageRepo) ConnectionsFor(kind, target string) ([]*core.Connection, error) {
	return nil, nil
}
func (r *stageRepo) InsertLedger(*core.LedgerEntry) error             { return nil }
func (r *stageRepo) DeleteLedgerFor(kind, scope, target string) error { return nil }
func (r *stageRepo) ClearProjections() error                          { return nil }

func capConn(scopeKey, target string, alpha, beta float64) *core.Connection {
	return &core.Connection{
		Kind: core.ConnCapability, ScopeKey: scopeKey, Target: target,
		Alpha: alpha, Beta: beta, LastUpdate: now, BornTS: now,
	}
}

func prefConn(scopeKey, target string, alpha, beta float64) *core.Connection {
	return &core.Connection{
		Kind: core.ConnPreference, ScopeKey: scopeKey, Target: target,
		Alpha: alpha, Beta: beta, LastUpdate: now, BornTS: now,
	}
}

// calibratedLedger returns n entries of zero excess surprisal — the ledger
// of a forecaster whose predictions match the world.
func calibratedLedger(n int) []*core.LedgerEntry {
	out := make([]*core.LedgerEntry, n)
	for i := range out {
		out[i] = &core.LedgerEntry{TS: now, SExcess: 0}
	}
	return out
}

// frequentIsland returns enough fresh execution experiences to clear
// islandMinFreq for the cap=impl scope.
func frequentIsland(n int) []*core.Experience {
	out := make([]*core.Experience, n)
	for i := range out {
		out[i] = &core.Experience{
			ID: string(rune('a' + i)), TS: now, Kind: core.KindExecution,
			Context: map[string]string{"cap": "impl"}, Provider: "claude",
		}
	}
	return out
}

// sharpBase is a repo that clears S4: one strong provider (no rival, so the
// lottery never wobbles), calibrated ledger, one frequent island.
func sharpBase() *stageRepo {
	c := capConn("cap=impl", "claude", 11, 1)
	return &stageRepo{
		conns: []*core.Connection{c},
		ledger: map[string][]*core.LedgerEntry{
			ledgerKey(c.Kind, c.ScopeKey, c.Target): calibratedLedger(10),
		},
		exps: frequentIsland(4),
	}
}

func stageOf(t *testing.T, r *stageRepo) int {
	t.Helper()
	got, err := StageFrom(r, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStageLadder(t *testing.T) {
	t.Run("S0 empty network", func(t *testing.T) {
		if got := stageOf(t, &stageRepo{}); got != StageEgg {
			t.Errorf("got %s, want たまご", StageName(got))
		}
	})

	t.Run("S1 below the S2 evidence gate", func(t *testing.T) {
		r := &stageRepo{conns: []*core.Connection{capConn("cap=impl", "claude", 2, 1)}}
		if got := stageOf(t, r); got != StageChick {
			t.Errorf("got %s, want ひよこ", StageName(got))
		}
	})

	t.Run("S2 with no ledger data — calibration undefined", func(t *testing.T) {
		r := &stageRepo{conns: []*core.Connection{capConn("cap=impl", "claude", 4, 1)}}
		if got := stageOf(t, r); got != StageChild {
			t.Errorf("got %s, want こども", StageName(got))
		}
	})

	t.Run("S2 when miscalibrated — 肥満は成長に見えない", func(t *testing.T) {
		r := sharpBase()
		key := ledgerKey(core.ConnCapability, "cap=impl", "claude")
		for i := 0; i < 10; i++ { // confident misses swamp the mean
			r.ledger[key] = append(r.ledger[key], &core.LedgerEntry{TS: now, SExcess: 1.9})
		}
		if got := stageOf(t, r); got != StageChild {
			t.Errorf("got %s, want こども", StageName(got))
		}
	})

	t.Run("S3 calibrated but no frequent island", func(t *testing.T) {
		r := sharpBase()
		r.exps = frequentIsland(1) // freq 1 < islandMinFreq
		if got := stageOf(t, r); got != StageYouth {
			t.Errorf("got %s, want わかもの", StageName(got))
		}
	})

	t.Run("S3 when the frequent island's judgment wobbles", func(t *testing.T) {
		r := sharpBase()
		// A rival with equal footing and no preference data: the lottery is
		// a coin — sharpness fails even though calibration holds.
		r.conns = append(r.conns, capConn("cap=impl", "codex", 11, 1))
		if got := stageOf(t, r); got != StageYouth {
			t.Errorf("got %s, want わかもの", StageName(got))
		}
	})

	t.Run("S4 calibrated and sharp", func(t *testing.T) {
		if got := stageOf(t, sharpBase()); got != StageAdult {
			t.Errorf("got %s, want おとな", StageName(got))
		}
	})

	t.Run("S4 sharp even with a rival once preference is settled", func(t *testing.T) {
		r := sharpBase()
		r.conns = append(r.conns,
			capConn("cap=impl", "codex", 11, 1),
			prefConn("cap=impl", "claude~codex", 30, 1))
		// Settled preference: the same rival no longer makes the lottery
		// wobble — and its evidence ≥ 1 lifts the ladder to S5.
		if got := stageOf(t, r); got != StagePartner {
			t.Errorf("got %s, want あいぼう", StageName(got))
		}
	})

	t.Run("S5 needs preference evidence at least 1", func(t *testing.T) {
		r := sharpBase()
		r.conns = append(r.conns, prefConn("cap=impl", "claude~codex", 1.5, 1.4))
		if got := stageOf(t, r); got != StageAdult {
			t.Errorf("got %s, want おとな (pref evidence 0.9 < 1)", StageName(got))
		}
	})

	t.Run("regression: fresh misses walk an adult back down", func(t *testing.T) {
		r := sharpBase()
		if got := stageOf(t, r); got != StageAdult {
			t.Fatalf("precondition: want おとな, got %s", StageName(got))
		}
		key := ledgerKey(core.ConnCapability, "cap=impl", "claude")
		for i := 0; i < 10; i++ {
			r.ledger[key] = append(r.ledger[key], &core.LedgerEntry{TS: now, SExcess: 1.9})
		}
		if got := stageOf(t, r); got != StageChild {
			t.Errorf("世界が変わって学び直している should show: got %s, want こども", StageName(got))
		}
	})
}

func TestStageNameOutOfRange(t *testing.T) {
	if got := StageName(-1); got != "" {
		t.Errorf("StageName(-1) = %q, want \"\"", got)
	}
	if got := StageName(StagePartner + 1); got != "" {
		t.Errorf("StageName(out of range) = %q, want \"\"", got)
	}
}
