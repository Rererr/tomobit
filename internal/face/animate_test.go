package face

import (
	"bytes"
	"strings"
	"testing"
)

// withZeroTick disables the real sleep so animation tests don't block on
// wall-clock time, restoring the interval afterward.
func withZeroTick(t *testing.T) {
	t.Helper()
	prev := tickInterval
	tickInterval = 0
	t.Cleanup(func() { tickInterval = prev })
}

func TestAnimateColor256DrawsAllTicksAndEndsOnFrameA(t *testing.T) {
	withZeroTick(t)
	var buf bytes.Buffer
	Animate(&buf, StageEgg, Color256)

	out := buf.String()
	if n := strings.Count(out, cursorUp6); n != animTicks {
		t.Errorf("cursor-up moves: got %d, want %d", n, animTicks)
	}
	frameAOut := Render(StageEgg, frameA, Color256)
	if !strings.HasSuffix(out, frameAOut) {
		t.Error("animation did not end on frame A")
	}
}

func TestAnimateMonoDrawsSingleStaticFrameWithNoCursorControl(t *testing.T) {
	withZeroTick(t)
	var buf bytes.Buffer
	Animate(&buf, StageEgg, Mono)

	want := Render(StageEgg, frameA, Mono)
	if got := buf.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(buf.String(), cursorUp6) {
		t.Error("Mono animation must not emit cursor control bytes")
	}
}
