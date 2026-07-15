package core

import "testing"

func TestConfidenceIsHalfAtEvidenceK(t *testing.T) {
	c := &Connection{Alpha: 11, Beta: 1, LastUpdate: 0}
	almostEqual(t, c.Confidence(0), 0.5, 1e-12, "evidence 10 -> conf 0.5")
}

func TestConfidenceIsZeroWithoutEvidence(t *testing.T) {
	c := &Connection{Alpha: 1, Beta: 1, LastUpdate: 0}
	almostEqual(t, c.Confidence(0), 0, 1e-12, "prior -> conf 0")
}

func TestConfidenceClampsNegativeEvidence(t *testing.T) {
	c := &Connection{Alpha: 0.5, Beta: 0.5, LastUpdate: 0}
	almostEqual(t, c.Confidence(0), 0, 1e-12, "negative evidence clamped")
}

func TestState(t *testing.T) {
	tests := []struct {
		name      string
		conn      *Connection
		now       int64
		ledgerSum float64
		want      string
	}{
		{"questioned when ledger over trigger", &Connection{Alpha: 11, Beta: 1, LastUpdate: 0}, 0, ThetaTrigger + 0.5, StateQuestioned},
		{"questioned beats dormant", &Connection{Alpha: 11, Beta: 1, LastUpdate: 0}, 2*HalfLifeMs + 1, ThetaTrigger + 0.5, StateQuestioned},
		{"trigger boundary is not questioned", &Connection{Alpha: 11, Beta: 1, LastUpdate: 0}, 0, ThetaTrigger, StateStable},
		{"dormant when silent past two half-lives", &Connection{Alpha: 6, Beta: 1, LastUpdate: 0}, 2*HalfLifeMs + 1, 0, StateDormant},
		{"dormancy boundary is not dormant", &Connection{Alpha: 41, Beta: 1, LastUpdate: 0}, 2 * HalfLifeMs, 0, StateStable},
		{"born below three evidence", &Connection{Alpha: 2, Beta: 1, LastUpdate: 0}, 0, 0, StateBorn},
		{"born just below three", &Connection{Alpha: 3.9, Beta: 1, LastUpdate: 0}, 0, 0, StateBorn},
		{"grow at exactly three evidence", &Connection{Alpha: 4, Beta: 1, LastUpdate: 0}, 0, 0, StateGrow},
		{"grow below confidence half", &Connection{Alpha: 6, Beta: 1, LastUpdate: 0}, 0, 0, StateGrow},
		{"stable at confidence half", &Connection{Alpha: 11, Beta: 1, LastUpdate: 0}, 0, 0, StateStable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.conn.State(tt.now, tt.ledgerSum); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
