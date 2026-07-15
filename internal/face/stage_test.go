package face

import (
	"testing"

	"github.com/Rererr/tomobit/internal/core"
)

func capability(alpha, beta float64, parentKey string) *core.Connection {
	return &core.Connection{Kind: core.ConnCapability, Alpha: alpha, Beta: beta, ParentKey: parentKey}
}

func preference(alpha, beta float64) *core.Connection {
	return &core.Connection{Kind: core.ConnPreference, Alpha: alpha, Beta: beta}
}

func TestStage(t *testing.T) {
	tests := []struct {
		name  string
		conns []*core.Connection
		now   int64
		want  int
	}{
		{"S0 no connections", nil, 0, StageEgg},
		{"S1 born connection below S2 evidence", []*core.Connection{capability(2, 1, "")}, 0, StageChick},
		{"S2 boundary at evidence exactly 3", []*core.Connection{capability(4, 1, "")}, 0, StageChild},
		{"S2 boundary just under 3 stays S1", []*core.Connection{capability(3.99, 1, "")}, 0, StageChick},
		{"S3 from a stable connection at confidence exactly 0.5", []*core.Connection{capability(11, 1, "")}, 0, StageYouth},
		{"S3 from a split child regardless of its own evidence", []*core.Connection{capability(2, 1, "parent-key")}, 0, StageYouth},
		{
			"S4 needs two stable and one split child",
			[]*core.Connection{capability(11, 1, ""), capability(11, 1, ""), capability(2, 1, "parent-key")},
			0, StageAdult,
		},
		{
			"S4 with only one stable connection stays S3",
			[]*core.Connection{capability(11, 1, ""), capability(2, 1, "parent-key")},
			0, StageYouth,
		},
		{
			"S5 adds a preference connection at evidence exactly 1.0",
			[]*core.Connection{capability(11, 1, ""), capability(11, 1, ""), capability(2, 1, "parent-key"), preference(2, 1)},
			0, StagePartner,
		},
		{
			"S5 boundary just under preference evidence 1.0 stays S4",
			[]*core.Connection{capability(11, 1, ""), capability(11, 1, ""), capability(2, 1, "parent-key"), preference(1.99, 1)},
			0, StageAdult,
		},
		{
			"decayed evidence falls back below the S2 threshold",
			[]*core.Connection{capability(3, 3, "")}, // raw evidence 4 at LastUpdate 0
			2 * core.HalfLifeMs,                      // decay factor 0.25 -> evidence 1.0
			StageChick,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Stage(tt.conns, tt.now); got != tt.want {
				t.Errorf("got stage %d (%s), want %d (%s)", got, StageName(got), tt.want, StageName(tt.want))
			}
		})
	}
}

func TestStageNameOutOfRange(t *testing.T) {
	if got := StageName(-1); got != "" {
		t.Errorf("StageName(-1) = %q, want \"\"", got)
	}
	if got := StageName(StagePartner + 1); got != "" {
		t.Errorf("StageName(out of range) = %q, want \"\"", got)
	}
}
