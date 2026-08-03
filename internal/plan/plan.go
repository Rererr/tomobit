// Package plan implements Plan learning (ADR-0014): plans are a closed menu
// of capability sequences — the ledger's second bet target. The LLM never
// generates a plan; new variants come from pure mutation operators, entry is
// a proposal, selection is math (変異は純関数、採否は数式).
package plan

import (
	"strings"

	"github.com/Rererr/tomobit/internal/store"
)

// Knobs (ADR-0014 Consequences).
const (
	// K is the per-capability survival cap on menu variants (Decision 5).
	K = 5

	// ProposalWindowMs is the proposal budget: at most one plan.generated in
	// the trailing window, checked across the whole event log — not
	// per-capability. It borrows the question budget's shape (ADR-0007's
	// `tomo.asked` check), and that budget is a single harness-wide gate,
	// not one per Preference Gap topic; K's "per-capability" scope
	// (Decision 5) is a separate knob and doesn't extend to this one.
	ProposalWindowMs = 24 * 3600 * 1000

	// maxSteps bounds mutation growth — a plan longer than this stops being
	// a plan and starts being a flowchart.
	maxSteps = 6
)

// Vocabulary is the closed capability vocabulary plans are built from
// (EXECUTION_MODEL.md — Capability). Mutation legality closes over it.
var Vocabulary = []string{
	"analyze", "implement", "review", "refactor", "summarize",
	"test", "benchmark", "commit", "deploy", "notify",
}

// Name is a plan's canonical identity: its steps joined by ">". The steps
// ARE the plan, so the identity is self-describing and collision-free with
// provider names.
func Name(steps []string) string { return strings.Join(steps, ">") }

// Steps parses a canonical name back into its steps.
func Steps(name string) []string {
	if name == "" {
		return nil
	}
	return strings.Split(name, ">")
}

// Initial returns the hand-written starting menu for a capability
// (Decision 2: 初期メニューは手書きの2〜3変種). Capabilities without a
// richer menu get the one-step plan of themselves — the current behavior,
// expressed as a plan.
func Initial(capability string) [][]string {
	switch capability {
	case "implement":
		return [][]string{
			{"analyze", "implement", "test", "review"}, // full
			{"implement", "test"},                      // direct
			{"implement"},                              // quick
		}
	default:
		if capability == "" {
			return nil
		}
		return [][]string{{capability}}
	}
}

// Resolve maps a --plan argument to a canonical plan name: an initial-menu
// label (implement: full / direct / quick) or a canonical ">"-joined steps
// name. ok=false for anything outside the closed vocabulary.
func Resolve(capability, arg string) (string, bool) {
	if capability == "implement" {
		ini := Initial(capability)
		switch arg {
		case "full":
			return Name(ini[0]), true
		case "direct":
			return Name(ini[1]), true
		case "quick":
			return Name(ini[2]), true
		}
	}
	if steps := Steps(arg); Legal(steps) {
		return Name(steps), true
	}
	return "", false
}

// Legal reports whether a step sequence is a valid plan: non-empty, closed
// over the vocabulary, no consecutive duplicates, bounded length
// (Decision 3's legality constraints).
func Legal(steps []string) bool {
	if len(steps) == 0 || len(steps) > maxSteps {
		return false
	}
	vocab := map[string]bool{}
	for _, v := range Vocabulary {
		vocab[v] = true
	}
	for i, s := range steps {
		if !vocab[s] {
			return false
		}
		if i > 0 && steps[i-1] == s {
			return false
		}
	}
	return true
}

// Mutation is one structural edit of an existing plan.
type Mutation struct {
	Op    string // "drop" | "insert" | "swap"
	Steps []string
}

// Mutations enumerates every legal mutation of steps, deterministically:
// drops (left to right), swaps (left to right), inserts (position-major,
// vocabulary order). Pure — same input, same list.
func Mutations(steps []string) []Mutation {
	var out []Mutation
	add := func(op string, s []string) {
		if Legal(s) {
			out = append(out, Mutation{Op: op, Steps: s})
		}
	}
	for i := range steps {
		dropped := append(append([]string{}, steps[:i]...), steps[i+1:]...)
		add("drop", dropped)
	}
	for i := 0; i+1 < len(steps); i++ {
		swapped := append([]string{}, steps...)
		swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
		add("swap", swapped)
	}
	for pos := 0; pos <= len(steps); pos++ {
		for _, v := range Vocabulary {
			inserted := append([]string{}, steps[:pos]...)
			inserted = append(inserted, v)
			inserted = append(inserted, steps[pos:]...)
			add("insert", inserted)
		}
	}
	return out
}

