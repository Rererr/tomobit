package lineedit

import (
	"fmt"
	"strings"
	"testing"
)

// cursorCol is the column the redraw finally moves to: the trailing
// "\r" + ESC[<n>C. Column 0 emits no move, so an absent code means 0.
func cursorCol(t *testing.T, s string) int {
	t.Helper()
	i := strings.LastIndex(s, "\r")
	if i < 0 {
		t.Fatalf("redraw never returns to column 0: %q", s)
	}
	tail := s[i+1:]
	if tail == "" {
		return 0
	}
	var col int
	if _, err := fmt.Sscanf(tail, "\x1b[%dC", &col); err != nil {
		t.Fatalf("unexpected tail %q in %q", tail, s)
	}
	return col
}

func TestDrawPlacesTheCursorAfterThePromptOnAnEmptyLine(t *testing.T) {
	s, p := draw(paint{}, "❯ ", nil, 0, 80, 0, "")
	if got := cursorCol(t, s); got != 2 {
		t.Errorf("cursor col: got %d, want 2 (the prompt's width)", got)
	}
	if p.curRow != 0 || p.endRow != 0 {
		t.Errorf("one row expected: got %+v", p)
	}
}

// Japanese is full-width: counting runes instead of columns puts the cursor
// in the wrong place on every prompt the user actually types.
func TestDrawCountsFullWidthRunesAsTwoColumns(t *testing.T) {
	text := []rune("日本語")
	s, _ := draw(paint{}, "❯ ", text, len(text), 80, 0, "")
	if got := cursorCol(t, s); got != 8 {
		t.Errorf("cursor col: got %d, want 8 (2 prompt + 3 runes x 2)", got)
	}
}

func TestDrawPlacesTheCursorMidText(t *testing.T) {
	s, _ := draw(paint{}, "❯ ", []rune("abcdef"), 2, 80, 0, "")
	if got := cursorCol(t, s); got != 4 {
		t.Errorf("cursor col: got %d, want 4", got)
	}
}

// The renderer wraps the text itself rather than leaving it to the terminal:
// only then does it know which row the cursor is on.
func TestDrawWrapsAtTheTerminalEdgeAndTracksTheRow(t *testing.T) {
	// width 10, prompt 2 => 8 columns of text on the first row.
	_, p := draw(paint{}, "❯ ", []rune("0123456789"), 10, 10, 0, "")
	if p.endRow != 1 {
		t.Errorf("10 runes in 8 columns should wrap to row 1: got %+v", p)
	}
	if p.curRow != 1 {
		t.Errorf("the cursor is on the wrapped row: got %+v", p)
	}
}

// A full-width rune never straddles the edge: it starts the next row.
func TestDrawNeverSplitsAWideRuneAcrossRows(t *testing.T) {
	// width 5, prompt 2 => 3 free columns: 日(2) fits, 本(2) does not.
	s, p := draw(paint{}, "❯ ", []rune("日本"), 2, 5, 0, "")
	if p.endRow != 1 {
		t.Fatalf("the second wide rune should wrap: got %+v", p)
	}
	if strings.Count(s, "\r\n") != 1 {
		t.Errorf("exactly one row break expected: %q", s)
	}
	if got := cursorCol(t, s); got != 2 {
		t.Errorf("cursor col on the wrapped row: got %d, want 2", got)
	}
}

// A row filled to the last column leaves the terminal in its deferred-wrap
// state, where the cursor is still on the old row. The renderer resolves it,
// or the next keystroke would be drawn a row too high.
func TestDrawResolvesTheDeferredWrapAtTheEndOfAFullRow(t *testing.T) {
	_, p := draw(paint{}, "", []rune("12345"), 5, 5, 0, "")
	if p.curRow != 1 || p.endRow != 1 {
		t.Errorf("a full row should push the cursor to the next one: got %+v", p)
	}
}

func TestDrawBreaksLinesOnNewlines(t *testing.T) {
	text := []rune("one\ntwo")
	s, p := draw(paint{}, "❯ ", text, len(text), 80, 0, "")
	if p.endRow != 1 {
		t.Errorf("a newline makes a second row: got %+v", p)
	}
	if got := cursorCol(t, s); got != 3 {
		t.Errorf("cursor col: got %d, want 3 (the second line has no prompt)", got)
	}
}

