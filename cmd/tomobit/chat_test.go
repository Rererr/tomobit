package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
)

// threadAdapter is a provider that runs `printf` instead of a CLI, so a chat
// can be driven turn by turn with no real one. It records the ResumeID of
// every launch — how a test sees whether the thread was continued.
type threadAdapter struct {
	resumes []string
	fail    bool
}

func (a *threadAdapter) Name() string { return "fake" }

func (a *threadAdapter) Command(req executor.Request) (string, []string, []string) {
	a.resumes = append(a.resumes, req.ResumeID)
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
	c, out := newTestChat(t, s, a, "e\n")

	c.turn("implement it")
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "採用?") {
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

// Nothing ran to completion, so there is nothing to judge: asking "採用?"
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
	if strings.Contains(out.String(), "採用?") {
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
	if !strings.Contains(out.String(), "採用?") {
		t.Errorf("the human's own work is judged like any provider's: %q", out.String())
	}
}

// A failed run still produced something the user may keep, so the boundary
// still asks — and the failure is recorded once (ADR-0006 Decision 4).
func TestChatFailedTurnStillReachesTheAdoptionQuestion(t *testing.T) {
	s := openTestStore(t)
	a := &threadAdapter{fail: true}
	c, out := newTestChat(t, s, a, "r\n")

	if err := c.turn("break it"); err != nil {
		t.Fatal(err)
	}
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}
	if n := countEventsOfType(t, s, "provider.error"); n != 1 {
		t.Errorf("provider.error: got %d, want 1", n)
	}
	if !strings.Contains(out.String(), "採用?") {
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