// Live derives the current menu for a capability: the initial variants
// (always offered) plus every proposed variant recorded in plan.generated
// events — minus retirements. Menu membership is derived from truth
// (events), so `tomobit rebuild` cannot lose it; retirement is the
// connection lifecycle doing its job (Decision 5: 勝てないPlanは減衰し
// Retireへ — a dormant plan connection drops its variant from the menu).
func Live(s *store.Store, capability string, nowMs int64) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	for _, steps := range Initial(capability) {
		n := Name(steps)
		names = append(names, n)
		seen[n] = true
	}

	proposed, err := generatedPlans(s, capability)
	if err != nil {
		return nil, err
	}
	for _, n := range proposed {
		if seen[n] {
			continue
		}
		retired, err := isRetired(s, capability, n, nowMs)
		if err != nil {
			return nil, err
		}
		if retired {
			continue
		}
		names = append(names, n)
		seen[n] = true
		if len(names) >= K {
			break
		}
	}
	return names, nil
}

// generatedPlans returns the proposed plan names for a capability, oldest
// first, from the plan.generated event log.
func generatedPlans(s *store.Store, capability string) ([]string, error) {
	rows, err := s.DB.Query(`
		SELECT json_extract(payload, '$.plan') FROM events
		WHERE type = 'plan.generated'
		  AND json_extract(payload, '$.cap') = ?
		ORDER BY id`, capability)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

// isRetired: a proposed variant retires when its plan connection has gone
// dormant (long quiet, evidence decayed). A variant with no connection yet
// is a newborn, not a retiree.
func isRetired(s *store.Store, capability, name string, nowMs int64) (bool, error) {
	scopeKey := "cap=" + capability
	c, err := s.GetConnection("plan", scopeKey, name)
	if err != nil || c == nil {
		return false, err
	}
	// Dormancy without the ledger part (a plan that stopped being chosen
	// gets no surprise): quiet for two half-lives.
	return c.State(nowMs, 0) == "dormant", nil
}

// Propose runs the Curiosity proposal (Decision 3/5): if the menu has room
// and the budget allows, generate the first legal mutation of the current
// menu that has never been proposed before, record plan.generated, and
// return the new variant's name. Priority within the mutation space is
// deterministic enumeration order — every candidate is a blank slate with
// identical wobble, so VoI cannot separate them yet (ADR-0016's tie falls
// back to a fixed order). Returns "" when nothing is proposed.
//
// "Never proposed" is checked against the full plan.generated history, not
// just the live menu: a retired variant has left the menu but its name is
// still spent. Checking against the menu alone would make Propose retrace
// the same drop→swap→insert prefix every window a variant retires — the
// same mutation coming back as "the first one not in the menu" forever,
// starving every candidate after it. Retired names stay excluded from
// enumeration permanently; that's fine because it's not the only way back
// in — an explicit `--plan <steps>` still resolves and records
// plan.selected regardless of retirement, so evidence can revive a retired
// plan's connection (CONNECTION_ENGINE Revived) without Propose's help.
func Propose(s *store.Store, capability string, menu []string, nowMs int64) (string, error) {
	if len(menu) >= K {
		return "", nil
	}
	ts, found, err := s.LastEventTS("plan.generated")
	if err != nil {
		return "", err
	}
	if found && nowMs-ts < ProposalWindowMs {
		return "", nil
	}

	existing := map[string]bool{}
	for _, n := range menu {
		existing[n] = true
	}
	generated, err := generatedPlans(s, capability)
	if err != nil {
		return "", err
	}
	for _, n := range generated {
		existing[n] = true
	}
	for _, parent := range menu {
		for _, m := range Mutations(Steps(parent)) {
			name := Name(m.Steps)
			if existing[name] {
				continue
			}
			if err := s.AppendEvent(store.NewID(nowMs), "plan.generated", nowMs,
				map[string]any{
					"cap": capability, "plan": name,
					"parent": parent, "op": m.Op,
				}); err != nil {
				return "", err
			}
			return name, nil
		}
	}
	return "", nil
}