// With a hanging indent, a hard newline starts the next row at the gutter, not
// the terminal's left edge — so a multi-line input aligns under the first line.
func TestDrawIndentsContinuationRowsAfterANewline(t *testing.T) {
	text := []rune("one\ntwo")
	s, p := draw(paint{}, " ❯ ", text, len(text), 80, 2, "")
	if p.endRow != 1 {
		t.Fatalf("a newline makes a second row: got %+v", p)
	}
	if !strings.Contains(s, "\r\n  two") {
		t.Errorf("the continuation row should start at a 2-column indent: %q", s)
	}
	if got := cursorCol(t, s); got != 5 {
		t.Errorf("cursor col: got %d, want 5 (2 indent + \"two\")", got)
	}
}

// A wrapped row indents to the gutter too, so long input hugs the same margin
// as the answer below it rather than the terminal edge.
func TestDrawIndentsWrappedRows(t *testing.T) {
	// width 10, prompt " ❯ " (3) => 7 columns of text on the first row; the
	// wrapped remainder then starts at the 2-column hanging indent.
	s, p := draw(paint{}, " ❯ ", []rune("0123456789"), 10, 10, 2, "")
	if p.endRow != 1 {
		t.Fatalf("10 runes past a 7-column first row should wrap: got %+v", p)
	}
	if !strings.Contains(s, "\r\n  ") {
		t.Errorf("the wrapped row should start at a 2-column indent: %q", s)
	}
}

// An indent as wide as the terminal must not make a continuation row loop
// forever with no room to place its first rune.
func TestDrawSurvivesAnIndentAsWideAsTheTerminal(t *testing.T) {
	draw(paint{}, " ❯ ", []rune("日本語"), 3, 2, 9, "")
}

// The background rides the typed text, not the prompt marker, and is always
// turned off before the redraw ends so nothing bleeds past the line.
func TestDrawPaintsBackgroundBehindTextOnly(t *testing.T) {
	const bg = "\x1b[48;5;237m"
	s, _ := draw(paint{}, " ❯ ", []rune("hi"), 2, 80, 2, bg)
	if pi, bi := strings.Index(s, " ❯ "), strings.Index(s, bg); pi < 0 || bi < pi {
		t.Fatalf("the prompt marker precedes the background — it is unstyled: %q", s)
	}
	if !strings.Contains(s, bg+"h") {
		t.Errorf("the text should sit inside the background run: %q", s)
	}
	if !strings.Contains(s, "\x1b[0m") {
		t.Fatalf("the background must be reset: %q", s)
	}
	if strings.LastIndex(s, bg) > strings.LastIndex(s, "\x1b[0m") {
		t.Errorf("no background code may survive the final reset: %q", s)
	}
}

// On a wrapped line the background stops at the row break and the gutter, then
// reopens on the continuation — it never paints the newline or the indent.
func TestDrawBackgroundStopsAtRowBreaksAndGutter(t *testing.T) {
	const bg = "\x1b[48;5;237m"
	s, _ := draw(paint{}, " ❯ ", []rune("0123456789"), 10, 10, 2, bg)
	if !strings.Contains(s, "\x1b[0m\r\n  "+bg) {
		t.Errorf("background should close before the break and reopen after the gutter: %q", s)
	}
}

// An empty style leaves the output byte-for-byte what it was before the
// background existed: no stray SGR on a plain prompt.
func TestDrawWithoutStyleEmitsNoSGRBeyondCursorMoves(t *testing.T) {
	s, _ := draw(paint{}, " ❯ ", []rune("hi"), 2, 80, 2, "")
	if strings.Contains(s, "\x1b[0m") || strings.Contains(s, "48;5;") {
		t.Errorf("an unstyled line must carry no colour codes: %q", s)
	}
}

// Every redraw starts by returning to the block's first row and clearing
// downwards: a shorter text must not leave the old one on screen.
func TestDrawReturnsToTheBlockStartBeforeClearing(t *testing.T) {
	s, _ := draw(paint{curRow: 2}, "❯ ", []rune("x"), 1, 80, 0, "")
	if !strings.HasPrefix(s, "\x1b[2A\r\x1b[0J") {
		t.Errorf("should move up 2 rows, return to column 0, clear down: %q", s)
	}
}

func TestDrawSurvivesAnAbsurdlyNarrowTerminal(t *testing.T) {
	// Nothing to assert but "it returns": a width below one wide rune must
	// not make the wrap check loop forever.
	draw(paint{}, "❯ ", []rune("日本語"), 3, 1, 0, "")
}

func TestRuneWidth(t *testing.T) {
	for _, tc := range []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{'日', 2},
		{'ｱ', 1}, // half-width katakana
		{'é', 1},
	} {
		if got := runeWidth(tc.r); got != tc.want {
			t.Errorf("%q: got %d, want %d", tc.r, got, tc.want)
		}
	}
}
