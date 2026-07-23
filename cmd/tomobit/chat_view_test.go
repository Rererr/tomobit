package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// ADR-0040 Decision 1: --provider auto's decision rides the NDJSON stream as a
// typed "decided" event — once per task, tagged with the task's own sid so a
// GUI can correlate it (it can arrive before this task's own task.started —
// see viewDecided) — carrying the exact same audit (provider/n/q/fallback/
// seed and each candidate's scope/quantile/passed/wins) that landed in the
// same tomo.decided ledger record, whether autoDecide picked a provider or
// routed to human.
func TestChatViewEmitsDecidedEventMatchingLedger(t *testing.T) {
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

	var decided []map[string]any
	for _, e := range viewEvents(t, buf) {
		if e["type"] == "decided" {
			decided = append(decided, e)
		}
	}
	if len(decided) != 1 {
		t.Fatalf("decided must fire exactly once per task, got %d", len(decided))
	}
	view := decided[0]
	ledger := payloadOf(t, s, "tomo.decided")

	if view["sid"] != c.sid {
		t.Errorf("decided sid = %v, want the task's own session %q", view["sid"], c.sid)
	}
	for _, key := range []string{"provider", "n", "q", "fallback", "seed"} {
		if view[key] != ledger[key] {
			t.Errorf("decided view %q = %v, want the same tomo.decided record's %v", key, view[key], ledger[key])
		}
	}

	viewCands, _ := view["candidates"].([]any)
	ledgerCands, _ := ledger["candidates"].([]any)
	if len(viewCands) == 0 || len(viewCands) != len(ledgerCands) {
		t.Fatalf("candidate count: view=%d ledger=%d, want equal and non-zero", len(viewCands), len(ledgerCands))
	}
	for i := range viewCands {
		vc := viewCands[i].(map[string]any)
		lc := ledgerCands[i].(map[string]any)
		if vc["provider"] != lc["provider"] || vc["quantile"] != lc["quantile"] ||
			vc["passed"] != lc["passed"] || vc["scope"] != lc["scope"] || vc["wins"] != lc["wins"] {
			t.Errorf("candidate %d must audit the same decision, key-for-key with the ledger: view=%v ledger=%v", i, vc, lc)
		}
	}
}

