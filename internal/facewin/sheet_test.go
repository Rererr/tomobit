package facewin

// ADR-0048: the sheet is a contract with a renderer that has no access to the
// assets. These tests pin the parts that renderer cannot check for itself —
// that the frames arrive in A/B order, that the palette covers every glyph the
// grids actually use, and that the overlay origin is the same placement Draw
// computes rather than a second opinion about it.

import (
	"strings"
	"testing"
)

func TestSpriteSheetCoversEveryStageAndFrame(t *testing.T) {
	for breed := BreedShiba; breed <= BreedPom; breed++ {
		sh := SpriteSheet(breed)
		if sh.Type != "sprite" {
			t.Errorf("%s: Type = %q, want \"sprite\"", sh.Breed, sh.Type)
		}
		if sh.Size != spriteSize {
			t.Errorf("%s: Size = %d, want %d", sh.Breed, sh.Size, spriteSize)
		}
		if sh.Breed != BreedName(breed) {
			t.Errorf("Breed = %q, want %q", sh.Breed, BreedName(breed))
		}
		if len(sh.Stages) != 6 {
			t.Fatalf("%s: %d stages, want 6 (S0..S5)", sh.Breed, len(sh.Stages))
		}
		for i, st := range sh.Stages {
			if st.Stage != i {
				t.Errorf("%s: Stages[%d].Stage = %d", sh.Breed, i, st.Stage)
			}
			if st.Name == "" {
				t.Errorf("%s: stage %d has no name", sh.Breed, i)
			}
			if len(st.Frames) != 2 {
				t.Fatalf("%s: stage %d has %d frames, want 2 (A, B)", sh.Breed, i, len(st.Frames))
			}
			// The order is the contract: Frames[0] is what the window draws at
			// rest, Frames[1] is what it swaps in to blink.
			if got, want := strings.Join(st.Frames[0], "\n"), strings.Join(sprites[breed][i][frameA][:], "\n"); got != want {
				t.Errorf("%s: stage %d Frames[0] is not frame A", sh.Breed, i)
			}
			if got, want := strings.Join(st.Frames[1], "\n"), strings.Join(sprites[breed][i][frameB][:], "\n"); got != want {
				t.Errorf("%s: stage %d Frames[1] is not frame B", sh.Breed, i)
			}
			for f, rows := range st.Frames {
				if len(rows) != spriteSize {
					t.Errorf("%s: stage %d frame %d has %d rows, want %d", sh.Breed, i, f, len(rows), spriteSize)
				}
				for y, row := range rows {
					if len(row) != spriteSize {
						t.Errorf("%s: stage %d frame %d row %d is %d wide, want %d", sh.Breed, i, f, y, len(row), spriteSize)
					}
				}
			}
		}
	}
}

// A glyph the grids use but the palette omits reaches the consumer as an
// undrawable pixel — it would silently vanish from the silhouette. '.' is the
// deliberate omission: transparent is an absence, not a colour.
func TestSpriteSheetPaletteCoversEveryGlyph(t *testing.T) {
	for breed := BreedShiba; breed <= BreedPom; breed++ {
		sh := SpriteSheet(breed)
		if _, ok := sh.Palette["."]; ok {
			t.Errorf("%s: palette names '.', which must stay transparent", sh.Breed)
		}
		seen := map[string]bool{}
		for _, st := range sh.Stages {
			for _, rows := range st.Frames {
				for _, row := range rows {
					for _, ch := range strings.Split(row, "") {
						seen[ch] = true
					}
				}
			}
		}
		for _, ov := range sh.Overlays {
			for _, row := range ov.Rows {
				for _, ch := range strings.Split(row, "") {
					seen[ch] = true
				}
			}
		}
		for ch := range seen {
			if ch == "." {
				continue
			}
			if _, ok := sh.Palette[ch]; !ok {
				t.Errorf("%s: glyph %q appears in the grids but not in the palette", sh.Breed, ch)
			}
		}
	}
}

// The origin must equal Draw's own arithmetic (window.go): flush to the right
// edge minus one, one row above where the silhouette starts. A consumer that
// trusted the field and got a re-derivation would paint the mood glyph
// somewhere the face window never puts it.
func TestSpriteSheetOverlayOriginMatchesDraw(t *testing.T) {
	for breed := BreedShiba; breed <= BreedPom; breed++ {
		sh := SpriteSheet(breed)
		for i, st := range sh.Stages {
			a := sprites[breed][i][frameA]
			top := topRow(a[:])
			for _, ov := range sh.Overlays {
				ow, oh := len(ov.Rows[0]), len(ov.Rows)
				want := [2]int{spriteSize - ow - 1, top - oh - 1}
				got, ok := st.OverlayOrigin[ov.Marker]
				if !ok {
					t.Errorf("%s: stage %d has no origin for marker %q", sh.Breed, i, ov.Marker)
					continue
				}
				if got != want {
					t.Errorf("%s: stage %d marker %q origin = %v, want %v", sh.Breed, i, ov.Marker, got, want)
				}
			}
		}
	}
}

// Idle animation is not decoration (ADR-0020 Decision 3: 「そこに居る」ことが
// 窓の存在意義), so a zero knob would hand a third renderer a still image.
func TestSpriteSheetCarriesAnimationKnobs(t *testing.T) {
	a := SpriteSheet(BreedShiba).Anim
	if a.BlinkMinMs <= 0 || a.BlinkHoldMs <= 0 || a.BobPeriodMs <= 0 || a.BobPx <= 0 {
		t.Errorf("anim = %+v, want every knob positive", a)
	}
	if a.BlinkJitterMs < 0 {
		t.Errorf("BlinkJitterMs = %d, want >= 0", a.BlinkJitterMs)
	}
}

func TestBreedNameRoundTripsParseBreed(t *testing.T) {
	for breed := BreedShiba; breed <= BreedPom; breed++ {
		name := BreedName(breed)
		got, ok := ParseBreed(name)
		if !ok || got != breed {
			t.Errorf("ParseBreed(BreedName(%d)) = %d, %v", breed, got, ok)
		}
	}
	if got := BreedName(Breed(99)); got != "" {
		t.Errorf("BreedName(out of range) = %q, want \"\"", got)
	}
}
