package facewin

// Expression overlays (ADR-0008 Decision 3 / ADR-0020 Decision 5): the body
// sprite never branches on mood — a separate glyph is composited above the
// head. Grids transcribed from docs/design/SPRITES-WINDOW.md (オーバーレイ).

// overlayQuestion is はてな — a questioned Connection exists. 8×8, `d`.
var overlayQuestion = []string{
	"..dddd..",
	".dd..dd.",
	".....dd.",
	"....dd..",
	"...dd...",
	"...dd...",
	"........",
	"...dd...",
}

// overlaySleep is ねむい — every Connection is dormant. Three z's rising to
// the right, 12×12, `m` (明暗両対応: シルエット外の要素は`m`).
var overlaySleep = []string{
	".......mmmmm",
	"..........m.",
	".........m..",
	"........m...",
	".......mmmmm",
	"...mmmm.....",
	".....m......",
	"....m.......",
	"...mmmm.....",
	".mmm........",
	"..m.........",
	".mmm........",
}
