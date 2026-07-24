// Package decide implements the Decision Engine (ADR-0012): a deterministic
// capability gate on the pessimistic posterior quantile, then Thompson
// Sampling on the preference side. It reads the finest-matching Connection
// only (ADR-0013 Decision 2) and holds no state of its own — same ledger +
// same seed → same decision (ADR-0012 Decision 5).
package decide

import (
	"math"
	"math/rand"
	"sort"

	"github.com/Rererr/tomobit/internal/core"
)

// Knobs (ADR-0012 Consequences: 分位点q と n(stakes)の固定表).
const (
	// QuantileQ is the quantile level, and — through gateBar — the bar for a
	// connection whose prior is uniform. The gate is self-referential: a
	// provider nobody knows anything about sits precisely on the bar and
	// passes, while one failure drops it well below (Beta(1,2) q20 ≈ 0.106).
	// For the uniform prior that state's q-quantile is exactly q, which is why
	// this one constant used to serve as both; gateBar (ADR-0038) states the
	// rule for every prior. No second semantic knob.
	QuantileQ = 0.20

	// gateMargin exists because decay approaches the prior's quantile from
	// below and never reaches it: without a margin, 名誉回復は減衰だけで
	// 行う (ADR-0012 Decision 3) would take literally forever. 0.02 puts a
	// single fresh failure back inside the gate after ~3 half-lives
	// (~9 months) — forgiveness on the same clock as forgetting.
	//
	// That ~3-half-life figure assumes a uniform prior Beta(1,1), where decay
	// has nowhere to land but the blank slate. A Split child inherits its
	// parent's posterior mean as its own prior (ADR-0013), so its floor is
	// not the blank slate — which is why the bar itself is computed per
	// connection now (gateBar, ADR-0038) instead of being this constant q.
	// Against each connection's own floor the ~3 half-lives hold again.
	gateMargin = 0.02
)

// Draws is n(stakes): how many times to imagine before deciding (ADR-0012
// Decision 4). A pure function of the size context attribute — v1 is the
// fixed table the ADR calls for. n=1 is pure Thompson Sampling; larger n
// cools exploration toward the posterior-mean greedy.
func Draws(size string) int {
	switch size {
	case "medium":
		return 3
	case "large":
		return 5
	default: // "", "small", and anything the extractor never emits
		return 1
	}
}

// Candidate is one provider's audit row in a Decision.
type Candidate struct {
	Provider string
	// ScopeKey/Quantile name the connection this row reports on. For a passer
	// it is the finest match selection actually read ("" = blank slate); for a
	// provider gated out under ADR-0042 対案2 it is the refusing sibling — the
	// most pessimistic same-granularity connection that missed its bar.
	ScopeKey string
	Quantile float64 // pessimistic quantile of that connection at decision time
	Passed   bool
	Wins     int // pairwise preference wins among gate passers (-1 = gated)

	// readQuantile is the finest match's quantile — the row selection read —
	// even when Quantile has been swapped to a refusing sibling for the audit.
	// The fallback's least-bad ranking runs on this so ADR-0042's audit swap
	// never leaks into selection (ADR-0042 W1: 選択は最細一致のまま). Unexported:
	// it is decision bookkeeping, not part of the audit record.
	readQuantile float64
}

// Decision is the full audit record of one choice.
type Decision struct {
	Provider   string
	Seed       int64
	N          int
	Q          float64
	Fallback   bool // no candidate passed the gate; least-bad was chosen
	Candidates []Candidate
}

// Choose picks a provider for the given context tokens. conns is the full
// projection (the function selects finest matches itself), providers the
// registered candidates, size the stakes attribute, seed the recorded lot.
func Choose(conns []*core.Connection, providers, tokens []string, size string, seed, nowMs int64) Decision {
	return chooseKind(core.ConnCapability, conns, providers, tokens, size, seed, nowMs)
}

// ChoosePlan picks a plan variant from the menu — the same gate and lottery
// over the ledger's second bet target (ADR-0014 Decision 1: 二段とも同じ
// 機構が全適用される).
func ChoosePlan(conns []*core.Connection, menu, tokens []string, size string, seed, nowMs int64) Decision {
	return chooseKind(core.ConnPlan, conns, menu, tokens, size, seed, nowMs)
}

