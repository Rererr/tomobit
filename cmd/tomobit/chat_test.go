package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
)

// threadAdapter is a provider that runs `printf` instead of a CLI, so a chat
// can be driven turn by turn with no real one. It records the ResumeID of
// every launch — how a test sees whether the thread was continued — and the
// working places it was handed (ADR-0047).
type threadAdapter struct {
	resumes  []string
	workDirs []string
	addDirs  [][]string
	fail     bool
}

func (a *threadAdapter) Name() string { return "fake" }

func (a *threadAdapter) Command(req executor.Request) (string, []string, []string) {
	a.resumes = append(a.resumes, req.ResumeID)
	a.workDirs = append(a.workDirs, req.WorkDir)
	a.addDirs = append(a.addDirs, req.AddDirs)
	if a.fail {
		return "false", nil, nil // exits non-zero, streams nothing
	}
	return "printf", []string{"SEL\n" + req.Prompt + "\n"}, nil
}

func (a *threadAdapter) Translate(line []byte) ([]executor.Event, error) {
	switch s := strings.TrimSpace(string(line)); s {
	case "":
		return nil, nil
	case "SEL":
		return []executor.Event{{Type: executor.EventProviderSelected, Payload: map[string]any{
			"provider": "fake", "provider_session_id": "th-1",
		}}}, nil
	case "TOOLDETAIL":
		return []executor.Event{{Type: executor.EventProviderOutput,
			Payload: map[string]any{"tool": "Edit", executor.PayloadDetail: "cmd/x.go"}}}, nil
	case "TOOLRESULT":
		return []executor.Event{{Type: executor.EventProviderOutput,
			Payload: map[string]any{executor.PayloadToolResult: "line1\nline2"}}}, nil
	default:
		return []executor.Event{{Type: executor.EventProviderOutput,
			Payload: map[string]any{"text": s}}}, nil
	}
}

// newTestChat wires a chat onto a fake provider and a fake perception, so
// nothing here touches a CLI or Ollama.
func newTestChat(t *testing.T, s *store.Store, a *threadAdapter, in string) (*chat, *bytes.Buffer) {
	t.Helper()
	providers["fake"] = a
	t.Cleanup(func() { delete(providers, "fake") })
	out := &bytes.Buffer{}
	return &chat{
		s: s, out: out, providerName: "fake", capability: "implement",
		extractor: &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}},
		in:        bufio.NewReader(strings.NewReader(in)),
	}, out
}

func eventTypes(t *testing.T, s *store.Store) []string {
	t.Helper()
	rows, err := s.DB.Query(`SELECT type FROM events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			t.Fatal(err)
		}
		got = append(got, typ)
	}
	return got
}

func payloadOf(t *testing.T, s *store.Store, typ string) map[string]any {
	t.Helper()
	var raw string
	if err := s.DB.QueryRow(`SELECT payload FROM events WHERE type = ? ORDER BY id LIMIT 1`, typ).Scan(&raw); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func sessionIDs(t *testing.T, s *store.Store) map[string]bool {
	t.Helper()
	rows, err := s.DB.Query(`SELECT DISTINCT session_id FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[id] = true
	}
	return ids
}

// ADR-0022 Decision 1: several turns are one task, one session, one intent —
// the follow-ups are recorded as task.turn, not as new tasks.
func TestChatTurnsShareOneSession(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, _ := newTestChat(t, s, a, "")

	for _, p := range []string{"implement it", "no, fix that", "now test it"} {
		if err := c.turn(p); err != nil {
			t.Fatal(err)
		}
	}

	if ids := sessionIDs(t, s); len(ids) != 1 {
		t.Errorf("three turns must stay in one session, got %d", len(ids))
	}
	if n := countEventsOfType(t, s, "task.started"); n != 1 {
		t.Errorf("task.started: got %d, want 1", n)
	}
	if n := countEventsOfType(t, s, "task.turn"); n != 2 {
		t.Errorf("task.turn: got %d, want 2 (the first turn is task.started)", n)
	}
	if got := payloadOf(t, s, "task.started")["intent"]; got != "implement it" {
		t.Errorf("the task's intent is its first prompt: got %v", got)
	}
	if got := payloadOf(t, s, "task.turn")["intent"]; got != "no, fix that" {
		t.Errorf("turn intent: got %v", got)
	}
	if got := payloadOf(t, s, "task.turn")["n"]; got != float64(2) {
		t.Errorf("turn number: got %v", got)
	}
}

