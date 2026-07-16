package lineedit

import (
	"fmt"
	"strings"

	"github.com/rivo/uniseg"
)

// paint is where the last redraw left the terminal, in rows relative to the
// input block's first row. curRow is all the next redraw needs to find its
// way home (move up that many rows, then ESC[0J clears the block); endRow is
// what a caller needs to step past the block once the line is submitted.
type paint struct {
	curRow int
	endRow int
}

// draw builds the ANSI string that repaints the whole input block and leaves
// the cursor at pos.
//
// Wrapping is computed and emitted here rather than left to the terminal's
// autowrap: only then does the renderer know which row the cursor is on, and
// a wrapped long prompt is exactly the case this editor exists for. The cost
// is that every redraw rewrites the block — cheap at prompt sizes, and it
// keeps a single code path for the one-line and the wrapped case.
func draw(prev paint, prompt string, text []rune, pos, width int) (string, paint) {
	// Two columns is the narrowest grid a wide rune can advance in; below
	// that the wrap check could never be satisfied and would loop.
	if width < 2 {
		width = 2
	}
	var sb strings.Builder
	if prev.curRow > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", prev.curRow)
	}
	sb.WriteString("\r\x1b[0J")

	row, col := 0, 0
	curRow, curCol := 0, 0
	newline := func() {
		sb.WriteString("\r\n")
		row++
		col = 0
	}
	// wraps reports whether r has to start a new row. A rune wider than the
	// whole terminal never fits, so it stays put rather than wrapping forever.
	wraps := func(r rune) bool { return col > 0 && col+runeWidth(r) > width }
	write := func(r rune) {
		if wraps(r) {
			newline()
		}
		sb.WriteRune(r)
		col += runeWidth(r)
	}

	for _, r := range prompt {
		write(r)
	}
	for i, r := range text {
		if i == pos {
			// The cursor belongs where this rune will land — including the
			// wrap it is about to cause.
			if r != '\n' && wraps(r) {
				newline()
			}
			curRow, curCol = row, col
		}
		if r == '\n' {
			newline()
			continue
		}
		write(r)
	}
	if pos >= len(text) {
		// At the end of the text a full row leaves the terminal in its
		// deferred-wrap state, where the cursor is still on the old row.
		// Resolve it now so the next keystroke is drawn where the cursor
		// actually is.
		if col >= width {
			newline()
		}
		curRow, curCol = row, col
	}

	if row > curRow {
		fmt.Fprintf(&sb, "\x1b[%dA", row-curRow)
	}
	sb.WriteString("\r")
	if curCol > 0 {
		fmt.Fprintf(&sb, "\x1b[%dC", curCol)
	}
	return sb.String(), paint{curRow: curRow, endRow: row}
}

// runeWidth is the rune's display width in terminal columns. Rune-wise (not
// grapheme-wise): the buffer is a rune slice, so a combining sequence or a
// ZWJ emoji can render narrower than the sum computed here. Prompt text is
// the target, and for it this is exact.
func runeWidth(r rune) int {
	if r < 0x80 {
		if r < 0x20 || r == 0x7f {
			return 0
		}
		return 1
	}
	return uniseg.StringWidth(string(r))
}
