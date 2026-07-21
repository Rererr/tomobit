package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/lineedit"
	"github.com/Rererr/tomobit/internal/store"
)

// newViewChat wires a chat in --view ndjson mode onto the fake provider: stdout
// is the NDJSON stream (captured in buf), and stdin is read through the same
// flush-on-read hook cmdChat installs, so the Feedback question's await note
// fires. The editor rides a /dev/null *os.File so ReadLine takes the cooked
// path deterministically regardless of the test runner's real stdin/stdout.
func newViewChat(t *testing.T, s *store.Store, a *threadAdapter, stdin string) (*chat, *bytes.Buffer) {
	t.Helper()
	providers["fake"] = a
	t.Cleanup(func() { delete(providers, "fake") })

	buf := &bytes.Buffer{}
	stream := newNDJSONStream(buf)

	dev, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dev.Close() })
	ed := lineedit.New(dev, dev)
	ed.SetReader(flushReader{r: strings.NewReader(stdin), flush: stream.flushAwait})

	c := &chat{
		s: s, ed: ed, in: ed.Reader(), out: noteWriter{s: stream}, stream: stream,
		providerName: "fake", capability: "implement",
		extractor: &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}},
	}
	stream.emit(map[string]any{"type": "init", "v": viewVersion})
	return c, buf
}

// viewEvents parses the buffer as NDJSON, failing on any line that is not a
// standalone JSON object — the contract's first promise (ADR-0032 Decision 1).
func viewEvents(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout line is not valid JSON: %q: %v", line, err)
		}
		evs = append(evs, m)
	}
	return evs
}

func viewTypes(evs []map[string]any) []string {
	types := make([]string, len(evs))
	for i, e := range evs {
		types[i], _ = e["type"].(string)
	}
	return types
}

// containsInOrder reports whether want appears as a subsequence of got.
func containsInOrder(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

func firstOfType(evs []map[string]any, typ string) map[string]any {
	for _, e := range evs {
		if e["type"] == typ {
			return e
		}
	}
	return nil
}

func anyNoteContains(evs []map[string]any, substr string) bool {
	for _, e := range evs {
		if e["type"] != "note" {
			continue
		}
		if txt, _ := e["text"].(string); strings.Contains(txt, substr) {
			return true
		}
	}
	return false
}

// ADR-0032 Decision 1: a whole session's stdout is one NDJSON stream, and its
// events fall in the contract's order — init, then a ready at the prompt, then
// the task and its turn framing around the provider text.
func TestChatViewStreamOrdersEventsAndStaysJSON(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "implement it\n")

	if err := c.loop(""); err != nil {
		t.Fatal(err)
	}

	evs := viewEvents(t, buf)
	got := viewTypes(evs)
	want := []string{"init", "ready", "task.started", "turn.started", "provider", "text", "turn.finished", "task.finished"}
	if !containsInOrder(got, want) {
		t.Errorf("view events out of order:\n got  %v\n want %v (as a subsequence)", got, want)
	}
	if init := firstOfType(evs, "init"); init == nil || init["v"] != float64(viewVersion) {
		t.Errorf("init must carry the contract version: %v", init)
	}
	if got[0] != "init" {
		t.Errorf("init must open the stream, got %q", got[0])
	}
	if txt := firstOfType(evs, "text"); txt == nil || txt["text"] != "implement it" {
		t.Errorf("text event must carry the provider's raw output: %v", txt)
	}
	if tf := firstOfType(evs, "turn.finished"); tf == nil || tf["n"] != float64(1) || tf["started"] != true {
		t.Errorf("turn.finished must carry n and started: %v", tf)
	}
}

// A seed turn (`tomobit chat "..."`) is not standing at the prompt, so no ready
// precedes it (ADR-0032 Decision 1).
func TestChatViewSeedTurnEmitsNoReadyBeforeIt(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "")

	if err := c.loop("seed task"); err != nil {
		t.Fatal(err)
	}

	got := viewTypes(viewEvents(t, buf))
	taskStarted, firstReady := -1, -1
	for i, ty := range got {
		if ty == "task.started" && taskStarted < 0 {
			taskStarted = i
		}
		if ty == "ready" && firstReady < 0 {
			firstReady = i
		}
	}
	if taskStarted < 0 {
		t.Fatalf("no task.started: %v", got)
	}
	if firstReady >= 0 && firstReady < taskStarted {
		t.Errorf("a seed turn must not be preceded by ready: %v", got)
	}
}

