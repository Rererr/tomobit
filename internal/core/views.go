package core

// Derived views (CONNECTION_ENGINE.md: Lifecycle is a View). Nothing here
// is stored — every value is computed from Beta(α,β) + the ledger.

// Confidence maps decayed evidence to (0,1): n/(n+K). K is the evidence
// count at which confidence reaches 0.5.
const confidenceK = 10.0

func (c *Connection) Confidence(nowMs int64) float64 {
	n := c.Evidence(nowMs)
	if n < 0 {
		n = 0
	}
	return n / (n + confidenceK)
}

// Lifecycle states, derived by query.
const (
	StateBorn       = "born"
	StateGrow       = "grow"
	StateStable     = "stable"
	StateQuestioned = "questioned"
	StateDormant    = "dormant"
)

// State derives the lifecycle label at nowMs. ledgerSum is the decayed
// cumulative excess surprisal (Engine.LedgerSum).
func (c *Connection) State(nowMs int64, ledgerSum float64) string {
	if ledgerSum > ThetaTrigger {
		return StateQuestioned
	}
	if nowMs-c.LastUpdate > 2*HalfLifeMs {
		return StateDormant
	}
	ev := c.Evidence(nowMs)
	switch {
	case ev < 3:
		return StateBorn
	case c.Confidence(nowMs) >= 0.5:
		return StateStable
	default:
		return StateGrow
	}
}
