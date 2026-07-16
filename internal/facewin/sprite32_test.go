package facewin

// SPRITES-WINDOW.md 実装規約: the Go source holds complete literals for every
// stage and frame; these tests re-apply the spec's derivation rules (S2 / S4 /
// S5 / Frame B) to the S0/S1/S3 正本 grids and verify both agree, so the
// literals can never drift from the spec without a test failing.

import (
	"strings"
	"testing"
)

// derivation holds the per-breed constants from SPRITES-WINDOW.md's rule
// tables (S2 sprout, S4 band, S5 stars, Frame B blink rows).
type derivation struct {
	body byte // blink replacement color

	s2CutLo, s2CutHi int       // S3 body rows removed for S2 (inclusive)
	sprout           [3][2]int // stem, leafA, leafB — (row, col)

	bandRows [2]int // S4 band rows
	bandLo   int    // band column start (end is always 16)

	blinkS1, blinkS2, blinkS3 int // eye-block top rows per stage group

	stars [4][2]int // A-left, A-right, B-left, B-right — top px of the plus
}

var derivations = map[Breed]derivation{
	BreedShiba: {
		body:    'm',
		s2CutLo: 19, s2CutHi: 22,
		sprout:   [3][2]int{{9, 11}, {8, 12}, {8, 10}},
		bandRows: [2]int{17, 18}, bandLo: 5,
		blinkS1: 17, blinkS2: 15, blinkS3: 11,
		stars: [4][2]int{{6, 2}, {4, 20}, {3, 2}, {8, 20}},
	},
	BreedRetriever: {
		body:    'l',
		s2CutLo: 19, s2CutHi: 22,
		sprout:   [3][2]int{{7, 10}, {6, 11}, {6, 9}},
		bandRows: [2]int{17, 18}, bandLo: 5,
		blinkS1: 17, blinkS2: 15, blinkS3: 11,
		stars: [4][2]int{{2, 1}, {5, 23}, {20, 1}, {2, 23}},
	},
	BreedPom: {
		body:    'W',
		s2CutLo: 17, s2CutHi: 20,
		sprout:   [3][2]int{{8, 10}, {7, 11}, {7, 9}},
		bandRows: [2]int{15, 16}, bandLo: 4,
		blinkS1: 17, blinkS2: 14, blinkS3: 10,
		stars: [4][2]int{{4, 1}, {3, 19}, {8, 1}, {6, 19}},
	},
}

const blankRow = "................................"

func put(g *grid, r, c int, ch byte) {
	row := []byte(g[r])
	row[c] = ch
	g[r] = string(row)
}

// blink applies the Frame B common rule: in the eye block's top and bottom
// rows every 'e' becomes the body color (the middle row survives as the
// closed-eye line; the nose sits outside these rows).
func blink(g grid, top int, body byte) grid {
	for _, r := range []int{top, top + 2} {
		g[r] = strings.ReplaceAll(g[r], "e", string(body))
	}
	return g
}

// starAt stamps the plus-shaped 5px star with its top pixel at (r, c).
func starAt(g *grid, r, c int) {
	put(g, r, c, 'm')
	for _, cc := range []int{c - 1, c, c + 1} {
		put(g, r+1, cc, 'm')
	}
	put(g, r+2, c, 'm')
}

// deriveS2 builds S2 Frame A from S3 Frame A: cut 4 body rows, pad 4 blank
// rows on top (the ground line stays), plant the sprout (stem + leaf A).
func deriveS2(s3 grid, d derivation) grid {
	var g grid
	i := 0
	for r := 0; r < 4; r++ {
		g[i] = blankRow
		i++
	}
	for r := 0; r < spriteSize; r++ {
		if r >= d.s2CutLo && r <= d.s2CutHi {
			continue
		}
		g[i] = s3[r]
		i++
	}
	put(&g, d.sprout[0][0], d.sprout[0][1], 'm')
	put(&g, d.sprout[1][0], d.sprout[1][1], 'm')
	return g
}

// deriveS4 builds S4 Frame A from S3 Frame A: two 'd' band rows plus the
// three-row triangle knot on the chest.
func deriveS4(s3 grid, d derivation) grid {
	g := s3
	for _, r := range d.bandRows {
		row := []byte(g[r])
		for c := d.bandLo; c <= 16; c++ {
			row[c] = 'd'
		}
		g[r] = string(row)
	}
	t0 := d.bandRows[1] + 1
	for i, span := range [][2]int{{8, 13}, {9, 12}, {10, 11}} {
		row := []byte(g[t0+i])
		for c := span[0]; c <= span[1]; c++ {
			row[c] = 'd'
		}
		g[t0+i] = string(row)
	}
	return g
}