// ADR-0032 Decision 1: tool lines and tool_result carry to the stream — the
// latter raw and uncapped, the terminal's display budget being none of the
// consumer's concern.
func TestChatViewEmitsToolAndToolResult(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "")

	if err := c.turn("TOOLDETAIL"); err != nil {
		t.Fatal(err)
	}
	if err := c.turn("TOOLRESULT"); err != nil {
		t.Fatal(err)
	}

	evs := viewEvents(t, buf)
	tool := firstOfType(evs, "tool")
	if tool == nil || tool["name"] != "Edit" || tool["detail"] != "cmd/x.go" {
		t.Errorf("tool event must carry name and detail: %v", tool)
	}
	tr := firstOfType(evs, "tool_result")
	if tr == nil || tr["text"] != "line1\nline2" {
		t.Errorf("tool_result must carry the raw output: %v", tr)
	}
}

// ADR-0032 Decision 1 (flush-on-read): the Feedback question is a partial line
// the organ writes before blocking on stdin; the read reaching stdin's bottom
// releases it as an await note, so a GUI receives the question instead of it
// stalling unseen in the note buffer.
func TestChatViewFeedbackQuestionArrivesAsAwaitNote(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "2\n")

	if err := c.turn("implement it"); err != nil {
		t.Fatal(err)
	}
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}

	evs := viewEvents(t, buf)
	var awaited map[string]any
	for _, e := range evs {
		if e["type"] == "note" && e["await"] == true {
			awaited = e
		}
	}
	if awaited == nil {
		t.Fatalf("the Feedback question must arrive as an await note: %v", viewTypes(evs))
	}
	if text, _ := awaited["text"].(string); !strings.Contains(text, "今回、どうだった?") {
		t.Errorf("the await note must hold the question text: %v", awaited)
	}
	if firstOfType(evs, "task.finished") == nil {
		t.Errorf("task.finished must close the task after the organs run: %v", viewTypes(evs))
	}
	// The answer "2" was consumed by the question, so it maps to with-edits.
	if p := payloadOf(t, s, "task.finished"); p["adopted"] != "with-edits" {
		t.Errorf("the answer past the await note must reach feedbackPayload: %v", p)
	}
}

// ADR-0032 Decision 1 & 台帳不変: the view stream is a reader's View, not a
// record. tool detail reaches the NDJSON but never the ledger, exactly as it
// never reaches the terminal turnView's ledger (SCHEMA.md R3 unchanged).
func TestChatViewRecordsToolNameOnlyLikePlainMode(t *testing.T) {
	s := openTestStore(t)
	c, _ := newViewChat(t, s, &threadAdapter{}, "")

	if err := c.turn("TOOLDETAIL"); err != nil {
		t.Fatal(err)
	}

	p := payloadOf(t, s, "provider.output")
	if p["tool"] != "Edit" {
		t.Errorf("tool name is the record: %v", p)
	}
	if _, ok := p["detail"]; ok {
		t.Errorf("view-only detail must never reach the ledger: %v", p)
	}
}

// ADR-0032 Decision 1 (layout is for the terminal): a speaker-separation blank
// line is typography, not content — it must not become an empty note that
// clutters the stream, the same discipline gutter and gap follow.
func TestChatViewSuppressesEmptyLayoutNotes(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "")

	fmt.Fprintln(c.out)         // a bare blank line — layout only
	fmt.Fprintln(c.out, "real") // a real line
	fmt.Fprintln(c.out)         // and another blank

	evs := viewEvents(t, buf)
	notes := 0
	for _, e := range evs {
		if e["type"] != "note" {
			continue
		}
		notes++
		if txt, _ := e["text"].(string); txt == "" {
			t.Errorf("a blank layout line must emit no note: %v", e)
		}
	}
	if notes != 1 {
		t.Errorf("only the real line becomes a note: got %d in %v", notes, viewTypes(evs))
	}
}

