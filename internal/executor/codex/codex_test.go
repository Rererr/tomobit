// Package codex tests pin the stream→events mapping (ADR-0010 Decision 3)
// against two kinds of fixtures:
//
//   - testdata/codex-stream*.jsonl: real bytes captured from a live `codex
//     exec --json` run (an unsupported-model rejection, and a usage-limit
//     rejection) — these fix the error/dedup mapping to actual observed
//     output.
//   - the agent_message / tool-item / turn.completed cases below: built
//     from the public Codex JSONL schema, not yet captured live.
//     Implementation day: this machine's codex account was over its usage
//     limit (ADR-0010 Decision 4, recovers 2026-07-21), so the success path
//     could not be exercised against a real run. Re-run once the account
//     recovers and reconcile these fixtures against the real stream.
package codex

import (
	"bufio"
	"os"
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

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return lines
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestCommandBuildsHeadlessJSONExec(t *testing.T) {
	name, args, _ := New().Command(executor.Request{Prompt: "fix the bug"})
	if name != "codex" {
		t.Errorf("executable: got %q, want codex", name)
	}
	for _, want := range []string{"exec", "--json", "--skip-git-repo-check", "fix the bug"} {
		if !contains(args, want) {
			t.Errorf("args missing %q: got %v", want, args)
		}
	}
	// 未設定は「渡さない」ではなく auto 相当 (ADR-0053 Decision 1)。
	if !contains(args, "--sandbox") || !contains(args, "workspace-write") {
		t.Errorf("未設定は workspace-write として明示されるべき: %v", args)
	}
	if got := args[len(args)-1]; got != "fix the bug" {
		t.Errorf("prompt should stay the trailing argument: got %v", args)
	}
}

// 中立3語が codex 自身の語へ訳される (ADR-0053 Decision 1)。
// **同型ではない** — claude の permission は訊く機構、codex の sandbox は囲う
// 機構で、対応しているのは意図の方だけである。
func TestPermissionModeIsTranslatedIntoCodexSandbox(t *testing.T) {
	for _, tc := range []struct {
		in   executor.Permission
		want string
	}{
		{"", "workspace-write"},
		{executor.PermissionAuto, "workspace-write"},
		{executor.PermissionStrict, "read-only"},
		{executor.PermissionOpen, "danger-full-access"},
	} {
		_, args, _ := New().Command(executor.Request{Prompt: "p", PermissionMode: tc.in})
		if !contains(args, tc.want) {
			t.Errorf("%q → %q が argv に無い: %v", tc.in, tc.want, args)
		}
	}
}

// codex の sandbox は道具ごとの許可を持たないので、AllowedTools は訳さない
// （無いものを発明せず無視する — ADR-0047 が --add-dir について書いたのと同じ）。
func TestAllowedToolsAreIgnoredWhereTheCLIHasNoSuchThing(t *testing.T) {
	_, args, _ := New().Command(executor.Request{Prompt: "p", AllowedTools: []string{"Read"}})
	if contains(args, "--allowedTools") || contains(args, "Read") {
		t.Errorf("codex に無いフラグを発明した: %v", args)
	}
}

func TestCommandPassesSandboxModeThrough(t *testing.T) {
	_, args, _ := New().Command(executor.Request{Prompt: "p", PermissionMode: executor.PermissionAuto})
	if !contains(args, "--sandbox") || !contains(args, "workspace-write") {
		t.Errorf("permission mode should map to --sandbox: %v", args)
	}
	if got := args[len(args)-1]; got != "p" {
		t.Errorf("prompt should stay the trailing argument even with --sandbox: got %v", args)
	}
}

// TestTranslateStreamOneModelFallbackThenRejected fixes the mapping against
// testdata/codex-stream.jsonl: an unsupported-model run that first warns via
// an item.completed error item (metadata fallback), then fails the turn.
// The top-level "error" line (line 4) carries the same message as
// turn.failed (line 5) and must be dropped so the failure is recorded once.
func TestTranslateStreamOneModelFallbackThenRejected(t *testing.T) {
	lines := readLines(t, "testdata/codex-stream.jsonl")
	if len(lines) != 5 {
		t.Fatalf("fixture line count changed: got %d, want 5", len(lines))
	}

	evs := translate(t, lines[0])
	if len(evs) != 1 || evs[0].Type != executor.EventProviderSelected {
		t.Fatalf("line 1 thread.started: expected one provider.selected, got %v", evs)
	}
	if evs[0].Payload["provider"] != "codex" || evs[0].Payload["model"] != "" {
		t.Errorf("line 1 payload: got %v", evs[0].Payload)
	}
	if evs[0].Payload["provider_session_id"] != "019f6570-ecf1-7b20-a03a-d7ff201f0eb7" {
		t.Errorf("line 1 provider_session_id from thread_id: got %v", evs[0].Payload["provider_session_id"])
	}

	evs = translate(t, lines[1])
	if len(evs) != 1 || evs[0].Type != executor.EventProviderError {
		t.Fatalf("line 2 item.completed error item: expected one provider.error, got %v", evs)
	}
	wantItemMsg := "Model metadata for `gpt-5.4` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
	if evs[0].Payload["message"] != wantItemMsg {
		t.Errorf("line 2 message: got %v, want %v", evs[0].Payload["message"], wantItemMsg)
	}

	if evs := translate(t, lines[2]); len(evs) != 0 {
		t.Errorf("line 3 turn.started should be dropped, got %v", evs)
	}

	if evs := translate(t, lines[3]); len(evs) != 0 {
		t.Errorf("line 4 top-level error should be dropped (duplicate of turn.failed), got %v", evs)
	}

	evs = translate(t, lines[4])
	if len(evs) != 1 || evs[0].Type != executor.EventProviderError {
		t.Fatalf("line 5 turn.failed: expected one provider.error, got %v", evs)
	}
	wantMsg := `{"type":"error","status":400,"error":{"type":"invalid_request_error","message":"The 'gpt-5.4' model is not supported when using Codex with a ChatGPT account."}}`
	if evs[0].Payload["message"] != wantMsg {
		t.Errorf("line 5 message: got %v, want %v", evs[0].Payload["message"], wantMsg)
	}
}

// TestTranslateStreamTwoUsageLimitDedup fixes the mapping against
// testdata/codex-stream2.jsonl: a usage-limit failure with no
// item.completed at all — thread.started, turn.started (dropped), a
// top-level error (dropped, duplicate), and turn.failed (kept).
func TestTranslateStreamTwoUsageLimitDedup(t *testing.T) {
	lines := readLines(t, "testdata/codex-stream2.jsonl")
	if len(lines) != 4 {
		t.Fatalf("fixture line count changed: got %d, want 4", len(lines))
	}

	evs := translate(t, lines[0])
	if len(evs) != 1 || evs[0].Payload["provider_session_id"] != "019f6571-6c02-7390-98fd-cd5866dcdaf9" {
		t.Fatalf("line 1 thread.started: got %v", evs)
	}

	if evs := translate(t, lines[1]); len(evs) != 0 {
		t.Errorf("line 2 turn.started should be dropped, got %v", evs)
	}

	if evs := translate(t, lines[2]); len(evs) != 0 {
		t.Errorf("line 3 top-level error should be dropped (duplicate of turn.failed), got %v", evs)
	}

	evs = translate(t, lines[3])
	wantMsg := "You've hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again at Jul 21st, 2026 7:43 AM."
	if len(evs) != 1 || evs[0].Type != executor.EventProviderError || evs[0].Payload["message"] != wantMsg {
		t.Fatalf("line 4 turn.failed: got %v", evs)
	}
}

func TestTranslateAgentMessageBecomesOutput(t *testing.T) {
	evs := translate(t, `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"here is the fix"}}`)
	if len(evs) != 1 || evs[0].Type != executor.EventProviderOutput {
		t.Fatalf("expected one provider.output, got %v", evs)
	}
	if evs[0].Payload["text"] != "here is the fix" {
		t.Errorf("text: got %v", evs[0].Payload["text"])
	}
}

func TestTranslateEmptyAgentMessageIsDropped(t *testing.T) {
	if evs := translate(t, `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":""}}`); len(evs) != 0 {
		t.Errorf("empty text should not produce an output event, got %v", evs)
	}
}

func TestTranslateToolItemsKeepOnlyTheToolName(t *testing.T) {
	for _, tool := range []string{"command_execution", "file_change", "mcp_tool_call", "web_search"} {
		evs := translate(t, `{"type":"item.completed","item":{"id":"item_1","type":"`+tool+`"}}`)
		if len(evs) != 1 || evs[0].Type != executor.EventProviderOutput {
			t.Fatalf("%s: expected one provider.output, got %v", tool, evs)
		}
		if evs[0].Payload["tool"] != tool {
			t.Errorf("%s: tool name: got %v", tool, evs[0].Payload["tool"])
		}
	}
}

func TestTranslateToolItemsAttachDetail(t *testing.T) {
	cases := []struct {
		name string
		item string
		want string
	}{
		{"command_execution", `{"type":"command_execution","command":"go build ./..."}`, "go build ./..."},
		{"file_change", `{"type":"file_change","changes":[{"path":"internal/x.go","kind":"update"}]}`, "internal/x.go"},
		{"web_search", `{"type":"web_search","query":"golang json stream"}`, "golang json stream"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evs := translate(t, `{"type":"item.completed","item":`+c.item+`}`)
			if len(evs) != 1 {
				t.Fatalf("expected one event, got %v", evs)
			}
			if evs[0].Payload[executor.PayloadDetail] != c.want {
				t.Errorf("detail: got %v, want %q", evs[0].Payload[executor.PayloadDetail], c.want)
			}
		})
	}
}

