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

	"github.com/Rererr/tomobit/internal/presence"
)

// Knobs (ADR-0020 Consequences: the numbers a human decides).
const (
	pollInterval = 500 * time.Millisecond // events末尾チェックの頻度
	blinkMin     = 3 * time.Second        // 瞬き周期 3〜4s
	blinkJitter  = time.Second
	blinkHold    = 180 * time.Millisecond
	bubbleFor    = 8 * time.Second // 吹き出し表示時間
	bobPeriod    = 3200 * time.Millisecond

	// residentGrace is how long presence must stay 0 before an ephemeral window
	// self-closes (ADR-0027 Decision 2): a momentary dip — a do finishing before
	// the next one, a startup race — never closes it. At pollInterval 500ms this
	// is six consecutive 0 observations.
	residentGrace = 3 * time.Second

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

	// resident keeps the window until Esc/Q regardless of presence (ADR-0027).
	// When false the window self-closes once no conversation is alive; liveCount
	// reports that count (injected so a test stubs it), and zeroSince tracks how
	// long the count has stood at 0 for the grace rule. liveCount is nil when
	// self-close is disabled (resident, or the sessions dir couldn't resolve).
	resident  bool
	liveCount func() int
	zeroSince time.Time

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
// resident (ADR-0027) fixes the window's lifetime: false makes it ephemeral
// (self-closes when no conversation is alive), true keeps it until Esc/Q.
func NewGame(p *Poller, breed Breed, scale int, plain bool, font *text.GoTextFaceSource, resident bool) *Game {
	g := &Game{
		poller: p, breed: breed, scale: scale, plain: plain, font: font,
		resident:  resident,
		liveCount: defaultLiveCount(resident),
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

// defaultLiveCount wires the ephemeral window's presence probe (ADR-0027): it
// counts live conversations via presence.CountLive. A resident window never
// self-closes, so it needs no probe (nil). If the sessions dir can't even be
// resolved we also return nil — the safe side is to keep the companion on
// screen, never to vanish on a probe we could not run. A count error returns
// -1 ("unknown"), which Update reads as "don't close this tick".
func defaultLiveCount(resident bool) func() int {
	if resident {
		return nil
	}
	dir, err := presence.DefaultDir()
	if err != nil {
		return nil
	}
	return func() int {
		n, err := presence.CountLive(dir)
		if err != nil {
			return -1
		}
		return n
	}
}

// closeDecision is the ephemeral window's self-close rule (ADR-0027 Decision 2),
// a pure function so the grace-window logic is table-tested without a window. A
// resident window never closes. Otherwise: the first 0 observation records
// zeroSince, any live>0 resets it, and the window closes only once presence has
// stood at 0 for a continuous grace — so a one-tick dip never closes it.
func closeDecision(resident bool, live int, now, zeroSince time.Time, grace time.Duration) (terminate bool, nextZeroSince time.Time) {
	if resident {
		return false, zeroSince
	}
	if live > 0 {
		return false, time.Time{}
	}
	if zeroSince.IsZero() {
		return false, now
	}
	if now.Sub(zeroSince) >= grace {
		return true, zeroSince
	}
	return false, zeroSince
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

		// Self-close on 0 live conversations (ADR-0027). A negative count is
		// "unknown" (probe failed): leave zeroSince as-is and don't close — the
		// safe side keeps the companion rather than vanishing on a bad read.
		if g.liveCount != nil {
			if live := g.liveCount(); live >= 0 {
				terminate, next := closeDecision(g.resident, live, now, g.zeroSince, residentGrace)
				g.zeroSince = next
				if terminate {
					return ebiten.Termination
				}
			}
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

	const edge, gap = 10, 8
	// Sit the clouds just above Tomo's head so the ⚪︎つなぎ trail stays short and
	// they read as *its* thoughts, not banners pinned to the ceiling. A shared
	// bottom line aligns the two bases; a taller cloud grows upward. The gap
	// scales with the sprite so the spacing holds at any --scale.
	bottomY := headY - float32(7*g.scale)
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
		g.drawThought(screen, th, x, bottomY, bw, headX, headY)
	}
}

// drawThought draws one thinking cloud whose bottom sits at bottomY (so a row
// of clouds aligns on a shared baseline just above the head) and a three-circle
// ⚪︎つなぎ tail rising from Tomo's head (toX,toY) to it. The circles — not a
// pointed tail — are what say "thinking, not speaking" (ADR-0026 Decision 5).
// Colors come from the sprite palette so it reads as the same asset family as
// the speech bubble.
func (g *Game) drawThought(screen *ebiten.Image, th Thought, x, bottomY, width, toX, toY float32) {
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
	// The label sits on its own line above the fragment; the cloud grows upward
	// from its fixed bottom.
	boxH := float32(2*bubblePad) + float32(lineH*float64(len(lines)+1))
	top := bottomY - boxH
	drawCloud(screen, x, top, width, boxH, white, dark)

	// ⚪︎つなぎ: a chain of circles from Tomo's head up to the cloud, growing as
	// they near it — "thinking", read bottom-up. Anchored at both ends so the
	// cloud reads as *this* head's thought, not a dot floating mid-air.
	bx, by := x+width/2, bottomY
	for _, c := range []struct{ t, r float32 }{{0.30, 2}, {0.54, 3}, {0.78, 4.5}} {
		cx := toX + (bx-toX)*c.t
		cy := toY + (by-toY)*c.t
		vector.DrawFilledCircle(screen, cx, cy, c.r, dark, true)
	}

	lbl := &text.DrawOptions{}
	lbl.GeoM.Translate(float64(x)+bubblePad, float64(top)+bubblePad)
	lbl.LineSpacing = lineH
	lbl.ColorScale.ScaleWithColor(color.NRGBA{0x8A, 0x8A, 0x8A, 0xFF}) // dim, like a tool name
	text.Draw(screen, label, face, lbl)

	body := &text.DrawOptions{}
	body.GeoM.Translate(float64(x)+bubblePad, float64(top)+bubblePad+lineH)
	body.LineSpacing = lineH
	body.ColorScale.ScaleWithColor(color.NRGBA{0x1A, 0x1A, 0x1A, 0xFF}) // palette e
	text.Draw(screen, joinLines(lines), face, body)
}

// drawCloud paints a scalloped thought-cloud with a clean outline and no inner
// seams (ADR-0026 Decision 5: 雲型で「考える」を形でも語る). It fills the whole
// lumpy silhouette in the outline color, then the same silhouette inset by the
// stroke width in the fill color — a border falls out for free, for any union
// of lobes, without tracing the boundary by hand.
func drawCloud(dst *ebiten.Image, x, y, w, h float32, fill, outline color.Color) {
	const sw = 2
	rl := h * 0.30
	switch {
	case rl > 15:
		rl = 15
	case rl < 6:
		rl = 6
	}
	cx0, cx1 := x+rl, x+w-rl
	cy0, cy1 := y+rl, y+h-rl
	n := int((cx1 - cx0) / (rl * 1.1))
	if n < 1 {
		n = 1
	}
	var lobes [][2]float32
	for i := 0; i <= n; i++ {
		cx := cx0 + (cx1-cx0)*float32(i)/float32(n)
		lobes = append(lobes, [2]float32{cx, cy0}, [2]float32{cx, cy1})
	}
	lobes = append(lobes, [2]float32{cx0, y + h/2}, [2]float32{cx1, y + h/2})

	paint := func(r, inset float32, clr color.Color) {
		for _, c := range lobes {
			vector.DrawFilledCircle(dst, c[0], c[1], r, clr, true)
		}
		vector.DrawFilledRect(dst, x+inset, y+inset, w-2*inset, h-2*inset, clr, false)
	}
	paint(rl+sw, rl-sw, outline) // silhouette, slightly larger
	paint(rl, rl, fill)          // inset fill leaves the outline as a rim
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
