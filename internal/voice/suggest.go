package voice

import (
	"fmt"

	"github.com/Rererr/tomobit/internal/core"
)

// Candidate pairs a capability Connection with the View values Suggest
// ranks it by. State and LedgerSum are core.Connection.State's own inputs
// (the connections table already computes both) — Suggest reuses them
// rather than recomputing (no new statistic, mirrors ADR-0008 Decision 2).
type Candidate struct {
	Conn      *core.Connection
	State     string
	LedgerSum float64
}

// Suggest picks the one companion-view remark (catalog #4). ADR-0009
// Decision 4: tone follows confidence, so the selection tier itself carries
// the wording — stable is a claim, questioned is unease, anything else is a
// guess. Only capability connections are eligible; a preference Connection
// has no "getting along with" framing.
func Suggest(cands []Candidate, now int64) (text string, ok bool) {
	var stable, questioned, other *Candidate
	for i := range cands {
		c := &cands[i]
		if c.Conn.Kind != core.ConnCapability {
			continue
		}
		switch c.State {
		case core.StateStable:
			if stable == nil || c.Conn.Evidence(now) > stable.Conn.Evidence(now) {
				stable = c
			}
		case core.StateQuestioned:
			if questioned == nil || c.LedgerSum > questioned.LedgerSum {
				questioned = c
			}
		}
		if other == nil || c.Conn.Evidence(now) > other.Conn.Evidence(now) {
			other = c
		}
	}
	switch {
	case stable != nil:
		return fmt.Sprintf("%sは%sとうまくいってるね", ScopeDisplay(stable.Conn.Scope()), stable.Conn.Target), true
	case questioned != nil:
		return fmt.Sprintf("%sと%s、最近ちょっと様子が違う気がする", ScopeDisplay(questioned.Conn.Scope()), questioned.Conn.Target), true
	case other != nil:
		return fmt.Sprintf("%sは%sと相性いいかも", ScopeDisplay(other.Conn.Scope()), other.Conn.Target), true
	default:
		return "", false
	}
}
