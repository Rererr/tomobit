package core

import (
	"fmt"
	"math"
	"sort"
)

// Repo is the storage the engine needs. Implemented by internal/store.
type Repo interface {
	GetConnection(kind, scopeKey, target string) (*Connection, error)
	UpsertConnection(c *Connection) error
	DeleteConnection(kind, scopeKey, target string) error
	ConnectionsFor(kind, target string) ([]*Connection, error)
	AllConnections() ([]*Connection, error)

	InsertLedger(e *LedgerEntry) error
	LedgerFor(kind, scopeKey, target string) ([]*LedgerEntry, error)
	DeleteLedgerFor(kind, scopeKey, target string) error

	// CurrentExperiences returns experiences_current ordered by (ts, id).
	CurrentExperiences() ([]*Experience, error)

	ClearProjections() error
}

// Engine applies experiences to the Knowledge Network.
type Engine struct {
	Repo Repo
}

// Apply folds one experience into the projections: births granularity-1
// connections, records surprise on every matching connection, updates
// posteriors, then summons the judgment where the ledger has surfaced.
//
// All time arithmetic is anchored to exp.TS, never the wall clock, and the
// judgment only ever consults experiences at or before exp.TS. Rebuild is
// therefore the canonical form: replaying experiences_current in (ts, id)
// order reproduces the live state exactly, provided the live stream was
// applied in that same order. If a live Apply ever runs out of (ts, id)
// order the projection can diverge; `tomobit rebuild` restores the
// canonical state.
func (en *Engine) Apply(exp *Experience) error {
	y, ok := OutcomeWeight(exp)
	if !ok {
		return nil // no outcome signal (e.g. cancelled) — nothing to learn
	}
	tokens := exp.Tokens()
	if len(tokens) == 0 {
		return nil
	}

	// Second bet target (ADR-0014 Decision 1): the same experience also
	// feeds the plan's ledger — same birth, same surprise, same judgment.
	// The attribution blur between plan and provider is the ADR's
	// documented weakness, kept small by the menu cap and decay.
	if exp.Kind == KindExecution && exp.Plan != "" {
		if err := en.applyTo(ConnPlan, exp.Plan, exp, y, tokens); err != nil {
			return err
		}
	}

	kind, target := exp.ConnKind(), exp.Target()
	if target == "" || target == "~" {
		return nil
	}
	return en.applyTo(kind, target, exp, y, tokens)
}

// applyTo folds one weighted outcome into every connection of one
// (kind, target) bet: birth at granularity 1, surprise ledger, posterior
// update, then judgment.
func (en *Engine) applyTo(kind, target string, exp *Experience, y float64, tokens []string) error {

	// Born: coarse granularity only (ADR-0001) — one single-token
	// connection per attribute. Finer scopes exist only through Split. A
	// reflection reaction (ADR-0015) skips this: 「それ違う」 is feedback on
	// knowledge that already exists, and the mirror's bookkeeping must never
	// birth capability structure of its own.
	if exp.Kind != KindReflection {
		for _, t := range tokens {
			key := NewScope(t).Key()
			c, err := en.Repo.GetConnection(kind, key, target)
			if err != nil {
				return err
			}
			if c == nil {
				// Parentless birth: the prior is the blank Beta(1,1) (ADR-0003;
				// ADR-0013 Decision 4 keeps it as the no-ancestor initial value).
				c = &Connection{
					Kind: kind, ScopeKey: key, Target: target,
					Alpha: PriorAlpha, Beta: PriorBeta,
					PriorA: PriorAlpha, PriorB: PriorBeta,
					LastUpdate: exp.TS, BornTS: exp.TS,
				}
				if err := en.Repo.UpsertConnection(c); err != nil {
					return err
				}
			}
		}
	}

	// Every matching connection records its own surprise against its own
	// prediction, then observes (ADR-0002: 多粒度の帰属).
	conns, err := en.Repo.ConnectionsFor(kind, target)
	if err != nil {
		return err
	}
	var touched []*Connection
	for _, c := range conns {
		if !c.Scope().SubsetOf(tokens) {
			continue
		}
		p := c.Mean(exp.TS)
		if err := en.Repo.InsertLedger(&LedgerEntry{
			Kind: kind, ScopeKey: c.ScopeKey, Target: target,
			ExperienceID: exp.ID, TS: exp.TS,
			P: p, Y: y, SExcess: ExcessSurprisal(p, y),
		}); err != nil {
			return err
		}
		c.Observe(y, exp.TS)
		if err := en.Repo.UpsertConnection(c); err != nil {
			return err
		}
		touched = append(touched, c)
	}

	// Judgment pass on the touched connections.
	for _, c := range touched {
		if err := en.judge(c, exp.TS); err != nil {
			return err
		}
	}
	return nil
}

