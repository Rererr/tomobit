package facewin

import (
	"testing"
	"time"
)

func TestCloseDecision(t *testing.T) {
	now := time.Unix(1000, 0)
	grace := 3 * time.Second
	set := now.Add(-2 * time.Second) // a 0-run that began 2s ago (< grace)
	old := now.Add(-grace)           // a 0-run that has reached grace

	cases := []struct {
		name          string
		resident      bool
		live          int
		zeroSince     time.Time
		wantTerminate bool
		wantZeroSince time.Time
	}{
		{"resident never closes even at 0", true, 0, old, false, old},
		{"live resets the 0-run", false, 2, set, false, time.Time{}},
		{"first 0 records the moment, does not close", false, 0, time.Time{}, false, now},
		{"0 within grace keeps waiting", false, 0, set, false, set},
		{"0 for the full grace closes", false, 0, old, true, old},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotT, gotZ := closeDecision(tc.resident, tc.live, now, tc.zeroSince, grace)
			if gotT != tc.wantTerminate {
				t.Errorf("terminate = %v, want %v", gotT, tc.wantTerminate)
			}
			if !gotZ.Equal(tc.wantZeroSince) {
				t.Errorf("nextZeroSince = %v, want %v", gotZ, tc.wantZeroSince)
			}
		})
	}
}
