// Package lineedit is Tomobit's inline line editor (ADR-0022 Decision 3).
//
// It owns one line of the terminal the user is already looking at: raw mode
// while a line is being typed, cooked mode the rest of the time. It is
// deliberately not a full-screen TUI — Tomo's lines and the provider's
// output must stay in the scrollback like any other log, the same reason the
// avatar is drawn with half-blocks instead of a canvas (ADR-0008).
//
// Everything but the terminal's physics is hand-rolled: golang.org/x/term is
// used for raw mode and the window size only, and uniseg for display width
// (Japanese is full-width — cursor math is wrong without it).
package lineedit

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrInterrupt is returned when the line was abandoned with Ctrl-C. The
// distinction from io.EOF (Ctrl-D) matters to the caller: one is "not this
// line", the other is "no more lines".
var ErrInterrupt = errors.New("lineedit: interrupted")

// maxHistory caps the in-process history ring. History is not persisted:
// recalling what you typed a moment ago is the friction this editor exists
// to remove; recalling last week's prompts is a different feature.
const maxHistory = 200

// Bracketed paste (DEC 2004). Without it a pasted multi-line task submits
// itself on the first newline — the exact accident that makes long prompts
// painful to hand over.
const (
	pasteOn  = "\x1b[?2004h"
	pasteOff = "\x1b[?2004l"
)

// Editor reads lines from a terminal. Zero value is not usable — use New.
type Editor struct {
	in      *os.File
	out     *os.File
	r       *bufio.Reader
	history []string
}

func New(in, out *os.File) *Editor {
	return &Editor{in: in, out: out, r: bufio.NewReader(in)}
}

// Interactive reports whether both ends are a terminal. A pipe gets the
// cooked path: same reader, one line per read, no ANSI.
func (e *Editor) Interactive() bool {
	return term.IsTerminal(int(e.in.Fd())) && term.IsTerminal(int(e.out.Fd()))
}

// Reader exposes the editor's buffered view of its input, so that prompts
// asked between lines (the adoption question, Tomo's) read from the same
// place.
//
// What must not happen is a prompt reading os.Stdin directly: this reader has
// already pulled bytes off that file — typeahead typed while a provider was
// working — and a reader over the file cannot see them. It would block for
// input that has already arrived, and the bytes would be stranded here.
// (Passing this value back through bufio.NewReader is harmless — measured:
// the stdlib hands the same reader back rather than stacking a second one.)
func (e *Editor) Reader() *bufio.Reader { return e.r }

// AddHistory records a submitted line. Blank lines and an immediate repeat
// are dropped — pressing Up should reach the last thing worth retyping.
func (e *Editor) AddHistory(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	if n := len(e.history); n > 0 && e.history[n-1] == s {
		return
	}
	e.history = append(e.history, s)
	if len(e.history) > maxHistory {
		e.history = append([]string(nil), e.history[1:]...)
	}
}

// ReadLine prints prompt and returns the submitted line. It returns io.EOF
// on Ctrl-D at an empty line (or a closed stdin) and ErrInterrupt on Ctrl-C.
func (e *Editor) ReadLine(prompt string) (string, error) {
	if !e.Interactive() {
		return e.readCooked(prompt)
	}
	fd := int(e.in.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		// No raw mode on this terminal: a degraded line beats no chat.
		return e.readCooked(prompt)
	}
	defer term.Restore(fd, old)
	io.WriteString(e.out, pasteOn)
	defer io.WriteString(e.out, pasteOff)
	return e.readRaw(prompt)
}

func (e *Editor) readCooked(prompt string) (string, error) {
	fmt.Fprint(e.out, prompt)
	line, err := e.r.ReadString('\n')
	if err != nil && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (e *Editor) readRaw(prompt string) (string, error) {
	var b buffer
	// hist indexes history; len(history) means "the draft", so Down from the
	// oldest recall walks back to what was actually being typed.
	hist, draft := len(e.history), ""
	p := paint{}

	redraw := func() {
		s, np := draw(p, prompt, b.runes, b.pos, e.width())
		io.WriteString(e.out, s)
		p = np
	}
	redraw()

	for {
		k, err := decode(e.r)
		if err != nil {
			// The terminal went away mid-line. EOF is the ordinary case
			// (stdin closed) and means "no more lines"; anything else is a
			// real fault and must not be dressed up as a clean exit.
			e.finish(p)
			if errors.Is(err, io.EOF) {
				return "", io.EOF
			}
			return "", fmt.Errorf("lineedit: reading input: %w", err)
		}
		switch k.Type {
		case KeyRune:
			b.insert(k.Rune)
		case KeyPaste:
			b.insert([]rune(k.Text)...)
		case KeyEnter:
			// A trailing backslash means "not done yet". Terminals send
			// nothing distinct for Shift+Enter, so this is the one keystroke
			// for a newline that works everywhere.
			if b.pos == len(b.runes) && strings.HasSuffix(b.String(), "\\") {
				b.backspace()
				b.insert('\n')
				break
			}
			e.finish(p)
			return b.String(), nil
		case KeyNewline:
			b.insert('\n')
		case KeyBackspace:
			b.backspace()
		case KeyDelete:
			b.del()
		case KeyEOT:
			if b.empty() {
				e.finish(p)
				return "", io.EOF
			}
			b.del()
		case KeyInterrupt:
			e.finish(p)
			return "", ErrInterrupt
		case KeyLeft:
			b.left()
		case KeyRight:
			b.right()
		case KeyWordLeft:
			b.wordLeft()
		case KeyWordRight:
			b.wordRight()
		case KeyHome:
			b.home()
		case KeyEnd:
			b.end()
		case KeyKillToEnd:
			b.killToEnd()
		case KeyClearInput:
			b.clear()
		case KeyKillWord:
			b.killWord()
		case KeyUp:
			// Inside a pasted block Up is movement; only at the top line does
			// it mean history.
			if b.up() {
				break
			}
			if hist == 0 {
				break
			}
			if hist == len(e.history) {
				draft = b.String()
			}
			hist--
			b.set(e.history[hist])
		case KeyDown:
			if b.down() {
				break
			}
			if hist >= len(e.history) {
				break
			}
			hist++
			if hist == len(e.history) {
				b.set(draft)
			} else {
				b.set(e.history[hist])
			}
		case KeyClearScreen:
			io.WriteString(e.out, "\x1b[H\x1b[2J")
			p = paint{}
		}
		redraw()
	}
}

// finish steps the cursor past the input block, so whatever prints next
// starts on its own line.
func (e *Editor) finish(p paint) {
	if p.endRow > p.curRow {
		fmt.Fprintf(e.out, "\x1b[%dB", p.endRow-p.curRow)
	}
	io.WriteString(e.out, "\r\n")
}

func (e *Editor) width() int {
	w, _, err := term.GetSize(int(e.out.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}
