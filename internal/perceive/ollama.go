package perceive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Rererr/tomobit/internal/store"
)

// Ollama extracts semantic context attributes with a local model.
//
// ADR-0005: the JSON schema passed via `format` guarantees SHAPE only —
// its descriptions never reach the model (dropped in GBNF conversion).
// All field SEMANTICS therefore live in the system prompt below. When
// adding a field, update BOTH, and bump extractor_ver.
type Ollama struct {
	URL   string // default http://localhost:11434
	Model string // e.g. "qwen3:8b"
}

func (o *Ollama) Name() string { return o.Model }

// Digest limits for the prompt (truth stays untruncated — ADR-0006: events
// keep the full assistant text, only the perception prompt is bounded).
const (
	// maxEventChars caps one event's marshalled payload. A single
	// provider.output can carry a full assistant turn with no upper bound at
	// the truth layer; without a per-event cap, one long turn could alone
	// consume the entire session budget below.
	maxEventChars = 2000
	// maxSessionChars caps the whole "Session events" block handed to the
	// model. qwen3:8b's context window is not the constraint here — latency
	// is (ADR-0004: ~2.4s/38 tok/s measured baseline). The four fields
	// extracted (lang/framework/topic/size) are decided by a handful of
	// lines, not the full transcript, so bounding the prompt trades away
	// transcript detail the extractor was not using anyway.
	maxSessionChars = 12000
)

// format: shape only (ADR-0005 Decision 2).
var extractSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"lang":      map[string]any{"type": "string"},
		"framework": map[string]any{"type": "string"},
		"topic":     map[string]any{"type": "string"},
		"size":      map[string]any{"type": "string", "enum": []string{"", "small", "medium", "large"}},
	},
	"required": []string{"lang", "framework", "topic", "size"},
}

// system prompt: semantics live here, never in the schema (ADR-0005).
const extractSystem = `You classify a coding task session for a knowledge base. Return JSON only.

Field semantics:
- "lang": the programming language of the code being worked on (e.g. "rust", "go", "typescript"). NOT the natural language of the conversation. Empty string if no code involved.
- "framework": the main library/framework involved (e.g. "axum", "react"). Must never equal the value of "lang" — a programming language is not a framework. Empty string when the task only uses the language and its standard library.
- "topic": the single most defining technical theme of the task (e.g. "lifetime", "concurrency", "auth"). One short lowercase word or hyphenated phrase. Empty string if nothing stands out.
- "size": rough task size. "small" = one-file tweak, "medium" = multi-file change, "large" = cross-cutting work. Empty string if unclear.

Rules:
- lowercase everything.
- Prefer a value from the known vocabulary below when it means the same thing; only invent a new value when nothing matches.
- Do not guess: when unsure, return "".`

func (o *Ollama) ExtractContext(events []*store.Event, vocab map[string][]string) (map[string]string, error) {
	url := o.URL
	if url == "" {
		url = "http://localhost:11434"
	}

	var vb strings.Builder
	vb.WriteString("Known vocabulary:\n")
	for _, k := range SemanticKeys {
		vb.WriteString(fmt.Sprintf("- %s: %s\n", k, strings.Join(vocab[k], ", ")))
	}

	body, err := json.Marshal(map[string]any{
		"model":  o.Model,
		"stream": false,
		"think":  false,
		"format": extractSchema,
		"options": map[string]any{
			"temperature": 0,
		},
		"messages": []map[string]string{
			{"role": "system", "content": extractSystem + "\n\n" + vb.String()},
			{"role": "user", "content": eventsSection(events)},
		},
	})
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Post(url+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: status %s", resp.Status)
	}

	var chat struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(chat.Message.Content), &out); err != nil {
		return nil, fmt.Errorf("ollama: model returned non-JSON content: %w", err)
	}
	return out, nil
}

// eventsSection renders a session's events for the prompt, within
// maxEventChars/maxSessionChars. Truncation is noted inline so the model
// (and anyone reading the prompt while debugging) can tell the digest is
// partial rather than assume the transcript ended there.
func eventsSection(events []*store.Event) string {
	var sb strings.Builder
	sb.WriteString("Session events:\n")
	total := 0
	omitted := 0
	for _, e := range events {
		p, _ := json.Marshal(e.Payload)
		line := fmt.Sprintf("%s %s\n", e.Type, p)
		if len(line) > maxEventChars {
			line = line[:maxEventChars] + "...[event truncated]\n"
		}
		if total+len(line) > maxSessionChars {
			omitted++
			continue
		}
		total += len(line)
		sb.WriteString(line)
	}
	if omitted > 0 {
		fmt.Fprintf(&sb, "...[%d further event(s) omitted: session digest limit reached]\n", omitted)
	}
	return sb.String()
}