// TestTranslateCommandDetailIsFirstLine keeps a multi-line command from
// spilling past the single line the view has, symmetric with claude-code.
func TestTranslateCommandDetailIsFirstLine(t *testing.T) {
	evs := translate(t, `{"type":"item.completed","item":{"type":"command_execution","command":"cd /tmp\nrm -rf x"}}`)
	if evs[0].Payload[executor.PayloadDetail] != "cd /tmp" {
		t.Errorf("only the first command line should show: got %v", evs[0].Payload[executor.PayloadDetail])
	}
}

// TestTranslateFileChangeDetailTruncatesPathKeepingTail caps a long path at
// 60 runes counted as characters (a multibyte path is not cut mid-rune), and
// keeps the tail — the filename is the information, same rule as claude-code.
func TestTranslateFileChangeDetailTruncatesPathKeepingTail(t *testing.T) {
	long := "internal/日本語/" + strings.Repeat("x", 76) + "file.go"
	evs := translate(t, `{"type":"item.completed","item":{"type":"file_change","changes":[{"path":"`+long+`","kind":"add"}]}}`)
	got, _ := evs[0].Payload[executor.PayloadDetail].(string)
	r := []rune(got)
	if len(r) != 60 || r[0] != '…' {
		t.Errorf("a cut path should be 60 runes starting with an ellipsis: got %q (%d runes)", got, len(r))
	}
	if !strings.HasSuffix(got, "file.go") {
		t.Errorf("the filename must survive the cut: got %q", got)
	}
}