// LedgerSum is the decayed cumulative excess surprisal of a connection —
// the trigger statistic (ADR-0002 第一段).
func (en *Engine) LedgerSum(c *Connection, nowMs int64) (float64, error) {
	entries, err := en.Repo.LedgerFor(c.Kind, c.ScopeKey, c.Target)
	if err != nil {
		return 0, err
	}
	sum := 0.0
	for _, e := range entries {
		sum += e.SExcess * DecayFactor(e.TS, nowMs)
	}
	return sum, nil
}

// judge runs the two-stage detector on one connection: the cheap trigger,
// then the Bayes-factor scan (Split), plus the reverse test (Merge) when
// the connection is a child.
func (en *Engine) judge(c *Connection, nowMs int64) error {
	// Merge check for children: fold back when the corrected ln BF of the
	// distinguishing token drops to the neutral point (ADR-0002 hysteresis).
	if c.ParentKey != "" {
		parentScope := ParseScopeKey(c.ParentKey)
		distinguishing := c.Scope().Minus(parentScope)
		if len(distinguishing) == 1 {
			bf, err := en.tokenBF(c.Kind, c.Target, parentScope, distinguishing[0], nowMs)
			if err != nil {
				return err
			}
			if bf <= ThetaMerge {
				if err := en.Repo.DeleteLedgerFor(c.Kind, c.ScopeKey, c.Target); err != nil {
					return err
				}
				return en.Repo.DeleteConnection(c.Kind, c.ScopeKey, c.Target)
			}
		}
	}

	// Split: trigger first (cheap), judgment only when summoned.
	sum, err := en.LedgerSum(c, nowMs)
	if err != nil {
		return err
	}
	if sum <= ThetaTrigger {
		return nil
	}
	return en.split(c, nowMs)
}

// split scans candidate attributes and births the strongest partition whose
// child does not exist yet, replaying its history.
func (en *Engine) split(c *Connection, nowMs int64) error {
	scope := c.Scope()
	exps, err := en.matchingExperiences(c.Kind, c.Target, scope, nowMs)
	if err != nil {
		return err
	}

	// Candidates: tokens present in matching experiences beyond the scope.
	candSet := make(map[string]bool)
	for _, e := range exps {
		for _, t := range e.Tokens() {
			candSet[t] = true
		}
	}
	for _, t := range scope {
		delete(candSet, t)
	}
	if len(candSet) == 0 {
		return nil
	}

	type candidate struct {
		token string
		bf    float64
	}
	scored := make([]candidate, 0, len(candSet))
	for t := range candSet {
		bf, err := en.tokenBF(c.Kind, c.Target, scope, t, nowMs)
		if err != nil {
			return err
		}
		scored = append(scored, candidate{t, bf})
	}
	// Strongest evidence first; token order breaks ties deterministically.
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].bf != scored[j].bf {
			return scored[i].bf > scored[j].bf
		}
		return scored[i].token < scored[j].token
	})

	lnM := math.Log(float64(len(scored))) // multiple-comparison correction
	for _, cand := range scored {
		childScope := scope.Plus(cand.token)
		existing, err := en.Repo.GetConnection(c.Kind, childScope.Key(), c.Target)
		if err != nil {
			return err
		}
		// Skip a partition that already has its own connection and try the
		// next candidate, rather than abandoning the split entirely.
		if existing != nil {
			continue
		}
		// Descending order: once the corrected evidence falls short, no later
		// candidate can clear the bar either.
		if cand.bf-lnM < ThetaSplit {
			return nil
		}

		// Inherited prior (ADR-0013 Decision 1): the parent's posterior mean
		// at split time, scaled to the fixed mass m₀ — 平均だけ継ぎ、確信は
		// 継がない。質量は証拠が運ぶ. This is also the whole backoff story
		// (Decision 2): coarse knowledge flows into the child exactly once,
		// at birth, and decay later sinks the child back to this μ.
		mu := c.Mean(nowMs)
		priorA, priorB := mu*InheritM0, (1-mu)*InheritM0

		// Born with History: replay every matching experience into the child
		// (ADR-0001/0002). The child is born already knowing. Replay keeps
		// the original timestamps (ADR-0013 Decision 3: 元の日付で数え直す)
		// via Observe(y, e.TS) — the same arithmetic Rebuild runs, so the
		// invariant "child (α,β) right after Split == rebuild from the same
		// experiences" holds by construction.
		child := &Connection{
			Kind: c.Kind, ScopeKey: childScope.Key(), Target: c.Target,
			Alpha: priorA, Beta: priorB, PriorA: priorA, PriorB: priorB,
			LastUpdate: 0, BornTS: nowMs, ParentKey: c.ScopeKey,
		}
		for _, e := range exps {
			if !childScope.SubsetOf(e.Tokens()) {
				continue
			}
			if y, ok := OutcomeWeight(e); ok {
				child.Observe(y, e.TS)
			}
		}
		if child.LastUpdate == 0 {
			child.LastUpdate = nowMs
		}
		if err := en.Repo.UpsertConnection(child); err != nil {
			return err
		}
		// The discovery answers the accumulated surprise: reset the parent's
		// ledger so one anomaly does not spawn a sibling per remaining token.
		return en.Repo.DeleteLedgerFor(c.Kind, c.ScopeKey, c.Target)
	}
	return nil
}

