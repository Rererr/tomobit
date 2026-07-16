package lineedit

import (
	"bufio"
	"strings"
)

// KeyType is one editing intent, already decoded from the terminal's bytes.
type KeyType int

const (
	KeyUnknown KeyType = iota
	KeyRune
	KeyEnter
	KeyNewline // insert a literal newline (Shift/Alt+Enter, or `\` before Enter)
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyWordLeft
	KeyWordRight
	KeyHome
	KeyEnd
	KeyKillToEnd
	KeyClearInput
	KeyKillWord
	KeyYank        // Ctrl-Y: put the kill slot back at the cursor
	KeyClearScreen
	KeyTab         // completion
	KeySearch      // Ctrl-R: reverse incremental history search
	KeySuspend     // Ctrl-Z: hand the terminal back and stop (unix only)
	KeyInterrupt   // Ctrl-C
	KeyEOT         // Ctrl-D: end of input on an empty buffer, delete otherwise
	KeyPaste       // Text holds the whole bracketed-paste block
)

// Key is a decoded keypress. Rune is set for KeyRune, Text for KeyPaste.
type Key struct {
	Type KeyType
	Rune rune
	Text string
}

// decode reads one key. The reader must be buffered: the lone-Escape
// heuristic in decodeEscape depends on Buffered().
func decode(r *bufio.Reader) (Key, error) {
	c, _, err := r.ReadRune()
	if err != nil {
		return Key{}, err
	}
	switch c {
	case 0x01:
		return Key{Type: KeyHome}, nil
	case 0x02:
		return Key{Type: KeyLeft}, nil
	case 0x03:
		return Key{Type: KeyInterrupt}, nil
	case 0x04:
		return Key{Type: KeyEOT}, nil
	case 0x05:
		return Key{Type: KeyEnd}, nil
	case 0x06:
		return Key{Type: KeyRight}, nil
	case 0x08, 0x7f:
		return Key{Type: KeyBackspace}, nil
	case '\t':
		return Key{Type: KeyTab}, nil
	case '\r', '\n':
		return Key{Type: KeyEnter}, nil
	case 0x0b:
		return Key{Type: KeyKillToEnd}, nil
	case 0x0c:
		return Key{Type: KeyClearScreen}, nil
	case 0x0e:
		return Key{Type: KeyDown}, nil
	case 0x10:
		return Key{Type: KeyUp}, nil
	case 0x12:
		return Key{Type: KeySearch}, nil
	case 0x15:
		return Key{Type: KeyClearInput}, nil
	case 0x17:
		return Key{Type: KeyKillWord}, nil
	case 0x19:
		return Key{Type: KeyYank}, nil
	case 0x1a:
		return Key{Type: KeySuspend}, nil
	case 0x1b:
		return decodeEscape(r)
	}
	if c < 0x20 {
		return Key{Type: KeyUnknown}, nil
	}
	return Key{Type: KeyRune, Rune: c}, nil
}

// decodeEscape reads what follows an ESC byte.
//
// A terminal delivers a whole escape sequence in one write, so it lands in
// the reader's buffer in one fill: nothing buffered after ESC means the user
// pressed the Escape key itself. That holds for a local pty; over a slow link
// a sequence can in principle arrive split across reads, and an arrow would
// then read as Escape plus a letter. The alternative is a read deadline on
// every ESC, which costs a timeout on every keystroke to fix a keypress that
// Backspace already undoes.
//
// Escape is deliberately bound to nothing — Claude Code users press it out of
// habit to interrupt, and silently wiping a long prompt that has no history
// entry yet is unrecoverable.
func decodeEscape(r *bufio.Reader) (Key, error) {
	if r.Buffered() == 0 {
		return Key{Type: KeyUnknown}, nil
	}
	c, _, err := r.ReadRune()
	if err != nil {
		return Key{}, err
	}
	switch c {
	case '[':
		return decodeCSI(r)
	case 'O':
		// SS3: some terminals send Home/End this way.
		c2, _, err := r.ReadRune()
		if err != nil {
			return Key{}, err
		}
		switch c2 {
		case 'H':
			return Key{Type: KeyHome}, nil
		case 'F':
			return Key{Type: KeyEnd}, nil
		}
		return Key{Type: KeyUnknown}, nil
	case 'b':
		return Key{Type: KeyWordLeft}, nil
	case 'f':
		return Key{Type: KeyWordRight}, nil
	case 0x7f:
		return Key{Type: KeyKillWord}, nil
	case '\r', '\n':
		return Key{Type: KeyNewline}, nil
	}
	if c < 0x20 {
		// A control byte cannot open an escape sequence (CSI/SS3 introducers
		// and Alt-letter payloads are printable), so this is the Escape key
		// with the next keystroke arriving in the same read — rapid typing,
		// not a sequence. Put the key back rather than eating it; Escape
		// itself stays unbound.
		r.UnreadRune()
	}
	return Key{Type: KeyUnknown}, nil
}

