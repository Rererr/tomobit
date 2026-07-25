// 姿の機械可読view (ADR-0048): the sprite sheet this package draws, handed out
// as data so a third renderer can draw the same Tomo without a second copy of
// the assets. Nothing here re-derives anything — Sheet is the same grids,
// palette, overlays and idle-animation knobs window.go itself uses, so a
// consumer that follows the fields is drawing what the face window draws.
//
// This file is the only exported surface over the assets; sprite32.go stays
// the正本 (docs/design/SPRITES-WINDOW.md の実装先) and is not duplicated.
package facewin

import (
	"fmt"

	"github.com/Rererr/tomobit/internal/face"
)

// Sheet is one breed's whole sprite sheet plus the rules for animating it.
// The consumer's whole job is: pick Stages[stage], draw Frames[0] normally and
// Frames[1] while blinking, offset by BobPx every half BobPeriodMs, and paint
// the Overlays entry matching the mood marker at that stage's OverlayOrigin.
type Sheet struct {
	Type string `json:"type"` // "sprite" — 他のviewと同じ自己申告
	// Size is the square canvas edge in logical pixels (all stages share it).
	Size  int    `json:"size"`
	Breed string `json:"breed"`
	// Palette maps a grid glyph to "#RRGGBB". '.' is absent: it is transparent,
	// and an entry for it would invite drawing it as a colour.
	Palette  map[string]string `json:"palette"`
	Stages   []SheetStage      `json:"stages"`
	Overlays []SheetOverlay    `json:"overlays"`
	Anim     SheetAnim         `json:"anim"`
}

// SheetStage is one growth stage's two frames. Frames[0] is A (基本),
// Frames[1] is B (瞬き等) — the order is the contract, not an accident of the
// literal's layout.
type SheetStage struct {
	Stage  int        `json:"stage"`
	Name   string     `json:"name"`
	Frames [][]string `json:"frames"`
	// OverlayOrigin gives each marker's top-left in logical pixels relative to
	// this stage's canvas. y is routinely negative: the glyph sits above the
	// head, and how far above depends on where this stage's silhouette starts
	// — which is exactly the derivation a consumer must not have to repeat.
	OverlayOrigin map[string][2]int `json:"overlay_origin"`
}

// SheetOverlay is one expression glyph (ADR-0008 Decision 3): the body sprite
// never branches on mood, a glyph is composited above the head. Marker matches
// face.Mood's marker — the same string `status --view json` reports.
type SheetOverlay struct {
	Marker string   `json:"marker"` // "?" はてな / "z" ねむい
	Rows   []string `json:"rows"`
}

// SheetAnim carries the idle-animation knobs (ADR-0020 Consequences: the
// numbers a human decides). Residency is the point — a consumer that drops
// these draws a still image, which is a different companion.
type SheetAnim struct {
	BlinkMinMs    int `json:"blink_min_ms"`    // 瞬き周期の下限
	BlinkJitterMs int `json:"blink_jitter_ms"` // 周期に足す揺らぎの幅
	BlinkHoldMs   int `json:"blink_hold_ms"`   // frame B を出している時間
	BobPeriodMs   int `json:"bob_period_ms"`   // 呼吸1往復
	BobPx         int `json:"bob_px"`          // 呼吸の振れ幅(論理px、整数のみ)
}

// breedNames is ParseBreed's inverse — the flag vocabulary, one table so the
// two can not drift.
var breedNames = map[Breed]string{
	BreedShiba:     "shiba",
	BreedRetriever: "retriever",
	BreedPom:       "pom",
}

// BreedName names b for a machine reader, or "" for a value outside the enum.
func BreedName(b Breed) string {
	return breedNames[b]
}

// SpriteSheet builds breed's sheet. It reads the same package-level assets
// NewGame reads, so a change to sprite32.go reaches both renderers at once.
func SpriteSheet(breed Breed) Sheet {
	overlays := []SheetOverlay{
		{Marker: "?", Rows: overlayQuestion},
		{Marker: "z", Rows: overlaySleep},
	}

	stages := make([]SheetStage, 0, len(sprites[breed]))
	for st := range sprites[breed] {
		a := sprites[breed][st][frameA]
		b := sprites[breed][st][frameB]
		top := topRow(a[:])
		origins := make(map[string][2]int, len(overlays))
		for _, ov := range overlays {
			// The placement rule is Draw's, verbatim (window.go): flush to the
			// canvas's right edge minus one, and one row above where the
			// silhouette starts. Computed here so no consumer re-derives it.
			origins[ov.Marker] = [2]int{
				spriteSize - len(ov.Rows[0]) - 1,
				top - len(ov.Rows) - 1,
			}
		}
		stages = append(stages, SheetStage{
			Stage:         st,
			Name:          face.StageName(st),
			Frames:        [][]string{append([]string(nil), a[:]...), append([]string(nil), b[:]...)},
			OverlayOrigin: origins,
		})
	}

	pal := make(map[string]string, len(palette))
	for ch, c := range palette {
		pal[string(ch)] = fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
	}

	return Sheet{
		Type:     "sprite",
		Size:     spriteSize,
		Breed:    BreedName(breed),
		Palette:  pal,
		Stages:   stages,
		Overlays: overlays,
		Anim: SheetAnim{
			BlinkMinMs:    int(blinkMin.Milliseconds()),
			BlinkJitterMs: int(blinkJitter.Milliseconds()),
			BlinkHoldMs:   int(blinkHold.Milliseconds()),
			BobPeriodMs:   int(bobPeriod.Milliseconds()),
			BobPx:         bobPx,
		},
	}
}
