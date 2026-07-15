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

	var sb strings.Builder
	sb.WriteString("Session events:\n")
	for _, e := range events {
		p, _ := json.Marshal(e.Payload)
		sb.WriteString(fmt.Sprintf("%s %s\n", e.Type, p))
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
			{"role": "user", "content": sb.String()},
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
