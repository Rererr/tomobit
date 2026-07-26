// Package claudecode adapts the Claude Code CLI (`claude -p ... stream-json`)
// to the executor's canonical events (ADR-0006 Decision 3). Translate is a
// pure per-line function; the fixtures in the test pin every mapping.
package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rererr/tomobit/internal/executor"
)

// providerName is the canonical target recorded for this provider (SCHEMA.md
// R3): the tool name only, never a model version.
const providerName = "claude-code"

type Adapter struct {
	// ConfigDir, when non-empty, launches claude under this
	// CLAUDE_CONFIG_DIR, selecting which profile (account, settings,
	// credentials) the run uses. Empty inherits the parent environment.
	// The provider name stays "claude-code" either way (SCHEMA.md R3):
	// the profile changes who is logged in, not which tool ran.
	ConfigDir string
	// ExtraArgs are appended to every launch, after the per-request flags.
	ExtraArgs []string
}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string { return providerName }

// Command builds the headless launch. stream-json requires --verbose in print
// mode, or the CLI refuses to emit the stream.
//
// --resume continues the thread (ADR-0022 Decision 2). Measured on claude
// 2.1.210: the resumed run replays no earlier turn — only the new one
// streams — so a chat's later turns add nothing to the ledger twice.
func (a *Adapter) Command(req executor.Request) (string, []string, []string) {
	args := []string{"-p", req.Prompt, "--output-format", "stream-json", "--verbose"}
	if req.ResumeID != "" {
		args = append(args, "--resume", req.ResumeID)
	}
	args = append(args, "--permission-mode", permissionFlag(req.PermissionMode))
	// 人が許した道具だけを積む (ADR-0053 Decision 3)。空なら足さない —
	// 「何も許されていない」と「全部許されている」の距離をフラグ1本で縮めない。
	if len(req.AllowedTools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, req.AllowedTools...)
	}
	// 作業場所の外で扱わせる場所 (ADR-0047 Decision 3)。--add-dir は可変長引数
	// なので、ディレクトリをまとめず1つにつき1回積む: `--add-dir a b` の形は
	// 後続フラグとの境界が並び順に依存する。
	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}
	args = append(args, a.ExtraArgs...)
	var env []string
	if a.ConfigDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+a.ConfigDir)
	}
	return "claude", args, env
}

