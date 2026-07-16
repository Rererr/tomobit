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

// maxHistory caps the history ring, and the count kept when the on-disk file
// is compacted (ADR-0024 Decision 1). Recalling a moment ago is the friction
// this editor exists to remove; the persisted file just carries that reach
// across process boundaries.
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

	// Completer, when set, drives Tab completion. Given the whole buffer and
	// the cursor's rune index it returns the candidate replacements for the
	// token at the cursor and the rune index where that token starts
	// (start<0 or start>pos means "nothing to complete here"). The editor
	// stays ignorant of what is being completed — the caller owns the
	// vocabulary, the same boundary ADR-0022 Decision 3 drew for lineedit.
	Completer func(text string, pos int) (candidates []string, start int)

	// kill is the one-slot kill buffer Ctrl-U/K/W fill and Ctrl-Y empties
	// (ADR-0024 Decision 3). It survives across lines, as readline's does.
	kill string

	// histPath is the file history is appended to; "" disables persistence.
	// histWarn takes a one-line note the first time an append fails, so a
	// read-only home does not silently drop history yet does not nag per line.
	// histLines counts the file's entries so a long-lived process compacts
	// in-flight instead of growing the file until its next restart.
	histPath   string
	histWarn   io.Writer
	histWarned bool
	histLines  int
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
// are dropped — pressing Up should reach the last thing worth retyping. The
// same line is appended to the history file if one is set, so the next process
// starts where this one left off.
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
	e.appendHistory(s)
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
	return e.readRaw(prompt, fd, old)
}

func (e *Editor) readCooked(prompt string) (string, error) {
	fmt.Fprint(e.out, prompt)
	line, err := e.r.ReadString('\n')
	if err != nil && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (e *Editor) readRaw(prompt string, fd int, old *term.State) (string, error) {
	var b buffer
	// hist indexes history; len(history) means "the draft", so Down from the
	// oldest recall walks back to what was actually being typed.
	hist, draft := len(e.history), ""
	p := paint{}
	// pending replays the key that ended a reverse search as ordinary editing
	// without touching the terminal — a one-slot pushback ahead of decode.
	var pending *Key
	// killAccum is true right after a word kill, so a run of Ctrl-W stacks into
	// one slot (readline's behavior) but any other key breaks the run.
	killAccum := false

	redraw := func() {
		s, np := draw(p, prompt, b.runes, b.pos, e.width())
		io.WriteString(e.out, s)
		p = np
	}
	redraw()

	for {
		var k Key
		if pending != nil {
			k, pending = *pending, nil
		} else {
			var err error
			k, err = decode(e.r)
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
		}

		wasKillWord := false
		switch k.Type {
		case KeyRune:
			b.insert(k.Rune)
		case KeyPaste:
			b.insert([]rune(k.Text)...)
		case KeyEnter:
			// A trailing backslash means "not done yet". Shift+Enter only
			// arrives distinctly from terminals that encode a modified Enter
			// (Ghostty, foot, kitty family — see csiKey); this is the newline
			// keystroke that works everywhere else.
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
			if s := b.killToEnd(); s != "" {
				e.kill = s
			}
		case KeyClearInput:
			if s := b.clear(); s != "" {
				e.kill = s
			}
		case KeyKillWord:
			if s := b.killWord(); s != "" {
				e.kill = stackKill(e.kill, s, killAccum)
				wasKillWord = true
			}
		case KeyYank:
			if e.kill != "" {
				b.insert([]rune(e.kill)...)
			}
		case KeyTab:
			e.complete(&b, &p)
		case KeySearch:
			submit, next, serr := e.reverseSearch(&b, &p)
			if serr != nil {
				e.finish(p)
				if errors.Is(serr, io.EOF) {
					return "", io.EOF
				}
				return "", fmt.Errorf("lineedit: reading input: %w", serr)
			}
			if submit {
				e.finish(p)
				return b.String(), nil
			}
			if next.Type != KeyUnknown {
				pending = &next
			}
		case KeySuspend:
			e.suspend(fd, old, &p)
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
		killAccum = wasKillWord
		redraw()
	}
}

// suspend hands the terminal back cooked, stops this process group, and on
// resume reclaims raw mode and forces a full repaint (ADR-0024 Decision 7).
// Off unix raiseSIGTSTP is a no-op and this returns without disturbing the
// line — Ctrl-Z there is simply ignored rather than half-toggling the terminal.
func (e *Editor) suspend(fd int, old *term.State, p *paint) {
	if !suspendSupported {
		return
	}
	// Step past the block before stopping. After SIGCONT the cursor sits
	// wherever the shell's job-control chatter left it, which is unknowable —
	// and a fresh paint{} anchors the repaint at the cursor's row, so on a
	// wrapped block resumed in place the stale rows above it would survive.
	// Stopping below the block instead makes every resume start from a clean
	// line: the suspended draft stays in the scrollback and the resumed one
	// is drawn anew, which is also what a shell's own editor does.
	e.finish(*p)
	io.WriteString(e.out, pasteOff)
	term.Restore(fd, old)
	raiseSIGTSTP()
	term.MakeRaw(fd)
	io.WriteString(e.out, pasteOn)
	*p = paint{}
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
