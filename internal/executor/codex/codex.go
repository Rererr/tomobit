// Package codex adapts the Codex CLI (`codex exec --json`) to the executor's
// canonical events (ADR-0010 Decision 3). Translate is a pure per-line
// function; the fixtures in the test pin every mapping.
package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rererr/tomobit/internal/executor"
)

// providerName is the canonical target recorded for this provider (SCHEMA.md
// R3): the tool name only, never a model version.
const providerName = "codex"

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Name() string { return providerName }

// Command builds the headless launch. The prompt stays the trailing
// positional argument regardless of --sandbox, so flag placement never
// depends on whether PermissionMode was set.
//
// --ephemeral is deliberately not used (ADR-0010 Decision 2): keeping the
// thread means provider_session_id (thread_id) still points at Codex's own
// session log, the original-reference pattern claude-code already uses
// (ADR-0006 Decision 3). ADR-0022 turns that kept thread into the chat's
// continuity: `exec resume <id> <prompt>`.
//
// A resumed turn carries no --sandbox: measured on codex 0.144.4, `exec
// resume` has no such flag — the thread keeps the sandbox it was started
// with. Dropping it is the truthful mapping, not a silent downgrade.
//
// 作業場所の外で扱わせる場所 (ADR-0047 Decision 3) は codex にも --add-dir が
// ある（"Additional directories that should be writable alongside the primary
// workspace"）。claude 側とは権限の細部が違うが「主ワークスペースの外もこの
// 場所なら扱ってよい」という同じ意図の口なので、同じ Request フィールドの
// 翻訳先として使う。--sandbox と同じく `exec resume` にはこのフラグが無いため
// 再開ターンでは積まない — スレッドは開始時の設定のまま続く。
// 働く場所そのもの（WorkDir）に codex の -C/--cd は使わない: cwd は Executor
// が全 Provider 共通で落とす（Decision 2）。
// sandboxFlag translates tomobit's three words into codex's own (ADR-0053
// Decision 1). Measured 2026-07-27 on codex 0.145.0: --sandbox takes
// read-only / workspace-write / danger-full-access.
//
// **これは同型ではない。** claude の permission は「訊く」機構で、codex の
// sandbox は「囲う」機構である。auto → workspace-write は「普通に働けて、外に
// 出るときだけ止まる」という**意図**の対応であって、挙動の等価ではない。
// その結果 codex には許可の問いが立たない（訊く相手がそもそも居ない）—
// Provider によって体験が違うことは ADR-0053 Consequences に明記してある。
//
// 未知の値は auto 相当へ落とす（claude 側と同じく、迷ったら開かない方へ）。
func sandboxFlag(p executor.Permission) string {
	switch p.Resolve() {
	case executor.PermissionStrict:
		return "read-only"
	case executor.PermissionOpen:
		return "danger-full-access"
	default:
		return "workspace-write"
	}
}

// AllowedTools は訳さない: codex の sandbox は道具ごとの許可を持たないので、
// 対応するフラグが存在しない。ADR-0047 が --add-dir の無い CLI について書いた
// 「無いものを発明せず無視する」と同じ扱い。

func (a *Adapter) Command(req executor.Request) (string, []string, []string) {
	if req.ResumeID != "" {
		return "codex", []string{"exec", "resume", req.ResumeID, req.Prompt,
			"--json", "--skip-git-repo-check"}, nil
	}
	args := []string{"exec", "--json", "--skip-git-repo-check"}
	args = append(args, "--sandbox", sandboxFlag(req.PermissionMode))
	// プロンプトは末尾の位置引数のまま保つので、可変長の --add-dir はその前に
	// 置き、1ディレクトリにつき1回積む（まとめると prompt を飲み込む）。
	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}
	args = append(args, req.Prompt)
	return "codex", args, nil
}

// streamLine is the union of the codex exec --json envelopes this adapter
// reads. Fields absent for a given type stay at their zero value.
type streamLine struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`

	// item.completed
	Item item `json:"item"`

	// turn.failed
	Error struct {
		Message string `json:"message"`
	} `json:"error"`

	// turn.completed
	Usage struct {
		InputTokens       int64 `json:"input_tokens"`
		CachedInputTokens int64 `json:"cached_input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
	} `json:"usage"`
}

