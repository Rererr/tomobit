package decide

import (
	"testing"

	"github.com/Rererr/tomobit/internal/core"
)

const now = int64(1_800_000_000_000)

func conn(kind, scopeKey, target string, alpha, beta float64) *core.Connection {
	return &core.Connection{
		Kind: kind, ScopeKey: scopeKey, Target: target,
		Alpha: alpha, Beta: beta, LastUpdate: now, BornTS: now,
	}
}

func TestDrawsTable(t *testing.T) {
	for size, want := range map[string]int{"": 1, "small": 1, "medium": 3, "large": 5} {
		if got := Draws(size); got != want {
			t.Errorf("Draws(%q) = %d, want %d", size, got, want)
		}
	}
}

// TestGateBlankSlatePasses: a provider with no connections sits exactly on
// the self-referential bar and passes — unknown is not "can't".
func TestGateBlankSlatePasses(t *testing.T) {
	d := Choose(nil, []string{"claude-code", "codex"}, []string{"cap=implement"}, "", 1, now)
	for _, c := range d.Candidates {
		if !c.Passed {
			t.Errorf("%s: blank slate should pass the gate (q=%v)", c.Provider, c.Quantile)
		}
	}
	if d.Fallback {
		t.Error("blank slates must not need the fallback")
	}
}

// TestGateOneFailureCloses: ミスは一度で重く刻まれる — a single failure on
// thin evidence drops the pessimistic quantile below the bar immediately.
func TestGateOneFailureCloses(t *testing.T) {
	conns := []*core.Connection{
		conn(core.ConnCapability, "cap=implement", "codex", 1, 2), // one failure
	}
	d := Choose(conns, []string{"claude-code", "codex"}, []string{"cap=implement"}, "", 1, now)
	var codex, claude Candidate
	for _, c := range d.Candidates {
		if c.Provider == "codex" {
			codex = c
		} else {
			claude = c
		}
	}
	if codex.Passed {
		t.Errorf("codex should be gated after one failure, quantile %v", codex.Quantile)
	}
	if !claude.Passed || d.Provider != "claude-code" {
		t.Errorf("claude-code should pass and win, got %q", d.Provider)
	}
}

// TestGateReopensByDecayAlone (ADR-0012 Decision 3): no rehabilitation
// mechanism — the same failed posterior clears the bar after enough decay.
func TestGateReopensByDecayAlone(t *testing.T) {
	failed := conn(core.ConnCapability, "cap=implement", "codex", 1, 2)
	failed.LastUpdate = now
	d := Choose([]*core.Connection{failed}, []string{"codex"}, []string{"cap=implement"}, "", 1, now)
	if d.Candidates[0].Passed {
		t.Fatal("fresh failure should be gated")
	}
	later := now + 20*core.HalfLifeMs
	d = Choose([]*core.Connection{failed}, []string{"codex"}, []string{"cap=implement"}, "", 1, later)
	if !d.Candidates[0].Passed {
		t.Errorf("decay alone should re-open the gate, quantile %v", d.Candidates[0].Quantile)
	}
}

// TestAllGatedFallsBackToLeastPessimistic: production never stops — with
// every candidate below the bar, the highest quantile wins deterministically.
func TestAllGatedFallsBackToLeastPessimistic(t *testing.T) {
	conns := []*core.Connection{
		conn(core.ConnCapability, "cap=implement", "codex", 1, 2),       // 1 failure
		conn(core.ConnCapability, "cap=implement", "claude-code", 1, 5), // 4 failures
	}
	d := Choose(conns, []string{"claude-code", "codex"}, []string{"cap=implement"}, "", 1, now)
	if !d.Fallback {
		t.Fatal("expected fallback")
	}
	if d.Provider != "codex" {
		t.Errorf("least-bad is codex (1 failure), got %q", d.Provider)
	}
}

