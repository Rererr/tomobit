package claudecode

import (
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
)

func translate(t *testing.T, line string) []executor.Event {
	t.Helper()
	evs, err := New().Translate([]byte(line))
	if err != nil {
		t.Fatalf("translate %q: %v", line, err)
	}
	return evs
}

func TestCommandBuildsHeadlessStreamJSON(t *testing.T) {
	name, args, env := New().Command(executor.Request{Prompt: "fix the bug"})
	if len(env) != 0 {
		t.Errorf("no extra env expected when ConfigDir is unset: %v", env)
	}
	if name != "claude" {
		t.Errorf("executable: got %q, want claude", name)
	}
	joined := ""
	for _, a := range args {
		joined += a + " "
	}
	for _, want := range []string{"-p", "fix the bug", "--output-format", "stream-json", "--verbose"} {
		if !contains(args, want) {
			t.Errorf("args missing %q: got %v", want, args)
		}
	}
	if contains(args, "--permission-mode") {
		t.Errorf("no permission mode should be passed when unset: %v", args)
	}
	_ = joined
}

func TestCommandPassesPermissionModeThrough(t *testing.T) {
	_, args, _ := New().Command(executor.Request{Prompt: "p", PermissionMode: "acceptEdits"})
	if !contains(args, "--permission-mode") || !contains(args, "acceptEdits") {
		t.Errorf("permission mode should be forwarded: %v", args)
	}
}

func TestCommandInjectsProfileAndExtraArgs(t *testing.T) {
	a := New()
	a.ConfigDir = "/home/u/.claude-personal"
	a.ExtraArgs = []string{"--exclude-dynamic-system-prompt-sections"}
	_, args, env := a.Command(executor.Request{Prompt: "p"})
	if !contains(args, "--exclude-dynamic-system-prompt-sections") {
		t.Errorf("extra args should be appended: %v", args)
	}
	if len(env) != 1 || env[0] != "CLAUDE_CONFIG_DIR=/home/u/.claude-personal" {
		t.Errorf("ConfigDir should become CLAUDE_CONFIG_DIR: %v", env)
	}
}

func TestTranslateInitBecomesProviderSelected(t *testing.T) {
	evs := translate(t, `{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-opus-4-8","tools":["Bash"]}`)
	if len(evs) != 1 || evs[0].Type != executor.EventProviderSelected {
		t.Fatalf("expected one provider.selected, got %v", evs)
	}
	if evs[0].Payload["provider"] != "claude-code" {
		t.Errorf("provider must be the canonical name: got %v", evs[0].Payload["provider"])
	}
	if evs[0].Payload["model"] != "claude-opus-4-8" {
		t.Errorf("model from init: got %v", evs[0].Payload["model"])
	}
}

func TestTranslateNonInitSystemLineIsDropped(t *testing.T) {
	if evs := translate(t, `{"type":"system","subtype":"other"}`); len(evs) != 0 {
		t.Errorf("non-init system line should be dropped, got %v", evs)
	}
}

func TestTranslateAssistantTextBecomesOutput(t *testing.T) {
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"here is the fix"}]}}`)
	if len(evs) != 1 || evs[0].Type != executor.EventProviderOutput {
		t.Fatalf("expected one provider.output, got %v", evs)
	}
	if evs[0].Payload["text"] != "here is the fix" {
		t.Errorf("text: got %v", evs[0].Payload["text"])
	}
}

func TestTranslateEmptyAssistantTextIsDropped(t *testing.T) {
	if evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"text","text":""}]}}`); len(evs) != 0 {
		t.Errorf("empty text should not produce an output event, got %v", evs)
	}
}

func TestTranslateToolUseKeepsOnlyTheName(t *testing.T) {
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file":"secret.go"}}]}}`)
	if len(evs) != 1 || evs[0].Type != executor.EventProviderOutput {
		t.Fatalf("expected one provider.output, got %v", evs)
	}
	if evs[0].Payload["tool"] != "Edit" {
		t.Errorf("tool name: got %v", evs[0].Payload["tool"])
	}
	if _, ok := evs[0].Payload["input"]; ok {
		t.Errorf("tool input must be dropped (digest policy): got %v", evs[0].Payload)
	}
}

func TestTranslateToolUseAttachesDetailFromKnownInputKey(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"file_path", `{"file_path":"/src/main.go"}`, "/src/main.go"},
		{"path", `{"path":"/src"}`, "/src"},
		{"command", `{"command":"go test ./..."}`, "go test ./..."},
		{"pattern", `{"pattern":"*.go"}`, "*.go"},
		{"url", `{"url":"https://example.com"}`, "https://example.com"},
		{"query", `{"query":"how to parse json"}`, "how to parse json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"T","input":`+c.input+`}]}}`)
			if len(evs) != 1 {
				t.Fatalf("expected one event, got %v", evs)
			}
			if evs[0].Payload[executor.PayloadDetail] != c.want {
				t.Errorf("detail: got %v, want %q", evs[0].Payload[executor.PayloadDetail], c.want)
			}
		})
	}
}

