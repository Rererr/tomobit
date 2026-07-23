package decide

import (
	"math"
	"math/rand"
	"sort"
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

// splitChild is a Connection born from a Split: it carries its parent's
// posterior mean as its own prior (ADR-0013 Decision 1), scaled to the fixed
// inheritance mass, and has no evidence of its own left after full decay.
func splitChild(mu float64, lastUpdate int64) *core.Connection {
	a, b := mu*core.InheritM0, (1-mu)*core.InheritM0
	return &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=implement|lang=rust", Target: "codex",
		Alpha: a, Beta: b, PriorA: a, PriorB: b,
		LastUpdate: lastUpdate, BornTS: lastUpdate, ParentKey: "cap=implement",
	}
}

// A uniform prior keeps the bar exactly where ADR-0012 calibrated it: this
// change must not move any judgment a parentless Connection takes part in
// (ADR-0038 Consequences).
func TestGateBarIsUnchangedForAUniformPrior(t *testing.T) {
	root := conn(core.ConnCapability, "cap=implement", "codex", 1, 2)
	want := QuantileQ - gateMargin
	if got := gateBar(root); math.Abs(got-want) > 1e-9 {
		t.Errorf("gateBar(uniform prior) = %v, want %v", got, want)
	}
	if got := gateBar(nil); math.Abs(got-want) > 1e-9 {
		t.Errorf("gateBar(absent) = %v, want %v", got, want)
	}
}

// The whole promise of ADR-0012 Decision 3 for an inherited prior: evidence
// closes the gate, and decay alone opens it again. Under the constant bar the
// decayed child never got back in, because its floor was its parent's mean
// rather than the blank slate (ADR-0038 Context).
func TestGateReopensByDecayForAChildOfALowMeanParent(t *testing.T) {
	child := splitChild(0.30, now)
	child.Observe(0, now)
	child.Observe(0, now)
	if GatePass(child, now) {
		t.Fatalf("fresh failures should close the gate, quantile %v bar %v",
			child.QuantileAt(now, QuantileQ), gateBar(child))
	}
	later := now + 50*core.HalfLifeMs
	if !GatePass(child, later) {
		t.Errorf("decay alone must re-open the gate for an inherited prior too, quantile %v bar %v",
			child.QuantileAt(later, QuantileQ), gateBar(child))
	}
}

// finestOnlyCandidate is the pre-ADR-0042 gate: it reads only the single
// finest match (ADR-0013 Decision 2 as it stood). It serves two roles here —
// the oracle the no-tie invariant is checked against, and the mutant the
// min-aggregation scenarios are checked against.
func finestOnlyCandidate(conns []*core.Connection, kind, provider string, tokens []string, nowMs int64) Candidate {
	c := finestMatch(conns, kind, provider, tokens)
	cand := Candidate{Provider: provider, Quantile: blankQuantile, Wins: -1}
	if c != nil {
		cand.ScopeKey = c.ScopeKey
		cand.Quantile = c.QuantileAt(nowMs, QuantileQ)
	}
	cand.Passed = cand.Quantile >= gateBar(c)
	return cand
}

// chooseFinestOracle mirrors chooseKind but keeps the pre-ADR-0042 finest-only
// gate, so a no-tie ledger's full per-seed decision can be compared bit-for-bit.
func chooseFinestOracle(kind string, conns []*core.Connection, candidates, tokens []string, size string, seed, nowMs int64) Decision {
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)
	d := Decision{Seed: seed, N: Draws(size), Q: QuantileQ}
	var passers []int
	for _, p := range sorted {
		cand := finestOnlyCandidate(conns, kind, p, tokens, nowMs)
		if cand.Passed {
			cand.Wins = 0
			passers = append(passers, len(d.Candidates))
		}
		d.Candidates = append(d.Candidates, cand)
	}
	if len(passers) == 0 {
		d.Fallback = true
		best := 0
		for i := 1; i < len(d.Candidates); i++ {
			if d.Candidates[i].Quantile > d.Candidates[best].Quantile {
				best = i
			}
		}
		d.Provider = d.Candidates[best].Provider
		return d
	}
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < len(passers); i++ {
		for j := i + 1; j < len(passers); j++ {
			a, b := &d.Candidates[passers[i]], &d.Candidates[passers[j]]
			if firstWins(rng, conns, tokens, a.Provider, b.Provider, d.N, nowMs) {
				a.Wins++
			} else {
				b.Wins++
			}
		}
	}
	best := passers[0]
	for _, i := range passers[1:] {
		c, b := d.Candidates[i], d.Candidates[best]
		if c.Wins > b.Wins || (c.Wins == b.Wins && c.Quantile > b.Quantile) {
			best = i
		}
	}
	d.Provider = d.Candidates[best].Provider
	return d
}