// TestTranslateToolItemWithoutDetailFieldsHasNoDetail pins that an item type
// whose stream fields are absent (or an mcp_tool_call, left summary-less)
// stays name-only — the raw item never leaks into the payload.
func TestTranslateToolItemWithoutDetailFieldsHasNoDetail(t *testing.T) {
	for _, item := range []string{
		`{"type":"command_execution"}`,
		`{"type":"file_change"}`,
		`{"type":"mcp_tool_call","server":"fs","tool":"read"}`,
	} {
		evs := translate(t, `{"type":"item.completed","item":`+item+`}`)
		if _, ok := evs[0].Payload[executor.PayloadDetail]; ok {
			t.Errorf("%s: no detail expected, got %v", item, evs[0].Payload)
		}
	}
}

func TestTranslateReasoningAndTodoListItemsAreDropped(t *testing.T) {
	for _, itemType := range []string{"reasoning", "todo_list"} {
		if evs := translate(t, `{"type":"item.completed","item":{"id":"item_1","type":"`+itemType+`"}}`); len(evs) != 0 {
			t.Errorf("%s item should be dropped, got %v", itemType, evs)
		}
	}
}

func TestTranslateItemStartedAndUpdatedAreDropped(t *testing.T) {
	for _, typ := range []string{"item.started", "item.updated"} {
		if evs := translate(t, `{"type":"`+typ+`","item":{"id":"item_1","type":"agent_message","text":"partial"}}`); len(evs) != 0 {
			t.Errorf("%s should be dropped (completed items only), got %v", typ, evs)
		}
	}
}

