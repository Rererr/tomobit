package mdlite

import "strings"

// ToolOutput prepares a tool's raw output for the chat's terminal view
// (ADR-0030). Tool output already carries its own ANSI — a Bash colour demo, a
// coloured diff — so it must not go through Render, which is for prose and would
// mangle live escapes (a `#` would bold, a backtick would be eaten, an SGR byte
// would be misread). ToolOutput keeps only SGR (colour/style) sequences
// (`ESC [ … m`) and drops every other escape and control byte: a stray cursor
// move, screen clear, or carriage return from a tool would break the chat's
// gutter and background, and passing arbitrary escapes through would hand the
// terminal to whatever the provider ran (Decision 2).
//
// A colour left open across a newline is closed before the break and reopened on
// the next line's first output, so the caller's per-line gutter (turnView.line's
// indent) is never painted by a background a tool opened and did not reset — the
// "gutter carries no background" invariant render.go's newline() keeps for the
// input, now kept for tool output too. A block-background demo (open once, print
// several lines, reset at the end) is the natural shape that would otherwise
// bleed.
//
// It caps the output at maxVisible visible runes and maxLines lines, whichever
// comes first, and reports whether it cut so the caller can mark the elision
// (Decision 3). SGR bytes and newlines do not count toward maxVisible — it
// measures visible width, not the escapes that colour it nor the rows — so the
// line cap is what bounds height for short-line output (a diff, a test log). A
// cut never lands inside a sequence, because a sequence is copied whole or not
// at all. A cut also never ends in a newline: the marker the caller appends
// belongs directly under the last kept line, and the lines shown must equal
// the lines a budgeting caller is charged (ADR-0031). A non-positive cap
// disables that dimension. The result always ends reset when it opened any
// colour, so no unclosed style reaches the next line.
func ToolOutput(s string, maxVisible, maxLines int) (out string, truncated bool) {
	runes := []rune(s)
	var b strings.Builder
	var active strings.Builder // the SGR sequences in effect since the last reset
	visible, lines := 0, 0
	pending := false // colour was closed at a line break; reopen before next output
	reopen := func() {
		if pending {
			b.WriteString(active.String())
			pending = false
		}
	}
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case r == 0x1b: // an escape sequence begins — keep it only if it is SGR
			seq, next, isSGR := scanEscape(runes, i)
			switch {
			case !isSGR:
			case isReset(seq):
				if active.Len() > 0 && !pending {
					b.WriteString(seq) // a reset only shows when colour is open
				}
				active.Reset()
				pending = false
			default:
				reopen() // re-establish prior colour before adding to it
				b.WriteString(seq)
				active.WriteString(seq)
			}
			i = next
		case r == '\r' || (r < 0x20 && r != '\n' && r != '\t'):
			// A carriage return would drag the cursor back over the gutter;
			// other C0 controls carry no display and can misbehave. Drop both.
			i++
		case r == '\n':
			lines++
			if maxLines > 0 && lines >= maxLines {
				// The cap lands on this break: stop before writing it, so the
				// caller's truncation marker sits directly under the last kept
				// line (a kept newline drew a blank row above the marker —
				// visible on a real turn) and a caller charging height by
				// lines shown is not billed for a row that never rendered
				// (ADR-0031's turn budget counts them). Any open colour is
				// closed by the reset below, as at any other end.
				truncated = true
				i = len(runes)
				break
			}
			if active.Len() > 0 && !pending {
				b.WriteString(ansiReset) // close colour so the break stays plain
			}
			b.WriteByte('\n')
			pending = active.Len() > 0 // reopened lazily on the next line
			i++
		case maxVisible > 0 && visible >= maxVisible:
			truncated = true
			i = len(runes) // stop: the visible budget is spent
		default:
			reopen()
			b.WriteRune(r)
			visible++
			i++
		}
	}
	if active.Len() > 0 && !pending {
		b.WriteString(ansiReset) // close any run the output left open
	}
	return b.String(), truncated
}

// isReset reports whether an SGR sequence clears all attributes (ESC[0m, ESC[m,
// ESC[0;0m …), so ToolOutput can forget the colours it was tracking. A sequence
// whose parameters are only zeros and separators is a full reset; anything with
// a non-zero code sets an attribute and is kept as state.
func isReset(seq string) bool {
	if len(seq) < 3 { // "\x1b[" + final; too short to hold a parameter
		return false
	}
	params := seq[2 : len(seq)-1] // between "\x1b[" and the final 'm'
	return strings.Trim(params, "0;") == ""
}

// scanEscape reads one escape sequence beginning at runes[i] (an ESC). It
// returns the sequence text, the index just past it, and whether it is an SGR
// (colour/style) sequence — the only kind ToolOutput keeps. Malformed or
// unterminated sequences are consumed to the end and reported non-SGR, so a
// broken escape is dropped rather than leaking its tail as text.
func scanEscape(runes []rune, i int) (seq string, next int, isSGR bool) {
	if i+1 >= len(runes) {
		return "", len(runes), false
	}
	switch runes[i+1] {
	case '[': // CSI: ESC [ <params/intermediates 0x20-0x3f> <final 0x40-0x7e>
		j := i + 2
		for j < len(runes) && runes[j] >= 0x20 && runes[j] <= 0x3f {
			j++
		}
		if j < len(runes) && runes[j] >= 0x40 && runes[j] <= 0x7e {
			if runes[j] == 'm' {
				return string(runes[i : j+1]), j + 1, true
			}
			return "", j + 1, false // a non-SGR CSI (cursor move, clear, …)
		}
		return "", len(runes), false // unterminated CSI
	case ']': // OSC: ESC ] … terminated by BEL or ST (ESC \)
		for j := i + 2; j < len(runes); j++ {
			if runes[j] == 0x07 {
				return "", j + 1, false
			}
			if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == 0x5c {
				return "", j + 2, false
			}
		}
		return "", len(runes), false
	default:
		// An nF/Fp/Fe/Fs escape: ESC, then zero or more intermediate bytes
		// (0x20-0x2f), then one final byte. Covers ESC ( B (charset select),
		// ESC = , ESC c , and the like. Consume the whole run so no final byte
		// leaks out as text.
		j := i + 1
		for j < len(runes) && runes[j] >= 0x20 && runes[j] <= 0x2f {
			j++
		}
		if j < len(runes) {
			j++ // the final byte
		}
		return "", j, false
	}
}
