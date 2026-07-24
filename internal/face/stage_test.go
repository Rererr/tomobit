package face

// Stage ladder tests (ADR-0017, revised by ADR-0045): quantity gates for
// infancy, calibration-with-a-sample for S3, sharpness-under-contest for S4,
// human-pair preference for S5 — and the regression story: a Tomo that
// starts missing walks back down.

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
// of a forecaster whose predictions match the world. Each entry is fresh
// (TS = now), so its decayed mass is exactly n.
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

// soloBase is a repo that cleared the pre-ADR-0045 S4: one strong provider,
// calibrated ledger, one frequent island — but no rival, so the lottery is a
// one-candidate election.
func soloBase() *stageRepo {
	c := capConn("cap=impl", "claude", 11, 1)
	return &stageRepo{
		conns: []*core.Connection{c},
		ledger: map[string][]*core.LedgerEntry{
			ledgerKey(c.Kind, c.ScopeKey, c.Target): calibratedLedger(10),
		},
		exps: frequentIsland(4),
	}
}

// sharpBase is a repo that clears S4 under ADR-0045: two rivals on one
// frequent island with their pairwise preference settled — a real contest
// the lottery still barely wobbles on — plus a calibrated ledger.
func sharpBase() *stageRepo {
	r := soloBase()
	r.conns = append(r.conns,
		capConn("cap=impl", "codex", 11, 1),
		prefConn("cap=impl", "claude~codex", 30, 1))
	return r
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
			t.Errorf("got %s, want 毛玉", StageName(got))
		}
	})

	t.Run("S1 below the S2 evidence gate", func(t *testing.T) {
		r := &stageRepo{conns: []*core.Connection{capConn("cap=impl", "claude", 2, 1)}}
		if got := stageOf(t, r); got != StageChick {
			t.Errorf("got %s, want あかちゃん", StageName(got))
		}
	})

	t.Run("S2 with no ledger data — calibration undefined", func(t *testing.T) {
		r := &stageRepo{conns: []*core.Connection{capConn("cap=impl", "claude", 4, 1)}}
		if got := stageOf(t, r); got != StageChild {
			t.Errorf("got %s, want こども", StageName(got))
		}
	})

	t.Run("S2 while the calibrated sample is below ThetaCalMin", func(t *testing.T) {
		r := sharpBase()
		key := ledgerKey(core.ConnCapability, "cap=impl", "claude")
		r.ledger[key] = calibratedLedger(int(ThetaCalMin) - 1)
		if got := stageOf(t, r); got != StageChild {
			t.Errorf("got %s, want こども (sumW %d < ThetaCalMin %v)", StageName(got), int(ThetaCalMin)-1, ThetaCalMin)
		}
	})

	t.Run("S3+ once the calibrated sample reaches ThetaCalMin", func(t *testing.T) {
		r := sharpBase()
		key := ledgerKey(core.ConnCapability, "cap=impl", "claude")
		r.ledger[key] = calibratedLedger(int(ThetaCalMin))
		if got := stageOf(t, r); got < StageYouth {
			t.Errorf("got %s, want わかもの以上 (sumW %v == ThetaCalMin)", StageName(got), ThetaCalMin)
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

	t.Run("S3 when every frequent island has a single candidate — 一人選挙は証明にならない", func(t *testing.T) {
		if got := stageOf(t, soloBase()); got != StageYouth {
			t.Errorf("got %s, want わかもの", StageName(got))
		}
	})

	t.Run("S3 when the contested frequent island's judgment wobbles", func(t *testing.T) {
		r := soloBase()
		// A rival with equal footing and no preference data: the lottery is
		// a coin — sharpness fails even though calibration holds.
		r.conns = append(r.conns, capConn("cap=impl", "codex", 11, 1))
		if got := stageOf(t, r); got != StageYouth {
			t.Errorf("got %s, want わかもの", StageName(got))
		}
	})

	t.Run("S4 calibrated and sharp on a contested island", func(t *testing.T) {
		if got := stageOf(t, sharpBase()); got != StageAdult {
			t.Errorf("got %s, want おとな", StageName(got))
		}
	})

	t.Run("S4 when the island's only rival bets from a coarser scope", func(t *testing.T) {
		// The finest island is cap=impl|lang=go and only claude holds a
		// connection at that exact key; codex competes from the coarser
		// cap=impl — the same subset rule a real Choose reads with
		// (decide.finestMatch). An exact-key rival lookup would see a
		// one-candidate election here and never reach おとな.
		claude := capConn("cap=impl|lang=go", "claude", 11, 1)
		repo := &stageRepo{
			conns: []*core.Connection{
				claude,
				capConn("cap=impl", "codex", 11, 1),
				prefConn("cap=impl", "claude~codex", 30, 1),
			},
			ledger: map[string][]*core.LedgerEntry{
				ledgerKey(claude.Kind, claude.ScopeKey, claude.Target): calibratedLedger(10),
			},
		}
		for i := 0; i < 4; i++ {
			repo.exps = append(repo.exps, &core.Experience{
				ID: string(rune('a' + i)), TS: now, Kind: core.KindExecution,
				Context: map[string]string{"cap": "impl", "lang": "go"}, Provider: "claude",
			})
		}
		if got := stageOf(t, repo); got != StageAdult {
			t.Errorf("got %s, want おとな — a coarser-scoped rival still contests this island's lottery", StageName(got))
		}
	})

	t.Run("S4 stays おとな on provider-pair preference alone", func(t *testing.T) {
		// sharpBase's claude~codex evidence is far above 1, yet no pair
		// includes the human — 道具同士の優劣しか知らない.
		if got := stageOf(t, sharpBase()); got != StageAdult {
			t.Errorf("got %s, want おとな", StageName(got))
		}
	})

	t.Run("S5 with human-pair preference evidence above the re-ask ceiling", func(t *testing.T) {
		r := sharpBase()
		r.conns = append(r.conns, prefConn("cap=impl", "claude~human", 3, 1))
		if got := stageOf(t, r); got != StagePartner {
			t.Errorf("got %s, want あいぼう", StageName(got))
		}
	})

	t.Run("S5 holds while curiosity would not re-ask — evidence between EMax and 1.0", func(t *testing.T) {
		// Evidence 0.6: below the old ≥1.0 gate (one answer decays past 1.0
		// overnight, ADR-0045 Decision 3 追記) but at or above core.PrefKnownMin,
		// where the gap stays closed — Tomo still knows this, so it stays あいぼう.
		r := sharpBase()
		r.conns = append(r.conns, prefConn("cap=impl", "claude~human", 1.3, 1.3))
		if got := stageOf(t, r); got != StagePartner {
			t.Errorf("got %s, want あいぼう (human-pair evidence 0.6 >= PrefKnownMin %v)", StageName(got), core.PrefKnownMin)
		}
	})

	t.Run("S4 once the human-pair evidence falls below the re-ask ceiling", func(t *testing.T) {
		r := sharpBase()
		r.conns = append(r.conns, prefConn("cap=impl", "claude~human", 1.2, 1.2))
		if got := stageOf(t, r); got != StageAdult {
			t.Errorf("got %s, want おとな (human-pair evidence 0.4 < PrefKnownMin %v — the question would reopen)", StageName(got), core.PrefKnownMin)
		}
	})

	t.Run("S5 sees human whichever side of the pair it canonicalized to", func(t *testing.T) {
		r := sharpBase()
		r.conns = append(r.conns, prefConn("cap=impl", "human~zeta", 3, 1))
		if got := stageOf(t, r); got != StagePartner {
			t.Errorf("got %s, want あいぼう", StageName(got))
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

// TestStageIsADeterministicView pins ADR-0008 Decision 1 through the revision:
// the stage is derived with a fixed seed, so the same network always shows
// the same face — even when the contested island's wobble sits near
// ThetaSharp, where a drifting seed would flip the answer between calls.
func TestStageIsADeterministicView(t *testing.T) {
	r := sharpBase()
	// Weaken the pair posterior until the lottery is genuinely borderline:
	// at Beta(2.5,1) the wobble crosses ThetaSharp on 7 of 40 seeds (実測),
	// so any seed drift shows up as a flapping stage here.
	r.conns[2] = prefConn("cap=impl", "claude~codex", 2.5, 1)
	first := stageOf(t, r)
	for i := 0; i < 40; i++ {
		if got := stageOf(t, r); got != first {
			t.Fatalf("call %d: got %s, first call %s — same DB must always show the same face", i, StageName(got), StageName(first))
		}
	}
}

func TestStageNameOutOfRange(t *testing.T) {
	if got := StageName(-1); got != "" {
		t.Errorf("StageName(-1) = %q, want \"\"", got)
	}
	if got := StageName(StagePartner + 1); got != "" {
		t.Errorf("StageName(out of range) = %q, want \"\"", got)
	}
}
