// 姿の機械可読view の口 (ADR-0048): `tomobit-face --view json` writes the
// sprite sheet this window draws and exits. The renderer that owns the assets
// is the one that hands them out — the same discipline `status --view json`
// follows for the stage (ADR-0039): the derivation stays where it lives, and
// the reader decodes instead of re-deriving.
package main

import (
	"encoding/json"
	"io"

	"github.com/Rererr/tomobit/internal/facewin"
)

// writeSpriteView encodes breed's sheet as one JSON object and a newline.
// One object, not a stream: assets do not change while anyone watches, so
// there is nothing to tail — a reader asks once and draws from what it got.
func writeSpriteView(w io.Writer, breed facewin.Breed) error {
	return json.NewEncoder(w).Encode(facewin.SpriteSheet(breed))
}