// ADR-0040 Decision 1 depends on autoDecide's out being exactly the writer
// decidedViewer is implemented on — chat wires c.out straight into openTask
// (startTask) and executeSplit (splitAndFold) with nothing in between. A
// future change that wraps c.out (e.g. an indent writer) before either call
// would compile cleanly — io.Writer satisfaction says nothing about
// decidedViewer — and silently stop the emit. This pins today's wiring: under
// --view ndjson, c.out must still assert to decidedViewer.
func TestChatViewOutStillImplementsDecidedViewer(t *testing.T) {
	s := openTestStore(t)
	c, _ := newViewChat(t, s, &threadAdapter{}, "")

	if _, ok := c.out.(decidedViewer); !ok {
		t.Fatalf("c.out (%T) no longer implements decidedViewer — autoDecide's ADR-0040 view emit would silently stop firing", c.out)
	}
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

// buildTomobitBinary compiles the real CLI once per test into a throwaway
// path — ADR-0035's fix lives in the wiring between cmdChat, finishTask and
// askWithIO/reflectWithIO, which a chat literal built by hand (newViewChat,
// above) never exercises: those tests set humanPresent/interactive directly,
// so they can pin every organ's behavior once humanPresent is true but
// cannot tell whether a real `--view ndjson` invocation ever makes it true.
// A prior unit test did exactly the hand-set thing (TestAskWithIOInteractive-
// RecordsTomoAsked passes interactive=true straight to askWithIO) and still
// let the bug ship: finishTask read isTTY(os.Stdin) for itself, so no amount
// of unit-level plumbing could have caught a pipe never reaching that branch.
func buildTomobitBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tomobit")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// seedPreferenceGap opens dbPath directly (bypassing perception, like
// growCapability's other callers) and grows a capability Connection pair that
// clears every ADR-0007 Decision 2 gate: even (identical 4-success/1-revert
// records), frequent (10 execution experiences in scope), and no preference
// Connection yet. ts is anchored to "now" rather than a fixed constant — the
// 90-day decay half-life (core.HalfLifeMs) would otherwise erase the evidence
// by the time the real chat process, running moments later, computes its own
// wall-clock now for curiosity.Gaps.
func seedPreferenceGap(t *testing.T, dbPath string) {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	en := &core.Engine{Repo: s}
	ts := time.Now().UnixMilli()
	growCapability(t, s, en, "lang=rust", "claude", ts, 4, 1)
	growCapability(t, s, en, "lang=rust", "codex", ts, 4, 1)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestPipedChatViewDeliversTomoQuestionAndRecordsTomoAsked is ADR-0035's own
// completion condition (Consequences: "検証は実環境で行う"): a real compiled
// tomobit, chatting over a real OS pipe with --view ndjson, against a ledger
// holding an open Preference Gap. Tomo's question must arrive as an
// {"type":"note",...,"await":true} event over that pipe, and answering it
// must record tomo.asked (and, since the reply picks a side, user.preference)
// — the two facts ADR-0035 Context found never happened through the GUI's
// entry point before this change.
func TestPipedChatViewDeliversTomoQuestionAndRecordsTomoAsked(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs a real binary")
	}
	bin := buildTomobitBinary(t)
	dbPath := filepath.Join(t.TempDir(), "tomobit.db")
	seedPreferenceGap(t, dbPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "chat", "--db", dbPath,
		"--provider", "human", "--view", "ndjson",
		// Unreachable on purpose: perception is best-effort (ADR-0006 Decision
		// 5) and this test's organs under test — Feedback, Tomo's question —
		// never depend on it succeeding; a real Ollama would only add latency
		// and an environment dependency this test does not need.
		"--backend", "ollama", "--url", "http://127.0.0.1:1",
		"first task")
	// A from-scratch env, not os.Environ(): the developer running this test may
	// have TOMOBIT_FACE=1 exported for daily use (ADR-0032 Consequences), which
	// would spawn a real face window under a bare inherited env. HOME is a
	// fresh temp dir so config.Load() finds no ~/.tomobit/config.json either.
	cmd.Env = []string{"HOME=" + t.TempDir()}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	events := make(chan map[string]any, 64)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("stdout line is not valid JSON: %q: %v", sc.Text(), err)
				continue
			}
			events <- m
		}
	}()

	waitFor := func(pred func(map[string]any) bool, label string) {
		t.Helper()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					t.Fatalf("stdout closed before %s arrived — stderr:\n%s", label, stderr.String())
				}
				if pred(ev) {
					return
				}
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s — stderr:\n%s", label, stderr.String())
			}
		}
	}
	isAwaitNoteContaining := func(substr string) func(map[string]any) bool {
		return func(ev map[string]any) bool {
			if ev["type"] != "note" || ev["await"] != true {
				return false
			}
			text, _ := ev["text"].(string)
			return strings.Contains(text, substr)
		}
	}
	isType := func(typ string) func(map[string]any) bool {
		return func(ev map[string]any) bool { return ev["type"] == typ }
	}

	// The seed turn ("first task") opens the task under --provider human — no
	// provider launches, so the next thing the process does is stand at the
	// prompt, marked "ready" instead of the ` ❯ ` a terminal would draw
	// (ADR-0032 Decision 1).
	waitFor(isType("ready"), "the prompt after the opening turn")
	if _, err := io.WriteString(stdin, "/exit\n"); err != nil {
		t.Fatal(err)
	}
	// /exit closes the task: Feedback fires unconditionally (ADR-0006 Decision
	// 4) and arrives as a partial line released by flush-on-read the moment the
	// next stdin read blocks (ADR-0032 Decision 1).
	waitFor(isAwaitNoteContaining("今回、どうだった?"), "the Feedback question")
	if _, err := io.WriteString(stdin, "1\n"); err != nil {
		t.Fatal(err)
	}
	// Tomo's question follows Feedback in finishTask (ADR-0007). Before this
	// change it never fired here at all: humanPresent was isTTY(os.Stdin), and
	// a piped stdin is never a TTY, view mode or not.
	waitFor(isAwaitNoteContaining("Enter=スキップ"), "Tomo's Preference Gap question (ADR-0007)")
	if _, err := io.WriteString(stdin, "1\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(isType("task.finished"), "task.finished")
	// Drain to EOF before Wait(): StdoutPipe's contract is that every read
	// completes before Wait is called, or the two can race the process exit.
	for range events {
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("chat exited with error: %v — stderr:\n%s", err, stderr.String())
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := countEventsOfType(t, s, "tomo.asked"); n != 1 {
		t.Errorf("tomo.asked: got %d, want 1 — the question must be recorded once it fired over the pipe", n)
	}
	// "1" picks gap.A, and Gaps sorts targets lexicographically — "claude" <
	// "codex" — so the piped answer must resolve to that pair, not just any.
	if p := payloadOf(t, s, "user.preference"); p["preferred"] != "claude" || p["over"] != "codex" {
		t.Errorf("user.preference: got %v, want preferred=claude over=codex (the piped \"1\" answer)", p)
	}
}
