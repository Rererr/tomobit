package facewin

import (
	"fmt"
	"image/color"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	text "github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// Knobs (ADR-0020 Consequences: the numbers a human decides).
const (
	pollInterval = 500 * time.Millisecond // events末尾チェックの頻度
	blinkMin     = 3 * time.Second        // 瞬き周期 3〜4s
	blinkJitter  = time.Second
	blinkHold    = 180 * time.Millisecond
	bubbleFor    = 8 * time.Second // 吹き出し表示時間
	bobPeriod    = 3200 * time.Millisecond

	fontSize    = 13.0
	bubblePad   = 8
	bubbleTailH = 9
)

// Game is the mascot window: residency is the point (ADR-0020 Decision 3 —
// 「そこに居る」ことが窓の存在意義), so idle animation never stops. Input is
// drag-only (Decision 4); Esc or Q closes, since a frameless window has no
// close button.
type Game struct {
	poller *Poller
	breed  Breed
	scale  int
	plain  bool
	font   *text.GoTextFaceSource

	w, h   int // logical window size
	frames [6][2]*ebiten.Image
	tops   [6]int
	ovQ    *ebiten.Image
	ovZ    *ebiten.Image

	view        Snapshot
	queue       []string
	bubble      string
	bubbleUntil time.Time

	started    bool
	start      time.Time
	lastPoll   time.Time
	nextBlink  time.Time
	blinkUntil time.Time

	dragging     bool
	dragX, dragY int
}

// NewGame builds the window for one breed at one integer scale (ADR-0020
// Decision 4: 非整数拡大はドットの輪郭を壊すため禁止 — enforced by type).
func NewGame(p *Poller, breed Breed, scale int, plain bool, font *text.GoTextFaceSource) *Game {
	g := &Game{
		poller: p, breed: breed, scale: scale, plain: plain, font: font,
		// Sprite box at bottom-center; the same amount of headroom above is
		// the bubble/overlay area (既定4倍で256×256の透明キャンバス).
		w: spriteSize * scale * 2,
		h: spriteSize * scale * 2,
	}
	for st := 0; st < 6; st++ {
		for fr := 0; fr < 2; fr++ {
			grid := sprites[breed][st][fr]
			g.frames[st][fr] = gridImage(grid[:])
		}
		a := sprites[breed][st][frameA]
		g.tops[st] = topRow(a[:])
	}
	g.ovQ = gridImage(overlayQuestion)
	g.ovZ = gridImage(overlaySleep)
	return g
}

// Size returns the logical window size for ebiten.SetWindowSize.
func (g *Game) Size() (int, int) { return g.w, g.h }

func (g *Game) Layout(_, _ int) (int, int) { return g.w, g.h }

func (g *Game) Update() error {
	now := time.Now()
	if !g.started {
		g.started = true
		g.start = now
		g.nextBlink = now.Add(blinkMin)
	}

	if ebiten.IsKeyPressed(ebiten.KeyEscape) || ebiten.IsKeyPressed(ebiten.KeyQ) {
		return ebiten.Termination
	}
	g.drag()

	if now.Sub(g.lastPoll) >= pollInterval {
		g.lastPoll = now
		u, err := g.poller.Poll(now.UnixMilli())
		if err != nil {
			// A transient read failure (e.g. mid-checkpoint) must not kill the
			// mascot; keep the last view and try again next tick.
			fmt.Fprintln(os.Stderr, err)
		} else {
			g.view = u.Snapshot
			g.queue = append(g.queue, u.Lines...)
		}
	}

	if g.bubble != "" && now.After(g.bubbleUntil) {
		g.bubble = ""
	}
	if g.bubble == "" && len(g.queue) > 0 {
		g.bubble = g.queue[0]
		g.queue = g.queue[1:]
		g.bubbleUntil = now.Add(bubbleFor)
	}

	if now.After(g.nextBlink) {
		g.blinkUntil = now.Add(blinkHold)
		g.nextBlink = now.Add(blinkMin + time.Duration(rand.Int63n(int64(blinkJitter))))
	}
	return nil
}

// drag moves the whole window from the cursor delta (ADR-0020 Decision 4:
// カーソル差分から自前実装). The press-point stays fixed in window
// coordinates, so shifting the window by the in-window delta tracks the
// cursor exactly.
func (g *Game) drag() {
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		g.dragging = true
		g.dragX, g.dragY = ebiten.CursorPosition()
		return
	}
	if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		g.dragging = false
		return
	}
	if g.dragging {
		cx, cy := ebiten.CursorPosition()
		if dx, dy := cx-g.dragX, cy-g.dragY; dx != 0 || dy != 0 {
			wx, wy := ebiten.WindowPosition()
			ebiten.SetWindowPosition(wx+dx, wy+dy)
		}
	}
}

