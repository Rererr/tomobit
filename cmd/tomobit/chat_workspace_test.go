package main

import (
	"bufio"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
)

// declaringAdapter answers with a fixed line instead of echoing the prompt, so
// a test can put a real workspace declaration on the wire. prompts keeps every
// prompt it was handed, which is how "the protocol rides only the opening
// turn" is pinned.
type declaringAdapter struct {
	answer  string
	prompts []string
}

func (a *declaringAdapter) Name() string { return "fake" }

func (a *declaringAdapter) Command(req executor.Request) (string, []string, []string) {
	a.prompts = append(a.prompts, req.Prompt)
	return "printf", []string{"SEL\n" + a.answer + "\n"}, nil
}

func (a *declaringAdapter) Translate(line []byte) ([]executor.Event, error) {
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

func newDeclaringChat(t *testing.T, s *store.Store, a *declaringAdapter) *chat {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // never build worktrees under the real home
	providers["fake"] = a
	t.Cleanup(func() { delete(providers, "fake") })
	return &chat{
		s: s, out: &strings.Builder{}, providerName: "fake", capability: "implement",
		extractor: &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}},
		in:        bufio.NewReader(strings.NewReader("\n\n\n")),
	}
}

// The isolation protocol rides the turn that opens a task and no other
// (ADR-0050 Decision 5): a workspace is decided when the task is born, and a
// continuing turn that wanted a different place would be a new task.
func TestChatIsolationRidesOnlyTheOpeningTurn(t *testing.T) {
	s := openTestStore(t)
	a := &declaringAdapter{answer: "ふつうに作業した"}
	c := newDeclaringChat(t, s, a)

	if err := c.turn("first task"); err != nil {
		t.Fatal(err)
	}
	if err := c.turn("keep going"); err != nil {
		t.Fatal(err)
	}

	if len(a.prompts) != 2 {
		t.Fatalf("two turns expected, got %d", len(a.prompts))
	}
	if !strings.Contains(a.prompts[0], "tomobit_workspace") {
		t.Errorf("the opening turn must carry the protocol:\n%s", a.prompts[0])
	}
	if strings.Contains(a.prompts[1], "tomobit_workspace") {
		t.Errorf("a continuing turn must not:\n%s", a.prompts[1])
	}
	// The place tomobit chose is named in the prompt, keyed by session.
	if !strings.Contains(a.prompts[0], c.sid) {
		t.Errorf("the isolation path must be keyed by session: %s", a.prompts[0])
	}
}

func TestChatRecordsTheDeclaration(t *testing.T) {
	s := openTestStore(t)
	a := &declaringAdapter{
		answer: `{"tomobit_workspace": {"isolated": true, "kind": "git worktree", "path": "/tmp/wt/x"}}`,
	}
	c := newDeclaringChat(t, s, a)

	if err := c.turn("first task"); err != nil {
		t.Fatal(err)
	}

	p := workspacePayload(t, s, c.sid)
	if p == nil {
		t.Fatalf("the declaration must be recorded on the chat's session")
	}
	if p["isolated"] != true || p["path"] != "/tmp/wt/x" {
		t.Errorf("payload: %v", p)
	}
}

// A model that quotes its instructions back hands Parse a schema-legal object.
// Recording that placeholder would tell the human their results live at
// "<作った場所>". The echo guard has to hold through the real chat pipeline,
// not just in the parser's own tests.
func TestChatDoesNotRecordAnEchoedInstruction(t *testing.T) {
	s := openTestStore(t)
	// threadAdapter echoes the whole prompt — instruction, examples and all.
	a := &threadAdapter{}
	t.Setenv("HOME", t.TempDir())
	c, _ := newTestChat(t, s, a, "\n\n")

	if err := c.turn("first task"); err != nil {
		t.Fatal(err)
	}

	if p := workspacePayload(t, s, c.sid); p != nil {
		t.Errorf("an echoed example is no declaration, got %v", p)
	}
}

// The kill switch reaches chat too, not just do.
func TestChatIsolationKillSwitch(t *testing.T) {
	s := openTestStore(t)
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	no := false
	cfg.IsolateProtocol = &no

	a := &declaringAdapter{answer: "作業した"}
	c := newDeclaringChat(t, s, a)

	if err := c.turn("first task"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.prompts[0], "tomobit_workspace") {
		t.Errorf("an explicit false must stop the protocol:\n%s", a.prompts[0])
	}
}
