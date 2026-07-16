package lineedit

import (
	"bufio"
	"strings"
	"testing"
)

func decodeAll(t *testing.T, in string) []Key {
	t.Helper()
	r := bufio.NewReader(strings.NewReader(in))
	var keys []Key
	for {
		k, err := decode(r)
		if err != nil {
			return keys
		}
		keys = append(keys, k)
	}
}

func decodeOne(t *testing.T, in string) Key {
	t.Helper()
	keys := decodeAll(t, in)
	if len(keys) == 0 {
		t.Fatalf("%q decoded to nothing", in)
	}
	return keys[0]
}

func TestDecodeControlKeys(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want KeyType
	}{
		{"\x01", KeyHome},
		{"\x03", KeyInterrupt},
		{"\x04", KeyEOT},
		{"\x05", KeyEnd},
		{"\x0b", KeyKillToEnd},
		{"\x0c", KeyClearScreen},
		{"\x15", KeyClearInput},
		{"\x17", KeyKillWord},
		{"\x7f", KeyBackspace},
		{"\r", KeyEnter},
		{"\n", KeyEnter},
	} {
		if got := decodeOne(t, tc.in).Type; got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDecodeArrowsAndEditingSequences(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want KeyType
	}{
		{"\x1b[A", KeyUp},
		{"\x1b[B", KeyDown},
		{"\x1b[C", KeyRight},
		{"\x1b[D", KeyLeft},
		{"\x1b[H", KeyHome},
		{"\x1b[F", KeyEnd},
		{"\x1b[3~", KeyDelete},
		{"\x1b[1~", KeyHome},
		{"\x1b[4~", KeyEnd},
		{"\x1bOH", KeyHome},
		{"\x1bOF", KeyEnd},
		{"\x1bb", KeyWordLeft},
		{"\x1bf", KeyWordRight},
	} {
		if got := decodeOne(t, tc.in).Type; got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Terminals disagree on which modifier they encode for word-wise arrows; any
// modifier means the same intent here.
func TestDecodeModifiedArrowsMoveByWord(t *testing.T) {
	if got := decodeOne(t, "\x1b[1;5C").Type; got != KeyWordRight {
		t.Errorf("ctrl+right: got %v", got)
	}
	if got := decodeOne(t, "\x1b[1;3D").Type; got != KeyWordLeft {
		t.Errorf("alt+left: got %v", got)
	}
}

func TestDecodeRunesIncludingMultibyte(t *testing.T) {
	keys := decodeAll(t, "a日")
	if len(keys) != 2 {
		t.Fatalf("got %d keys", len(keys))
	}
	if keys[0].Type != KeyRune || keys[0].Rune != 'a' {
		t.Errorf("ascii: got %+v", keys[0])
	}
	if keys[1].Type != KeyRune || keys[1].Rune != '日' {
		t.Errorf("multibyte rune must survive as one key: got %+v", keys[1])
	}
}

// A lone Escape must not eat the next keystroke: with nothing buffered behind
// it, ESC is the Escape key itself, and the rune after it is a rune.
func TestDecodeLoneEscapeIsNotASequence(t *testing.T) {
	if got := decodeOne(t, "\x1b").Type; got != KeyUnknown {
		t.Errorf("lone ESC: got %v, want KeyUnknown", got)
	}
}

// The whole point of bracketed paste: newlines inside a pasted block are
// text, not submits.
func TestDecodePasteBecomesOneKeyWithNewlinesIntact(t *testing.T) {
	k := decodeOne(t, "\x1b[200~one\ntwo\x1b[201~")
	if k.Type != KeyPaste {
		t.Fatalf("got %v, want KeyPaste", k.Type)
	}
	if k.Text != "one\ntwo" {
		t.Errorf("paste text: got %q", k.Text)
	}
}

func TestDecodePasteNormalisesCarriageReturnsAndTabs(t *testing.T) {
	k := decodeOne(t, "\x1b[200~a\r\nb\rc\td\x1b[201~")
	if k.Text != "a\nb\nc    d" {
		t.Errorf("got %q", k.Text)
	}
}

// Pasting a task copied out of a coloured terminal must yield the text, not
// the codes: the whole sequence goes, not just the ESC that introduced it.
func TestDecodePasteDropsEscapeSequencesAndControlBytes(t *testing.T) {
	k := decodeOne(t, "\x1b[200~a\x07\x1b[31mb\x1b]0;title\x07c\x1b[201~")
	if k.Text != "abc" {
		t.Errorf("got %q", k.Text)
	}
}

// Enter arrives as a key; the trailing-backslash rule lives in the editor, so
// what matters here is only that the paste ended and the next key is read.
func TestDecodeContinuesAfterAPasteEnds(t *testing.T) {
	keys := decodeAll(t, "\x1b[200~x\x1b[201~\r")
	if len(keys) != 2 || keys[0].Type != KeyPaste || keys[1].Type != KeyEnter {
		t.Errorf("got %+v", keys)
	}
}
