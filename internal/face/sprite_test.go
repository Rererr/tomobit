package face

import "testing"

// TestSpriteGrids guards the docs/design/SPRITES.md transcription: a bad row width
// or a stray glyph outside the palette would otherwise only surface as a
// garbled avatar at runtime.
func TestSpriteGrids(t *testing.T) {
	for stage := StageEgg; stage <= StagePartner; stage++ {
		for frame := frameA; frame <= frameB; frame++ {
			grid := sprites[stage][frame]
			for row, line := range grid {
				runes := []rune(line)
				if len(runes) != spriteWidth {
					t.Errorf("stage %d frame %d row %d: width %d, want %d", stage, frame, row, len(runes), spriteWidth)
				}
				for col, r := range runes {
					if r == transparent {
						continue
					}
					if _, ok := palette256[byte(r)]; !ok {
						t.Errorf("stage %d frame %d row %d col %d: glyph %q outside palette", stage, frame, row, col, r)
					}
				}
			}
		}
	}
}