// TestGateAllUnchangedOnNoTieUniformLedger (ADR-0042 対案2 / ADR-0038 不変条件):
// on a uniform-prior ledger with no same-granularity ties, the gate visits only
// the finest match, so every seed's whole decision must equal the finest-only
// oracle bit-for-bit.
func TestGateAllUnchangedOnNoTieUniformLedger(t *testing.T) {
	conns := []*core.Connection{
		conn(core.ConnCapability, "cap=implement", "codex", 6, 3),
		conn(core.ConnCapability, "cap=implement", "claude-code", 10, 2),
		conn(core.ConnPreference, "cap=implement", "claude-code~codex", 6, 4),
	}
	providers := []string{"claude-code", "codex"}
	tokens := []string{"cap=implement"}
	claude, codex := 0, 0
	for seed := int64(0); seed < 200; seed++ {
		got := Choose(conns, providers, tokens, "", seed, now)
		want := chooseFinestOracle(core.ConnCapability, conns, providers, tokens, "", seed, now)
		if got.Provider != want.Provider || got.Fallback != want.Fallback {
			t.Fatalf("seed %d diverged: got %+v want %+v", seed, got, want)
		}
		if len(got.Candidates) != len(want.Candidates) {
			t.Fatalf("seed %d: candidate count %d vs %d", seed, len(got.Candidates), len(want.Candidates))
		}
		for i := range got.Candidates {
			g, w := got.Candidates[i], want.Candidates[i]
			if g.ScopeKey != w.ScopeKey || g.Passed != w.Passed || math.Abs(g.Quantile-w.Quantile) > 1e-12 {
				t.Fatalf("seed %d cand %d: %+v vs %+v", seed, i, g, w)
			}
		}
		switch got.Provider {
		case "claude-code":
			claude++
		case "codex":
			codex++
		}
	}
	if claude == 0 || codex == 0 {
		t.Errorf("distribution collapsed, expected both explored: claude %d codex %d", claude, codex)
	}
}

// TestGateAllDropsProviderOnSameGranularityDanger reproduces the ADR-0042 rust
// scenario minimally: claude-code is healthy on cap=implement but has lost
// lang=rust 11 times. Both are same-granularity (1 token); finestMatch selects
// cap= lexically so selection looks healthy. 対案2 reads all finest matches, so
// lang=rust drives the min below the bar and gates claude-code out — and the
// audit row names lang=rust / passed=false.
func TestGateAllDropsProviderOnSameGranularityDanger(t *testing.T) {
	claudeCap := conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "claude-code", 12, 1)
	claudeRust := conn(core.ConnCapability, core.NewScope("lang=rust").Key(), "claude-code", 1, 12)
	codexCap := conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "codex", 8, 2)
	conns := []*core.Connection{claudeCap, claudeRust, codexCap}
	tokens := []string{"cap=implement", "lang=rust"}

	d := Choose(conns, []string{"claude-code", "codex"}, tokens, "", 1, now)

	var claude Candidate
	for _, c := range d.Candidates {
		if c.Provider == "claude-code" {
			claude = c
		}
	}
	if claude.Passed {
		t.Fatalf("claude-code should be gated by its rust losses, quantile %v scope %q", claude.Quantile, claude.ScopeKey)
	}
	if claude.ScopeKey != claudeRust.ScopeKey {
		t.Errorf("audit row must name the rejecting connection %q, got %q", claudeRust.ScopeKey, claude.ScopeKey)
	}
	if d.Provider == "claude-code" {
		t.Errorf("a gated provider must not be chosen while codex passes, got %q", d.Provider)
	}
}

