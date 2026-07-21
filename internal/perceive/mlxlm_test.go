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

func TestMLXExtractContextBuildsRequestAndParsesResponse(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path: got %q, want /v1/chat/completions", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Fatalf("request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"lang\":\"rust\",\"framework\":\"\",\"topic\":\"lifetime\",\"size\":\"small\"}"}}]}`)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "mlx-community/Qwen3-8B-4bit"}
	events := []*store.Event{ev("capability.started", map[string]any{"capability": "impl"})}
	vocab := map[string][]string{"lang": {"rust", "go"}, "framework": {}, "topic": {}, "size": {}}

	out, err := m.ExtractContext(events, vocab)
	if err != nil {
		t.Fatal(err)
	}
	if out["lang"] != "rust" || out["topic"] != "lifetime" || out["size"] != "small" {
		t.Errorf("parsed content mismatch: %v", out)
	}
	if len(out) != 4 {
		t.Errorf("result must carry exactly the 4 semantic keys, got %v", out)
	}

	if gotBody["model"] != "mlx-community/Qwen3-8B-4bit" {
		t.Errorf("model: got %v", gotBody["model"])
	}
	if gotBody["stream"] != false {
		t.Errorf("stream should be false, got %v", gotBody["stream"])
	}
	if gotBody["temperature"] != float64(0) {
		t.Errorf("temperature should be 0, got %v", gotBody["temperature"])
	}
	if gotBody["max_tokens"] != float64(512) {
		t.Errorf("max_tokens should be 512, got %v", gotBody["max_tokens"])
	}
	kwargs, ok := gotBody["chat_template_kwargs"].(map[string]any)
	if !ok || kwargs["enable_thinking"] != false {
		t.Errorf("chat_template_kwargs should disable thinking, got %v", gotBody["chat_template_kwargs"])
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
	if !strings.Contains(content, "JSON object") {
		t.Errorf("system prompt should carry the MLX shape block, got %q", content)
	}
}

func TestMLXExtractTaskContextSendsTheTaskDescriptionInsteadOfEvents(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path: got %q, want /v1/chat/completions", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Fatalf("request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"lang\":\"rust\",\"framework\":\"\",\"topic\":\"rate-limiting\",\"size\":\"medium\"}"}}]}`)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "mlx-community/Qwen3-8B-4bit"}
	vocab := map[string][]string{"lang": {"rust", "go"}, "framework": {}, "topic": {}, "size": {}}

	out, err := m.ExtractTaskContext("add rate limiting to the payment API in rust", vocab)
	if err != nil {
		t.Fatal(err)
	}
	if out["lang"] != "rust" || out["topic"] != "rate-limiting" || out["size"] != "medium" {
		t.Errorf("parsed content mismatch: %v", out)
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

	sys, _ := messages[0].(map[string]any)
	sysContent, _ := sys["content"].(string)
	if !strings.Contains(sysContent, "JSON object") {
		t.Errorf("system prompt should still carry the MLX shape block, got %q", sysContent)
	}
}

func TestMLXExtractContextStripsCodeFenceAroundTheObject(t *testing.T) {
	content := "```json\n{\"lang\":\"go\",\"framework\":\"\",\"topic\":\"worker-pool\",\"size\":\"medium\"}\n```"
	body, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": content}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	out, gotErr := m.ExtractContext(nil, map[string][]string{})
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	if out["lang"] != "go" || out["size"] != "medium" {
		t.Errorf("fenced JSON should still parse: %v", out)
	}
}

func TestMLXExtractContextStripsLeadingProseBeforeTheObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"Sure, here is the JSON: {\"lang\":\"go\",\"framework\":\"\",\"topic\":\"x\",\"size\":\"\"}"}}]}`)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	out, err := m.ExtractContext(nil, map[string][]string{})
	if err != nil {
		t.Fatal(err)
	}
	if out["lang"] != "go" {
		t.Errorf("leading prose should be skipped to reach the object: %v", out)
	}
}

func TestMLXExtractContextSkipsStrayBracesToReachTheRealObjectFurtherOn(t *testing.T) {
	content := `Braces like { this } are punctuation, but here is the real one: ` +
		`{"lang":"go","framework":"","topic":"worker-pool","size":"medium"}`
	body, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": content}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	out, gotErr := m.ExtractContext(nil, map[string][]string{})
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	if out["lang"] != "go" || out["size"] != "medium" {
		t.Errorf("a stray brace in prose must not stop the search for the real object: %v", out)
	}
}

func TestMLXExtractContextNormalizesSizeCaseInsteadOfErroring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"lang\":\"go\",\"framework\":\"\",\"topic\":\"x\",\"size\":\"Medium\"}"}}]}`)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	out, err := m.ExtractContext(nil, map[string][]string{})
	if err != nil {
		t.Fatalf("a case-different enum value must not error: %v", err)
	}
	if out["size"] != "medium" {
		t.Errorf("size must be normalized to lowercase, got %q", out["size"])
	}
}