// TestTranslateTurnCompletedBecomesProviderFinished fixes the success-path
// mapping against the public Codex JSONL schema (not yet captured live —
// see the package doc comment above).
func TestTranslateTurnCompletedBecomesProviderFinished(t *testing.T) {
	evs := translate(t, `{"type":"turn.completed","usage":{"input_tokens":120,"cached_input_tokens":40,"output_tokens":85}}`)
	if len(evs) != 1 || evs[0].Type != executor.EventProviderFinished {
		t.Fatalf("expected one provider.finished, got %v", evs)
	}
	p := evs[0].Payload
	if p["input_tokens"].(int64) != 120 || p["cached_input_tokens"].(int64) != 40 || p["output_tokens"].(int64) != 85 {
		t.Errorf("usage: got %v", p)
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

// TestCommandResumesTheThread pins ADR-0022 Decision 2 against the measured
// CLI shape: `codex exec resume <id> <prompt>`.
func TestCommandResumesTheThread(t *testing.T) {
	name, args, _ := New().Command(executor.Request{Prompt: "and now fix it", ResumeID: "th-1"})
	if name != "codex" {
		t.Errorf("executable: got %q", name)
	}
	want := []string{"exec", "resume", "th-1", "and now fix it", "--json", "--skip-git-repo-check"}
	if len(args) != len(want) {
		t.Fatalf("args: got %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args: got %v, want %v", args, want)
		}
	}
}

// A resumed turn carries no --sandbox: `codex exec resume` has no such flag
// (measured on 0.144.4) — the thread keeps the sandbox it started with.
func TestCommandResumeDropsTheSandboxFlag(t *testing.T) {
	_, args, _ := New().Command(executor.Request{Prompt: "p", ResumeID: "th-1", PermissionMode: "workspace-write"})
	for _, a := range args {
		if a == "--sandbox" {
			t.Errorf("resume has no --sandbox seat: %v", args)
		}
	}
}

// ADR-0047 Decision 3: codex にも --add-dir がある（0.144.x）。同じ Request
// フィールドがここでも翻訳され、プロンプトは末尾の位置引数のまま残る。
func TestCommandTranslatesAddDirsToAddDirFlags(t *testing.T) {
	_, args, _ := New().Command(executor.Request{
		Prompt:  "p",
		AddDirs: []string{"/w/notes", "/w/shared"},
	})
	pairs := 0
	for i, a := range args {
		if a == "--add-dir" {
			pairs++
			if i+1 >= len(args) || args[i+1] == "p" {
				t.Fatalf("--add-dir swallowed the prompt or has no value: %v", args)
			}
		}
	}
	if pairs != 2 {
		t.Errorf("one --add-dir per directory expected, got %d: %v", pairs, args)
	}
	if got := args[len(args)-1]; got != "p" {
		t.Errorf("prompt should stay the trailing argument: got %v", args)
	}
}

// `codex exec resume` には --add-dir の席が無い（--sandbox と同じ）。
// スレッドは開始時の設定のまま続くので、積まないのが正直なマッピング。
func TestCommandResumeDropsTheAddDirFlag(t *testing.T) {
	_, args, _ := New().Command(executor.Request{Prompt: "p", ResumeID: "th-1", AddDirs: []string{"/w/notes"}})
	for _, a := range args {
		if a == "--add-dir" {
			t.Errorf("resume has no --add-dir seat: %v", args)
		}
	}
}