// decodeCSI reads a CSI sequence's parameter bytes up to its final byte
// (0x40..0x7e), then maps it.
func decodeCSI(r *bufio.Reader) (Key, error) {
	var params []rune
	for {
		c, _, err := r.ReadRune()
		if err != nil {
			return Key{}, err
		}
		if c >= 0x40 && c <= 0x7e {
			return csiKey(string(params), c, r)
		}
		params = append(params, c)
		// A sequence this long is not one we know; stop before growing
		// unbounded on a stream that never sends a final byte.
		if len(params) > 16 {
			return Key{Type: KeyUnknown}, nil
		}
	}
}

func csiKey(params string, final rune, r *bufio.Reader) (Key, error) {
	switch final {
	case 'A':
		return Key{Type: KeyUp}, nil
	case 'B':
		return Key{Type: KeyDown}, nil
	case 'C':
		if modified(params) {
			return Key{Type: KeyWordRight}, nil
		}
		return Key{Type: KeyRight}, nil
	case 'D':
		if modified(params) {
			return Key{Type: KeyWordLeft}, nil
		}
		return Key{Type: KeyLeft}, nil
	case 'H':
		return Key{Type: KeyHome}, nil
	case 'F':
		return Key{Type: KeyEnd}, nil
	case '~':
		switch params {
		case "1", "7":
			return Key{Type: KeyHome}, nil
		case "3":
			return Key{Type: KeyDelete}, nil
		case "4", "8":
			return Key{Type: KeyEnd}, nil
		case "200":
			return readPaste(r)
		}
		// "27;<mod>;13" is a modified Enter in xterm's modifyOtherKeys
		// form. Ghostty and foot send it for Shift+Enter with nothing
		// negotiated, which is what lets Shift+Enter insert a newline
		// without pushing a keyboard protocol that would re-encode every
		// other key this decoder speaks. As with modified(), the exact
		// modifier does not change the intent: Enter plus anything held
		// down means "newline, don't submit".
		if rest, ok := strings.CutPrefix(params, "27;"); ok && strings.HasSuffix(rest, ";13") {
			return Key{Type: KeyNewline}, nil
		}
	case 'u':
		// The same modified Enter in CSI u (fixterms/kitty) form, from
		// terminals that encode it this way unprompted. Modifier "1"
		// means none held: that is a plain Enter from a pushed keyboard
		// protocol, not a request for a newline.
		if code, mod, _ := strings.Cut(params, ";"); code == "13" {
			if mod == "" || mod == "1" {
				return Key{Type: KeyEnter}, nil
			}
			return Key{Type: KeyNewline}, nil
		}
	}
	return Key{Type: KeyUnknown}, nil
}

// modified reports whether a CSI arrow carries a modifier ("1;5C" = Ctrl,
// "1;3D" = Alt). Any modifier means word-wise movement here — the exact
// modifier does not change the intent, and terminals disagree on which one
// they send.
func modified(params string) bool {
	return strings.Contains(params, ";")
}

// readPaste consumes a bracketed-paste body up to the ESC[201~ end marker.
// The whole block becomes one key, so a pasted task never submits itself on
// the newlines inside it — the reason bracketed paste is enabled at all.
func readPaste(r *bufio.Reader) (Key, error) {
	var sb strings.Builder
	for {
		c, _, err := r.ReadRune()
		if err != nil {
			// A truncated paste still holds text the user meant to insert.
			return Key{Type: KeyPaste, Text: cleanPaste(sb.String())}, err
		}
		if c == 0x1b {
			if b, perr := r.Peek(5); perr == nil && string(b) == "[201~" {
				r.Discard(5)
				return Key{Type: KeyPaste, Text: cleanPaste(sb.String())}, nil
			}
			// Any other ESC belongs to the body; cleanPaste takes the whole
			// sequence out, not just this byte.
		}
		sb.WriteRune(c)
	}
}

// cleanPaste normalises a pasted block: escape sequences go first, CR/CRLF
// become the editor's own newline, tabs become spaces (a tab's display width
// depends on tab stops, which the renderer deliberately does not model), and
// any remaining control rune is dropped — none of them may move the cursor
// behind the renderer's back.
func cleanPaste(s string) string {
	s = stripEscapes(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	var sb strings.Builder
	for _, r := range s {
		if r == '\n' || r >= 0x20 && r != 0x7f {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// stripEscapes removes whole ANSI sequences from pasted text. Copying a task
// out of a coloured terminal is ordinary, and dropping only the ESC byte
// would leave its parameters ("[31m") sitting in the prompt as text — and in
// the ledger, as part of the recorded intent.
func stripEscapes(s string) string {
	rs := []rune(s)
	var sb strings.Builder
	for i := 0; i < len(rs); i++ {
		if rs[i] != 0x1b {
			sb.WriteRune(rs[i])
			continue
		}
		if i++; i >= len(rs) {
			break
		}
		switch rs[i] {
		case '[': // CSI: parameters up to a final byte
			for i++; i < len(rs) && (rs[i] < 0x40 || rs[i] > 0x7e); i++ {
			}
		case ']': // OSC: up to BEL or ST
			for i++; i < len(rs) && rs[i] != 0x07; i++ {
				if rs[i] == 0x1b {
					i++
					break
				}
			}
		}
		// Anything else is a two-rune sequence: both are already skipped.
	}
	return sb.String()
}
