// Package reflection is the organ that mirrors Tomo's discoveries back to
// the human (ADR-0015): Curiosity learns for Tomo, Tomo's question asks for
// Tomo — Reflection alone exists so the *user* grows. It is a projection:
// candidates are derived from ledger events already computed elsewhere, the
// telling is never stored, and only two facts become truth — 「語った事実」
// (tomo.reflected) and 「反応」 (experiences kind='reflection').
package reflection

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

// Knobs (ADR-0015 Consequences).
const (
	// BudgetWindowMs: 1日1つ — the question budget's shape, borrowed.
	BudgetWindowMs = 24 * 3600 * 1000

	// ReversalBand is the hysteresis width on 逆転 detection (ADR-0002's
	// Schmitt trigger, borrowed): the new leader must be ahead by this much
	// in posterior mean, so a photo-finish jitter is never narrated.
	ReversalBand = 0.1
)

// Reaction vocabulary (Outcome.Reaction).
const (
	ReactionUnexpected = "unexpected" // 意外 — the telling had value
	ReactionKnown      = "known"      // 知ってた — low value
	ReactionWrong      = "wrong"      // それ違う — refutes the content itself
)

// Candidate is one tellable ledger event.
type Candidate struct {
	Type     string     // voice.Insight* (5 trigger types)
	Scope    core.Scope // the connection's scope — where feedback lands
	Diff     core.Scope // split only: the distinguishing tokens (for the line)
	Provider string     // the subject (winner, for reversal)
	Other    string     // reversal only: the overtaken provider
	OldToken string     // reperceived only: the attribution that moved away
	NewToken string     // reperceived only: the attribution that moved in
}

// Text renders the candidate's spoken line (deterministic voice templates —
// the LLM verbalization seat of ADR-0015 Decision 4 stays reserved, but the
// mirror must not need Ollama awake to speak).
func (c Candidate) Text() string {
	switch c.Type {
	case voice.InsightSplit:
		return voice.ReflectSplit(c.Diff, c.Provider)
	case voice.InsightReversal:
		// The human overtaking Tomo's providers gets its own line — the
		// mirror also reflects the user's growth (ADR-0019 Decision 3;
		// ADR-0018 Decision 4: Companionshipの核心).
		if c.Provider == "human" {
			return voice.ReflectHumanReversal(c.Scope, c.Other)
		}
		return voice.ReflectReversal(c.Scope, c.Provider, c.Other)
	case voice.InsightQuestioned:
		return voice.ReflectQuestioned(c.Scope, c.Provider)
	case voice.InsightRehabilitated:
		return voice.ReflectRehabilitated(c.Scope, c.Provider)
	case voice.InsightReperceived:
		return voice.ReflectReperceived(c.OldToken, c.NewToken)
	}
	return ""
}

// Snapshot is the derived state of every connection before an Apply batch —
// what Detect compares the after-state against. All four triggers are
// already-computed derivations; no new detector exists (ADR-0015 Decision 2).
type Snapshot struct {
	entries map[string]snapEntry
	leaders map[string]leader // capability scopes with ≥2 providers
	nowMs   int64
}

type snapEntry struct {
	state    string
	gatePass bool
	mean     float64
}

type leader struct {
	provider string
	mean     float64
}

func connKey(c *core.Connection) string {
	return c.Kind + "\x00" + c.ScopeKey + "\x00" + c.Target
}

// TakeSnapshot derives the before-state. Call it immediately before the
// perception/Apply batch whose discoveries should be tellable.
func TakeSnapshot(repo core.Repo, nowMs int64) (*Snapshot, error) {
	en := &core.Engine{Repo: repo}
	conns, err := repo.AllConnections()
	if err != nil {
		return nil, err
	}
	s := &Snapshot{entries: map[string]snapEntry{}, nowMs: nowMs}
	for _, c := range conns {
		sum, err := en.LedgerSum(c, nowMs)
		if err != nil {
			return nil, err
		}
		s.entries[connKey(c)] = snapEntry{
			state:    c.State(nowMs, sum),
			gatePass: decide.GatePass(c, nowMs),
			mean:     c.Mean(nowMs),
		}
	}
	s.leaders = leadersOf(conns, nowMs)
	return s, nil
}

// leadersOf maps each capability scope with at least two providers to its
// posterior-mean leader (ties break lexically — deterministic).
func leadersOf(conns []*core.Connection, nowMs int64) map[string]leader {
	byScope := map[string][]*core.Connection{}
	for _, c := range conns {
		if c.Kind == core.ConnCapability {
			byScope[c.ScopeKey] = append(byScope[c.ScopeKey], c)
		}
	}
	out := map[string]leader{}
	for key, group := range byScope {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].Target < group[j].Target })
		best := leader{provider: group[0].Target, mean: group[0].Mean(nowMs)}
		for _, c := range group[1:] {
			if m := c.Mean(nowMs); m > best.mean {
				best = leader{provider: c.Target, mean: m}
			}
		}
		out[key] = best
	}
	return out
}