// chooseKind is the shared decision rule: pessimistic gate over the bet
// kind's connections, then a pairwise Thompson tournament on the shared
// preference ledger.
func chooseKind(kind string, conns []*core.Connection, candidates, tokens []string, size string, seed, nowMs int64) Decision {
	sorted := append([]string(nil), candidates...)
	sort.Strings(sorted)

	d := Decision{Seed: seed, N: Draws(size), Q: QuantileQ}

	// Capability gate: deterministic, no sampling (ADR-0012 Decision 2 —
	// 「できないかもしれない」の探索を本番のタスクで張らない).
	var passers []int
	for _, p := range sorted {
		cand := gateAll(conns, kind, p, tokens, nowMs)
		if cand.Passed {
			cand.Wins = 0
			passers = append(passers, len(d.Candidates))
		}
		d.Candidates = append(d.Candidates, cand)
	}

	// Everyone gated out: work must not stop (Curiosity Never Blocks
	// Production) — take the least-pessimistic candidate deterministically.
	if len(passers) == 0 {
		d.Fallback = true
		best := 0
		for i := 1; i < len(d.Candidates); i++ {
			// Rank by the finest match each candidate read, not the refusing
			// sibling its audit row now names (ADR-0042 W1).
			if d.Candidates[i].readQuantile > d.Candidates[best].readQuantile {
				best = i
			}
		}
		d.Provider = d.Candidates[best].Provider
		return d
	}

	// Preference side: a seeded pairwise tournament. Each pair averages n
	// draws from its finest preference posterior (absent = Beta(1,1) — full
	// exploration); n→∞ degenerates to the posterior-mean greedy.
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

// blankQuantile is BetaQuantile(1,1,q) = q: what an absent connection scores.
const blankQuantile = QuantileQ

// gateBar is the line one connection has to clear (ADR-0038): its own state
// of ignorance, never higher than the uniform bar.
//
//	bar = min(q, PriorQuantile(q)) − margin
//
// The constant q was only ever the uniform prior's q-quantile written out —
// the rule ADR-0012 states is "a provider nobody knows anything about sits
// precisely on the bar". Under ADR-0013's inherited prior that state sits
// below the constant, so a child of a family whose mean was under ~0.48 could
// never re-enter no matter how far its evidence decayed, and ADR-0037 measured
// that merge cannot rescue it either (ln BF approaches ThetaMerge=0 from
// above, ~60 half-lives). Grounding the bar in the connection's own prior
// restores the stated rule for every prior, not just the uniform one.
//
// The min keeps it a one-sided relaxation. A bare self-reference would demand
// 0.86 of a child that inherited μ=0.9 and gate it out on a single failure —
// the family's memory turning into an absolute floor, which is not what
// ADR-0013 asked for. That memory already works where it belongs: in the
// posterior mean the Thompson tournament samples.
func gateBar(c *core.Connection) float64 {
	if c == nil {
		return QuantileQ - gateMargin
	}
	a, b := c.Prior()
	return math.Min(QuantileQ, core.BetaQuantile(a, b, QuantileQ)) - gateMargin
}

// GatePass reports whether ONE connection clears its own bar — the per-
// connection question rehabilitation asks (ADR-0015: 名誉回復 = 悲観ゲートへの
// 再入場). This is a finer grain than Choose's gate, which under ADR-0042 対案2
// clears a provider only if EVERY same-granularity sibling passes: a connection
// can GatePass on its own yet still see its provider gated out by a failing
// sibling. nil is the blank slate: pass.
func GatePass(c *core.Connection, nowMs int64) bool {
	if c == nil {
		return true
	}
	return c.QuantileAt(nowMs, QuantileQ) >= gateBar(c)
}

// firstWins samples the pairwise preference "a preferred over b" n times and
// averages (ADR-0012 Decision 4). a and b arrive lexically ordered (the
// caller iterates a sorted list), matching the canonical "a~b" target where
// y=1 means the lexical first won. An exact 0.5 tie goes to a — the same
// lexical tie-break every other deterministic path uses.
func firstWins(rng *rand.Rand, conns []*core.Connection, tokens []string, a, b string, n int, nowMs int64) bool {
	target := a + "~" + b
	c := finestMatch(conns, core.ConnPreference, target, tokens)
	sum := 0.0
	for i := 0; i < n; i++ {
		if c != nil {
			sum += c.SampleAt(rng, nowMs)
		} else {
			sum += core.SampleBeta(rng, core.PriorAlpha, core.PriorBeta)
		}
	}
	return sum/float64(n) >= 0.5
}

// gateAll builds a provider's capability candidate under ADR-0042 対案2. The
// selection side (Thompson Sampling, firstWins) still reads the single finest
// match, but the pessimistic gate reads EVERY finest-granularity match and
// clears the provider only if all of them clear their own bar — 選ぶのは一つ、
// 拒否は全員ができる, revising ADR-0013 Decision 2's「判断は最細一致のみを読む」
// for the gate alone.
//
// This exists because finestMatch breaks same-granularity ties lexically
// (「監査が一意の行を指すため」), so under {cap=implement, lang=rust} the cap=
// connection always shadowed the lang= one — a provider that had failed rust
// 11 times still passed on its healthy cap=implement row (ADR-0042 実測: 選択
// 分布 claude 81/200, lang=rust connection 一度も読まれず). Reading only the
// finest match let the decision stay blind to a danger the ledger already held.
//
// The min over siblings is confined to the gate decision alone. A passer keeps
// the finest match it read in ScopeKey/Quantile; only when the provider is
// gated out does the audit swap in the refuser — the most pessimistic sibling
// that missed its own bar (lexical ScopeKey breaks equal-quantile ties, like
// finestMatch, so the row never depends on the store's ORDER BY). Selection
// never touches the swapped value: readQuantile carries the finest quantile for
// the fallback's least-bad ranking, and the passer tie-break only compares
// passers (never swapped). 「なぜ落ちたか」は拒否者を、「何を読んで選んだか」は
// 最細を指す。On a ledger with no same-granularity ties the only sibling is the
// finest match itself, so a pass leaves the finest row and a fail names that
// same row — bit-identical to reading one connection (ADR-0038 不変条件).
func gateAll(conns []*core.Connection, kind, provider string, tokens []string, nowMs int64) Candidate {
	sel := finestMatch(conns, kind, provider, tokens)
	cand := Candidate{Provider: provider, Quantile: blankQuantile, readQuantile: blankQuantile, Passed: true, Wins: -1}
	if sel == nil {
		return cand // blank slate: bar = q − margin, blank quantile = q, passes
	}
	bestLen := len(sel.Scope())
	selQ := sel.QuantileAt(nowMs, QuantileQ)
	cand.ScopeKey = sel.ScopeKey
	cand.Quantile = selQ
	cand.readQuantile = selQ

	var refuser *core.Connection
	refuserQ := math.Inf(1)
	for _, c := range conns {
		if c.Kind != kind || c.Target != provider {
			continue
		}
		scope := c.Scope()
		if len(scope) != bestLen || !scope.SubsetOf(tokens) {
			continue
		}
		q := c.QuantileAt(nowMs, QuantileQ)
		if q >= gateBar(c) {
			continue
		}
		if q < refuserQ || (q == refuserQ && c.ScopeKey < refuser.ScopeKey) {
			refuser, refuserQ = c, q
		}
	}
	if refuser != nil {
		cand.Passed = false
		cand.ScopeKey = refuser.ScopeKey
		cand.Quantile = refuserQ
	}
	return cand
}

// KnowsCapability reports whether the ledger holds any capability connection
// for target readable under tokens — i.e. whether Choose's gate would read a
// real row rather than the Beta(1,1) blank slate. It answers by the same
// finest-match subset rule the decision itself reads with (ADR-0013 Decision
// 2), so "knowing a target in this context" and "deciding in this context"
// can never diverge on what the context is. ADR-0043 Decision 4 gates human's
// candidacy on this: ignorance is full exploration for a provider, but for
// human it would be handing the task back to the user.
func KnowsCapability(conns []*core.Connection, target string, tokens []string) bool {
	return finestMatch(conns, core.ConnCapability, target, tokens) != nil
}

// finestMatch returns the finest-scoped connection of the kind/target whose
// scope is contained in tokens — the ONE connection the Decision Engine
// reads (ADR-0013 Decision 2: 判断は最細一致のみを読む). Ties on granularity
// break lexically so the audit trail names a unique row.
func finestMatch(conns []*core.Connection, kind, target string, tokens []string) *core.Connection {
	var best *core.Connection
	bestLen := -1
	for _, c := range conns {
		if c.Kind != kind || c.Target != target {
			continue
		}
		scope := c.Scope()
		if !scope.SubsetOf(tokens) {
			continue
		}
		if len(scope) > bestLen || (len(scope) == bestLen && c.ScopeKey < best.ScopeKey) {
			best, bestLen = c, len(scope)
		}
	}
	return best
}

// Wobble is the judgment's flicker (ADR-0016: 判断の揺らぎ): run the same
// seeded decision m times and return the rate at which the winner disagrees
// with the modal winner. 0 = the lottery always names the same provider.
// Reused by VoI (ADR-0016) and the S4 sharpness gate (ADR-0017).
func Wobble(conns []*core.Connection, providers, tokens []string, size string, m int, seed, nowMs int64) float64 {
	if m <= 0 || len(providers) == 0 {
		return 0
	}
	counts := map[string]int{}
	for i := 0; i < m; i++ {
		d := Choose(conns, providers, tokens, size, seed+int64(i), nowMs)
		counts[d.Provider]++
	}
	max := 0
	for _, n := range counts {
		if n > max {
			max = n
		}
	}
	return 1 - float64(max)/float64(m)
}

// PairWobble is Wobble for one preference posterior directly: the rate at
// which n=1 draws land on opposite sides of even. Absent connection =
// Beta(1,1), the maximum-flicker blank slate. Used by the Preference Gap's
// VoI, whose judgment is exactly this pair (ADR-0016 Decision 2).
func PairWobble(c *core.Connection, m int, seed, nowMs int64) float64 {
	if m <= 0 {
		return 0
	}
	rng := rand.New(rand.NewSource(seed))
	wins := 0
	for i := 0; i < m; i++ {
		x := 0.0
		if c != nil {
			x = c.SampleAt(rng, nowMs)
		} else {
			x = core.SampleBeta(rng, core.PriorAlpha, core.PriorBeta)
		}
		if x >= 0.5 {
			wins++
		}
	}
	if wins*2 < m {
		wins = m - wins
	}
	return 1 - float64(wins)/float64(m)
}