func (g *Game) Draw(screen *ebiten.Image) {
	if g.plain {
		screen.Fill(color.NRGBA{0xF0, 0xF0, 0xF0, 0xFF})
	}

	now := time.Now()
	stage := g.view.Stage
	if stage < 0 || stage > 5 {
		stage = 0
	}
	frame := frameA
	if now.Before(g.blinkUntil) {
		frame = frameB
	}
	// Breathing bob: one logical pixel, half the period down — integer
	// offsets only, so the nearest-neighbor edges never smear.
	bob := 0
	if g.started && (now.Sub(g.start)/(bobPeriod/2))%2 == 1 {
		bob = 1
	}

	spriteX := (g.w - spriteSize*g.scale) / 2
	spriteY := g.h - spriteSize*g.scale

	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(float64(g.scale), float64(g.scale))
	op.GeoM.Translate(float64(spriteX), float64(spriteY+bob*g.scale))
	screen.DrawImage(g.frames[stage][frame], op)

	if ov := g.overlayImage(); ov != nil {
		ow, oh := ov.Bounds().Dx(), ov.Bounds().Dy()
		ox := spriteX + (spriteSize-ow-1)*g.scale
		oy := spriteY + (g.tops[stage]-oh-1+bob)*g.scale
		oop := &ebiten.DrawImageOptions{}
		oop.GeoM.Scale(float64(g.scale), float64(g.scale))
		oop.GeoM.Translate(float64(ox), float64(oy))
		screen.DrawImage(ov, oop)
	}

	// Thinking takes the headroom while providers run (ADR-0026 Decision 5):
	// one "考える" bubble per active provider, so a duel shows two at once. A
	// spoken line waits its turn — speech happens at idle boundaries, not while
	// work is in flight, so the two rarely compete for the same frame.
	if len(g.view.Thoughts) > 0 && g.font != nil {
		g.drawThoughts(screen, g.view.Thoughts, spriteX, spriteY+bob*g.scale)
	} else if g.bubble != "" && g.font != nil {
		g.drawBubble(screen, g.bubble)
	}
}

func (g *Game) overlayImage() *ebiten.Image {
	switch g.view.Marker {
	case "?":
		return g.ovQ
	case "z":
		return g.ovZ
	}
	return nil
}

// drawBubble draws the speech bubble pinned to the top of the window with a
// chunky stepped tail pointing at Tomo. Colors come from the sprite palette
// so the bubble reads as part of the same asset family.
func (g *Game) drawBubble(screen *ebiten.Image, line string) {
	face := &text.GoTextFace{Source: g.font, Size: fontSize}
	lineH := face.Metrics().HAscent + face.Metrics().HDescent + 3

	maxW := float64(g.w - 8 - 2*bubblePad)
	lines := wrapRunes(line, face, maxW)
	// The bubble may not swallow the sprite: cap the text block to the
	// headroom above the sprite box and ellipsize the rest.
	maxLines := int((float64(g.h/2) - 2*bubblePad - bubbleTailH - 8) / lineH)
	if maxLines < 1 {
		maxLines = 1
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ellipsize(lines[maxLines-1])
	}

	boxW := float32(g.w - 8)
	boxH := float32(2*bubblePad + float64(len(lines))*lineH)
	boxX, boxY := float32(4), float32(4)

	white := color.NRGBA{0xFA, 0xFA, 0xFA, 0xFF} // palette W
	dark := color.NRGBA{0x2E, 0x2E, 0x2E, 0xFF}  // palette k
	vector.DrawFilledRect(screen, boxX, boxY, boxW, boxH, white, false)
	vector.StrokeRect(screen, boxX, boxY, boxW, boxH, 2, dark, false)

	// Stepped tail: three shrinking slabs, pixel-corner aesthetic.
	cx := float32(g.w) / 2
	y := boxY + boxH
	for i, w := range []float32{14, 8, 4} {
		vector.DrawFilledRect(screen, cx-w/2, y+float32(i*3), w, 3, dark, false)
	}

	top := &text.DrawOptions{}
	top.GeoM.Translate(float64(boxX)+bubblePad, float64(boxY)+bubblePad)
	top.LineSpacing = lineH
	top.ColorScale.ScaleWithColor(color.NRGBA{0x1A, 0x1A, 0x1A, 0xFF}) // palette e
	text.Draw(screen, joinLines(lines), face, top)
}