// Detect compares the before-snapshot with the repo's current state and
// returns every tellable event, deterministically ordered.
func Detect(before *Snapshot, repo core.Repo, nowMs int64) ([]Candidate, error) {
	en := &core.Engine{Repo: repo}
	conns, err := repo.AllConnections()
	if err != nil {
		return nil, err
	}

	var cands []Candidate
	for _, c := range conns {
		prev, existed := before.entries[connKey(c)]

		// Splitの誕生 — a meaningful distinction was discovered.
		if c.ParentKey != "" && !existed {
			diff := core.Scope(c.Scope().Minus(core.ParseScopeKey(c.ParentKey)))
			cands = append(cands, Candidate{
				Type: voice.InsightSplit, Scope: c.Scope(), Diff: diff, Provider: c.Target,
			})
			continue
		}

		sum, err := en.LedgerSum(c, nowMs)
		if err != nil {
			return nil, err
		}

		// Questioned — the surprise ledger surfaced (existing mechanism).
		if state := c.State(nowMs, sum); state == core.StateQuestioned &&
			(!existed || prev.state != core.StateQuestioned) {
			cands = append(cands, Candidate{
				Type: voice.InsightQuestioned, Scope: c.Scope(), Provider: c.Target,
			})
		}

		// 名誉回復 — re-entry through the pessimistic gate (ADR-0012).
		if c.Kind == core.ConnCapability && existed && !prev.gatePass &&
			decide.GatePass(c, nowMs) {
			cands = append(cands, Candidate{
				Type: voice.InsightRehabilitated, Scope: c.Scope(), Provider: c.Target,
			})
		}
	}

	// 逆転 — the posterior-mean leadership crossed, beyond the hysteresis
	// band, at a scope that had a leader before.
	for key, now := range leadersOf(conns, nowMs) {
		prev, existed := before.leaders[key]
		if !existed || now.provider == prev.provider {
			continue
		}
		runnerUp := 0.0
		for _, c := range conns {
			if c.Kind == core.ConnCapability && c.ScopeKey == key && c.Target != now.provider {
				if m := c.Mean(nowMs); m > runnerUp {
					runnerUp = m
				}
			}
		}
		if now.mean-runnerUp < ReversalBand {
			continue
		}
		cands = append(cands, Candidate{
			Type: voice.InsightReversal, Scope: core.ParseScopeKey(key),
			Provider: now.provider, Other: prev.provider,
		})
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Type != cands[j].Type {
			return cands[i].Type < cands[j].Type
		}
		if k1, k2 := cands[i].Scope.Key(), cands[j].Scope.Key(); k1 != k2 {
			return k1 < k2
		}
		return cands[i].Provider < cands[j].Provider
	})
	return cands, nil
}

// ReperceptionCandidates is the 5th trigger (ADR-0019 Decision 4): compare
// the current-experience view before and after a perceive batch — a session
// whose current extraction was superseded at a higher version with different
// tokens means idle work re-read the past and the attribution moved.
func ReperceptionCandidates(before, after []*core.Experience) []Candidate {
	type key struct{ session, kind string }
	prev := map[key]*core.Experience{}
	for _, e := range before {
		prev[key{e.SessionID, e.Kind}] = e
	}
	var cands []Candidate
	for _, e := range after {
		old, ok := prev[key{e.SessionID, e.Kind}]
		if !ok || e.ExtractorVer <= old.ExtractorVer {
			continue
		}
		oldTok, newTok := firstDiff(old.Tokens(), e.Tokens())
		if oldTok == "" && newTok == "" {
			continue // same reading — nothing moved, nothing to tell
		}
		cands = append(cands, Candidate{
			Type:     voice.InsightReperceived,
			Scope:    core.NewScope(e.Tokens()...),
			Provider: e.Target(),
			OldToken: displayValue(oldTok),
			NewToken: displayValue(newTok),
		})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Scope.Key() < cands[j].Scope.Key() })
	return cands
}

// firstDiff returns the first token present only in old and the first
// present only in new (either may be empty).
func firstDiff(old, new []string) (onlyOld, onlyNew string) {
	oldSet := map[string]bool{}
	for _, t := range old {
		oldSet[t] = true
	}
	newSet := map[string]bool{}
	for _, t := range new {
		newSet[t] = true
	}
	for _, t := range old {
		if !newSet[t] {
			onlyOld = t
			break
		}
	}
	for _, t := range new {
		if !oldSet[t] {
			onlyNew = t
			break
		}
	}
	return onlyOld, onlyNew
}

// displayValue renders one "k=v" token as its value, matching how scopes
// speak (ADR-0009 Decision 2).
func displayValue(tok string) string {
	if tok == "" {
		return ""
	}
	if _, v, ok := strings.Cut(tok, "="); ok {
		return v
	}
	return tok
}

