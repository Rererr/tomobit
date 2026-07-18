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
//
// contCol is the hanging indent every continuation row (a wrap or a hard
// newline) starts at, so a multi-line input aligns under a gutter instead of
// the terminal's left edge. Zero keeps the old flush-left wrap.
//
// textStyle, when non-empty, is an SGR sequence (a faint background) laid
// behind the typed text — not the prompt, not the gutter — so the user's line
// reads as one block, the way Claude Code marks a message. It is turned off at
// every row break and before the final cursor move, so no colour bleeds into
// the gutter or the scrollback tail.
func draw(prev paint, prompt string, text []rune, pos, width, contCol int, textStyle string) (string, paint) {
	// Two columns is the narrowest grid a wide rune can advance in; below
	// that the wrap check could never be satisfied and would loop.
	if width < 2 {
		width = 2
	}
	// An indent as wide as the row would leave no room to place a rune after a
	// wrap, and — since the row's first rune never wraps — that first rune would
	// overflow forever. Clamp below the width so a continuation row always has
	// at least one column of its own.
	if contCol < 0 {
		contCol = 0
	}
	if contCol > width-1 {
		contCol = width - 1
	}
	var sb strings.Builder
	if prev.curRow > 0 {
		fmt.Fprintf(&sb, "\x1b[%dA", prev.curRow)
	}
	sb.WriteString("\r\x1b[0J")

	row, col := 0, 0
	curRow, curCol := 0, 0
	// styleOn tracks whether the background is currently painted, so the SGR is
	// emitted once per run rather than per rune. setStyle is a no-op when no
	// style was asked for, keeping the plain-prompt output byte-for-byte as it
	// was before.
	styleOn := false
	setStyle := func(on bool) {
		if textStyle == "" || on == styleOn {
			return
		}
		if on {
			sb.WriteString(textStyle)
		} else {
			sb.WriteString("\x1b[0m")
		}
		styleOn = on
	}
	// fillToWidth extends the current row's background from the last painted
	// column to the terminal's right edge, so a styled input reads as a filled
	// block edge to edge rather than a bar ending at the text (ADR-0030's
	// companion: the whole question area is background). A no-op when no style
	// is asked for or the row carries no text background — the prompt and a
	// blank continuation stay their plain selves. Idempotent: it advances col
	// to width so a second call on the same row adds nothing.
	fillToWidth := func() {
		if !styleOn || col >= width {
			return
		}
		for c := col; c < width; c++ {
			sb.WriteByte(' ')
		}
		col = width
	}
	newline := func() {
		fillToWidth()   // paint this row's background out to the edge first
		setStyle(false) // the row break and the gutter carry no background
		sb.WriteString("\r\n")
		row++
		for i := 0; i < contCol; i++ {
			sb.WriteByte(' ')
		}
		col = contCol
	}
	// wraps reports whether r has to start a new row. A rune at the row's start
	// column (contCol, or 0 with no indent) stays put rather than wrapping
	// forever, the same guard a rune wider than the whole terminal gets.
	wraps := func(r rune) bool { return col > contCol && col+runeWidth(r) > width }
	// write lays out one prompt rune. The prompt takes no background, so it does
	// not touch the style — it stays whatever setStyle last left, which is off.
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
		if wraps(r) {
			newline()
		}
		setStyle(true) // background rides only the typed text, per row
		sb.WriteRune(r)
		col += runeWidth(r)
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
	fillToWidth()   // the final row's background reaches the edge too
	setStyle(false) // no background across the cursor move or into scrollback

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