func TestMLXExtractContextErrorsWhenContentKeyIsAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// mlx_lm.server omits "content" entirely when the text is empty.
		io.WriteString(w, `{"choices":[{"message":{"reasoning":"thinking..."}}]}`)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	_, err := m.ExtractContext(nil, map[string][]string{})
	if err == nil || !strings.Contains(err.Error(), "no content") {
		t.Errorf("expected a no-content error, got %v", err)
	}
}

func TestMLXExtractContextErrorsOnMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"lang\":\"go\",\"framework\":\"\",\"topic\":\"x\"}"}}]}`)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	_, err := m.ExtractContext(nil, map[string][]string{})
	if err == nil || !strings.Contains(err.Error(), `missing key "size"`) {
		t.Errorf("expected a missing-key error, got %v", err)
	}
}

func TestMLXExtractContextErrorsOnSizeOutsideEnum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"lang\":\"go\",\"framework\":\"\",\"topic\":\"x\",\"size\":\"huge\"}"}}]}`)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	_, err := m.ExtractContext(nil, map[string][]string{})
	if err == nil || !strings.Contains(err.Error(), "invalid value") {
		t.Errorf("expected a size-enum error, got %v", err)
	}
}

func TestMLXExtractContextErrorsOnNonJSONContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"not json at all"}}]}`)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	_, err := m.ExtractContext(nil, map[string][]string{})
	if err == nil || !strings.Contains(err.Error(), "no JSON object") {
		t.Errorf("expected a no-JSON-object error, got %v", err)
	}
}

func TestMLXExtractContextErrorsOnBraceThatNeverDecodesAsJSON(t *testing.T) {
	content := `{lang: "go", framework: "", topic: "x", size: ""}` // unquoted keys — invalid JSON
	body, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": content}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	_, gotErr := m.ExtractContext(nil, map[string][]string{})
	if gotErr == nil || !strings.Contains(gotErr.Error(), "non-JSON") {
		t.Errorf("a brace that never decodes as JSON must error as non-JSON content, got %v", gotErr)
	}
}

// TestMLXExtractContextRecoversWhenAnEarlierObjectFailsShapeValidation pins
// the bug this fix closes: a wrong-shaped JSON object earlier in content
// (a leaked note, an echoed `{}`) must not shadow the real answer that
// follows it, the way stopping at the first *decodable* object used to.
func TestMLXExtractContextRecoversWhenAnEarlierObjectFailsShapeValidation(t *testing.T) {
	cases := map[string]string{
		"empty object before the answer": `{}` +
			` {"lang":"go","framework":"","topic":"worker-pool","size":"medium"}`,
		"partial-key aside before the answer": `Note: {"considering":"axum"} ` +
			`the real classification is {"lang":"go","framework":"","topic":"worker-pool","size":"medium"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": content}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(body)
			}))
			defer srv.Close()

			m := &MLXLM{URL: srv.URL, Model: "m"}
			out, gotErr := m.ExtractContext(nil, map[string][]string{})
			if gotErr != nil {
				t.Fatalf("a shape-invalid object earlier in content must not block the real one: %v", gotErr)
			}
			if out["lang"] != "go" || out["topic"] != "worker-pool" || out["size"] != "medium" {
				t.Errorf("expected the later valid object to be adopted: %v", out)
			}
		})
	}
}

// TestMLXExtractContextAdoptsTheFirstOfSeveralFullyValidObjects pins the
// tie-break policy: when multiple objects in content each independently
// satisfy the shape (a deviation from mlxShapeBlock's "one object" rule),
// the earliest one wins rather than the last.
func TestMLXExtractContextAdoptsTheFirstOfSeveralFullyValidObjects(t *testing.T) {
	content := `{"lang":"go","framework":"","topic":"draft","size":"small"} ` +
		`{"lang":"rust","framework":"","topic":"final","size":"large"}`
	body, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": content}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	out, gotErr := m.ExtractContext(nil, map[string][]string{})
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	if out["topic"] != "draft" || out["lang"] != "go" {
		t.Errorf("expected the first fully valid object to win, got %v", out)
	}
}

// TestMLXExtractContextErrorsUsingTheFirstCandidatesReasonWhenNoneValidate
// pins the error-reporting side of the same policy: with several
// shape-invalid candidates and no valid one, the message names the earliest
// candidate's defect, not a later one's.
func TestMLXExtractContextErrorsUsingTheFirstCandidatesReasonWhenNoneValidate(t *testing.T) {
	content := `{"lang":"go","framework":"","topic":"x"} ` + // missing "size"
		`{"lang":"go","framework":"","topic":"x","size":"huge"}` // size outside enum
	body, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": content}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	_, gotErr := m.ExtractContext(nil, map[string][]string{})
	if gotErr == nil || !strings.Contains(gotErr.Error(), `missing key "size"`) {
		t.Errorf("expected the first candidate's missing-key reason, got %v", gotErr)
	}
}

func TestMLXExtractContextErrorsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := &MLXLM{URL: srv.URL, Model: "m"}
	_, err := m.ExtractContext(nil, map[string][]string{})
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("expected a status error, got %v", err)
	}
}
