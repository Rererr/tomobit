package face

import "testing"

func TestRenderMonoGoldenEggFrameA(t *testing.T) {
	want := "" +
		"                \n" +
		"   ▄████████▄   \n" +
		"  ████████████  \n" +
		"  ████████████  \n" +
		"  ████████████  \n" +
		"   ▀████████▀   \n"
	if got := Render(StageEgg, frameA, Mono); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMonoHasNoEscapeCodes(t *testing.T) {
	for stage := StageEgg; stage <= StagePartner; stage++ {
		for frame := frameA; frame <= frameB; frame++ {
			out := Render(stage, frame, Mono)
			for _, r := range out {
				if r == '\x1b' {
					t.Fatalf("stage %d frame %d: Mono output contains an escape byte", stage, frame)
				}
			}
		}
	}
}

func TestRenderColor256HasResetAtEndOfEachLine(t *testing.T) {
	out := Render(StageEgg, frameA, Color256)
	lines := splitLines(out)
	if len(lines) != spriteHeight/2 {
		t.Fatalf("got %d lines, want %d", len(lines), spriteHeight/2)
	}
	sawEscape := false
	for i, line := range lines {
		if len(line) < len(sgrReset) || line[len(line)-len(sgrReset):] != sgrReset {
			t.Errorf("line %d does not end with SGR reset: %q", i, line)
		}
		if containsEscape(line) {
			sawEscape = true
		}
	}
	if !sawEscape {
		t.Error("Color256 output has no SGR escape codes at all")
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	return lines
}

func containsEscape(s string) bool {
	for _, r := range s {
		if r == '\x1b' {
			return true
		}
	}
	return false
}