// HasBudget: no tomo.reflected in the trailing window (the question budget's
// shape — a skip still spends it, ADR-0007の型).
func HasBudget(s *store.Store, nowMs int64) (bool, error) {
	ts, found, err := s.LastEventTS("tomo.reflected")
	if err != nil {
		return false, err
	}
	return !found || nowMs-ts >= BudgetWindowMs, nil
}

// Pick selects which candidate to tell: one Thompson draw per candidate from
// its type's reaction posterior (ADR-0015 Decision 3 — 語りの選択もJudgment
// by Math; the sampler is ADR-0012's). Blank types draw from Beta(1,1) and
// explore. Returns the seed's winner; deterministic per (ledger, seed).
func Pick(cands []Candidate, exps []*core.Experience, seed, nowMs int64) Candidate {
	k := map[string]float64{}
	n := map[string]float64{}
	for _, e := range exps {
		if e.Kind != core.KindReflection {
			continue
		}
		y, ok := mirrorWeight(e.Outcome)
		if !ok {
			continue
		}
		w := core.DecayFactor(e.TS, nowMs)
		k[e.Outcome.Insight] += w * y
		n[e.Outcome.Insight] += w
	}
	rng := rand.New(rand.NewSource(seed))
	best, bestDraw := 0, -1.0
	for i, c := range cands {
		draw := core.SampleBeta(rng,
			core.PriorAlpha+k[c.Type], core.PriorBeta+(n[c.Type]-k[c.Type]))
		if draw > bestDraw {
			best, bestDraw = i, draw
		}
	}
	return cands[best]
}

// mirrorWeight is the mirror's own ledger weight (選球眼): 意外 and それ違う
// both mark a telling that moved information (a provoked correction is the
// mirror doing its job — the content penalty flows separately through the
// verdict), 知ってた marks a wasted telling.
func mirrorWeight(o core.Outcome) (float64, bool) {
	switch o.Reaction {
	case ReactionUnexpected, ReactionWrong:
		return 1, true
	case ReactionKnown:
		return 0, true
	}
	return 0, false
}

// Ask prints the telling and reads the reaction. Anything but 1/2/3
// (including EOF) is a free skip.
func Ask(in *bufio.Reader, out io.Writer, text string) (reaction string) {
	fmt.Fprintf(out, "\n「%s」\n%s", text, voice.ReflectPrompt())
	line, _ := in.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		return ReactionUnexpected
	case "2":
		return ReactionKnown
	case "3":
		return ReactionWrong
	default:
		return ""
	}
}

// RecordAndApply writes the two truths: the telling (tomo.reflected — a skip
// still spends the budget) and, when the user reacted, the reflection
// experience. 「それ違う」 additionally carries a layer-2 verdict against the
// subject connection (ADR-0015 Decision 3: 内容の出所へ), which Apply routes
// through the normal capability path without births.
func RecordAndApply(s *store.Store, en *core.Engine, cand Candidate, seed int64, reaction, askedAfter string, extractorVer int, nowMs int64) error {
	sid := store.NewID(nowMs)
	if err := s.AppendEvent(sid, "tomo.reflected", nowMs, map[string]any{
		"type":     cand.Type,
		"scope":    []string(cand.Scope),
		"provider": cand.Provider,
		"other":    cand.Other,
		"text":     cand.Text(),
		"seed":     strconv.FormatInt(seed, 10),
		"after":    askedAfter,
	}); err != nil {
		return fmt.Errorf("record tomo.reflected: %w", err)
	}
	if reaction == "" {
		return nil
	}

	outcome := core.Outcome{Insight: cand.Type, Reaction: reaction}
	if reaction == ReactionWrong {
		outcome.Verdict = "down"
	}
	exp := &core.Experience{
		ID: store.NewID(nowMs), SessionID: sid, TS: nowMs,
		Kind: core.KindReflection, ExtractorVer: extractorVer,
		ExtractorModel: "deterministic",
		Context:        scopeContext(cand.Scope),
		Provider:       cand.Provider,
		Outcome:        outcome,
		Source:         "learning",
	}
	if err := s.InsertExperiences([]*core.Experience{exp}); err != nil {
		return fmt.Errorf("insert reflection experience: %w", err)
	}
	if err := en.Apply(exp); err != nil {
		return fmt.Errorf("apply reflection: %w (experience is saved; run `tomobit rebuild` to repair the projection)", err)
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
// reflection experience inherits exactly the candidate's scope (the same
// shape curiosity uses for answers).
func scopeContext(scope core.Scope) map[string]string {
	ctx := map[string]string{}
	for _, tok := range scope {
		if k, v, ok := strings.Cut(tok, "="); ok && k != "" && v != "" {
			ctx[k] = v
		}
	}
	return ctx
}
