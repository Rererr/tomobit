package lineedit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A history entry may hold backslashes and newlines (a pasted multi-line
// task): both must survive one physical line and come back exactly.
func TestHistoryLineRoundTripsBackslashesAndNewlines(t *testing.T) {
	for _, s := range []string{
		`plain`,
		"two\nlines",
		`a\b`,
		`trailing\`,
		"mix\\and\nnewline",
		`\n literal backslash-n`,
	} {
		enc := encodeHistoryLine(s)
		if strings.Contains(enc, "\n") {
			t.Errorf("encoded form still holds a real newline: %q", enc)
		}
		if got := decodeHistoryLine(enc); got != s {
			t.Errorf("round trip: %q -> %q -> %q", s, enc, got)
		}
	}
}

func TestSetHistoryFileLoadsPastEntriesOldestToNewest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o600)

	e := New(os.Stdin, os.Stdout)
	if err := e.SetHistoryFile(path, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.history, ","); got != "first,second,third" {
		t.Errorf("history: got %q", got)
	}
}

func TestSetHistoryFileIsNotAnErrorWhenAbsent(t *testing.T) {
	e := New(os.Stdin, os.Stdout)
	if err := e.SetHistoryFile(filepath.Join(t.TempDir(), "missing"), nil); err != nil {
		t.Errorf("a first run has no history and that is not a failure: %v", err)
	}
}

func TestAddHistoryAppendsToTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	e := New(os.Stdin, os.Stdout)
	if err := e.SetHistoryFile(path, nil); err != nil {
		t.Fatal(err)
	}
	e.AddHistory("one")
	e.AddHistory("two\nlines")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "one\ntwo\\nlines\n" {
		t.Errorf("file: got %q", got)
	}
}

// The persisted entry must survive the process boundary: a second editor over
// the same file recalls what the first one submitted, encoding and all.
func TestHistoryPersistsAcrossEditors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	first := New(os.Stdin, os.Stdout)
	first.SetHistoryFile(path, nil)
	first.AddHistory(`edit main.go \ then test`)
	first.AddHistory("do\nthis")

	second := New(os.Stdin, os.Stdout)
	if err := second.SetHistoryFile(path, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{`edit main.go \ then test`, "do\nthis"}
	if strings.Join(second.history, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("reloaded history: got %q", second.history)
	}
}

// A file grown past the cap is compacted on load, so appending forever cannot
// bloat it without bound (ADR-0024 Decision 1).
func TestSetHistoryFileCompactsToTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	var sb strings.Builder
	for i := 0; i < maxHistory+50; i++ {
		sb.WriteString("entry")
		sb.WriteString(strings.Repeat("x", i%3))
		sb.WriteByte('\n')
	}
	os.WriteFile(path, []byte(sb.String()), 0o600)

	e := New(os.Stdin, os.Stdout)
	if err := e.SetHistoryFile(path, nil); err != nil {
		t.Fatal(err)
	}
	if len(e.history) != maxHistory {
		t.Errorf("ring: got %d, want %d", len(e.history), maxHistory)
	}
	data, _ := os.ReadFile(path)
	if n := strings.Count(string(data), "\n"); n != maxHistory {
		t.Errorf("file was not compacted: %d lines", n)
	}
}

// A failed compaction write must not take the loaded past with it: the ring
// keeps what was read (the append path already keeps the ring on a write
// failure — losing history over a failed disk optimisation would be worse
// than the growth it prevents).
func TestSetHistoryFileKeepsRingWhenCompactionCannotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	var sb strings.Builder
	for i := 0; i < maxHistory+50; i++ {
		fmt.Fprintf(&sb, "entry%d\n", i)
	}
	os.WriteFile(path, []byte(sb.String()), 0o400) // read-only: the rewrite fails

	e := New(os.Stdin, os.Stdout)
	if err := e.SetHistoryFile(path, nil); err == nil {
		t.Fatal("a failed compaction must be reported, not swallowed")
	}
	if len(e.history) != maxHistory {
		t.Errorf("the ring must keep the loaded entries: got %d, want %d", len(e.history), maxHistory)
	}
}

// One long-lived process must not grow the file without bound: once the file
// holds twice the ring, an append compacts in-flight — and the compacted file
// still round-trips into the next process.
func TestAppendHistoryCompactsALongLivedProcessesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	e := New(os.Stdin, os.Stdout)
	if err := e.SetHistoryFile(path, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2*maxHistory+50; i++ {
		e.AddHistory(fmt.Sprintf("turn %d", i))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n > 2*maxHistory {
		t.Errorf("the file must stay bounded within one process: %d lines", n)
	}
	e2 := New(os.Stdin, os.Stdout)
	if err := e2.SetHistoryFile(path, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(e2.history); n != maxHistory {
		t.Errorf("reloaded ring: got %d, want %d", n, maxHistory)
	}
}

// A write failure must not be swallowed, but neither may it nag: one note to
// the warn sink, then the in-memory ring carries on.
func TestHistoryWriteFailureWarnsOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "h") // parent missing: OpenFile fails
	var warn bytes.Buffer
	e := New(os.Stdin, os.Stdout)
	e.SetHistoryFile(path, &warn)
	e.AddHistory("one")
	e.AddHistory("two")

	if got := strings.Count(warn.String(), "\n"); got != 1 {
		t.Errorf("expected exactly one warning, got %d in %q", got, warn.String())
	}
	if len(e.history) != 2 {
		t.Errorf("the in-memory ring must still work: got %v", e.history)
	}
}
