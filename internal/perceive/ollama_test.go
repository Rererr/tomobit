package perceive

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/store"
)

func TestExtractContextBuildsRequestAndParsesResponse(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path: got %q, want /api/chat", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Fatalf("request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"message":{"content":"{\"lang\":\"rust\",\"framework\":\"\",\"topic\":\"lifetime\",\"size\":\"small\"}"}}`)
	}))
	defer srv.Close()

	o := &Ollama{URL: srv.URL, Model: "qwen3:8b"}
	events := []*store.Event{ev("capability.started", map[string]any{"capability": "impl"})}
	vocab := map[string][]string{"lang": {"rust", "go"}, "framework": {}, "topic": {}, "size": {}}

	out, err := o.ExtractContext(events, vocab)
	if err != nil {
		t.Fatal(err)
	}
	if out["lang"] != "rust" || out["topic"] != "lifetime" || out["size"] != "small" {
		t.Errorf("parsed content mismatch: %v", out)
	}

	if gotBody["model"] != "qwen3:8b" {
		t.Errorf("model: got %v", gotBody["model"])
	}
	if gotBody["stream"] != false {
		t.Errorf("stream should be false, got %v", gotBody["stream"])
	}
	format, ok := gotBody["format"].(map[string]any)
	if !ok {
		t.Fatalf("format schema missing: %v", gotBody["format"])
	}
	props, _ := format["properties"].(map[string]any)
	for _, k := range SemanticKeys {
		if _, ok := props[k]; !ok {
			t.Errorf("format schema missing property %q", k)
		}
	}
	options, _ := gotBody["options"].(map[string]any)
	if options["temperature"] != float64(0) {
		t.Errorf("temperature should be 0, got %v", options["temperature"])
	}
	messages, _ := gotBody["messages"].([]any)
	if len(messages) < 1 {
		t.Fatal("expected at least a system message")
	}
	sys, _ := messages[0].(map[string]any)
	if sys["role"] != "system" {
		t.Errorf("first message role: got %v", sys["role"])
	}
	content, _ := sys["content"].(string)
	if !strings.Contains(content, "rust") || !strings.Contains(content, "Known vocabulary") {
		t.Errorf("system prompt should embed the vocabulary, got %q", content)
	}
}

func TestExtractTaskContextSendsTheTaskDescriptionInsteadOfEvents(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path: got %q, want /api/chat", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Fatalf("request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"message":{"content":"{\"lang\":\"rust\",\"framework\":\"\",\"topic\":\"rate-limiting\",\"size\":\"medium\"}"}}`)
	}))
	defer srv.Close()

	o := &Ollama{URL: srv.URL, Model: "qwen3:8b"}
	vocab := map[string][]string{"lang": {"rust", "go"}, "framework": {}, "topic": {}, "size": {}}

	out, err := o.ExtractTaskContext("add rate limiting to the payment API in rust", vocab)
	if err != nil {
		t.Fatal(err)
	}
	if out["lang"] != "rust" || out["topic"] != "rate-limiting" || out["size"] != "medium" {
		t.Errorf("parsed content mismatch: %v", out)
	}

	format, ok := gotBody["format"].(map[string]any)
	if !ok {
		t.Fatalf("format schema missing: %v", gotBody["format"])
	}
	props, _ := format["properties"].(map[string]any)
	for _, k := range SemanticKeys {
		if _, ok := props[k]; !ok {
			t.Errorf("format schema missing property %q", k)
		}
	}

	messages, _ := gotBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("expected a system and a user message, got %d", len(messages))
	}
	user, _ := messages[1].(map[string]any)
	content, _ := user["content"].(string)
	if !strings.Contains(content, "Task description:") {
		t.Errorf("user message should carry a task description, got %q", content)
	}
	if !strings.Contains(content, "add rate limiting to the payment API in rust") {
		t.Errorf("user message should carry the intent verbatim, got %q", content)
	}
	if strings.Contains(content, "Session events:") {
		t.Errorf("task perception must not use the session-events framing, got %q", content)
	}
}

func TestExtractContextErrorsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	o := &Ollama{URL: srv.URL, Model: "qwen3:8b"}
	_, err := o.ExtractContext(nil, map[string][]string{})
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("expected a status error, got %v", err)
	}
}

