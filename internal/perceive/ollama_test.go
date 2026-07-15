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