// streamLine is the union of the stream-json envelopes this adapter reads.
// Fields absent for a given type stay at their zero value.
type streamLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// system/init
	Model     string `json:"model"`
	SessionID string `json:"session_id"`

	// assistant / user
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
			// Input is the tool_use argument object. It never enters the
			// payload (SCHEMA.md R3 keeps the tool name only); it is read
			// solely to derive the view-only detail (ADR-0024 Decision 6).
			Input map[string]any `json:"input"`
			// Content is a tool_result's output (on a `user` message). A string
			// for most tools; occasionally an array of content blocks. Read as
			// raw and decoded by toolResultText, which handles both. It rides
			// the view-only channel (ADR-0030), never the ledger.
			Content json.RawMessage `json:"content"`
		} `json:"content"`
	} `json:"message"`

	// result
	DurationMs   *int64   `json:"duration_ms"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	NumTurns     *int64   `json:"num_turns"`
	IsError      bool     `json:"is_error"`
	Result       string   `json:"result"`
	// PermissionDenials lists the tools this run was not allowed to use
	// (ADR-0053). Measured 2026-07-27 on claude 2.1.220: the turn ends here —
	// even under --input-format stream-json no control message arrives to
	// answer with — so this is the only place the request is legible.
	PermissionDenials []struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	} `json:"permission_denials"`
}

// permissionFlag translates tomobit's three words into claude's own
// (ADR-0053 Decision 1). Measured 2026-07-27 on claude 2.1.220: the flag takes
// acceptEdits / auto / bypassPermissions / manual / dontAsk / plan.
//
// auto は「普通に働いて、要るところで人に訊く」— この CLI がまさにその名前を
// 持っている。strict → manual は「訊かれるまで何もしない」で、read-only の
// 意味に一番近い。open → bypassPermissions は「訊かない」そのもの。
//
// 未知の値は auto へ落とす: 権限は開く側へ倒れてはいけないので、迷ったら
// 「訊く」に着地させる。
func permissionFlag(p executor.Permission) string {
	switch p.Resolve() {
	case executor.PermissionStrict:
		return "manual"
	case executor.PermissionOpen:
		return "bypassPermissions"
	default:
		return "auto"
	}
}

// permissionDetail summarises what a denied tool was about to do, for the one
// line a person reads before deciding (ADR-0053 Decision 3 — 入力は見せるが、
// 許可の単位にはしない).
//
// The keys are claude's tool-input shapes, tried in the order that answers
// "what would this touch?" first. An unknown shape yields "" rather than a
// dump: a person deciding on a permission is not helped by raw JSON, and the
// tool name alone is still a real question.
func permissionDetail(input map[string]any) string {
	for _, k := range []string{"file_path", "command", "path", "pattern", "url"} {
		if v, ok := input[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (a *Adapter) Translate(line []byte) ([]executor.Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	var s streamLine
	if err := json.Unmarshal(line, &s); err != nil {
		return nil, fmt.Errorf("claude-code: parse stream line: %w", err)
	}

	switch s.Type {
	case "system":
		if s.Subtype != "init" {
			return nil, nil
		}
		// provider_session_id is carried here as well as on provider.finished
		// (codex has always done so on thread.started — the two adapters are
		// symmetric now): the init line is the only place a run that is later
		// cancelled ever names its thread, and a chat must be able to resume
		// the turn the user just interrupted (ADR-0022 Decision 2).
		return []executor.Event{{
			Type: executor.EventProviderSelected,
			Payload: map[string]any{
				"provider": providerName, "model": s.Model,
				"provider_session_id": s.SessionID,
			},
		}}, nil

	case "assistant":
		var out []executor.Event
		for _, c := range s.Message.Content {
			switch c.Type {
			case "text":
				if c.Text != "" {
					out = append(out, executor.Event{
						Type:    executor.EventProviderOutput,
						Payload: map[string]any{"text": c.Text},
					})
				}
			case "tool_use":
				// The ledger keeps only the tool name (SCHEMA.md R3): inputs and
				// results are the digest we deliberately drop (ADR-0006 Decision
				// 3). A short summary of the input rides along as the view-only
				// detail (ADR-0024 Decision 6) — the raw input still never lands
				// in the payload.
				p := map[string]any{"tool": c.Name}
				if d := toolDetail(c.Input); d != "" {
					p[executor.PayloadDetail] = d
				}
				out = append(out, executor.Event{
					Type:    executor.EventProviderOutput,
					Payload: p,
				})
			case "thinking":
				// Deliberately dropped: extended thinking is not the answer the
				// user judges for adoption, and the semantic extractor derives
				// lang/framework/topic/size from the final text and tool calls,
				// not the reasoning that led there (ADR-0006 digest policy).
			}
		}
		return out, nil

	case "result":
		// exit_code is filled by the Executor, which alone observes it.
		payload := map[string]any{"provider_session_id": s.SessionID}
		if s.DurationMs != nil {
			payload["duration_ms"] = *s.DurationMs
		}
		if s.TotalCostUSD != nil {
			payload["cost_usd"] = *s.TotalCostUSD
		}
		if s.NumTurns != nil {
			payload["num_turns"] = *s.NumTurns
		}
		out := []executor.Event{{Type: executor.EventProviderFinished, Payload: payload}}
		// 権限で止まったことは失敗ではない (ADR-0053 Decision 4): 実行は正常に
		// 終わっており、進まなかったのは作業の方である。provider.error にすると
		// 「人が許可を渡さなかった」判断が Provider の成績になる。
		for _, d := range s.PermissionDenials {
			if d.ToolName == "" {
				continue
			}
			out = append(out, executor.Event{
				Type: executor.EventPermissionRequired,
				Payload: map[string]any{
					"tool":   d.ToolName,
					"detail": permissionDetail(d.ToolInput),
				},
			})
		}
		if s.IsError || strings.HasPrefix(s.Subtype, "error") {
			out = append(out, executor.Event{
				Type:    executor.EventProviderError,
				Payload: map[string]any{"message": errorMessage(s)},
			})
		}
		return out, nil

	case "user":
		// A user message carries the tool_result: the output of a tool the
		// assistant ran (ADR-0030). It rides the view-only channel so the human
		// can judge an answer that IS terminal output — a Bash colour demo, a
		// diff — while the ledger keeps only the tool name (R3). The raw ANSI is
		// carried through untouched; the view sanitises it (mdlite.ToolOutput).
		var out []executor.Event
		for _, c := range s.Message.Content {
			if c.Type != "tool_result" {
				continue
			}
			if text := toolResultText(c.Content); text != "" {
				out = append(out, executor.Event{
					Type:    executor.EventProviderOutput,
					Payload: map[string]any{executor.PayloadToolResult: text},
				})
			}
		}
		return out, nil

	default:
		// Any unknown type: dropped, not an error, so a new stream type never
		// breaks a run (forward compatible). The Executor surfaces dropped
		// lines under Debug.
		return nil, nil
	}
}

// toolResultText decodes a tool_result's content, which the CLI sends either as
// a plain string (the common case — a Bash command's stdout) or as an array of
// content blocks (e.g. a tool that returns text alongside an image). Both forms
// reduce to the concatenated text; anything else (a null, an image-only block)
// yields "". Errors are swallowed to "": a shape this adapter does not model is
// a dropped line, never a failed run (the same forward-compatibility the
// unknown-type default keeps).
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, bl := range blocks {
		if bl.Type == "text" {
			b.WriteString(bl.Text)
		}
	}
	return b.String()
}

// detailKeys are the tool_use input fields, in priority order, that answer
// "what, where" for someone watching a turn: the path a file tool touched
// (file_path across Edit/Write/Read/NotebookEdit, path for Glob/Grep), the
// Bash command, the Glob/Grep pattern, the WebFetch url, the WebSearch query.
// The first non-empty one wins; keys carrying content rather than a target
// (old_string, new_string, prompt) are deliberately left out.
var detailKeys = []string{"file_path", "path", "command", "pattern", "url", "query"}

// toolDetail derives the view-only summary from a tool_use input, or "" when
// none of the known keys carry a usable value.
func toolDetail(input map[string]any) string {
	for _, k := range detailKeys {
		s, ok := input[k].(string)
		if !ok {
			continue
		}
		if k == "command" {
			// Only the first line: a heredoc or a chained command would
			// bury the intent under its body, and the view has one line.
			s, _, _ = strings.Cut(s, "\n")
		}
		if d := executor.TruncateDetail(s, k == "file_path" || k == "path"); d != "" {
			return d
		}
	}
	return ""
}

func errorMessage(s streamLine) string {
	if s.Result != "" {
		return s.Result
	}
	if s.Subtype != "" {
		return s.Subtype
	}
	return "provider reported an error"
}