// thoughtMax caps how many thinking bubbles draw at once: a duel is two, and
// two is all the headroom holds side by side (ADR-0026 Decision 5).
const thoughtMax = 2

// drawThoughts lays the active providers' thinking bubbles across the top
// headroom — one centered, two split left/right — each trailing ⚪︎つなぎ
// circles down toward Tomo's head, so it reads as "Tomo is turning several
// thoughts over", not a dashboard of separate agents. headX/headY is the
// sprite's top-center, where every trail points.
func (g *Game) drawThoughts(screen *ebiten.Image, thoughts []Thought, spriteX, spriteY int) {
	if len(thoughts) > thoughtMax {
		thoughts = thoughts[:thoughtMax]
	}
	headX := float32(spriteX + spriteSize*g.scale/2)
	headY := float32(spriteY)

	const edge, gap = 4, 6
	total := float32(g.w - 2*edge)
	bw := total
	if len(thoughts) == 2 {
		bw = (total - gap) / 2
	}
	for i, th := range thoughts {
		x := float32(edge)
		if i == 1 {
			x += bw + gap
		}
		g.drawThought(screen, th, x, edge, bw, headX, headY)
	}
}

// drawThought draws one thinking bubble at (x,y) of the given width and a
// three-circle tail shrinking from its underside toward (toX,toY). The circles
// — not a pointed tail — are what say "thinking, not speaking" (ADR-0026
// Decision 5). Colors come from the sprite palette so it reads as the same
// asset family as the speech bubble.
func (g *Game) drawThought(screen *ebiten.Image, th Thought, x, y, width, toX, toY float32) {
	face := &text.GoTextFace{Source: g.font, Size: fontSize}
	lineH := face.Metrics().HAscent + face.Metrics().HDescent + 3

	label := th.Provider
	if label == "" {
		label = "…"
	}
	lines := wrapRunes(thoughtFragment(th.Text), face, float64(width)-2*bubblePad)
	if len(lines) > 2 {
		lines = lines[:2]
		lines[1] = ellipsize(lines[1])
	}

	white := color.NRGBA{0xFA, 0xFA, 0xFA, 0xFF} // palette W
	dark := color.NRGBA{0x2E, 0x2E, 0x2E, 0xFF}  // palette k
	// The label sits on its own line above the fragment.
	boxH := float32(2*bubblePad) + float32(lineH*float64(len(lines)+1))
	vector.DrawFilledRect(screen, x, y, width, boxH, white, false)
	vector.StrokeRect(screen, x, y, width, boxH, 2, dark, false)

	// ⚪︎つなぎ: three shrinking circles from the box down toward Tomo.
	fromX, fromY := x+width/2, y+boxH
	for i, r := range []float32{4, 3, 2} {
		t := float32(i+1) / float32(len(lines)+3)
		cx := fromX + (toX-fromX)*t
		cy := fromY + (toY-fromY)*t
		vector.DrawFilledCircle(screen, cx, cy, r, dark, true)
	}

	lbl := &text.DrawOptions{}
	lbl.GeoM.Translate(float64(x)+bubblePad, float64(y)+bubblePad)
	lbl.LineSpacing = lineH
	lbl.ColorScale.ScaleWithColor(color.NRGBA{0x8A, 0x8A, 0x8A, 0xFF}) // dim, like a tool name
	text.Draw(screen, label, face, lbl)

	body := &text.DrawOptions{}
	body.GeoM.Translate(float64(x)+bubblePad, float64(y)+bubblePad+lineH)
	body.LineSpacing = lineH
	body.ColorScale.ScaleWithColor(color.NRGBA{0x1A, 0x1A, 0x1A, 0xFF}) // palette e
	text.Draw(screen, joinLines(lines), face, body)
}

// thoughtFragment collapses a provider's latest text to a single short line —
// a glimpse of what it is thinking, not the answer (回答は端末). Whitespace and
// newlines fold to single spaces so a multi-line message still reads as one
// fragment.
func thoughtFragment(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 40 {
		return string(r[:39]) + "…"
	}
	return s
}

// wrapRunes greedily wraps at any rune — Japanese has no word spaces, so
// per-rune breaking is the natural unit.
func wrapRunes(s string, face text.Face, maxW float64) []string {
	var lines []string
	cur := ""
	for _, r := range s {
		next := cur + string(r)
		if cur != "" && text.Advance(next, face) > maxW {
			lines = append(lines, cur)
			cur = string(r)
			continue
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func ellipsize(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return "…"
	}
	return string(runes[:len(runes)-1]) + "…"
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
