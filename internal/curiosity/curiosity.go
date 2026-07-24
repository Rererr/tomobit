// Package curiosity derives Tomo's questions from the Knowledge Network.
// A Preference Gap is a View over connections and experiences (ADR-0007
// Decision 2): nothing here is stored — it is recomputed the moment before
// Tomo asks, so it can never go stale.
package curiosity

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

// Knobs (ADR-0007 Consequences: the numbers a human decides).
//
// NMin/FMin sit at 2.5, not 3.0: n fresh experiences decay to just under n
// the instant they are observed, so a 3.0 gate would demand a 4th use forever
// (measured in the organic E2E — 3+3 runs left evidence at 2.9999). 2.5 keeps
// the intent "3 uses open the gate" and lets it stay open for ~24 days of
// decay. EMax mirrors the same trap in reverse: at 1.0 a single answered
// question (evidence 1.0) would fall below the ceiling seconds later and Tomo
// would re-ask a preference it already knows; 0.5 keeps one answer binding
// for one half-life (~90 days), after which the preference is stale enough
// that asking again is the desired behavior (ADR-0007 追記).
const (
	ThetaEven = 1.0 // nats: providers are indistinguishable below this ln BF
	NMin      = 2.5 // effective evidence each capability Connection needs
	FMin      = 2.5 // decayed scope frequency worth a question

	// EMax: "we don't know the preference yet" ceiling. The value lives in
	// core (PrefKnownMin) because the face's S5 gate must read the same
	// number and cannot import this package (see core's comment).
	EMax = core.PrefKnownMin

	// BudgetWindowMs is the rolling window in which a single tomo.asked spends
	// the whole budget (ADR-0007 Decision 3).
	BudgetWindowMs = 24 * 3600 * 1000

	// VoIDraws is M (ADR-0016): how many judgment-lottery draws measure the
	// wobble. The seed is fixed so the ordering is a deterministic View —
	// the same ledger always ranks its gaps the same way.
	VoIDraws = 64
	voiSeed  = 1
)

// Gap is a scope where two providers are indistinguishable on capability,
// seen often, and never yet compared on preference (ADR-0007 Decision 2).
type Gap struct {
	Scope    core.Scope
	ScopeKey string
	A, B     string // providers, lexicographically A < B
	LnBF     float64
	Freq     float64 // decayed scope frequency
	Wobble   float64 // TS-lottery winner split rate (ADR-0016)
	VoI      float64 // Freq × Wobble = priority
}

