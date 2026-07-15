package face

import (
	"testing"

	"github.com/Rererr/tomobit/internal/core"
)

func TestMood(t *testing.T) {
	tests := []struct {
		name       string
		states     []string
		wantName   string
		wantMarker string
	}{
		{"no connections", nil, "ふつう", ""},
		{"all healthy states", []string{core.StateBorn, core.StateGrow, core.StateStable}, "ふつう", ""},
		{"one questioned among others", []string{core.StateStable, core.StateQuestioned}, "はてな", "?"},
		{"all dormant", []string{core.StateDormant, core.StateDormant}, "ねむい", "z"},
		{"one dormant one active is not sleepy", []string{core.StateDormant, core.StateGrow}, "ふつう", ""},
		{"questioned outranks dormant", []string{core.StateDormant, core.StateQuestioned}, "はてな", "?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, marker := Mood(tt.states)
			if name != tt.wantName || marker != tt.wantMarker {
				t.Errorf("got (%q, %q), want (%q, %q)", name, marker, tt.wantName, tt.wantMarker)
			}
		})
	}
}