// ADR-0022 Decision 2: the first turn opens a thread, every later one
// continues it — without this a "chat" is just repeated cold starts.
func TestChatLaterTurnsResumeTheProviderThread(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, _ := newTestChat(t, s, a, "")

	c.turn("first")
	c.turn("second")
	c.turn("third")

	want := []string{"", "th-1", "th-1"}
	if len(a.resumes) != len(want) {
		t.Fatalf("launches: got %v", a.resumes)
	}
	for i := range want {
		if a.resumes[i] != want[i] {
			t.Errorf("launch %d resume id: got %q, want %q", i+1, a.resumes[i], want[i])
		}
	}
}

// A new task must not inherit the previous conversation's thread.
func TestChatNewTaskStartsAFreshThreadAndSession(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, _ := newTestChat(t, s, a, "\n")

	c.turn("first task")
	if _, err := c.command("/new"); err != nil {
		t.Fatal(err)
	}
	c.turn("second task")

	if ids := sessionIDs(t, s); len(ids) != 2 {
		t.Errorf("/new must open a new session, got %d", len(ids))
	}
	if n := countEventsOfType(t, s, "task.started"); n != 2 {
		t.Errorf("task.started: got %d, want 2", n)
	}
	if len(a.resumes) != 2 || a.resumes[1] != "" {
		t.Errorf("the new task must launch cold: got %v", a.resumes)
	}
}

// The boundary is where the ledger learns: the adoption answer becomes the
// task's outcome (ADR-0006 Decision 4, now at the chat's boundary).
func TestChatCloseAsksAdoptionAndRecordsIt(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, out := newTestChat(t, s, a, "2\n")

	c.turn("implement it")
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "今回、どうだった?") {
		t.Errorf("the adoption question must be asked at the boundary: %q", out.String())
	}
	p := payloadOf(t, s, "task.finished")
	if p["adopted"] != "with-edits" {
		t.Errorf("task.finished: got %v", p)
	}
	if c.sid != "" || c.threadID != "" {
		t.Errorf("the session must be closed: sid=%q thread=%q", c.sid, c.threadID)
	}
}

// countingExtractor counts ExtractTaskContext calls, so a chat test can pin
// how many times Task Perception actually ran without reaching a real LLM
// backend.
type countingExtractor struct{ calls int }

func (c *countingExtractor) ExtractContext([]*store.Event, map[string][]string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (c *countingExtractor) ExtractTaskContext(string, map[string][]string) (map[string]string, error) {
	c.calls++
	return map[string]string{"lang": "go"}, nil
}
func (c *countingExtractor) Name() string { return "counting" }

// TestChatPerceptionHolderIsFreshPerTask pins ADR-0036 Decision 2b's holder
// lifetime: /new (closeTask) discards it, so a later task's own extraction
// runs again from its own intent rather than silently reusing a stale
// holder — the human provider keeps this test from launching anything real.
func TestChatPerceptionHolderIsFreshPerTask(t *testing.T) {
	s := openTestStore(t)
	ext := &countingExtractor{}
	c := &chat{
		s: s, out: io.Discard, providerName: "human", capability: "implement",
		extractor: ext, in: bufio.NewReader(strings.NewReader("\n\n\n\n")),
	}

	if err := c.startTask("first task"); err != nil {
		t.Fatal(err)
	}
	if c.perception == nil {
		t.Fatal("startTask must build a holder")
	}
	c.perception.semanticTokens(io.Discard) // stands in for whichever path asks first
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}
	if c.perception != nil {
		t.Error("closeTask (/new) must discard the task's holder")
	}

	if err := c.startTask("second task"); err != nil {
		t.Fatal(err)
	}
	c.perception.semanticTokens(io.Discard)
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}

	if ext.calls != 2 {
		t.Errorf("each task must extract fresh from its own intent, got %d calls across two tasks", ext.calls)
	}
}