// Gaps derives every open Preference Gap at nowMs, ordered by Value of
// Information (ADR-0016 Decision 2): 到来頻度 × 判断の揺らぎ. Ties break on
// scope_key then pair — fully deterministic (fixed wobble seed).
// 不確実性は好奇心の理由にならない。判断が変わることだけが理由になる。
func Gaps(repo core.Repo, nowMs int64) ([]Gap, error) {
	conns, err := repo.AllConnections()
	if err != nil {
		return nil, err
	}
	exps, err := repo.CurrentExperiences()
	if err != nil {
		return nil, err
	}

	providersByScope := map[string][]string{}
	plansByScope := map[string][]string{}
	betEvidence := map[string]float64{}
	prefEvidence := map[string]float64{}
	prefConns := map[string]*core.Connection{}
	for _, c := range conns {
		switch c.Kind {
		case core.ConnCapability:
			providersByScope[c.ScopeKey] = append(providersByScope[c.ScopeKey], c.Target)
			betEvidence[c.ScopeKey+"\x00"+c.Target] = c.Evidence(nowMs)
		case core.ConnPlan:
			// Plans are the second bet target (ADR-0014 Decision 1) — Tomo
			// may also ask 「どっちの手順が好みだった?」.
			plansByScope[c.ScopeKey] = append(plansByScope[c.ScopeKey], c.Target)
			betEvidence[c.ScopeKey+"\x00"+c.Target] = c.Evidence(nowMs)
		case core.ConnPreference:
			prefEvidence[c.ScopeKey+"\x00"+c.Target] = c.Evidence(nowMs)
			prefConns[c.ScopeKey+"\x00"+c.Target] = c
		}
	}

	var gaps []Gap
	// stats reads the outcome tallies keyed by the given attribute of each
	// matching experience: the provider for capability bets, the plan for
	// plan bets.
	collect := func(byScope map[string][]string, targetOf func(*core.Experience) string) {
		for scopeKey, targets := range byScope {
			if len(targets) < 2 {
				continue
			}
			sort.Strings(targets)
			scope := core.ParseScopeKey(scopeKey)
			freq, k, n := scopeStats(scope, exps, nowMs, targetOf)
			if freq < FMin { // frequent gate
				continue
			}
			for i := 0; i < len(targets); i++ {
				for j := i + 1; j < len(targets); j++ {
					a, b := targets[i], targets[j]
					// Even gate. Evidence guards against calling a Bayes factor
					// neutral-by-ignorance "even": that is a Knowledge Gap, not a
					// Preference Gap (ADR-0007 Decision 2).
					if betEvidence[scopeKey+"\x00"+a] < NMin || betEvidence[scopeKey+"\x00"+b] < NMin {
						continue
					}
					lnBF := core.LnBF(k[a], n[a], k[b], n[b])
					if lnBF >= ThetaEven {
						continue
					}
					// No-evidence gate: an absent preference Connection is 0.
					if prefEvidence[scopeKey+"\x00"+a+"~"+b] >= EMax {
						continue
					}
					// The gap's judgment is exactly this pair's lottery, so its
					// wobble prices the question (ADR-0016: PreferenceGapの発火
					// 条件は実はVoIの特殊例だった). The preference connection is
					// usually absent here — Beta(1,1), maximum wobble.
					wobble := decide.PairWobble(
						prefConns[scopeKey+"\x00"+a+"~"+b], VoIDraws, voiSeed, nowMs)
					gaps = append(gaps, Gap{
						Scope: scope, ScopeKey: scopeKey,
						A: a, B: b, LnBF: lnBF, Freq: freq,
						Wobble: wobble, VoI: freq * wobble,
					})
				}
			}
		}
	}
	collect(providersByScope, func(e *core.Experience) string { return e.Target() })
	collect(plansByScope, func(e *core.Experience) string { return e.Plan })

	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].VoI != gaps[j].VoI {
			return gaps[i].VoI > gaps[j].VoI
		}
		if gaps[i].ScopeKey != gaps[j].ScopeKey {
			return gaps[i].ScopeKey < gaps[j].ScopeKey
		}
		if gaps[i].A != gaps[j].A {
			return gaps[i].A < gaps[j].A
		}
		return gaps[i].B < gaps[j].B
	})
	return gaps, nil
}

// scopeStats returns the decayed scope frequency and, per bet target, the
// decayed (k, n) over usable execution experiences matching the scope.
// targetOf selects which attribute the tallies key on (provider or plan).
func scopeStats(scope core.Scope, exps []*core.Experience, nowMs int64, targetOf func(*core.Experience) string) (freq float64, k, n map[string]float64) {
	k, n = map[string]float64{}, map[string]float64{}
	for _, e := range exps {
		if e.Kind != core.KindExecution || !scope.SubsetOf(e.Tokens()) {
			continue
		}
		w := core.DecayFactor(e.TS, nowMs)
		// Frequency counts every scope-matching run, including cancelled ones
		// with no usable outcome: it measures "this context keeps coming up",
		// not evidence. Filtering by OutcomeWeight would conflate the two.
		freq += w
		y, ok := core.OutcomeWeight(e)
		if !ok {
			continue
		}
		p := targetOf(e)
		if p == "" {
			continue
		}
		k[p] += w * y
		n[p] += w
	}
	return freq, k, n
}