// TestGateAllKeepsProviderWhenSameGranularityIsHealthy is the go-side control:
// when the same-granularity sibling (lang=go) is healthy, the min stays above
// the bar and 対案2 does not gate the provider — the change costs no healthy
// provider its pass.
func TestGateAllKeepsProviderWhenSameGranularityIsHealthy(t *testing.T) {
	claudeCap := conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "claude-code", 12, 1)
	claudeGo := conn(core.ConnCapability, core.NewScope("lang=go").Key(), "claude-code", 11, 1)
	conns := []*core.Connection{claudeCap, claudeGo}
	tokens := []string{"cap=implement", "lang=go"}

	d := Choose(conns, []string{"claude-code"}, tokens, "", 1, now)

	claude := d.Candidates[0]
	if !claude.Passed {
		t.Fatalf("healthy go must keep claude-code in the gate, quantile %v scope %q", claude.Quantile, claude.ScopeKey)
	}
	if d.Provider != "claude-code" {
		t.Errorf("claude-code should be chosen, got %q", d.Provider)
	}
	// S3: a passer's audit row still names the finest match it read, not the
	// min sibling — the gate's min never leaks into the audit unless it gates.
	if claude.ScopeKey != claudeCap.ScopeKey {
		t.Errorf("passer audit must name the finest match %q, got %q", claudeCap.ScopeKey, claude.ScopeKey)
	}
}

// TestGateAllIgnoresCoarserSiblings (S1): the gate reads only same-granularity
// siblings. A coarser same-target connection that also matches the tokens must
// not touch the decision — loosening gateAll's len(scope) != bestLen guard so
// coarser rows leak in gates a provider its finest match cleared.
func TestGateAllIgnoresCoarserSiblings(t *testing.T) {
	fine := conn(core.ConnCapability, core.NewScope("cap=implement", "lang=rust").Key(), "claude-code", 11, 1)
	coarse := conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "claude-code", 1, 2) // unhealthy, coarser
	conns := []*core.Connection{fine, coarse}
	tokens := []string{"cap=implement", "lang=rust"}

	d := Choose(conns, []string{"claude-code"}, tokens, "", 1, now)

	claude := d.Candidates[0]
	if !claude.Passed {
		t.Fatalf("a coarser unhealthy sibling must not reach the gate, got gated %+v", claude)
	}
	if claude.ScopeKey != fine.ScopeKey {
		t.Errorf("audit must name the finest match %q, got %q", fine.ScopeKey, claude.ScopeKey)
	}
}

// TestFallbackLeastBadUsesFinestMatchQuantile (S2a): with every provider gated
// by a failing rust sibling, the fallback's least-bad ranks on the finest
// quantile each provider read — not the refusing sibling its audit now names.
// aaa read a strong cap= row (0.87) but its refuser is deeper than bbb's, so a
// ranking that leaked to the refuser would pick bbb (ADR-0042 W1).
func TestFallbackLeastBadUsesFinestMatchQuantile(t *testing.T) {
	conns := []*core.Connection{
		conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "aaa", 12, 1), // finest 0.8745
		conn(core.ConnCapability, core.NewScope("lang=rust").Key(), "aaa", 1, 12),     // refuser 0.0184
		conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "bbb", 3, 2),  // finest 0.4175
		conn(core.ConnCapability, core.NewScope("lang=rust").Key(), "bbb", 1, 6),      // refuser 0.0365
	}
	tokens := []string{"cap=implement", "lang=rust"}

	d := Choose(conns, []string{"aaa", "bbb"}, tokens, "", 1, now)

	if !d.Fallback {
		t.Fatalf("both providers gated → fallback expected, got %+v", d)
	}
	if d.Provider != "aaa" {
		t.Errorf("least-bad by finest quantile is aaa (read 0.87), got %q — ranking leaked to the refuser", d.Provider)
	}
}

