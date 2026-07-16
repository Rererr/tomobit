package facewin

import "testing"

// Overlays are free-form glyphs (no derivation rule), so the tests guard the
// spec's hard constraints: rectangular, palette-only, and single-tone — `d`
// for はてな, `m` for ねむい (SPRITES-WINDOW.md オーバーレイ).
func TestOverlays(t *testing.T) {
	cases := []struct {
		name string
		rows []string
		tone byte
	}{
		{"question", overlayQuestion, 'd'},
		{"sleep", overlaySleep, 'm'},
	}
	for _, tc := range cases {
		w := len(tc.rows[0])
		for r, row := range tc.rows {
			if len(row) != w {
				t.Errorf("%s row %d: len %d, want %d", tc.name, r, len(row), w)
			}
			for c := 0; c < len(row); c++ {
				if row[c] != '.' && row[c] != tc.tone {
					t.Errorf("%s row %d col %d: glyph %q, want %q or '.'", tc.name, r, c, row[c], tc.tone)
				}
			}
		}
	}
}
