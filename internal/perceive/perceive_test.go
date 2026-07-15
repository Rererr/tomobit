package perceive

import (
	"path/filepath"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

func ev(typ string, payload map[string]any) *store.Event {
	return &store.Event{Type: typ, Payload: payload}
}

func TestParseDeterministic(t *testing.T) {
	events := []*store.Event{
		ev("task.started", map[string]any{"source": "Learning"}),
		ev("capability.started", map[string]any{"capability": " Impl "}),
		ev("provider.selected", map[string]any{"provider": "Claude", "model": "Opus"}),
		ev("test.result", map[string]any{"passed": true}),
		ev("user.verdict", map[string]any{"verdict": "Up"}),
		ev("user.preference", map[string]any{"preferred": "React", "over": "Vue"}),
		ev("user.preference", map[string]any{"preferred": "Axum", "over": "Actix"}),
		ev("task.finished", map[string]any{"adopted": "As-Is", "reverted": false}),
	}
	d := parseDeterministic(events)

	if d.source != "learning" {
		t.Errorf("source: got %q, want learning", d.source)
	}
	if d.capability != "impl" {
		t.Errorf("capability lowercased/trimmed: got %q", d.capability)
	}
	if d.provider != "claude" || d.model != "opus" {
		t.Errorf("provider/model: got %q/%q", d.provider, d.model)
	}
	if d.outcome.TestsPassed == nil || !*d.outcome.TestsPassed {
		t.Errorf("tests passed: got %+v", d.outcome.TestsPassed)
	}
	if d.outcome.Verdict != "up" {
		t.Errorf("verdict: got %q", d.outcome.Verdict)
	}
	if d.outcome.Adopted != "as-is" {
		t.Errorf("adopted: got %q", d.outcome.Adopted)
	}
	if len(d.preferences) != 2 {
		t.Fatalf("expected 2 preferences, got %d", len(d.preferences))
	}
	if d.preferences[0].Preferred != "react" || d.preferences[0].Over != "vue" {
		t.Errorf("preference[0]: got %+v", d.preferences[0])
	}
	if d.preferences[1].Preferred != "axum" || d.preferences[1].Over != "actix" {
		t.Errorf("preference[1]: got %+v", d.preferences[1])
	}
}

func TestParseDeterministicTestsFailedAndCancelled(t *testing.T) {
	d := parseDeterministic([]*store.Event{
		ev("test.result", map[string]any{"passed": false}),
		ev("task.cancelled", map[string]any{}),
	})
	if d.outcome.TestsPassed == nil || *d.outcome.TestsPassed {
		t.Errorf("tests failed: got %+v", d.outcome.TestsPassed)
	}
	if !d.outcome.Cancelled {
		t.Error("cancelled should be set")
	}
	if d.source != "production" {
		t.Errorf("default source: got %q, want production", d.source)
	}
}

func TestParseDeterministicRevertedFinish(t *testing.T) {
	d := parseDeterministic([]*store.Event{
		ev("task.finished", map[string]any{"adopted": "with-edits", "reverted": true}),
	})
	if d.outcome.Adopted != "with-edits" || !d.outcome.Reverted {
		t.Errorf("got %+v", d.outcome)
	}
}

// fakeExtractor returns a fixed semantic map and records the vocabulary it
// was handed, so tests can assert the prompt input.
type fakeExtractor struct {
	semantic  map[string]string
	lastVocab map[string][]string
}

func (f *fakeExtractor) ExtractContext(events []*store.Event, vocab map[string][]string) (map[string]string, error) {
	f.lastVocab = vocab
	return f.semantic, nil
}
func (f *fakeExtractor) Name() string { return "fake-extractor" }

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func finishedSession(t *testing.T, s *store.Store, sid string) {
	t.Helper()
	appends := []struct {
		typ     string
		payload map[string]any
	}{
		{"task.started", map[string]any{"source": "production"}},
		{"capability.started", map[string]any{"capability": "impl"}},
		{"provider.selected", map[string]any{"provider": "claude", "model": "opus"}},
		{"test.result", map[string]any{"passed": true}},
		{"user.preference", map[string]any{"preferred": "react", "over": "vue"}},
		{"task.finished", map[string]any{"adopted": "as-is"}},
	}
	for i, a := range appends {
		if err := s.AppendEvent(sid, a.typ, int64(1000+i), a.payload); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunProducesExecutionAndPreferenceExperiences(t *testing.T) {
	s := openStore(t)
	finishedSession(t, s, "sess")
	ext := &fakeExtractor{semantic: map[string]string{"lang": "Rust", "topic": "", "framework": "axum"}}
	p := &Perceiver{Store: s, Extractor: ext, Ver: 1}

	made, err := p.Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 2 {
		t.Fatalf("expected execution + one preference experience, got %d", len(made))
	}

	cur, _ := s.CurrentExperiences()
	var exec, pref *core.Experience
	for _, e := range cur {
		switch e.Kind {
		case core.KindExecution:
			exec = e
		case core.KindPreference:
			pref = e
		}
	}
	if exec == nil || pref == nil {
		t.Fatalf("missing experiences: exec=%v pref=%v", exec, pref)
	}

	if exec.Context["lang"] != "rust" {
		t.Errorf("semantic lang should be lowercased: %v", exec.Context)
	}
	if _, ok := exec.Context["topic"]; ok {
		t.Errorf("empty semantic value should be dropped: %v", exec.Context)
	}
	if exec.Context["framework"] != "axum" {
		t.Errorf("framework: %v", exec.Context)
	}
	if exec.Context["cap"] != "impl" || exec.Context["model"] != "opus" {
		t.Errorf("deterministic cap/model missing: %v", exec.Context)
	}
	if exec.Provider != "claude" {
		t.Errorf("provider: got %q", exec.Provider)
	}
	if exec.Outcome.Adopted != "as-is" || exec.Outcome.TestsPassed == nil {
		t.Errorf("outcome: %+v", exec.Outcome)
	}
	if exec.Source != "production" {
		t.Errorf("execution source: got %q", exec.Source)
	}

	if pref.Source != "learning" {
		t.Errorf("preference source: got %q, want learning", pref.Source)
	}
	if pref.Outcome.Preferred != "react" || pref.Outcome.Over != "vue" {
		t.Errorf("preference outcome: %+v", pref.Outcome)
	}
	if pref.Context["lang"] != "rust" || pref.Context["cap"] != "impl" {
		t.Errorf("preference should copy the execution context: %v", pref.Context)
	}
}

func TestRunWithoutExtractorSkipsSemanticContext(t *testing.T) {
	s := openStore(t)
	finishedSession(t, s, "sess")
	p := &Perceiver{Store: s, Extractor: nil, Ver: 1}

	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	cur, _ := s.CurrentExperiences()
	var exec *core.Experience
	for _, e := range cur {
		if e.Kind == core.KindExecution {
			exec = e
		}
	}
	if exec.ExtractorModel != "none" {
		t.Errorf("extractor_model: got %q, want none", exec.ExtractorModel)
	}
	for _, k := range SemanticKeys {
		if _, ok := exec.Context[k]; ok {
			t.Errorf("no semantic key expected without extractor, found %q", k)
		}
	}
	if exec.Context["cap"] != "impl" || exec.Context["model"] != "opus" {
		t.Errorf("deterministic context should still be present: %v", exec.Context)
	}
}

func TestRunHandsKnownVocabularyToExtractor(t *testing.T) {
	s := openStore(t)
	if err := s.InsertExperience(&core.Experience{
		ID: "seed", SessionID: "prior", TS: 1, Kind: core.KindExecution,
		ExtractorVer: 1, ExtractorModel: "none",
		Context: map[string]string{"lang": "rust"}, Provider: "claude",
		Outcome: core.Outcome{Adopted: "as-is"}, Source: "production",
	}); err != nil {
		t.Fatal(err)
	}
	finishedSession(t, s, "sess")
	ext := &fakeExtractor{semantic: map[string]string{}}
	p := &Perceiver{Store: s, Extractor: ext, Ver: 1}
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}

	langVocab := ext.lastVocab["lang"]
	if len(langVocab) != 1 || langVocab[0] != "rust" {
		t.Errorf("extractor should receive known lang vocabulary, got %v", langVocab)
	}
}

func TestRunClearsPerceivedSessionsFromPending(t *testing.T) {
	s := openStore(t)
	finishedSession(t, s, "sess")
	p := &Perceiver{Store: s, Extractor: &fakeExtractor{semantic: map[string]string{}}, Ver: 1}

	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if pending, _ := s.PendingSessions(1); len(pending) != 0 {
		t.Errorf("session should be gone from pending, got %v", pending)
	}
	again, err := p.Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("second run should produce nothing, got %d", len(again))
	}
}