// Nothing ran to completion, so there is nothing to judge: asking "今回、どうだった?"
// about work that never happened would fabricate a signal (ADR-0003).
func TestChatCloseWithNoCompletedTurnRecordsCancelled(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, out := newTestChat(t, s, a, "\n")

	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(t, s); len(got) != 0 {
		t.Errorf("closing with no task open must record nothing: %v", got)
	}

	c.turn("do it")
	c.completed = false // the run was interrupted before it finished
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}
	if n := countEventsOfType(t, s, "task.cancelled"); n != 1 {
		t.Errorf("task.cancelled: got %d, want 1", n)
	}
	if n := countEventsOfType(t, s, "task.finished"); n != 0 {
		t.Errorf("no task.finished expected, got %d", n)
	}
	if strings.Contains(out.String(), "今回、どうだった?") {
		t.Errorf("nothing ran, so nothing may be asked: %q", out.String())
	}
}

// SCHEMA.md gives an experience one provider and one capability for the whole
// session, so swapping either mid-task would record a task that never
// happened. The chat refuses instead of lying.
func TestChatWiringCannotChangeMidTask(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, out := newTestChat(t, s, a, "")

	c.turn("implement it")
	if _, err := c.command("/cap review"); err != nil {
		t.Fatal(err)
	}
	if c.capability != "implement" {
		t.Errorf("capability changed mid-task: %q", c.capability)
	}
	if !strings.Contains(out.String(), "/new") {
		t.Errorf("the refusal should point at /new: %q", out.String())
	}
}

func TestChatWiringChangesBetweenTasks(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, _ := newTestChat(t, s, a, "")

	if _, err := c.command("/cap review"); err != nil {
		t.Fatal(err)
	}
	if c.capability != "review" {
		t.Fatalf("capability: got %q", c.capability)
	}
	c.turn("look at this")
	if got := payloadOf(t, s, "capability.started")["capability"]; got != "review" {
		t.Errorf("capability.started: got %v", got)
	}
}

// A typo must not end the conversation.
func TestChatUnknownCommandIsAnsweredAndTheChatContinues(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, out := newTestChat(t, s, a, "")

	done, err := c.command("/provder codex")
	if err != nil || done {
		t.Errorf("got done=%v err=%v", done, err)
	}
	if !strings.Contains(out.String(), "知らないコマンド") {
		t.Errorf("got %q", out.String())
	}
}

// ADR-0018 Decision 2 inside a chat: the human is a provider on the same
// ledger. Tomo records the routing and waits for the boundary — it must not
// try to launch anything.
func TestChatHumanProviderRecordsRoutingAndRunsNothing(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, out := newTestChat(t, s, a, "\n")
	c.providerName = "human"

	if err := c.turn("I will do this one"); err != nil {
		t.Fatal(err)
	}
	if len(a.resumes) != 0 {
		t.Errorf("a human task must launch no provider: %v", a.resumes)
	}
	if got := payloadOf(t, s, "provider.selected")["provider"]; got != "human" {
		t.Errorf("provider.selected: got %v", got)
	}
	if err := c.turn("still working"); err != nil {
		t.Fatal(err)
	}
	if n := countEventsOfType(t, s, "task.turn"); n != 0 {
		t.Errorf("a human task has no provider turns to record, got %d", n)
	}

	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "今回、どうだった?") {
		t.Errorf("the human's own work is judged like any provider's: %q", out.String())
	}
}

