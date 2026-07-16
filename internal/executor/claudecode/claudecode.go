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
func (a *Adapter) Command(req executor.Request) (string, []string, []string) {
	args := []string{"-p", req.Prompt, "--output-format", "stream-json", "--verbose"}
	if req.PermissionMode != "" {
		args = append(args, "--permission-mode", req.PermissionMode)
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

	// assistant
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`

	// result
	DurationMs   *int64   `json:"duration_ms"`
	TotalCostUSD *float64 `json:"total_cost_usd"`
	NumTurns     *int64   `json:"num_turns"`
	IsError      bool     `json:"is_error"`
	Result       string   `json:"result"`
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
		return []executor.Event{{
			Type:    executor.EventProviderSelected,
			Payload: map[string]any{"provider": providerName, "model": s.Model},
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
				// Only the tool name: tool inputs and results are the digest we
				// deliberately drop (ADR-0006 Decision 3).
				out = append(out, executor.Event{
					Type:    executor.EventProviderOutput,
					Payload: map[string]any{"tool": c.Name},
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
		if s.IsError || strings.HasPrefix(s.Subtype, "error") {
			out = append(out, executor.Event{
				Type:    executor.EventProviderError,
				Payload: map[string]any{"message": errorMessage(s)},
			})
		}
		return out, nil

	default:
		// user (tool_result) and any unknown type: dropped, not an error, so a
		// new stream type never breaks a run (forward compatible). The Executor
		// surfaces dropped lines under Debug.
		return nil, nil
	}
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
