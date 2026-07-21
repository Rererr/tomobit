package lineedit

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// cookedReader drives readCooked over a fixed input without a terminal:
// SetReader swaps the buffered input, and an empty prompt keeps the *os.File
// out (os.Stdout) silent.
func cookedReader(t *testing.T, input string) *Editor {
	t.Helper()
	e := New(os.Stdin, os.Stdout)
	e.SetReader(strings.NewReader(input))
	return e
}

// errReader models a broken pipe: it yields data once, then a fixed non-EOF
// error on every Read after — unlike strings.Reader, which only ever settles
// on io.EOF, this exercises the fault path readCooked must not confuse with
// the pipe's own close.
type errReader struct {
	data string
	err  error
	done bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		if r.data != "" {
			return copy(p, r.data), nil
		}
	}
	return 0, r.err
}

// A trailing `\` joins the next line with a newline, dropping the `\` — the
// cooked mirror of raw mode's `\`+Enter (ADR-0032 Decision 2).
func TestReadCookedJoinsBackslashContinuation(t *testing.T) {
	e := cookedReader(t, "実装して。仕様は\\\n- Aであること\\\n- Bであること\n")
	got, err := e.readCooked("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if want := "実装して。仕様は\n- Aであること\n- Bであること"; got != want {
		t.Errorf("continuation join: got %q, want %q", got, want)
	}
}

// `\\` peels one `\`, leaving a literal backslash, and still continues —
// exactly what raw mode does (foo\\ + Enter → foo\ + newline).
func TestReadCookedDoubleBackslashLeavesLiteralAndContinues(t *testing.T) {
	e := cookedReader(t, "foo\\\\\n\n")
	got, err := e.readCooked("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if want := "foo\\\n"; got != want {
		t.Errorf("literal backslash then continue: got %q, want %q", got, want)
	}
}

// EOF while a continuation is open returns the accumulation, not io.EOF: a
// pipe's dangling `\` is a whole a script flushed, unlike raw's live draft.
func TestReadCookedEOFMidContinuationReturnsAccumulated(t *testing.T) {
	e := cookedReader(t, "first\\\nsecond")
	got, err := e.readCooked("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if want := "first\nsecond"; got != want {
		t.Errorf("EOF mid continuation: got %q, want %q", got, want)
	}
}

// A final line without a trailing newline is still one turn — the pre-ADR-0032
// behavior the continuation must not regress.
func TestReadCookedFinalLineWithoutNewline(t *testing.T) {
	e := cookedReader(t, "no newline")
	got, err := e.readCooked("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "no newline" {
		t.Errorf("no-newline final line: got %q", got)
	}
}

// The `\` is judged after \r\n is stripped, so a CRLF pipe continues the same
// way a LF one does.
func TestReadCookedContinuationSurvivesCRLF(t *testing.T) {
	e := cookedReader(t, "one\\\r\ntwo\r\n")
	got, err := e.readCooked("")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if want := "one\ntwo"; got != want {
		t.Errorf("CRLF continuation: got %q, want %q", got, want)
	}
}

// A closed stdin with nothing accumulated is still io.EOF — the caller's "no
// more turns" signal must survive the continuation rework.
func TestReadCookedEmptyInputIsEOF(t *testing.T) {
	e := cookedReader(t, "")
	if _, err := e.readCooked(""); !errors.Is(err, io.EOF) {
		t.Errorf("empty input must be io.EOF, got %v", err)
	}
}

// A non-EOF fault before any line arrives must not be reported as io.EOF —
// the caller (chat.go) treats io.EOF as "no more turns" and would end the
// session cleanly instead of surfacing the broken input.
func TestReadCookedNonEOFErrorIsNotReportedAsEOF(t *testing.T) {
	fault := errors.New("broken pipe")
	e := New(os.Stdin, os.Stdout)
	e.SetReader(&errReader{err: fault})
	_, err := e.readCooked("")
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if errors.Is(err, io.EOF) {
		t.Errorf("non-EOF fault reported as io.EOF: %v", err)
	}
	if !errors.Is(err, fault) {
		t.Errorf("fault not wrapped in returned error: got %v, want wrapping %v", err, fault)
	}
}

// A non-EOF fault mid-continuation must not be folded into "the pipe closed,
// flush the accumulation as a turn" (ADR-0032 Decision 2 covers EOF only): a
// dropped connection leaves a truncated fragment, not the whole instruction a
// human or script meant to send.
func TestReadCookedNonEOFErrorMidContinuationIsNotReturnedAsATurn(t *testing.T) {
	fault := errors.New("broken pipe")
	e := New(os.Stdin, os.Stdout)
	e.SetReader(&errReader{data: "a\\\n", err: fault})
	got, err := e.readCooked("")
	if err == nil {
		t.Fatalf("want an error, got turn %q with nil error", got)
	}
	if !errors.Is(err, fault) {
		t.Errorf("fault not wrapped in returned error: got %v, want wrapping %v", err, fault)
	}
}