// ADR-0040 Decision 1 is view-only: a plain (non-ndjson) chat's out is a bare
// io.Writer, not a decidedViewer, so autoDecide's structured payload never
// reaches it — only the unchanged human-readable "decided ..." line and voice
// note do.
func TestChatPlainModeAutoDecideEmitsNoDecidedJSON(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	saved := providers
	providers = map[string]executor.Adapter{"fake": a}
	t.Cleanup(func() { providers = saved })
	c, out := newTestChat(t, s, a, "")
	c.providerName = "auto"

	if err := c.turn("implement it"); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "decided ") {
		t.Errorf("the human-readable routing line must still print: %q", got)
	}
	for _, leak := range []string{`"type":"decided"`, `"candidates"`, `"scope"`} {
		if strings.Contains(got, leak) {
			t.Errorf("plain mode must never print the decided payload as JSON, found %q in: %q", leak, got)
		}
	}
}

// A failed run still produced something the user may keep, so the boundary
// still asks — and the failure is recorded once (ADR-0006 Decision 4).
func TestChatFailedTurnStillReachesTheAdoptionQuestion(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{fail: true}
	c, out := newTestChat(t, s, a, "3\n")

	if err := c.turn("break it"); err != nil {
		t.Fatal(err)
	}
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}
	if n := countEventsOfType(t, s, "provider.error"); n != 1 {
		t.Errorf("provider.error: got %d, want 1", n)
	}
	if !strings.Contains(out.String(), "今回、どうだった?") {
		t.Errorf("a failed run is still judged: %q", out.String())
	}
	if p := payloadOf(t, s, "task.finished"); p["reverted"] != true {
		t.Errorf("task.finished: got %v", p)
	}
}

// A profile that was already chosen is not asked about again — the gate is on
// the missing choice, not on the provider (ADR-0021 Decision 4). What the
// question itself reads from is pinned by ensureClaudeProfileIO's own tests.
func TestChatProviderSwitchWithAChosenProfileAsksNothing(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	// "0" = inherit the parent environment, the one answer that writes no path.
	c, out := newTestChat(t, s, a, "0\n")

	t.Setenv("TOMOBIT_CLAUDE_CONFIG_DIR", "")
	wireClaude() // env is now a choice: the gate must not ask at all
	if _, err := c.command("/provider claude-code"); err != nil {
		t.Fatal(err)
	}
	if c.providerName != "claude-code" {
		t.Errorf("provider: got %q", c.providerName)
	}
	if strings.Contains(out.String(), "プロファイル") {
		t.Errorf("a chosen profile must not be asked about again: %q", out.String())
	}
}

// A provider that does not exist must not become a task: /provider says no,
// and the chat keeps the one it had.
func TestChatProviderSwitchRejectsAnUnknownName(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, out := newTestChat(t, s, a, "")

	if _, err := c.command("/provider gemni"); err != nil {
		t.Fatal(err)
	}
	if c.providerName != "fake" {
		t.Errorf("provider changed to an unknown name: %q", c.providerName)
	}
	if !strings.Contains(out.String(), "gemni") {
		t.Errorf("the refusal should name what was typed: %q", out.String())
	}
}