// TestTranslateToolUseDetailPrefersFilePathOverCommand pins the priority order:
// a tool carrying both a target path and a command shows the path.
func TestTranslateToolUseDetailPrefersFilePathOverCommand(t *testing.T) {
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"T","input":{"command":"cat x","file_path":"/x"}}]}}`)
	if evs[0].Payload[executor.PayloadDetail] != "/x" {
		t.Errorf("file_path should win over command: got %v", evs[0].Payload[executor.PayloadDetail])
	}
}

// TestTranslateToolUseDetailIsFirstLineOfCommand keeps a heredoc or a chained
// command from spilling past the single line the view has.
func TestTranslateToolUseDetailIsFirstLineOfCommand(t *testing.T) {
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo hi\nrm -rf /tmp/x"}}]}}`)
	if evs[0].Payload[executor.PayloadDetail] != "echo hi" {
		t.Errorf("only the first command line should show: got %v", evs[0].Payload[executor.PayloadDetail])
	}
}

// TestTranslateToolUseDetailTruncatesPathKeepingTail caps a long path at 60
// runes counted as characters (a multibyte path is not cut mid-rune), and
// keeps the tail: the filename answers "where", the shared prefix of a deep
// absolute path answers nothing.
func TestTranslateToolUseDetailTruncatesPathKeepingTail(t *testing.T) {
	long := "/very/long/日本語/" + strings.Repeat("x", 76) + "file.go"
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"T","input":{"file_path":"`+long+`"}}]}}`)
	got, _ := evs[0].Payload[executor.PayloadDetail].(string)
	r := []rune(got)
	if len(r) != 60 || r[0] != '…' {
		t.Errorf("a cut path should be 60 runes starting with an ellipsis: got %q (%d runes)", got, len(r))
	}
	if !strings.HasSuffix(got, "file.go") {
		t.Errorf("the filename must survive the cut: got %q", got)
	}
}

// TestTranslateToolUseDetailTruncatesCommandKeepingHead pins the other
// direction: a command's intent is its verb, so the head survives.
func TestTranslateToolUseDetailTruncatesCommandKeepingHead(t *testing.T) {
	long := "git log --oneline " + strings.Repeat("x", 80)
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"`+long+`"}}]}}`)
	got, _ := evs[0].Payload[executor.PayloadDetail].(string)
	r := []rune(got)
	if len(r) != 60 || r[59] != '…' {
		t.Errorf("a cut command should be 60 runes ending in an ellipsis: got %q (%d runes)", got, len(r))
	}
	if !strings.HasPrefix(got, "git log --oneline") {
		t.Errorf("the command's head must survive the cut: got %q", got)
	}
}

func TestTranslateToolUseWithoutKnownKeyHasNoDetail(t *testing.T) {
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"T","input":{"old_string":"a","new_string":"b"}}]}}`)
	if _, ok := evs[0].Payload[executor.PayloadDetail]; ok {
		t.Errorf("unknown input keys should not produce a detail: got %v", evs[0].Payload)
	}
}

// TestTranslateToolUseDetailNeverCarriesRawInput keeps ADR-0024's promise that
// only the derived summary is added — the raw input object is still dropped
// even when a detail is attached (SCHEMA.md R3 digest policy, unchanged).
func TestTranslateToolUseDetailNeverCarriesRawInput(t *testing.T) {
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/x","old_string":"secret"}}]}}`)
	p := evs[0].Payload
	if _, ok := p["input"]; ok {
		t.Errorf("raw input must be dropped: got %v", p)
	}
	if _, ok := p["old_string"]; ok {
		t.Errorf("no input field other than the derived detail may appear: got %v", p)
	}
	if p[executor.PayloadDetail] != "/x" {
		t.Errorf("detail should carry only the target: got %v", p[executor.PayloadDetail])
	}
}

