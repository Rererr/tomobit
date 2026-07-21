package perceive

import (
	"fmt"
	"path/filepath"
	"strings"
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

// TestParseDeterministicStripsControlChars guards the terminal-escape entry
// point at the event-payload boundary: a provider/model string carrying
// ESC/BEL bytes must come out clean, same as core.CanonValue on its own.
func TestParseDeterministicStripsControlChars(t *testing.T) {
	d := parseDeterministic([]*store.Event{
		ev("provider.selected", map[string]any{
			"provider": "claude\x1b]0;pwned\x07", "model": "opus",
		}),
	})
	if strings.ContainsAny(d.provider, "\x1b\x07") {
		t.Errorf("control chars survived: %q", d.provider)
	}
	if want := "claude]0;pwned"; d.provider != want {
		t.Errorf("got %q, want %q", d.provider, want)
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

// TestFailedSubtaskShapeScoresZero pins the C1 wiring end to end (ADR-0028
// Decision 5): a session with provider.error and an empty task.finished — the
// exact shape of a split subtask or a duel child — parses to a failed Outcome
// that OutcomeWeight scores y=0, so a failed child's Beta drops instead of
// staying neutral.
func TestFailedSubtaskShapeScoresZero(t *testing.T) {
	d := parseDeterministic([]*store.Event{
		ev("task.started", map[string]any{"source": "production", "parent": "p"}),
		ev("capability.started", map[string]any{"capability": "impl"}),
		ev("provider.selected", map[string]any{"provider": "claude"}),
		ev("provider.error", map[string]any{"message": "exit 1"}),
		ev("task.finished", map[string]any{}),
	})
	if !d.outcome.Failed {
		t.Fatal("provider.error should mark the outcome failed")
	}
	e := &core.Experience{Kind: core.KindExecution, Outcome: d.outcome}
	y, ok := core.OutcomeWeight(e)
	if !ok || y != 0 {
		t.Fatalf("a failed child should score y=0 (ok=true), got y=%v ok=%v", y, ok)
	}
}

// TestSuccessfulSubtaskShapeStaysNeutral: the same empty-task.finished shape
// without provider.error carries no signal — no objective failure and no
// subjective grade — so it stays neutral (ok=false), never fabricated into y=1
// (ADR-0028 Decision 5).
func TestSuccessfulSubtaskShapeStaysNeutral(t *testing.T) {
	d := parseDeterministic([]*store.Event{
		ev("task.started", map[string]any{"source": "production", "parent": "p"}),
		ev("capability.started", map[string]any{"capability": "impl"}),
		ev("provider.selected", map[string]any{"provider": "claude"}),
		ev("task.finished", map[string]any{}),
	})
	if d.outcome.Failed {
		t.Fatal("a clean run must not be marked failed")
	}
	e := &core.Experience{Kind: core.KindExecution, Outcome: d.outcome}
	if _, ok := core.OutcomeWeight(e); ok {
		t.Fatal("an unverified success must stay neutral (ok=false), not fabricate y=1")
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

func TestRunDropsFrameworkWhenItEqualsLang(t *testing.T) {
	s := openStore(t)
	finishedSession(t, s, "sess")
	ext := &fakeExtractor{semantic: map[string]string{"lang": "go", "framework": "Go"}}
	p := &Perceiver{Store: s, Extractor: ext, Ver: 1}

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
	if _, ok := exec.Context["framework"]; ok {
		t.Errorf("a language is never a framework — framework should be dropped: %v", exec.Context)
	}
	if exec.Context["lang"] != "go" {
		t.Errorf("lang should survive the guard: %v", exec.Context)
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

// expWithContext builds a minimal current-generation experience for
// capVocab tests — only SessionID/TS/Context/Kind matter to the ranking.
func expWithContext(id, sessionID string, ts int64, kind string, ctx map[string]string) *core.Experience {
	return &core.Experience{
		ID: id, SessionID: sessionID, TS: ts, Kind: kind,
		ExtractorVer: 1, ExtractorModel: "none", Context: ctx, Source: "production",
	}
}

func TestCapVocabKeepsEveryValueWhenUnderTheLimit(t *testing.T) {
	exps := []*core.Experience{
		expWithContext("a", "s1", 1, core.KindExecution, map[string]string{"lang": "go"}),
		expWithContext("b", "s2", 2, core.KindExecution, map[string]string{"lang": "rust"}),
	}
	got := capVocab(exps, 20)
	if strings.Join(got["lang"], ",") != "go,rust" {
		t.Errorf("got %v, want [go rust]", got["lang"])
	}
}

func TestCapVocabIgnoresEmptyContextValues(t *testing.T) {
	exps := []*core.Experience{
		expWithContext("a", "s1", 1, core.KindExecution, map[string]string{"lang": ""}),
		expWithContext("b", "s2", 2, core.KindExecution, map[string]string{"lang": "go"}),
	}
	got := capVocab(exps, 20)
	if len(got["lang"]) != 1 || got["lang"][0] != "go" {
		t.Errorf("empty value should be excluded, got %v", got["lang"])
	}
}

// TestCapVocabRanksByDistinctSessionCountOverAlphabeticalFirst pins the
// primary ranking signal: a value used across more sessions is kept over
// one used across fewer, even though a naive "first N alphabetically" cap
// would have kept the alphabetically earlier value instead.
func TestCapVocabRanksByDistinctSessionCountOverAlphabeticalFirst(t *testing.T) {
	exps := []*core.Experience{
		expWithContext("a1", "s1", 1, core.KindExecution, map[string]string{"lang": "axum-used-once"}),
		expWithContext("b1", "s2", 2, core.KindExecution, map[string]string{"lang": "zig-used-thrice"}),
		expWithContext("b2", "s3", 3, core.KindExecution, map[string]string{"lang": "zig-used-thrice"}),
		expWithContext("b3", "s4", 4, core.KindExecution, map[string]string{"lang": "zig-used-thrice"}),
	}
	got := capVocab(exps, 1)
	if len(got["lang"]) != 1 || got["lang"][0] != "zig-used-thrice" {
		t.Errorf("the more-frequently-recurring value should survive the cap, got %v", got["lang"])
	}
}

// TestCapVocabCountsSessionsNotExperienceRows pins the double-counting
// guard: a session's preference experiences copy the execution
// experience's Context verbatim (perceiveSession), so a single session
// with several preferences must still count once, not once per row.
func TestCapVocabCountsSessionsNotExperienceRows(t *testing.T) {
	exps := []*core.Experience{
		expWithContext("exec", "s1", 1, core.KindExecution, map[string]string{"lang": "rust"}),
		expWithContext("pref1", "s1", 1, core.KindPreference, map[string]string{"lang": "rust"}),
		expWithContext("pref2", "s1", 1, core.KindPreference, map[string]string{"lang": "rust"}),
		expWithContext("pref3", "s1", 1, core.KindPreference, map[string]string{"lang": "rust"}),
		expWithContext("other1", "s2", 2, core.KindExecution, map[string]string{"lang": "go"}),
		expWithContext("other2", "s3", 3, core.KindExecution, map[string]string{"lang": "go"}),
	}
	got := capVocab(exps, 1)
	if len(got["lang"]) != 1 || got["lang"][0] != "go" {
		t.Errorf("four rows in one session must not outrank two distinct sessions, got %v", got["lang"])
	}
}

// TestCapVocabBreaksFrequencyTiesByRecency pins the secondary ranking
// signal: among equally-frequent values, the one used more recently
// survives, so a long-dormant value cannot permanently block a currently
// active one from ever entering the vocabulary.
func TestCapVocabBreaksFrequencyTiesByRecency(t *testing.T) {
	exps := []*core.Experience{
		expWithContext("old", "s1", 1000, core.KindExecution, map[string]string{"lang": "cobol-old"}),
		expWithContext("new", "s2", 9000, core.KindExecution, map[string]string{"lang": "zig-new"}),
	}
	got := capVocab(exps, 1)
	if len(got["lang"]) != 1 || got["lang"][0] != "zig-new" {
		t.Errorf("the more recently used value should win an equal-frequency tie, got %v", got["lang"])
	}
}

// TestCapVocabBreaksRemainingTiesAlphabetically pins full determinism: with
// count and recency both tied, selection must not depend on Go's
// randomized map iteration order (ADR-0011: 判断は数学).
func TestCapVocabBreaksRemainingTiesAlphabetically(t *testing.T) {
	exps := []*core.Experience{
		expWithContext("z", "s1", 5, core.KindExecution, map[string]string{"lang": "zig"}),
		expWithContext("a", "s2", 5, core.KindExecution, map[string]string{"lang": "ada"}),
	}
	for i := 0; i < 20; i++ {
		got := capVocab(exps, 1)
		if len(got["lang"]) != 1 || got["lang"][0] != "ada" {
			t.Fatalf("run %d: fully-tied selection must deterministically favor the lexicographically smaller value, got %v", i, got["lang"])
		}
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

// TestRunCapsVocabularyHandedToExtractor wires capVocab's limit through the
// real Store: a ledger carrying more distinct lang values than vocabLimit
// must not hand the extractor more than vocabLimit of them.
func TestRunCapsVocabularyHandedToExtractor(t *testing.T) {
	s := openStore(t)
	for i := 0; i < vocabLimit+5; i++ {
		if err := s.InsertExperience(&core.Experience{
			ID: fmt.Sprintf("seed%d", i), SessionID: fmt.Sprintf("prior%d", i), TS: int64(i + 1),
			Kind: core.KindExecution, ExtractorVer: 1, ExtractorModel: "none",
			Context: map[string]string{"lang": fmt.Sprintf("lang%02d", i)}, Provider: "claude",
			Outcome: core.Outcome{Adopted: "as-is"}, Source: "production",
		}); err != nil {
			t.Fatal(err)
		}
	}
	finishedSession(t, s, "sess")
	ext := &fakeExtractor{semantic: map[string]string{}}
	p := &Perceiver{Store: s, Extractor: ext, Ver: 1}
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}

	if got := len(ext.lastVocab["lang"]); got != vocabLimit {
		t.Errorf("vocabulary handed to the extractor should be capped at %d, got %d", vocabLimit, got)
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
