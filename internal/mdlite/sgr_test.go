package mdlite

import (
	"strings"
	"testing"
)

// The whole point of surfacing tool output is its colour, so an SGR sequence
// survives verbatim — and the result is reset so no background bleeds past it.
func TestToolOutputKeepsSGRAndResets(t *testing.T) {
	out, truncated := ToolOutput("\x1b[48;2;30;44;74m  SAMPLE  \x1b[0m", 0, 0)
	if !strings.Contains(out, "\x1b[48;2;30;44;74m  SAMPLE  ") {
		t.Errorf("SGR colour must survive verbatim: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Errorf("output must end reset so colour cannot bleed: %q", out)
	}
	if truncated {
		t.Errorf("short output is not truncated")
	}
}

// A cursor move, screen clear, or carriage return from a tool would break the
// chat's gutter and background — the sanitiser drops every non-SGR escape and
// control byte while keeping the visible text.
func TestToolOutputDropsNonSGREscapesAndControls(t *testing.T) {
	cases := map[string]string{
		"cursor up":    "a\x1b[2Ab",
		"screen clear": "a\x1b[2Jb",
		"cursor hide":  "a\x1b[?25lb",
		"carriage ret": "a\rb",
		"OSC title":    "a\x1b]0;title\x07b",
		"bell":         "a\x07b",
		"two-byte esc": "a\x1b(Bb",
	}
	for name, in := range cases {
		out, _ := ToolOutput(in, 0, 0)
		if out != "ab" {
			t.Errorf("%s: should keep only the visible text, got %q", name, out)
		}
	}
}

// A colour opened on one line and left open across the newline is closed before
// the break and reopened after it, so a caller that prefixes a gutter to each
// line never paints the gutter with the tool's background (ADR-0030). This is
// the block-background shape — open once, print rows, reset at the end.
func TestToolOutputClosesColourAcrossNewlines(t *testing.T) {
	out, _ := ToolOutput("\x1b[41mAAA\nBBB\x1b[0m", 0, 0)
	if !strings.Contains(out, "AAA\x1b[0m\n") {
		t.Errorf("colour must close before the line break so the gutter stays plain: %q", out)
	}
	if !strings.Contains(out, "\n\x1b[41mBBB") {
		t.Errorf("colour must reopen on the next line's content: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Errorf("output must end reset: %q", out)
	}
}

// A per-line-reset tool (the common case: each line closes its own colour) needs
// no reopen and gains no spurious extra resets.
func TestToolOutputPerLineColourIsLeftClean(t *testing.T) {
	out, _ := ToolOutput("\x1b[31mA\x1b[0m\n\x1b[32mB\x1b[0m", 0, 0)
	if out != "\x1b[31mA\x1b[0m\n\x1b[32mB\x1b[0m" {
		t.Errorf("per-line colour should pass through unchanged: %q", out)
	}
}

// The cap measures visible runes, not the escape bytes that colour them: a long
// SGR prefix does not eat the budget.
func TestToolOutputCapCountsVisibleRunesOnly(t *testing.T) {
	out, truncated := ToolOutput("\x1b[31m"+"hello", 5, 0)
	if truncated {
		t.Errorf("5 visible runes under a cap of 5 is not truncated: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("all 5 visible runes should survive: %q", out)
	}
}

// Over the visible cap, the output is cut at the budget and the caller is told
// so it can mark the elision; the cut never lands inside an escape.
func TestToolOutputTruncatesPastTheVisibleCap(t *testing.T) {
	out, truncated := ToolOutput("abcdefgh", 3, 0)
	if !truncated {
		t.Fatalf("8 runes under a cap of 3 must truncate: %q", out)
	}
	if visible := strings.TrimSuffix(out, "\x1b[0m"); visible != "abc" {
		t.Errorf("only the first 3 visible runes should remain: %q", out)
	}
}

// The line cap bounds height where the visible cap cannot: short lines carry few
// visible runes but many rows, so a diff or a test log is cut by row count.
func TestToolOutputTruncatesPastTheLineCap(t *testing.T) {
	in := strings.Repeat("x\n", 10) // 10 one-rune rows: 20 visible runes
	out, truncated := ToolOutput(in, 0, 3)
	if !truncated {
		t.Fatalf("10 rows under a line cap of 3 must truncate: %q", out)
	}
	if got := strings.Count(out, "\n"); got != 3 {
		t.Errorf("the line cap should keep 3 rows, got %d: %q", got, out)
	}
}

// Newlines are structure, not visible width: they pass through and do not count
// against the visible cap, so a multi-line result is not cut short by its rows.
func TestToolOutputNewlinesPassThroughUncounted(t *testing.T) {
	out, truncated := ToolOutput("ab\ncd", 4, 0)
	if truncated || out != "ab\ncd" {
		t.Errorf("4 visible runes across two rows fit a cap of 4: %q (trunc=%v)", out, truncated)
	}
}