// tokenBF computes the corrected-input statistic: ln BF of "token
// partitions the world" within the experiences matching scope+target,
// with evidence decayed to nowMs.
func (en *Engine) tokenBF(kind, target string, scope Scope, token string, nowMs int64) (float64, error) {
	exps, err := en.matchingExperiences(kind, target, scope, nowMs)
	if err != nil {
		return 0, err
	}
	var kWith, nWith, kWithout, nWithout float64
	for _, e := range exps {
		y, ok := OutcomeWeight(e)
		if !ok {
			continue
		}
		w := DecayFactor(e.TS, nowMs)
		if NewScope(token).SubsetOf(e.Tokens()) {
			kWith += w * y
			nWith += w
		} else {
			kWithout += w * y
			nWithout += w
		}
	}
	return LnBF(kWith, nWith, kWithout, nWithout), nil
}

// matchingExperiences returns experiences up to nowMs (inclusive) that match
// kind+target and contain scope. The nowMs bound keeps judgment causal: an
// Apply anchored at exp.TS must never learn from evidence dated after it, or
// live and rebuilt projections would disagree.
func (en *Engine) matchingExperiences(kind, target string, scope Scope, nowMs int64) ([]*Experience, error) {
	all, err := en.Repo.CurrentExperiences()
	if err != nil {
		return nil, err
	}
	var out []*Experience
	for _, e := range all {
		if e.TS > nowMs {
			continue
		}
		if !experienceMatches(e, kind, target) {
			continue
		}
		if scope.SubsetOf(e.Tokens()) {
			out = append(out, e)
		}
	}
	return out, nil
}

// experienceMatches reports whether e is evidence for the (kind, target)
// bet. Plan connections read the plan attribute (ADR-0014); everything else
// keeps the ConnKind/Target mapping.
func experienceMatches(e *Experience, kind, target string) bool {
	if kind == ConnPlan {
		return e.Kind == KindExecution && e.Plan == target
	}
	return e.ConnKind() == kind && e.Target() == target
}

// Rebuild wipes the projections and replays experiences_current in order —
// Knowledge is Rebuildable made executable (ADR-0004 D10).
func (en *Engine) Rebuild() error {
	if err := en.Repo.ClearProjections(); err != nil {
		return err
	}
	exps, err := en.Repo.CurrentExperiences()
	if err != nil {
		return err
	}
	for _, e := range exps {
		if err := en.Apply(e); err != nil {
			return fmt.Errorf("replaying %s: %w", e.ID, err)
		}
	}
	return nil
}