func TestTranslateMixedContentEmitsEventPerBlock(t *testing.T) {
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"running"},{"type":"tool_use","name":"Bash"}]}}`)
	if len(evs) != 2 {
		t.Fatalf("expected an event per content block, got %d: %v", len(evs), evs)
	}
	if evs[0].Payload["text"] != "running" || evs[1].Payload["tool"] != "Bash" {
		t.Errorf("blocks translated out of order: %v", evs)
	}
}

func TestTranslateThinkingBlockIsDroppedButTextSurvives(t *testing.T) {
	evs := translate(t, `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"let me consider the options..."},{"type":"text","text":"answer"}]}}`)
	if len(evs) != 1 || evs[0].Payload["text"] != "answer" {
		t.Fatalf("thinking block should be dropped, only the text event should remain: got %v", evs)
	}
}

func TestTranslateToolResultIsDropped(t *testing.T) {
	if evs := translate(t, `{"type":"user","message":{"content":[{"type":"tool_result","content":"file contents"}]}}`); len(evs) != 0 {
		t.Errorf("tool_result must be dropped, got %v", evs)
	}
}

func TestTranslateResultBecomesProviderFinished(t *testing.T) {
	evs := translate(t, `{"type":"result","subtype":"success","is_error":false,"duration_ms":1234,"num_turns":3,"total_cost_usd":0.0567,"session_id":"sess-1","result":"done"}`)
	if len(evs) != 1 || evs[0].Type != executor.EventProviderFinished {
		t.Fatalf("expected one provider.finished, got %v", evs)
	}
	p := evs[0].Payload
	if p["duration_ms"].(int64) != 1234 || p["num_turns"].(int64) != 3 {
		t.Errorf("metadata: got %v", p)
	}
	if p["cost_usd"].(float64) != 0.0567 {
		t.Errorf("cost from total_cost_usd: got %v", p["cost_usd"])
	}
	if p["provider_session_id"] != "sess-1" {
		t.Errorf("provider_session_id from result line: got %v", p["provider_session_id"])
	}
	if _, ok := p["exit_code"]; ok {
		t.Errorf("exit_code is the Executor's to fill, not the adapter's: got %v", p)
	}
}

func TestTranslateErrorResultAlsoEmitsProviderError(t *testing.T) {
	evs := translate(t, `{"type":"result","subtype":"error_max_turns","is_error":true,"duration_ms":10,"session_id":"s","result":"turn limit reached"}`)
	if len(evs) != 2 {
		t.Fatalf("error result should emit finished + error, got %d: %v", len(evs), evs)
	}
	if evs[0].Type != executor.EventProviderFinished || evs[1].Type != executor.EventProviderError {
		t.Fatalf("order should be finished then error: %v", evs)
	}
	if evs[1].Payload["message"] != "turn limit reached" {
		t.Errorf("error message: got %v", evs[1].Payload["message"])
	}
}

func TestTranslateErrorResultFallsBackToSubtypeMessage(t *testing.T) {
	evs := translate(t, `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"s"}`)
	if len(evs) != 2 {
		t.Fatalf("expected finished + error, got %v", evs)
	}
	if evs[1].Payload["message"] != "error_during_execution" {
		t.Errorf("message should fall back to subtype: got %v", evs[1].Payload["message"])
	}
}

func TestTranslateUnknownTypeIsDroppedNotAnError(t *testing.T) {
	evs, err := New().Translate([]byte(`{"type":"future_thing","data":42}`))
	if err != nil {
		t.Fatalf("unknown type must not error (forward compatible): %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("unknown type should be dropped, got %v", evs)
	}
}

func TestTranslateBlankLineIsDropped(t *testing.T) {
	if evs := translate(t, "   \n"); len(evs) != 0 {
		t.Errorf("blank line should be dropped, got %v", evs)
	}
}

func TestTranslateMalformedJSONErrors(t *testing.T) {
	if _, err := New().Translate([]byte(`{not json`)); err == nil {
		t.Error("malformed JSON should return an error so the Executor can log it")
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestCommandResumesTheThread pins ADR-0022 Decision 2: a chat's later turns
// continue the provider's own session rather than starting a cold one.
func TestCommandResumesTheThread(t *testing.T) {
	_, args, _ := New().Command(executor.Request{Prompt: "and now fix it", ResumeID: "sess-1"})
	if !contains(args, "--resume") || !contains(args, "sess-1") {
		t.Errorf("resume id should be forwarded: %v", args)
	}
}

func TestCommandWithoutResumeIDStartsAFreshThread(t *testing.T) {
	_, args, _ := New().Command(executor.Request{Prompt: "p"})
	if contains(args, "--resume") {
		t.Errorf("no --resume without an id: %v", args)
	}
}

// TestTranslateInitCarriesTheThreadID guards the chat's continuity: the init
// line is the only place a run that is later cancelled ever names its
// thread, so the id must not wait for the result line.
func TestTranslateInitCarriesTheThreadID(t *testing.T) {
	evs := translate(t, `{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-opus-4-8"}`)
	if len(evs) != 1 {
		t.Fatalf("got %v", evs)
	}
	if evs[0].Payload["provider_session_id"] != "sess-1" {
		t.Errorf("provider_session_id from init: got %v", evs[0].Payload["provider_session_id"])
	}
}
