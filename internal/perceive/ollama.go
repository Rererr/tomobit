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
	// maxTaskChars caps the task description Task Perception reads (ADR-0036).
	// It sits well under maxSessionChars because a task description is one
	// person's request, not a transcript: the dogfood ledger's intents top out
	// at 1138 characters (measured 2026-07-21), so 4000 leaves room for a
	// pasted spec while still bounding a paste of a whole file.
	maxTaskChars = 4000
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
	return o.extract(eventsSection(events), vocab)
}

// ExtractTaskContext is ExtractContext's pre-execution counterpart
// (ADR-0036 Decision 2c): same schema, prompt, and vocabulary — only
// taskSection's text differs from eventsSection's.
func (o *Ollama) ExtractTaskContext(intent string, vocab map[string][]string) (map[string]string, error) {
	return o.extract(taskSection(intent), vocab)
}

// extract runs the request/response cycle shared by ExtractContext and
// ExtractTaskContext; userContent is the only thing that differs between a
// session's events and a task description.
func (o *Ollama) extract(userContent string, vocab map[string][]string) (map[string]string, error) {
	url := o.URL
	if url == "" {
		url = "http://localhost:11434"
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
			{"role": "system", "content": extractSystem + "\n\n" + vocabSection(vocab)},
			{"role": "user", "content": userContent},
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

// decisionRecordTypes are the harness's own internal record of what it
// decided (tomo.decided) or selected (plan.selected) — not Reality
// (PERCEPTION_ENGINE.md: Reality → Observation). eventsSection excludes
// them so a re-perceived session cannot read back its own prior guess and
// call that agreement (ADR-0036 Decision 2d). The ledger keeps them
// (parseDeterministic still reads plan.selected directly, unaffected by
// this map — it consumes the events slice, not eventsSection's rendering)
// so the audit trail is intact; only what the extraction prompt sees
// changes.
var decisionRecordTypes = map[string]bool{
	"tomo.decided":  true,
	"plan.selected": true,
}

// eventsSection renders a session's events for the prompt, within
// maxEventChars/maxSessionChars. Truncation is noted inline so the model
// (and anyone reading the prompt while debugging) can tell the digest is
// partial rather than assume the transcript ended there. decisionRecordTypes
// events are dropped before that accounting — their absence is a category
// exclusion, not a budget one, so it does not count toward the "omitted"
// note (which would otherwise wrongly suggest they were cut for space).
func eventsSection(events []*store.Event) string {
	var sb strings.Builder
	sb.WriteString("Session events:\n")
	total := 0
	omitted := 0
	for _, e := range events {
		if decisionRecordTypes[e.Type] {
			continue
		}
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

// taskSection renders a task description for the prompt — the pre-execution
// counterpart to eventsSection (ADR-0036 Decision 2c).
//
// It carries its own budget for the same reason eventsSection does. The
// dogfood ledger's task.started intents average 226 characters and top out at
// 1138 (measured 2026-07-21), so this cap never fires on a typed one-liner —
// but a chat turn can be a pasted block (ADR-0024: 複数行貼り付けはそのまま
// 1つの依頼になる), and the attributes being extracted (lang / framework /
// topic) are announced in the opening sentences, not the appendix. Cutting
// head-first keeps the part that decides the answer and drops the part that
// only costs tokens.
func taskSection(intent string) string {
	if len(intent) > maxTaskChars {
		intent = intent[:maxTaskChars] + "…"
	}
	return "Task description:\n" + intent
}

// vocabSection renders the known-vocabulary block both eventsSection and
// taskSection prompts embed (SCHEMA.md D5), so a value already in the ledger
// converges onto its existing spelling instead of drifting into a
// near-duplicate.
func vocabSection(vocab map[string][]string) string {
	var vb strings.Builder
	vb.WriteString("Known vocabulary:\n")
	for _, k := range SemanticKeys {
		vb.WriteString(fmt.Sprintf("- %s: %s\n", k, strings.Join(vocab[k], ", ")))
	}
	return vb.String()
}
