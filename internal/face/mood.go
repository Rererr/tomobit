package face

import "github.com/Rererr/tomobit/internal/core"

// Mood derives the expression from the Connections' lifecycle states
// (ADR-0008 Decision 3). states is the caller-computed core.Connection.State
// output for each Connection; Mood itself stores nothing.
//
// Priority: はてな (questioned) > ねむい (all dormant) > ふつう.
func Mood(states []string) (name, marker string) {
	if len(states) == 0 {
		return "ふつう", ""
	}
	allDormant := true
	for _, s := range states {
		if s == core.StateQuestioned {
			return "はてな", "?"
		}
		if s != core.StateDormant {
			allDormant = false
		}
	}
	if allDormant {
		return "ねむい", "z"
	}
	return "ふつう", ""
}
