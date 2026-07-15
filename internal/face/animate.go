package face

import (
	"io"
	"time"
)

// tickInterval is the pause between frame swaps (ADR-0008 Decision 4: 150ms
// x 8 ticks = ~1.2s total). A package variable so tests can zero it out
// instead of waiting on real time.
var tickInterval = 150 * time.Millisecond

const animTicks = 8
const cursorUp6 = "\x1b[6A"

// Animate writes stage's sprite to w, alternating frame A/B for animTicks
// ticks, then leaves frame A on screen (ADR-0008 Decision 4). TTY detection
// is the caller's responsibility.
//
// Mono skips the animation loop entirely: NO_COLOR terminals get a single
// static frame with no cursor-control bytes, so the avatar never leaves
// stray escape codes on a terminal that can't render color.
func Animate(w io.Writer, stage int, mode Mode) {
	if mode == Mono {
		io.WriteString(w, Render(stage, frameA, mode))
		return
	}

	io.WriteString(w, Render(stage, frameA, mode))
	frame := frameB
	for i := 0; i < animTicks; i++ {
		time.Sleep(tickInterval)
		io.WriteString(w, cursorUp6)
		io.WriteString(w, Render(stage, frame, mode))
		frame = frameA + frameB - frame
	}
}
