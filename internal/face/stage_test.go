package face

// Stage ladder tests (ADR-0017, revised by ADR-0045): quantity gates for
// infancy, calibration-with-a-sample for S3, sharpness-under-contest for S4,
// human-pair preference for S5 — and the regression story: a Tomo that
// starts missing walks back down.

import (
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/decide"
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

// twoIslandBase extends sharpBase with a second contested frequent island
// (cap=review) whose pairwise preference never got settled: island cap=impl
// is quiet, island cap=review is a coin.
func twoIslandBase() *stageRepo {
	r := sharpBase()
	r.conns = append(r.conns,
		capConn("cap=review", "claude", 11, 1),
		capConn("cap=review", "codex", 11, 1))
	for i := 0; i < 4; i++ {
		r.exps = append(r.exps, &core.Experience{
			ID: "rev-" + string(rune('a'+i)), TS: now, Kind: core.KindExecution,
			Context: map[string]string{"cap": "review"}, Provider: "claude",
		})
	}
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

func growthOf(t *testing.T, r *stageRepo) Growth {
	t.Helper()
	got, err := GrowthFrom(r, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func gateNames(g Growth) []string {
	names := make([]string, len(g.Gates))
	for i, gate := range g.Gates {
		names[i] = gate.Name
	}
	return names
}

func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestGrowthDisclosesOnlyTheGatesTheNextStageNeeds pins ADR-0046 Decision 1:
// Gates is the cumulative requirement list for Next() and nothing more — a
// こども is not shown the S5 preference gate as if it were the next step,
// and every disclosed value agrees with the stage the same call decided.
func TestGrowthDisclosesOnlyTheGatesTheNextStageNeeds(t *testing.T) {
	t.Run("S0 毛玉: next=あかちゃん, connection の誕生ゲートだけ — evidence 0/3 という偽の遠さを見せない", func(t *testing.T) {
		g := growthOf(t, &stageRepo{})
		if g.Stage != StageEgg {
			t.Fatalf("stage = %s, want 毛玉", StageName(g.Stage))
		}
		if next, ok := g.Next(); !ok || next != StageChick {
			t.Errorf("Next() = %d,%v, want %d,true", next, ok, StageChick)
		}
		if !equalNames(gateNames(g), []string{GateConnection}) {
			t.Errorf("gates = %v, want [connection] — 孵化の実条件は connection の誕生であって evidence≥%v ではない", gateNames(g), ThetaEvidence)
		}
		gate := g.Gates[0]
		if gate.Met || gate.Value == nil || *gate.Value != 0 || gate.Threshold != 1 {
			t.Errorf("connection gate = %+v, want value 0, threshold 1, unmet", gate)
		}
		if gate.Hint() != "もっと一緒に仕事をする" {
			t.Errorf("connection hint = %q, want もっと一緒に仕事をする", gate.Hint())
		}
	})

	t.Run("S1 あかちゃん: evidence が値ごと未達で出る", func(t *testing.T) {
		g := growthOf(t, &stageRepo{conns: []*core.Connection{capConn("cap=impl", "claude", 2, 1)}})
		if g.Stage != StageChick {
			t.Fatalf("stage = %s, want あかちゃん", StageName(g.Stage))
		}
		if !equalNames(gateNames(g), []string{GateEvidence}) {
			t.Errorf("gates = %v, want [evidence]", gateNames(g))
		}
		gate := g.Gates[0]
		if gate.Met || gate.Value == nil || *gate.Value >= gate.Threshold {
			t.Errorf("evidence gate = %+v, want unmet with value below threshold %v", gate, gate.Threshold)
		}
	})

	t.Run("S2 標本不足: sample が未達・mean は充足 — 落ちた理由が読める", func(t *testing.T) {
		r := sharpBase()
		key := ledgerKey(core.ConnCapability, "cap=impl", "claude")
		r.ledger[key] = calibratedLedger(int(ThetaCalMin) - 1)
		g := growthOf(t, r)
		if g.Stage != StageChild {
			t.Fatalf("stage = %s, want こども", StageName(g.Stage))
		}
		if !equalNames(gateNames(g), []string{GateEvidence, GateCalibrationSample, GateCalibration}) {
			t.Errorf("gates = %v, want [evidence calibration_sample calibration]", gateNames(g))
		}
		sample, mean := g.Gates[1], g.Gates[2]
		if sample.Met || sample.Value == nil || *sample.Value != ThetaCalMin-1 {
			t.Errorf("sample gate = %+v, want unmet with value %v", sample, ThetaCalMin-1)
		}
		if !mean.Met || mean.Value == nil || *mean.Value != 0 {
			t.Errorf("mean gate = %+v, want met with value 0 — ズレていないのに未達の顔をさせない", mean)
		}
	})

	t.Run("S2 予測ズレ: sample は充足・mean が未達", func(t *testing.T) {
		r := sharpBase()
		key := ledgerKey(core.ConnCapability, "cap=impl", "claude")
		for i := 0; i < 10; i++ {
			r.ledger[key] = append(r.ledger[key], &core.LedgerEntry{TS: now, SExcess: 1.9})
		}
		g := growthOf(t, r)
		if g.Stage != StageChild {
			t.Fatalf("stage = %s, want こども", StageName(g.Stage))
		}
		sample, mean := g.Gates[1], g.Gates[2]
		if !sample.Met {
			t.Errorf("sample gate = %+v, want met", sample)
		}
		if mean.Met || mean.Value == nil || *mean.Value <= ThetaCal {
			t.Errorf("mean gate = %+v, want unmet with value above %v", mean, ThetaCal)
		}
	})

	t.Run("S3 わかもの: sharpness まで出て preference は出ない", func(t *testing.T) {
		g := growthOf(t, soloBase())
		if g.Stage != StageYouth {
			t.Fatalf("stage = %s, want わかもの", StageName(g.Stage))
		}
		if next, ok := g.Next(); !ok || next != StageAdult {
			t.Errorf("Next() = %d,%v, want %d,true", next, ok, StageAdult)
		}
		want := []string{GateEvidence, GateCalibrationSample, GateCalibration, GateSharpness}
		if !equalNames(gateNames(g), want) {
			t.Errorf("gates = %v, want %v — S5のゲートは次の段の要件ではない", gateNames(g), want)
		}
	})

	t.Run("S4 おとな: 全ゲートが出て preference だけ未達", func(t *testing.T) {
		g := growthOf(t, sharpBase())
		if g.Stage != StageAdult {
			t.Fatalf("stage = %s, want おとな", StageName(g.Stage))
		}
		want := []string{GateEvidence, GateCalibrationSample, GateCalibration, GateSharpness, GatePreferenceWithHuman}
		if !equalNames(gateNames(g), want) {
			t.Fatalf("gates = %v, want %v", gateNames(g), want)
		}
		for _, gate := range g.Gates[:4] {
			if !gate.Met {
				t.Errorf("gate %s = %+v, want met — おとなは前段を全て通過している", gate.Name, gate)
			}
		}
		pref := g.Gates[4]
		if pref.Met || pref.Value == nil || *pref.Value != 0 || pref.Threshold != core.PrefKnownMin {
			t.Errorf("preference gate = %+v, want unmet, value 0, threshold %v", pref, core.PrefKnownMin)
		}
	})
}

// TestGrowthSharpnessUnmeasurableIsNullNotZero pins the point ADR-0046 calls
// the implementation's pass/fail line: with no contested frequent island the
// sharpness gate is 測定不能 (Value nil), while a contested island that
// wobbles is 未達 (Value present, over the threshold) — two different states
// that must not wear the same face.
func TestGrowthSharpnessUnmeasurableIsNullNotZero(t *testing.T) {
	g := growthOf(t, soloBase())
	unmeasured := g.Gates[len(g.Gates)-1]
	if unmeasured.Name != GateSharpness {
		t.Fatalf("last gate = %s, want sharpness", unmeasured.Name)
	}
	if unmeasured.Value != nil {
		t.Errorf("uncontested sharpness Value = %v, want nil (測定不能≠0)", *unmeasured.Value)
	}
	if unmeasured.Met {
		t.Error("uncontested sharpness must not be met (ADR-0045 Decision 1)")
	}

	r := soloBase()
	r.conns = append(r.conns, capConn("cap=impl", "codex", 11, 1)) // rival, no preference: a coin
	g = growthOf(t, r)
	wobbling := g.Gates[len(g.Gates)-1]
	if wobbling.Name != GateSharpness {
		t.Fatalf("last gate = %s, want sharpness", wobbling.Name)
	}
	if wobbling.Value == nil {
		t.Fatal("contested sharpness Value = nil, want the measured wobble")
	}
	if *wobbling.Value <= ThetaSharp || wobbling.Met {
		t.Errorf("wobbling sharpness = %+v, want unmet with value above %v", wobbling, ThetaSharp)
	}
}

// TestSharpnessGateValueIsTheWorstWobbleAcrossIslands pins the gate's
// max-across-contested-islands contract on a network with two contested
// frequent islands — one settled and quiet, one a preference-less coin:
// sharpness fails because of the coin, the disclosed Value is exactly the
// coin's wobble (the max — not whichever island the map yielded first or
// last), and the stage never flips with iteration order (View property).
func TestSharpnessGateValueIsTheWorstWobbleAcrossIslands(t *testing.T) {
	r := twoIslandBase()
	quietScope := core.ParseScopeKey("cap=impl")
	coinScope := core.ParseScopeKey("cap=review")
	quiet := decide.Wobble(r.conns, islandRivals(r.conns, quietScope), quietScope, "", sharpDraws, stageSeed, now)
	coin := decide.Wobble(r.conns, islandRivals(r.conns, coinScope), coinScope, "", sharpDraws, stageSeed, now)
	if quiet > ThetaSharp || coin <= ThetaSharp {
		t.Fatalf("fixture broke: want quiet %v <= ThetaSharp %v < coin %v", quiet, ThetaSharp, coin)
	}

	// 32 calls: sharpnessGate walks the island map fresh each time, so an
	// implementation reading anything but the max gets a fair chance to
	// meet every iteration order.
	for i := 0; i < 32; i++ {
		g := growthOf(t, r)
		if g.Stage != StageYouth {
			t.Fatalf("call %d: stage = %s, want わかもの — コインの島がある限り鋭さは未達", i, StageName(g.Stage))
		}
		sharp := g.Gates[len(g.Gates)-1]
		if sharp.Name != GateSharpness {
			t.Fatalf("call %d: last gate = %s, want sharpness", i, sharp.Name)
		}
		if sharp.Met {
			t.Errorf("call %d: sharpness met — 静かな島がコインの島を隠した", i)
		}
		if sharp.Value == nil {
			t.Fatalf("call %d: sharpness value = nil, want the coin island's wobble %v", i, coin)
		}
		if *sharp.Value != coin {
			t.Fatalf("call %d: sharpness value = %v, want the worst wobble %v (coin island), not the quiet island's %v", i, *sharp.Value, coin, quiet)
		}
	}
}

// TestGrowthHasNoNextAtTheTop pins ADR-0046 Decision 1's refusal of fake
// progress: あいぼう has no next stage and no gate list to dress up as 100%.
func TestGrowthHasNoNextAtTheTop(t *testing.T) {
	r := sharpBase()
	r.conns = append(r.conns, prefConn("cap=impl", "claude~human", 3, 1))
	g := growthOf(t, r)
	if g.Stage != StagePartner {
		t.Fatalf("stage = %s, want あいぼう", StageName(g.Stage))
	}
	if next, ok := g.Next(); ok {
		t.Errorf("Next() = %d,true, want none", next)
	}
	if len(g.Gates) != 0 {
		t.Errorf("gates = %v, want none — 達成率100%%の偽進捗を作らない", gateNames(g))
	}
}

// TestStageFromAgreesWithGrowthFrom pins ADR-0046 Consequences (1): the
// stage every existing caller reads and the disclosed evaluation are one
// derivation, not two that can drift.
func TestStageFromAgreesWithGrowthFrom(t *testing.T) {
	partner := sharpBase()
	partner.conns = append(partner.conns, prefConn("cap=impl", "claude~human", 3, 1))
	miscal := sharpBase()
	key := ledgerKey(core.ConnCapability, "cap=impl", "claude")
	for i := 0; i < 10; i++ {
		miscal.ledger[key] = append(miscal.ledger[key], &core.LedgerEntry{TS: now, SExcess: 1.9})
	}
	sampleShort := sharpBase()
	sampleShort.ledger[key] = calibratedLedger(int(ThetaCalMin) - 1)
	repos := map[string]*stageRepo{
		"empty":       {},
		"infant":      {conns: []*core.Connection{capConn("cap=impl", "claude", 2, 1)}},
		"sampleshort": sampleShort,
		"miscal":      miscal,
		"solo":        soloBase(),
		"sharp":       sharpBase(),
		"partner":     partner,
	}
	// The gates the current stage's own admission guarantees, as a prefix
	// length of the cumulative list. Not 「最後以外は全部Met」: the
	// calibration pair evaluates together, so a sample-short repo legally
	// discloses an unmet sample gate mid-list with a met mean behind it.
	guaranteed := map[int]int{StageChild: 1, StageYouth: 3, StageAdult: 4}
	for name, r := range repos {
		stage := stageOf(t, r)
		g := growthOf(t, r)
		if g.Stage != stage {
			t.Errorf("%s: GrowthFrom stage %s != StageFrom %s", name, StageName(g.Stage), StageName(stage))
		}
		for _, gate := range g.Gates[:guaranteed[g.Stage]] {
			if !gate.Met {
				t.Errorf("%s: gate %s unmet but the ladder passed the stage that requires it — 内訳と結論が食い違っている", name, gate.Name)
			}
		}
	}
}

// TestGrowthIsADeterministicView pins ADR-0046 Consequences (5) on the same
// borderline network TestStageIsADeterministicView uses: the same database
// must always disclose the same gates, wobble value included.
func TestGrowthIsADeterministicView(t *testing.T) {
	r := sharpBase()
	r.conns[2] = prefConn("cap=impl", "claude~codex", 2.5, 1)
	first := growthOf(t, r)
	for i := 0; i < 40; i++ {
		got := growthOf(t, r)
		if got.Stage != first.Stage || len(got.Gates) != len(first.Gates) {
			t.Fatalf("call %d: growth shape changed: %+v vs %+v", i, got, first)
		}
		for j := range got.Gates {
			a, b := got.Gates[j], first.Gates[j]
			if a.Name != b.Name || a.Met != b.Met || a.Threshold != b.Threshold ||
				(a.Value == nil) != (b.Value == nil) || (a.Value != nil && *a.Value != *b.Value) {
				t.Fatalf("call %d gate %s: %+v vs first %+v — same DB must always disclose the same growth", i, a.Name, a, b)
			}
		}
	}
}

// TestGateHint pins ADR-0046 Decision 3: each unmet gate carries the one
// move that raises its value, and 測定不能 and 未達 sharpness translate to
// two different moves — that difference is what the null exists for.
func TestGateHint(t *testing.T) {
	v := func(x float64) *float64 { return &x }
	cases := []struct {
		name string
		gate Gate
		want string
	}{
		{"met gate needs no hint", Gate{Name: GateSharpness, Value: v(0.1), Met: true}, ""},
		{"connection", Gate{Name: GateConnection, Value: v(0)}, "もっと一緒に仕事をする"},
		{"evidence", Gate{Name: GateEvidence, Value: v(1)}, "もっと一緒に仕事をする"},
		{"calibration sample", Gate{Name: GateCalibrationSample, Value: v(4)}, "もっと一緒に仕事をする"},
		{"calibration mean off", Gate{Name: GateCalibration, Value: v(0.3)}, "予測が外れている文脈がある"},
		{"calibration mean with no sample at all", Gate{Name: GateCalibration}, "もっと一緒に仕事をする"},
		{"sharpness unmeasurable — 二人目に会わせる", Gate{Name: GateSharpness}, "二人目のProviderに会わせる（autoに任せる）"},
		{"sharpness wobbling — 好みを教える", Gate{Name: GateSharpness, Value: v(0.49)}, "duelや質問に答えて好みを教える"},
		{"preference with human", Gate{Name: GatePreferenceWithHuman, Value: v(0)}, "自分でやった仕事をTomoに見せる（--provider human）"},
		{"unknown gate stays silent", Gate{Name: "future_gate"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.gate.Hint(); got != tc.want {
				t.Errorf("Hint() = %q, want %q", got, tc.want)
			}
		})
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