func diff(t *testing.T, label string, want, got grid) {
	t.Helper()
	for r := 0; r < spriteSize; r++ {
		if want[r] != got[r] {
			t.Errorf("%s row %d:\n derived %q\n literal %q", label, r, want[r], got[r])
		}
	}
}

func TestGridShapeAndPalette(t *testing.T) {
	for breed, name := range map[Breed]string{BreedShiba: "shiba", BreedRetriever: "retriever", BreedPom: "pom"} {
		for stage := 0; stage < 6; stage++ {
			for frame := 0; frame < 2; frame++ {
				g := sprites[breed][stage][frame]
				for r, row := range g {
					if len(row) != spriteSize {
						t.Errorf("%s S%d frame %d row %d: len %d", name, stage, frame, r, len(row))
					}
					for c := 0; c < len(row); c++ {
						if _, ok := palette[row[c]]; !ok && row[c] != '.' {
							t.Errorf("%s S%d frame %d row %d col %d: unknown glyph %q", name, stage, frame, r, c, row[c])
						}
					}
				}
			}
		}
	}
}

// TestS0SharedFluff: every breed points at the same fluff asset — the breed
// must be invisible while Tomo is a sleeping ball (ADR-0020 Decision 5).
func TestS0SharedFluff(t *testing.T) {
	for breed := BreedShiba; breed <= BreedPom; breed++ {
		if sprites[breed][0][frameA] != fluff[frameA] || sprites[breed][0][frameB] != fluff[frameB] {
			t.Errorf("breed %d S0 is not the shared fluff", breed)
		}
	}
}

// TestFluffShimmer: Frame B differs from Frame A only in the four documented
// shimmer rows, which must match the spec verbatim.
func TestFluffShimmer(t *testing.T) {
	want := map[int]string{
		16: "........klllllmmlllllllk........",
		18: "........klllllllllmmlllk........",
		20: "........klllmmlllllllllk........",
		22: "........kllllllllmmllllk........",
	}
	for r := 0; r < spriteSize; r++ {
		if w, moved := want[r]; moved {
			if fluff[frameB][r] != w {
				t.Errorf("fluff B row %d:\n want %q\n got  %q", r, w, fluff[frameB][r])
			}
		} else if fluff[frameB][r] != fluff[frameA][r] {
			t.Errorf("fluff B row %d changed outside the shimmer rows", r)
		}
	}
}

func TestS2Derivation(t *testing.T) {
	for breed, d := range derivations {
		s3 := sprites[breed][3][frameA]
		a := deriveS2(s3, d)
		diff(t, "S2A", a, sprites[breed][2][frameA])

		// Frame B: blink + the leaf swings to the opposite side.
		b := blink(a, d.blinkS2, d.body)
		put(&b, d.sprout[1][0], d.sprout[1][1], '.')
		put(&b, d.sprout[2][0], d.sprout[2][1], 'm')
		diff(t, "S2B", b, sprites[breed][2][frameB])
	}
}

func TestS3FrameB(t *testing.T) {
	for breed, d := range derivations {
		b := blink(sprites[breed][3][frameA], d.blinkS3, d.body)
		diff(t, "S3B", b, sprites[breed][3][frameB])
	}
}

func TestS1FrameB(t *testing.T) {
	for breed, d := range derivations {
		b := blink(sprites[breed][1][frameA], d.blinkS1, d.body)
		diff(t, "S1B", b, sprites[breed][1][frameB])
	}
}

func TestS4Derivation(t *testing.T) {
	for breed, d := range derivations {
		a := deriveS4(sprites[breed][3][frameA], d)
		diff(t, "S4A", a, sprites[breed][4][frameA])
		b := blink(a, d.blinkS3, d.body)
		diff(t, "S4B", b, sprites[breed][4][frameB])
	}
}

// TestS5Derivation: S5 = S4 + twin stars; in Frame B the stars swap position
// (both frames start from S4 Frame A, so the blink is re-applied for B).
func TestS5Derivation(t *testing.T) {
	for breed, d := range derivations {
		s4 := sprites[breed][4][frameA]
		a := s4
		starAt(&a, d.stars[0][0], d.stars[0][1])
		starAt(&a, d.stars[1][0], d.stars[1][1])
		diff(t, "S5A", a, sprites[breed][5][frameA])

		b := blink(s4, d.blinkS3, d.body)
		starAt(&b, d.stars[2][0], d.stars[2][1])
		starAt(&b, d.stars[3][0], d.stars[3][1])
		diff(t, "S5B", b, sprites[breed][5][frameB])
	}
}
