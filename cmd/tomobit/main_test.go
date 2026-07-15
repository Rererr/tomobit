package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
)

func TestAdoptionPayloadEnterMeansAsIs(t *testing.T) {
	got := adoptionPayload(strings.NewReader("\n"))
	if got["adopted"] != "as-is" || got["reverted"] != false {
		t.Errorf("Enter: got %v", got)
	}
}

func TestAdoptionPayloadEMeansWithEdits(t *testing.T) {
	got := adoptionPayload(strings.NewReader("e\n"))
	if got["adopted"] != "with-edits" || got["reverted"] != false {
		t.Errorf("e: got %v", got)
	}
}

func TestAdoptionPayloadRMeansReverted(t *testing.T) {
	got := adoptionPayload(strings.NewReader("r\n"))
	if got["adopted"] != "" || got["reverted"] != true {
		t.Errorf("r: got %v", got)
	}
}

func TestAdoptionPayloadSCarriesNoSignal(t *testing.T) {
	got := adoptionPayload(strings.NewReader("s\n"))
	if len(got) != 0 {
		t.Errorf("s: got %v, want empty payload", got)
	}
}

// TestAdoptionPayloadEOFCarriesNoSignal guards against EOF (non-interactive
// stdin, e.g. a headless invocation with no terminal attached) being read as
// an empty line and mistaken for Enter — which would fabricate "as-is"
// adoption nobody actually confirmed.
func TestAdoptionPayloadEOFCarriesNoSignal(t *testing.T) {
	got := adoptionPayload(strings.NewReader(""))
	if len(got) != 0 {
		t.Errorf("EOF: got %v, want empty payload (not as-is)", got)
	}
}

func TestProviderErrorPayloadNoneOnCleanExit(t *testing.T) {
	_, need := providerErrorPayload(nil, executor.Result{Started: true, ExitCode: 0})
	if need {
		t.Error("a clean exit should not record provider.error")
	}
}

func TestProviderErrorPayloadRecordsExecutorFailureNotSeenByAdapter(t *testing.T) {
	payload, need := providerErrorPayload(fmt.Errorf("timed out"), executor.Result{})
	if !need || payload["message"] != "timed out" {
		t.Errorf("got need=%v payload=%v", need, payload)
	}
}

func TestProviderErrorPayloadRecordsBareNonZeroExit(t *testing.T) {
	payload, need := providerErrorPayload(nil, executor.Result{Started: true, ExitCode: 3})
	if !need || payload["message"] != "provider exited with code 3" {
		t.Errorf("got need=%v payload=%v", need, payload)
	}
}

// TestProviderErrorPayloadSkipsWhenAdapterAlreadyReported guards against
// double-recording: when the adapter's own stream already emitted
// provider.error (translated from e.g. a claude-code result line), cmdDo
// must not append a second one for the same failure.
func TestProviderErrorPayloadSkipsWhenAdapterAlreadyReported(t *testing.T) {
	_, need := providerErrorPayload(nil, executor.Result{Started: true, ExitCode: 1, ErrorReported: true})
	if need {
		t.Error("should not double-record when the adapter's stream already emitted provider.error")
	}
}

// fakePerceiveExtractor lets perceiveBestEffort be exercised without a real
// Ollama server.
type fakePerceiveExtractor struct {
	semantic map[string]string
	err      error
}

func (f *fakePerceiveExtractor) ExtractContext([]*store.Event, map[string][]string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.semantic, nil
}
func (f *fakePerceiveExtractor) Name() string { return "fake" }

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func appendFinishedSession(t *testing.T, s *store.Store, sid string) {
	t.Helper()
	steps := []struct {
		typ     string
		payload map[string]any
	}{
		{"task.started", map[string]any{"intent": "fix it", "source": "production"}},
		{"capability.started", map[string]any{"capability": "implement"}},
		{"provider.selected", map[string]any{"provider": "claude-code", "model": "opus"}},
		{"task.finished", map[string]any{"adopted": "as-is", "reverted": false}},
	}
	for i, step := range steps {
		if err := s.AppendEvent(sid, step.typ, int64(1000+i), step.payload); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPerceiveBestEffortAppliesExperiencesLive(t *testing.T) {
	s := openTestStore(t)
	appendFinishedSession(t, s, "sess")

	perceiveBestEffort(s, &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}})

	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 1 || cur[0].Context["lang"] != "go" {
		t.Fatalf("expected one execution experience with lang=go, got %v", cur)
	}

	conns, err := s.AllConnections()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range conns {
		if c.Kind == core.ConnCapability && c.Target == "claude-code" {
			found = true
		}
	}
	if !found {
		t.Errorf("perceiveBestEffort should apply live, producing a claude-code connection: %v", conns)
	}
}

// TestPerceiveBestEffortLeavesSessionPendingOnExtractorFailure is the
// Deferred Perception guarantee (ADR-0006 Decision 5): a `do` run must
// finish successfully even if Ollama is unreachable, with the session left
// for a later `tomobit perceive`.
func TestPerceiveBestEffortLeavesSessionPendingOnExtractorFailure(t *testing.T) {
	s := openTestStore(t)
	appendFinishedSession(t, s, "sess")

	perceiveBestEffort(s, &fakePerceiveExtractor{err: fmt.Errorf("ollama: connection refused")})

	pending, err := s.PendingSessions(extractorVer)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != "sess" {
		t.Errorf("session should stay pending after an extractor failure, got %v", pending)
	}
}