// Tab completes the leading slash command and the argument vocabulary of
// /provider and /size, and declines everywhere else (ADR-0024 Decision 4).
func TestChatCompleterOffersCommandsAndArgVocab(t *testing.T) {
	c := &chat{}
	for _, tc := range []struct {
		name  string
		text  string
		pos   int
		want  []string
		start int
	}{
		{"command prefix", "/pro", 4, []string{"/provider"}, 0},
		{"ambiguous command", "/s", 2, []string{"/size", "/status"}, 0},
		{"provider values", "/provider ", 10, []string{"claude-code", "codex", "human", "auto"}, 10},
		{"provider prefix", "/provider c", 11, []string{"claude-code", "codex"}, 10},
		{"size values", "/size ", 6, []string{"small", "medium", "large"}, 6},
		{"free text declines", "implement it", 12, nil, -1},
		{"third token declines", "/provider codex now", 19, nil, -1},
		{"unknown command arg declines", "/help x", 7, nil, -1},
	} {
		got, start := c.complete(tc.text, tc.pos)
		if !equalStrings(got, tc.want) {
			t.Errorf("%s: candidates got %v, want %v", tc.name, got, tc.want)
		}
		if start != tc.start {
			t.Errorf("%s: start got %d, want %d", tc.name, start, tc.start)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ADR-0024 Decision 6: tool detail reaches the human but never the ledger —
// the recorded provider.output keeps the shape R3 fixed (tool name only),
// no matter what the adapter attached for display.
func TestChatSinkShowsToolDetailButRecordsToolNameOnly(t *testing.T) {
	s := openTestStore(t)
	c, out := newTestChat(t, s, &threadAdapter{}, "")

	if err := c.turn("TOOLDETAIL"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "· Edit cmd/x.go") {
		t.Errorf("the view must show the detail, got %q", out.String())
	}
	p := payloadOf(t, s, "provider.output")
	if p["tool"] != "Edit" {
		t.Errorf("tool name is the record: got %v", p)
	}
	if _, ok := p["detail"]; ok {
		t.Errorf("view-only detail must not be recorded: %v", p)
	}
}

// ADR-0024 Decision 5: markdown-lite is decoration for a human's terminal.
// Piped/test stdout is not a terminal, so provider text passes through raw.
func TestTurnViewLeavesMarkdownRawWhenPiped(t *testing.T) {
	out := &bytes.Buffer{}
	v := newTurnView(out, "fake")
	v.show(executor.Event{Type: executor.EventProviderOutput,
		Payload: map[string]any{"text": "**bold** and `code`"}})
	if got := out.String(); got != "**bold** and `code`\n" {
		t.Errorf("piped text must be untouched: %q", got)
	}
}

// floodLines builds one tool_result's content: n lines identifying
// themselves as R{result}-L{01..n}, so a test can tell exactly which lines
// of which result reached the output.
func floodLines(result, n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("R%d-L%02d", result, i+1)
	}
	return strings.Join(lines, "\n")
}

func toolResultEvent(content string) executor.Event {
	return executor.Event{Type: executor.EventProviderOutput,
		Payload: map[string]any{executor.PayloadToolResult: content}}
}

const elisionNotice = "…（以降のツール出力は省略）"

// ADR-0031 Decision 1: a per-result cap alone cannot stop a turn that calls a
// tool many times from flooding the answer off screen — the turn itself
// carries a budget, spent across every result it shows.
func TestTurnViewToolBudgetCapsAccumulatedLinesAcrossResults(t *testing.T) {
	out := &bytes.Buffer{}
	v := &turnView{out: out, styled: true, toolBudget: turnToolResultMaxLines}

	for i := 1; i <= 6; i++ {
		v.show(toolResultEvent(floodLines(i, 12)))
	}
	got := out.String()

	if n := strings.Count(got, "-L"); n != turnToolResultMaxLines {
		t.Errorf("total tool_result lines shown: got %d, want %d", n, turnToolResultMaxLines)
	}
	for _, want := range []string{"R1-L01", "R2-L01", "R3-L01", "R4-L01", "R4-L12"} {
		if !strings.Contains(got, want) {
			t.Errorf("result within budget must reach the screen: %q missing from %q", want, got)
		}
	}
	for _, absent := range []string{"R5-L01", "R6-L01"} {
		if strings.Contains(got, absent) {
			t.Errorf("result past the exhausted budget must not print: %q found in %q", absent, got)
		}
	}
}

// ADR-0031 Decision 1: the omission is announced once, honestly, and then
// stays quiet — a repeated notice for every further silenced result would be
// noise, not honesty.
func TestTurnViewToolBudgetElisionNoticeFiresOnlyOnce(t *testing.T) {
	out := &bytes.Buffer{}
	v := &turnView{out: out, styled: true, toolBudget: turnToolResultMaxLines}

	for i := 1; i <= 6; i++ {
		v.show(toolResultEvent(floodLines(i, 12)))
	}

	if n := strings.Count(out.String(), elisionNotice); n != 1 {
		t.Errorf("elision notice: got %d occurrences, want 1: %q", n, out.String())
	}
}

// A result that straddles the budget boundary is cut to whatever remains,
// like a per-result truncation, and carries the same per-result marker —
// the budget does not silently drop the tail without saying so. The marker
// spends the budget too, so the straddle leaves it overdrawn and the next
// result meets only the elision notice.
func TestTurnViewToolBudgetCutsAResultStraddlingTheBoundary(t *testing.T) {
	out := &bytes.Buffer{}
	v := &turnView{out: out, styled: true, toolBudget: 5}

	v.show(toolResultEvent(floodLines(1, 12)))
	got := out.String()

	for _, want := range []string{"R1-L01", "R1-L02", "R1-L03", "R1-L04", "R1-L05"} {
		if !strings.Contains(got, want) {
			t.Errorf("lines within the remaining budget must show: %q missing from %q", want, got)
		}
	}
	if strings.Contains(got, "R1-L06") {
		t.Errorf("lines past the remaining budget must not show: %q", got)
	}
	if !strings.Contains(got, "…（ツール出力は先頭のみ）") {
		t.Errorf("a result cut by the budget still carries the per-result truncation marker: %q", got)
	}
	if n := strings.Count(got, "\n"); n != 6 {
		t.Errorf("the straddle draws its kept lines plus the marker, nothing more: got %d lines: %q", n, got)
	}

	v.show(toolResultEvent(floodLines(2, 3)))
	after := out.String()[len(got):]
	if strings.Contains(after, "R2-") || after != elisionNotice+"\n" {
		t.Errorf("after the straddle the budget is spent: only the elision notice may follow: %q", after)
	}
}

// The per-result cap still bites on its own: one over-tall result is cut at
// toolResultMaxLines even when the turn budget has room to spare, and its
// marker line is charged like any other display line.
func TestTurnViewPerResultCapCutsAnOvertallResultWithBudgetToSpare(t *testing.T) {
	out := &bytes.Buffer{}
	v := &turnView{out: out, styled: true, toolBudget: turnToolResultMaxLines}

	v.show(toolResultEvent(floodLines(1, toolResultMaxLines+4)))
	got := out.String()

	if !strings.Contains(got, fmt.Sprintf("R1-L%02d", toolResultMaxLines)) {
		t.Errorf("lines up to the per-result cap must show: %q", got)
	}
	if strings.Contains(got, fmt.Sprintf("R1-L%02d", toolResultMaxLines+1)) {
		t.Errorf("lines past the per-result cap must not show: %q", got)
	}
	if !strings.Contains(got, "…（ツール出力は先頭のみ）") {
		t.Errorf("a per-result cut carries the truncation marker: %q", got)
	}
	if want := turnToolResultMaxLines - (toolResultMaxLines + 1); v.toolBudget != want {
		t.Errorf("the cut lines and the marker spend the budget: got %d, want %d", v.toolBudget, want)
	}
}

// ADR-0031 Consequences: markers are display lines and spend the budget, so
// the turn's whole tool output is bounded by the budget plus two lines (the
// final straddle's marker and the one elision notice). Free markers would
// leak one extra line per cut result and break the bound.
func TestTurnViewToolBudgetBoundsTheTurnIncludingMarkers(t *testing.T) {
	out := &bytes.Buffer{}
	v := &turnView{out: out, styled: true, toolBudget: turnToolResultMaxLines}

	for i := 1; i <= 4; i++ {
		v.show(toolResultEvent(floodLines(i, toolResultMaxLines+4)))
	}
	got := out.String()

	if n := strings.Count(got, "\n"); n > turnToolResultMaxLines+2 {
		t.Errorf("the turn's tool output must stay within budget+2 lines: got %d: %q", n, got)
	}
	if n := strings.Count(got, elisionNotice); n != 1 {
		t.Errorf("the flood past the bound collapses to one elision notice: got %d: %q", n, got)
	}
}

// ADR-0031 Decision 1: detail lines and the turn's own text are outside the
// tool budget entirely — a spent budget silences only tool_result.
func TestTurnViewToolBudgetExhaustedStillShowsDetailAndText(t *testing.T) {
	out := &bytes.Buffer{}
	v := &turnView{out: out, styled: true, toolBudget: 0}

	v.show(executor.Event{Type: executor.EventProviderOutput,
		Payload: map[string]any{"tool": "Bash", executor.PayloadDetail: "ls -la"}})
	v.show(executor.Event{Type: executor.EventProviderOutput,
		Payload: map[string]any{"text": "**done**"}})
	got := out.String()

	if !strings.Contains(got, "· Bash ls -la") {
		t.Errorf("tool detail must show even with the budget spent: %q", got)
	}
	if strings.Contains(got, "**done**") {
		t.Errorf("text must still render through mdlite, not pass through raw: %q", got)
	}
}

// The motivating case (ADR-0030's colour sample, ADR-0031's Context): a short
// result must reach the screen whole, untouched by the turn budget.
func TestTurnViewToolBudgetLeavesAShortResultWhole(t *testing.T) {
	out := &bytes.Buffer{}
	v := &turnView{out: out, styled: true, toolBudget: turnToolResultMaxLines}

	v.show(toolResultEvent(floodLines(1, 3)))
	got := out.String()

	for _, want := range []string{"R1-L01", "R1-L02", "R1-L03"} {
		if !strings.Contains(got, want) {
			t.Errorf("a short result must show whole: %q missing from %q", want, got)
		}
	}
	if strings.Contains(got, "先頭のみ") || strings.Contains(got, elisionNotice) {
		t.Errorf("a short result must trip neither truncation marker: %q", got)
	}
	if v.toolBudget != turnToolResultMaxLines-3 {
		t.Errorf("budget must fall by exactly the lines shown: got %d, want %d", v.toolBudget, turnToolResultMaxLines-3)
	}
}

// Off a terminal (or NO_COLOR) tool_result draws nothing at all (ADR-0030
// Decision 1's styled() gate) — and, since nothing draws, the turn budget
// this ADR adds must not move either.
func TestTurnViewUnstyledSkipsToolResultAndLeavesBudgetUntouched(t *testing.T) {
	out := &bytes.Buffer{}
	v := &turnView{out: out, styled: false, toolBudget: turnToolResultMaxLines}

	v.show(toolResultEvent(floodLines(1, 12)))

	if out.String() != "" {
		t.Errorf("unstyled must draw nothing: %q", out.String())
	}
	if v.toolBudget != turnToolResultMaxLines {
		t.Errorf("unstyled must not spend the budget: got %d, want %d", v.toolBudget, turnToolResultMaxLines)
	}
}

// Piped output is read by a script or a test: escape codes in it are
// corruption, not decoration (the rule the avatar already follows). Under
// `go test` stdout is not a terminal, which is exactly the piped case.
func TestChatBookkeepingLinesCarryNoEscapeCodesOffATerminal(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, out := newTestChat(t, s, a, "\n")

	c.turn("implement it")
	if _, err := c.command("/new"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.command("/nope"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("no ANSI off a terminal: %q", out.String())
	}
}

// ADR-0047: /cd と /add-dir で置いた働く場所が、そのタスクの起動条件として
// Request に載る。同じ場所を二度足しても一度だけ持つ。
func TestChatWorkingPlacesRideOnTheRequest(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, _ := newTestChat(t, s, a, "\n")
	work, extra := t.TempDir(), t.TempDir()

	if _, err := c.command("/cd " + work); err != nil {
		t.Fatal(err)
	}
	if _, err := c.command("/add-dir " + extra); err != nil {
		t.Fatal(err)
	}
	if _, err := c.command("/add-dir " + extra); err != nil {
		t.Fatal(err)
	}
	if len(c.addDirs) != 1 {
		t.Errorf("the same place twice should be held once: %v", c.addDirs)
	}

	if err := c.turn("first task"); err != nil {
		t.Fatal(err)
	}
	if len(a.workDirs) != 1 || a.workDirs[0] != work {
		t.Errorf("work dir on the request: got %v, want [%s]", a.workDirs, work)
	}
	// The user's declaration must arrive first and intact. The opening turn
	// also carries the session's isolation dir (ADR-0050 Decision 4), which is
	// per-session wiring rather than something the person asked for — so it
	// rides alongside without displacing or reordering /add-dir.
	if len(a.addDirs) != 1 || len(a.addDirs[0]) == 0 || a.addDirs[0][0] != extra {
		t.Errorf("add dirs on the request: got %v, want [%s] first", a.addDirs, extra)
	}
	// c.addDirs is the user's list and must not have grown: the isolation dir
	// belongs to one turn's Request, not to their declaration.
	if len(c.addDirs) != 1 || c.addDirs[0] != extra {
		t.Errorf("the user's /add-dir list must stay theirs: %v", c.addDirs)
	}
}

// 実在しない場所は受け取らない: 立てない場所を cwd にした exec の chdir エラーは
// どの配線が悪いか語らない（ADR-0047 Decision 4）。
func TestChatCdRefusesAPlaceItCannotStandIn(t *testing.T) {
	s := openTestStore(t)
	c, out := newTestChat(t, s, &threadAdapter{}, "\n")
	gone := filepath.Join(t.TempDir(), "gone")

	if _, err := c.command("/cd " + gone); err != nil {
		t.Fatal(err)
	}
	if c.workDir != "" {
		t.Errorf("a missing place must not be accepted: %q", c.workDir)
	}
	if !strings.Contains(out.String(), gone) {
		t.Errorf("the refusal should name the path: %q", out.String())
	}
}

// 配線はタスク境界でだけ替わる（/provider と同じ規律・ADR-0047 Decision 4）。
func TestChatWorkingPlacesRefuseToChangeMidTask(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{}
	c, out := newTestChat(t, s, a, "\n")
	first, second := t.TempDir(), t.TempDir()

	if _, err := c.command("/cd " + first); err != nil {
		t.Fatal(err)
	}
	if err := c.turn("open the task"); err != nil {
		t.Fatal(err)
	}
	out.Reset()

	if _, err := c.command("/cd " + second); err != nil {
		t.Fatal(err)
	}
	if _, err := c.command("/add-dir " + second); err != nil {
		t.Fatal(err)
	}
	if c.workDir != first {
		t.Errorf("mid-task /cd must not take effect: %q", c.workDir)
	}
	if len(c.addDirs) != 0 {
		t.Errorf("mid-task /add-dir must not take effect: %v", c.addDirs)
	}
	if !strings.Contains(out.String(), "/new") {
		t.Errorf("the refusal should point at the boundary: %q", out.String())
	}
}

// /add-dir clear は全部外す。一覧（引数なし）は現在値を答えるだけで替えない。
func TestChatAddDirClearEmptiesTheList(t *testing.T) {
	s := openTestStore(t)
	c, out := newTestChat(t, s, &threadAdapter{}, "\n")
	extra := t.TempDir()

	if _, err := c.command("/add-dir " + extra); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, err := c.command("/add-dir"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), extra) {
		t.Errorf("a bare /add-dir should list what is set: %q", out.String())
	}
	if len(c.addDirs) != 1 {
		t.Errorf("listing must not change the list: %v", c.addDirs)
	}
	if _, err := c.command("/add-dir clear"); err != nil {
		t.Fatal(err)
	}
	if len(c.addDirs) != 0 {
		t.Errorf("clear should empty the list: %v", c.addDirs)
	}
}