// ADR-0032 Decision 1: await is a best-effort signal, not a guarantee. When
// stdin arrives in one batch, the shared bufio's read-ahead already holds the
// answer, so the Feedback read never reaches bottom — the question note is
// carried out unmarked, yet the buffered answer still reaches feedbackPayload
// and lands in the ledger. The block cannot be detected, so no await fires.
func TestChatViewBatchWrittenStdinYieldsNoAwaitButStillRecords(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "prime\n2\n")

	// One over-reading read pulls the whole batch into the shared bufio, so the
	// answer "2" sits buffered before the question is even asked.
	if _, err := c.in.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err := c.turn("implement it"); err != nil {
		t.Fatal(err)
	}
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}
	c.stream.flushClose() // the chat's end, where any dangling note is carried out

	evs := viewEvents(t, buf)
	for _, e := range evs {
		if e["type"] == "note" && e["await"] == true {
			t.Errorf("a buffered answer leaves the block undetectable — no await may fire: %v", e)
		}
	}
	if !anyNoteContains(evs, "今回、どうだった?") {
		t.Errorf("the question must still arrive, just unmarked: %v", viewTypes(evs))
	}
	if p := payloadOf(t, s, "task.finished"); p["adopted"] != "with-edits" {
		t.Errorf("the buffered answer must still reach feedbackPayload: %v", p)
	}
}

// ADR-0032 Decision 1: /status routes through the framing writer (showStatus's
// io.Writer seam), so the companion table stays valid NDJSON — never a bare
// text block leaking into the stream.
func TestChatViewStatusStaysJSON(t *testing.T) {
	s := openTestStore(t)
	en := &core.Engine{Repo: s}
	growCapability(t, s, en, "lang=go", "claude-code", 1000, 4, 1)
	c, buf := newViewChat(t, s, &threadAdapter{}, "")

	if _, err := c.command("/status"); err != nil {
		t.Fatal(err)
	}

	evs := viewEvents(t, buf) // fails on any non-JSON line
	if !anyNoteContains(evs, "claude-code") {
		t.Errorf("the connections table must ride notes: %v", viewTypes(evs))
	}
}

// ADR-0032 Decision 1: --provider auto reaches autoDecide, whose operational
// lines used to write os.Stdout directly. In a chat they must frame through
// c.out — every stdout line stays valid JSON, no bare "decided ..." leaks. The
// candidate set is restricted so auto lands on a test-safe provider (fake) or
// human, neither launching anything real.
func TestChatViewAutoRoutingStaysJSON(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	saved := providers
	providers = map[string]executor.Adapter{"fake": a}
	t.Cleanup(func() { providers = saved })

	buf := &bytes.Buffer{}
	stream := newNDJSONStream(buf)
	dev, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dev.Close() })
	ed := lineedit.New(dev, dev)
	ed.SetReader(flushReader{r: strings.NewReader(""), flush: stream.flushAwait})
	c := &chat{
		s: s, ed: ed, in: ed.Reader(), out: noteWriter{s: stream}, stream: stream,
		providerName: "auto", capability: "implement",
		extractor: &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}},
	}
	stream.emit(map[string]any{"type": "init", "v": viewVersion})

	if err := c.turn("implement it"); err != nil {
		t.Fatal(err)
	}

	evs := viewEvents(t, buf) // fails on any non-JSON line — a bare decided line would
	if firstOfType(evs, "task.started") == nil {
		t.Errorf("the auto turn must open a task: %v", viewTypes(evs))
	}
	if anyNoteContains(evs, "decided ") {
		return // decided rode a note — the routing worked
	}
	// If auto picked human, there is no "decided" voice line but there is a
	// routing note; either way nothing left the stream as raw text (viewEvents
	// would have failed). A human pick records provider.selected=human.
}

// ADR-0032 Decision 1: --view takes only ndjson, and ndjson refuses a TTY (the
// view mode assumes every terminal gate shut). Pure, so both rejections pin
// without a real terminal.
func TestValidateViewFlag(t *testing.T) {
	cases := []struct {
		name    string
		view    string
		tty     bool
		wantErr bool
	}{
		{"empty is the plain-text default", "", false, false},
		{"empty is fine on a tty", "", true, false},
		{"ndjson on a pipe is accepted", "ndjson", false, false},
		{"ndjson on a tty is refused", "ndjson", true, true},
		{"an unknown value is refused", "yaml", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateViewFlag(tc.view, tc.tty)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateViewFlag(%q, tty=%v) err = %v, wantErr = %v", tc.view, tc.tty, err, tc.wantErr)
			}
		})
	}
}