// TestPasserTieBreakUsesFinestMatchQuantile (S2b): three passers in a Condorcet
// preference cycle each win one pairwise duel, so the winner is decided by the
// capability tie-break — which must use the finest match each read. aaa's finest
// is highest (0.8745) but its healthy same-granularity sibling is lower (0.3266);
// a tie-break that used the min sibling would drop aaa below bbb (ADR-0042 W1).
func TestPasserTieBreakUsesFinestMatchQuantile(t *testing.T) {
	conns := []*core.Connection{
		conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "aaa", 12, 1), // finest 0.8745
		conn(core.ConnCapability, core.NewScope("lang=rust").Key(), "aaa", 3, 3),      // sibling 0.3266 (passes)
		conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "bbb", 6, 2),  // 0.6291
		conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "ccc", 4, 3),  // 0.4146
		// Condorcet cycle: aaa>bbb, bbb>ccc, ccc>aaa (target "a~b", y=1 = first won).
		conn(core.ConnPreference, "cap=implement", "aaa~bbb", 40, 1),
		conn(core.ConnPreference, "cap=implement", "bbb~ccc", 40, 1),
		conn(core.ConnPreference, "cap=implement", "aaa~ccc", 1, 40),
	}
	tokens := []string{"cap=implement", "lang=rust"}

	d := Choose(conns, []string{"aaa", "bbb", "ccc"}, tokens, "large", 1, now)

	if d.Fallback {
		t.Fatalf("all three should pass the gate, got fallback %+v", d)
	}
	for _, c := range d.Candidates {
		if c.Wins != 1 {
			t.Fatalf("expected a 1-1-1 Condorcet cycle, got %s Wins=%d", c.Provider, c.Wins)
		}
	}
	if d.Provider != "aaa" {
		t.Errorf("tie-break by finest quantile is aaa (0.8745), got %q — tie-break used the min sibling", d.Provider)
	}
	// aaa's audit stays at its finest read, not the lower sibling.
	for _, c := range d.Candidates {
		if c.Provider == "aaa" && c.ScopeKey != core.NewScope("cap=implement").Key() {
			t.Errorf("aaa passer audit must name cap=implement, got %q", c.ScopeKey)
		}
	}
}

// TestGateAllMinAggregationIsLoadBearing is the mutation guard: on the rust
// ledger the finest-only mutant (the pre-0042 gate) lets claude-code through
// because cap= shadows lang=rust, while gateAll gates it. So restoring
// finest-only aggregation makes TestGateAllDropsProviderOnSameGranularityDanger
// fail — pinning the min over all finest matches as the load-bearing change.
func TestGateAllMinAggregationIsLoadBearing(t *testing.T) {
	claudeCap := conn(core.ConnCapability, core.NewScope("cap=implement").Key(), "claude-code", 12, 1)
	claudeRust := conn(core.ConnCapability, core.NewScope("lang=rust").Key(), "claude-code", 1, 12)
	conns := []*core.Connection{claudeCap, claudeRust}
	tokens := []string{"cap=implement", "lang=rust"}

	mutant := finestOnlyCandidate(conns, core.ConnCapability, "claude-code", tokens, now)
	if !mutant.Passed || mutant.ScopeKey != claudeCap.ScopeKey {
		t.Fatalf("finest-only mutant should read cap= and pass, got %+v", mutant)
	}
	if real := gateAll(conns, core.ConnCapability, "claude-code", tokens, now); real.Passed {
		t.Fatalf("gateAll must gate the same ledger the mutant passes, got %+v", real)
	}
}

// The relaxation is one-sided: a child of an excellent family is judged
// against the uniform bar, not against its family's 0.86. Otherwise the
// inherited mean would become an absolute floor, which is not what ADR-0013
// asked of it (ADR-0038 Decision).
func TestGateNeverBecomesStricterForAHighMeanParent(t *testing.T) {
	child := splitChild(0.90, now)
	want := QuantileQ - gateMargin
	if got := gateBar(child); math.Abs(got-want) > 1e-9 {
		t.Errorf("gateBar(μ=0.90 child) = %v, want %v — the bar must never rise above the uniform one", got, want)
	}
	// The bare self-reference this min() rejects would have demanded the
	// family's own q20 instead; record what that would have been so the
	// rejected alternative stays legible.
	a, b := child.Prior()
	if bare := core.BetaQuantile(a, b, QuantileQ); bare <= QuantileQ {
		t.Fatalf("this test only says something while the family's bar (%v) is above the uniform one", bare)
	}
}