// HasBudget reports whether Tomo may ask now: no tomo.asked in the trailing
// BudgetWindowMs (ADR-0007 Decision 3 — the budget is a View over events).
func HasBudget(s *store.Store, nowMs int64) (bool, error) {
	ts, found, err := s.LastEventTS("tomo.asked")
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	return nowMs-ts >= BudgetWindowMs, nil
}

// Ask prints the one fixed question (ADR-0007 Decision 4: a Go template, no
// LLM) and maps the reply: "1" -> A wins, "2" -> B wins, anything else
// (including EOF) is a skip.
func Ask(in *bufio.Reader, out io.Writer, gap Gap) (preferred, over string, answered bool) {
	fmt.Fprintf(out, "「%s」 [1=%s / 2=%s / Enter=スキップ] ",
		voice.Asked(gap.Scope, gap.A, gap.B), gap.A, gap.B)
	line, _ := in.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		return gap.A, gap.B, true
	case "2":
		return gap.B, gap.A, true
	default:
		return "", "", false
	}
}

// RecordAndPerceive writes the question to a dedicated session and, on an
// answer, perceives it deterministically (ADR-0007 Decision 4).
//
// The ask session never holds a task.finished, so deferred perception skips
// it; only this synchronous path ever perceives it. Recording onto the do's
// session was rejected: perception would copy the do's context onto the
// preference experience, but the Gap's scope is independent of the do.
func RecordAndPerceive(s *store.Store, en *core.Engine, gap Gap, preferred, over string, answered bool, askedAfter string, extractorVer int, nowMs int64) error {
	askSession := store.NewID(nowMs)
	asked := map[string]any{
		"scope":       []string(gap.Scope),
		"pair":        []string{gap.A, gap.B},
		"ln_bf":       gap.LnBF,
		"freq":        gap.Freq,
		"wobble":      gap.Wobble,
		"voi":         gap.VoI,
		"asked_after": askedAfter,
	}
	if err := s.AppendEvent(askSession, "tomo.asked", nowMs, asked); err != nil {
		return fmt.Errorf("record tomo.asked: %w", err)
	}
	// A skip still spends the budget — that stress is the design (ADR-0007
	// Decision 3): record the question, learn nothing.
	if !answered {
		return nil
	}
	if err := s.AppendEvent(askSession, "user.preference", nowMs,
		map[string]any{"preferred": preferred, "over": over}); err != nil {
		return fmt.Errorf("record user.preference: %w", err)
	}
	exp := &core.Experience{
		ID: store.NewID(nowMs), SessionID: askSession, TS: nowMs,
		Kind: core.KindPreference, ExtractorVer: extractorVer,
		ExtractorModel: "deterministic",
		Context:        scopeContext(gap.Scope),
		Outcome:        core.Outcome{Preferred: preferred, Over: over},
		Source:         "learning",
	}
	if err := s.InsertExperiences([]*core.Experience{exp}); err != nil {
		return fmt.Errorf("insert preference experience: %w", err)
	}
	if err := en.Apply(exp); err != nil {
		return fmt.Errorf("apply preference: %w (experience is saved; run `tomobit rebuild` to repair the projection)", err)
	}
	// This experience is its own batch (ADR-0037 Decision 2): reconcile at
	// its boundary so merge judgment reaches children this Apply didn't
	// touch, the same reach Rebuild's closing sweep already has.
	if err := en.ReconcileMerges(nowMs); err != nil {
		return fmt.Errorf("reconcile merges: %w (experience is saved; run `tomobit rebuild` to repair the projection)", err)
	}
	return nil
}

// scopeContext parses "k=v" scope tokens back into a context map — the
// preference experience inherits exactly the Gap's scope.
func scopeContext(scope core.Scope) map[string]string {
	ctx := map[string]string{}
	for _, tok := range scope {
		if k, v, ok := strings.Cut(tok, "="); ok && k != "" && v != "" {
			ctx[k] = v
		}
	}
	return ctx
}
