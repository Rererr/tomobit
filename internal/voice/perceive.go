package voice

import (
	"fmt"
	"sort"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/face"
)

// Perceive selects the one line for a perceive boundary (`do`'s end or
// `tomobit perceive`), priority growth > insight > miss-reaction > murmur —
// ADR-0009 Decision 3, "rarest and most informative first", with ADR-0019
// Decision 1's graded surprise slotted above the mere murmur. Centralizing
// the branch here keeps callers from re-deriving the priority themselves.
// maxExcess is the batch's sharpest recorded excess surprisal (0 = none).
func Perceive(stageBefore, stageAfter int, newSplits []*core.Connection, exps []*core.Experience, maxExcess float64) (text string, ok bool) {
	if text, ok := Growth(stageBefore, stageAfter); ok {
		return text, ok
	}
	if text, ok := Insight(newSplits); ok {
		return text, ok
	}
	if text, ok := Missed(maxExcess); ok {
		return text, ok
	}
	return Murmur(exps)
}

// Growth reports a stage transition (catalog #2). before/after are
// face.Stage results taken immediately around one Engine.Apply batch
// (ADR-0009 Decision 2: stages are never stored, so this is the only place
// a transition can be seen). The same line covers regression on purpose —
// decayed evidence can walk the stage back down, and that is honesty, not
// a bug to hide (ADR-0008 Decision 2).
func Growth(before, after int) (text string, ok bool) {
	if before == after {
		return "", false
	}
	name := face.StageName(after)
	if after > before {
		return fmt.Sprintf("……なんだか、少し育った気がする。いまは%sだよ", name), true
	}
	return fmt.Sprintf("……なんだか、少し縮んだ気がする。いまは%sだよ", name), true
}

// Insight reports the first newly split connection, if any (catalog #3).
// newSplits should come from NewSplits, which sorts deterministically so
// the pick is stable when a single Apply batch splits more than one
// connection.
func Insight(newSplits []*core.Connection) (text string, ok bool) {
	if len(newSplits) == 0 {
		return "", false
	}
	c := newSplits[0]
	diff := core.Scope(c.Scope().Minus(core.ParseScopeKey(c.ParentKey)))
	return fmt.Sprintf("%sのときは勝手が違うんだって、わかってきたよ", ScopeDisplay(diff)), true
}

// Murmur reports what perceive just turned into experience (catalog #1).
func Murmur(exps []*core.Experience) (text string, ok bool) {
	switch len(exps) {
	case 0:
		return "", false
	case 1:
		return fmt.Sprintf("%sの仕事、経験にしたよ", ScopeDisplay(core.NewScope(exps[0].Tokens()...))), true
	default:
		return fmt.Sprintf("%d件の仕事、経験にしたよ", len(exps)), true
	}
}

// NewSplits returns the connections present in after but not before,
// restricted to ones Split actually produced (ParentKey set). Comparing
// two AllConnections snapshots around an Apply batch is the whole
// detector — the Engine itself stores no "a split just happened" event
// (ADR-0009 Decision 2/Consequences: Engine stays unchanged).
//
// The result is sorted by (scope, target) so Insight's pick is
// deterministic when more than one split lands in the same batch.
func NewSplits(before, after []*core.Connection) []*core.Connection {
	seen := make(map[string]bool, len(before))
	for _, c := range before {
		seen[connKey(c)] = true
	}
	var out []*core.Connection
	for _, c := range after {
		if c.ParentKey == "" || seen[connKey(c)] {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ScopeKey != out[j].ScopeKey {
			return out[i].ScopeKey < out[j].ScopeKey
		}
		return out[i].Target < out[j].Target
	})
	return out
}

func connKey(c *core.Connection) string {
	return c.Kind + "\x00" + c.ScopeKey + "\x00" + c.Target
}