// TestFinestMatchWins (ADR-0013 Decision 2): the child connection, not its
// coarse parent, is the one row the decision reads.
func TestFinestMatchWins(t *testing.T) {
	coarse := conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "codex", 20, 1)
	fine := conn(core.ConnCapability, core.NewScope("cap=implement", "lang=rust").Key(), "codex", 1, 10)
	fine.ParentKey = coarse.ScopeKey
	conns := []*core.Connection{coarse, fine}

	// In a rust context the (terrible) child gates codex out even though the
	// coarse parent is excellent.
	d := Choose(conns, []string{"codex"}, []string{"cap=implement", "lang=rust"}, "", 1, now)
	if d.Candidates[0].ScopeKey != fine.ScopeKey {
		t.Errorf("read %q, want the finest match %q", d.Candidates[0].ScopeKey, fine.ScopeKey)
	}
	if d.Candidates[0].Passed {
		t.Error("the rust child should gate codex out")
	}

	// Outside rust the parent is the finest match and codex sails through.
	d = Choose(conns, []string{"codex"}, []string{"cap=implement", "lang=go"}, "", 1, now)
	if d.Candidates[0].ScopeKey != coarse.ScopeKey || !d.Candidates[0].Passed {
		t.Errorf("go context should read the parent and pass: %+v", d.Candidates[0])
	}
}

// TestChooseDeterministicPerSeed (ADR-0012 Decision 5): same ledger + same
// seed = same decision; the seed is the whole lottery.
func TestChooseDeterministicPerSeed(t *testing.T) {
	providers := []string{"claude-code", "codex"}
	tokens := []string{"cap=implement"}
	a := Choose(nil, providers, tokens, "", 42, now)
	b := Choose(nil, providers, tokens, "", 42, now)
	if a.Provider != b.Provider {
		t.Errorf("same seed gave different choices: %q vs %q", a.Provider, b.Provider)
	}
	flips := map[string]bool{}
	for seed := int64(0); seed < 64; seed++ {
		flips[Choose(nil, providers, tokens, "", seed, now).Provider] = true
	}
	if len(flips) != 2 {
		t.Error("blank preference should explore both providers across seeds")
	}
}

// TestPreferenceEvidenceSteersTheChoice: with a decisive preference
// posterior, essentially every seed picks the preferred provider — and a
// large n (high stakes) locks it in completely.
func TestPreferenceEvidenceSteersTheChoice(t *testing.T) {
	// "claude-code~codex": y=1 means the lexical first (claude-code) won.
	pref := conn(core.ConnPreference, "cap=implement", "claude-code~codex", 20, 1)
	conns := []*core.Connection{pref}
	providers := []string{"claude-code", "codex"}
	tokens := []string{"cap=implement"}

	wins := 0
	const trials = 200
	for seed := int64(0); seed < trials; seed++ {
		if Choose(conns, providers, tokens, "", seed, now).Provider == "claude-code" {
			wins++
		}
	}
	if wins < trials*9/10 {
		t.Errorf("strong preference should dominate: claude-code won %d/%d", wins, trials)
	}
	if wins == trials {
		t.Log("note: no exploration at all in this sample — acceptable but unusual")
	}
}

// TestWobbleShrinksWithEvidence (ADR-0016): the blank slate flickers near
// the maximum; a decisive posterior barely flickers.
func TestWobbleShrinksWithEvidence(t *testing.T) {
	providers := []string{"claude-code", "codex"}
	tokens := []string{"cap=implement"}
	blank := Wobble(nil, providers, tokens, "", 128, 1, now)
	if blank < 0.3 {
		t.Errorf("blank judgment should flicker heavily, got %v", blank)
	}
	pref := conn(core.ConnPreference, "cap=implement", "claude-code~codex", 30, 1)
	settled := Wobble([]*core.Connection{pref}, providers, tokens, "", 128, 1, now)
	if settled > 0.1 {
		t.Errorf("settled judgment should barely flicker, got %v", settled)
	}
	if settled >= blank {
		t.Errorf("evidence must reduce wobble: %v vs %v", settled, blank)
	}
}

func TestPairWobble(t *testing.T) {
	if w := PairWobble(nil, 128, 1, now); w < 0.3 {
		t.Errorf("blank pair should flicker near max, got %v", w)
	}
	sure := conn(core.ConnPreference, "cap=implement", "a~b", 40, 1)
	if w := PairWobble(sure, 128, 1, now); w > 0.05 {
		t.Errorf("settled pair should not flicker, got %v", w)
	}
}
