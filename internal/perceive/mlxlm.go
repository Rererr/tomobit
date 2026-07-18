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

// MLXLM extracts semantic context attributes via mlx_lm.server's OpenAI-
// compatible endpoint (ADR-0029). mlx-lm has no structured output
// (`response_format`/`json_schema` are unimplemented, confirmed by reading
// mlx_lm/server.py), so the shape guarantee Ollama gets from `format` here
// falls to mlxShapeBlock in the prompt plus parseMLXContent's validation
// below (ADR-0029 Decision 2). Field SEMANTICS stay shared with Ollama via
// extractSystem (ADR-0005) — only the shape instruction differs per backend.
type MLXLM struct {
	URL   string // default http://localhost:8080
	Model string // HF repo id, e.g. "mlx-community/Qwen3-8B-4bit"
}

func (m *MLXLM) Name() string { return m.Model }

// mlxShapeBlock spells out the shape Ollama's `format` schema would
// otherwise guarantee. Deliberately no fully valid example JSON: an example
// that already satisfies the schema risks being echoed back verbatim
// regardless of the actual session content (a known failure mode of
// prompted-JSON extraction), so the shape is described, never demonstrated.
const mlxShapeBlock = `

Output format: respond with a single JSON object and nothing else — no code fence, no preamble, no explanation. The object has exactly these 4 keys, all strings: "lang", "framework", "topic", "size". "size" must be one of "", "small", "medium", or "large".`

func (m *MLXLM) ExtractContext(events []*store.Event, vocab map[string][]string) (map[string]string, error) {
	url := m.URL
	if url == "" {
		url = "http://localhost:8080"
	}

	var vb strings.Builder
	vb.WriteString("Known vocabulary:\n")
	for _, k := range SemanticKeys {
		vb.WriteString(fmt.Sprintf("- %s: %s\n", k, strings.Join(vocab[k], ", ")))
	}

	body, err := json.Marshal(map[string]any{
		"model":                m.Model,
		"stream":               false,
		"temperature":          0,
		"max_tokens":           512,
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
		"messages": []map[string]string{
			{"role": "system", "content": extractSystem + mlxShapeBlock + "\n\n" + vb.String()},
			{"role": "user", "content": eventsSection(events)},
		},
	})
	if err != nil {
		return nil, err
	}

	// 120s matches Ollama's timeout. An uncached model's first request also
	// triggers an on-demand download from Hugging Face, which can exceed
	// this — but perception is deferred and replayable (ADR-0004, ADR-0029
	// Decision 2), so a slow first call just leaves the session pending for
	// the next `tomobit perceive` instead of needing a bespoke longer wait.
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Post(url+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mlx-lm: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mlx-lm: status %s", resp.Status)
	}

	var chat struct {
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		return nil, fmt.Errorf("mlx-lm: decode response: %w", err)
	}
	if len(chat.Choices) == 0 {
		return nil, fmt.Errorf("mlx-lm: response has no choices")
	}
	// mlx_lm.server omits the "content" key entirely when the text is empty
	// (confirmed in mlx_lm/server.py) — a nil pointer here means the model
	// produced nothing, not a spurious empty string to swallow.
	content := chat.Choices[0].Message.Content
	if content == nil {
		return nil, fmt.Errorf("mlx-lm: response has no content (model produced no text)")
	}

	out, err := parseMLXContent(*content)
	if err != nil {
		return nil, fmt.Errorf("mlx-lm: %w", err)
	}
	return out, nil
}

// parseMLXContent extracts the JSON object mlxShapeBlock asked for. Without
// structured output, the response may carry a code fence or leading prose
// around the object — and prose can itself contain stray braces ("braces
// like { this } are punctuation") that are not the object at all. So every
// '{' in content is tried in order, decoding straight into
// map[string]string: a non-object, a syntax error, or a non-string field
// value all fail that attempt (the last case skips a JSON-valid-but-wrong-
// shape object like {"a":1} for free) and move on to the next '{'; the first
// attempt that decodes cleanly is adopted. Field validation (4 keys, size
// enum) then runs once, on the adopted object — an invalid shape is an
// error, never coerced to "" — because a session left pending (Deferred
// Perception) is safer than an experience recorded with a silently wrong
// shape (ADR-0005: 射影は静かに歪む).
func parseMLXContent(content string) (map[string]string, error) {
	rest := content
	sawBrace := false
	var raw map[string]string
	for {
		idx := strings.IndexByte(rest, '{')
		if idx < 0 {
			break
		}
		sawBrace = true
		var candidate map[string]string
		dec := json.NewDecoder(strings.NewReader(rest[idx:]))
		if err := dec.Decode(&candidate); err == nil {
			raw = candidate
			break
		}
		rest = rest[idx+1:]
	}
	if !sawBrace {
		return nil, fmt.Errorf("model returned no JSON object: %q", content)
	}
	if raw == nil {
		return nil, fmt.Errorf("model returned non-JSON content: %q", content)
	}

	out := make(map[string]string, len(SemanticKeys))
	for _, k := range SemanticKeys {
		v, ok := raw[k]
		if !ok {
			return nil, fmt.Errorf("model response missing key %q: %q", k, content)
		}
		out[k] = v
	}
	// Case is not semantic ambiguity (unlike an out-of-enum value), so it is
	// normalized rather than sent to Deferred Perception over a spelling.
	size := strings.ToLower(out["size"])
	switch size {
	case "", "small", "medium", "large":
	default:
		return nil, fmt.Errorf("model response key \"size\" has an invalid value: %q", out["size"])
	}
	out["size"] = size
	return out, nil
}
