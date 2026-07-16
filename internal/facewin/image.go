package facewin

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
)

// gridImage rasterizes a text grid at 1px per logical pixel. Scaling happens
// at draw time with nearest-neighbor GeoM (integer factors only, ADR-0020
// Decision 4), so the asset itself stays resolution-independent.
func gridImage(rows []string) *ebiten.Image {
	w := len(rows[0])
	img := image.NewNRGBA(image.Rect(0, 0, w, len(rows)))
	for y, row := range rows {
		for x := 0; x < len(row); x++ {
			if c, ok := palette[row[x]]; ok {
				img.SetNRGBA(x, y, c)
			}
		}
	}
	return ebiten.NewImageFromImage(img)
}

// topRow returns the first row with any opaque pixel — where the head starts,
// so overlays can sit just above it whatever the stage's height.
func topRow(rows []string) int {
	for y, row := range rows {
		for x := 0; x < len(row); x++ {
			if row[x] != '.' {
				return y
			}
		}
	}
	return len(rows)
}