// item is one item.completed's item. Text/Message drive the events; the
// remaining fields (command, query, changes) never enter the payload — like
// claude-code's tool inputs they are the digest SCHEMA.md R3 drops, read only
// to derive the view-only detail (ADR-0024 Decision 6).
type item struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Message string `json:"message"`

	Command string `json:"command"`
	Query   string `json:"query"`
	Changes []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
}

func (a *Adapter) Translate(line []byte) ([]executor.Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	var s streamLine
	if err := json.Unmarshal(line, &s); err != nil {
		return nil, fmt.Errorf("codex: parse stream line: %w", err)
	}

	switch s.Type {
	case "thread.started":
		return []executor.Event{{
			Type: executor.EventProviderSelected,
			// model stays empty: codex's JSONL never echoes it, and the
			// ~/.codex/config.toml choice isn't observable from the stream
			// (ADR-0010 Decision 2) — an empty field beats a guess.
			Payload: map[string]any{
				"provider": providerName, "model": "",
				"provider_session_id": s.ThreadID,
			},
		}}, nil

	case "item.completed":
		return translateItem(s.Item), nil

	case "turn.completed":
		return []executor.Event{{
			Type: executor.EventProviderFinished,
			Payload: map[string]any{
				"input_tokens":        s.Usage.InputTokens,
				"cached_input_tokens": s.Usage.CachedInputTokens,
				"output_tokens":       s.Usage.OutputTokens,
			},
		}}, nil

	case "turn.failed":
		return []executor.Event{{
			Type:    executor.EventProviderError,
			Payload: map[string]any{"message": s.Error.Message},
		}}, nil

	default:
		// turn.started, item.started/item.updated, the top-level "error" (a
		// same-text duplicate of turn.failed in every stream captured so
		// far — Translate is a per-line pure function, so dedup can only
		// drop one side unconditionally, not compare across lines), and any
		// future type: dropped, not an error, so a new stream type never
		// breaks a run. The Executor surfaces dropped lines under Debug.
		return nil, nil
	}
}

// translateItem maps one item.completed's item to zero or one events.
func translateItem(it item) []executor.Event {
	switch it.Type {
	case "agent_message":
		if it.Text == "" {
			return nil
		}
		return []executor.Event{{
			Type:    executor.EventProviderOutput,
			Payload: map[string]any{"text": it.Text},
		}}
	case "command_execution", "file_change", "mcp_tool_call", "web_search":
		// The ledger keeps only the tool name (SCHEMA.md R3); a summary of
		// what it did rides along as the view-only detail (ADR-0024 Decision
		// 6), symmetric with claude-code's tool_use.
		p := map[string]any{"tool": it.Type}
		if d := itemDetail(it); d != "" {
			p[executor.PayloadDetail] = d
		}
		return []executor.Event{{Type: executor.EventProviderOutput, Payload: p}}
	case "error":
		return []executor.Event{{
			Type:    executor.EventProviderError,
			Payload: map[string]any{"message": it.Message},
		}}
	default:
		// reasoning / todo_list dropped here too: same digest policy as
		// claude-code's thinking block (ADR-0006 Decision 3) — not an
		// artifact the user judges for adoption.
		return nil
	}
}

// itemDetail derives the view-only summary from a tool item, or "" when the
// item carries nothing worth showing (e.g. an mcp_tool_call, whose fields the
// stream does not name in a stable way, is left summary-less).
func itemDetail(it item) string {
	switch it.Type {
	case "command_execution":
		// Only the first line: the rest is the command's body, and the view
		// has one line — same rule as claude-code's Bash command.
		first, _, _ := strings.Cut(it.Command, "\n")
		return executor.TruncateDetail(first, false)
	case "file_change":
		if len(it.Changes) > 0 {
			return executor.TruncateDetail(it.Changes[0].Path, true)
		}
	case "web_search":
		return executor.TruncateDetail(it.Query, false)
	}
	return ""
}