func TestEventsSectionTruncatesAnOversizedSingleEvent(t *testing.T) {
	huge := strings.Repeat("a", maxEventChars*2)
	events := []*store.Event{ev("provider.output", map[string]any{"text": huge})}

	got := eventsSection(events)
	if len(got) >= len(huge) {
		t.Errorf("an oversized event should be truncated, got %d chars", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncation of a single event should be noted in the prompt: %q", got)
	}
}

func TestEventsSectionOmitsEventsBeyondTheSessionBudget(t *testing.T) {
	var events []*store.Event
	for i := 0; i < 50; i++ {
		events = append(events, ev("provider.output", map[string]any{"text": strings.Repeat("x", maxEventChars-100)}))
	}

	got := eventsSection(events)
	if len(got) > maxSessionChars+500 { // headroom for the trailing omission note
		t.Errorf("session digest should stay near the budget, got %d chars", len(got))
	}
	if !strings.Contains(got, "omitted") {
		t.Errorf("omitting events should be noted in the prompt: tail=%q", got[len(got)-200:])
	}
}

// TestEventsSectionExcludesTheHarnessOwnDecisionRecords pins ADR-0036
// Decision 2d: tomo.decided/plan.selected are the harness's own prior guess,
// not Reality, so a re-perceived session must not be able to read them back
// and call that agreement.
func TestEventsSectionExcludesTheHarnessOwnDecisionRecords(t *testing.T) {
	events := []*store.Event{
		ev("task.started", map[string]any{"source": "production"}),
		ev("plan.selected", map[string]any{"plan": "direct"}),
		ev("tomo.decided", map[string]any{"provider": "claude"}),
		ev("task.finished", map[string]any{"adopted": "as-is"}),
	}
	got := eventsSection(events)
	if strings.Contains(got, "plan.selected") || strings.Contains(got, "tomo.decided") {
		t.Errorf("decision records must not reach the extraction prompt: %q", got)
	}
	if !strings.Contains(got, "task.started") || !strings.Contains(got, "task.finished") {
		t.Errorf("real events must still reach the prompt: %q", got)
	}
	if strings.Contains(got, "omitted") {
		t.Errorf("excluding decision records is a category filter, not budget truncation, so it must not claim events were omitted: %q", got)
	}
}

func TestEventsSectionKeepsSmallSessionsIntact(t *testing.T) {
	events := []*store.Event{
		ev("task.started", map[string]any{"intent": "fix the bug"}),
		ev("task.finished", map[string]any{"adopted": "as-is"}),
	}
	got := eventsSection(events)
	if strings.Contains(got, "truncated") || strings.Contains(got, "omitted") {
		t.Errorf("a session well under budget should not be marked as truncated: %q", got)
	}
	if !strings.Contains(got, "task.started") || !strings.Contains(got, "task.finished") {
		t.Errorf("both events should be present verbatim: %q", got)
	}
}

func TestExtractContextErrorsOnNonJSONContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"message":{"content":"not json at all"}}`)
	}))
	defer srv.Close()

	o := &Ollama{URL: srv.URL, Model: "qwen3:8b"}
	_, err := o.ExtractContext(nil, map[string][]string{})
	if err == nil || !strings.Contains(err.Error(), "non-JSON") {
		t.Errorf("expected a non-JSON content error, got %v", err)
	}
}

// A task description carries its own prompt budget (ADR-0036): a chat turn can
// be a pasted block, and the attributes being extracted are announced in the
// opening sentences. The cut is head-first, so what decides the answer stays.
func TestTaskSectionBoundsAPastedTaskDescription(t *testing.T) {
	head := "rust axum handler: fix the lifetime error"
	intent := head + strings.Repeat("x", maxTaskChars*2)
	got := taskSection(intent)
	if len(got) > maxTaskChars+len("Task description:\n")+len("…") {
		t.Errorf("taskSection kept %d chars, want it bounded at %d", len(got), maxTaskChars)
	}
	if !strings.Contains(got, head) {
		t.Error("the head of the description must survive the cut — that is where the attributes are named")
	}
	short := "just a line"
	if want := "Task description:\n" + short; taskSection(short) != want {
		t.Errorf("a short description must pass through untouched, got %q", taskSection(short))
	}
}
