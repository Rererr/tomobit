package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestResolveProviderFindsRegisteredAdapters(t *testing.T) {
	for _, name := range []string{"claude-code", "codex"} {
		a, err := resolveProvider(name)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if a.Name() != name {
			t.Errorf("%s: adapter Name() = %q", name, a.Name())
		}
	}
}

// TestResolveProviderUnknownNameListsRegisteredNames guards --provider typos:
// the error must name the available adapters, not just reject silently.
func TestResolveProviderUnknownNameListsRegisteredNames(t *testing.T) {
	_, err := resolveProvider("gemini")
	if err == nil {
		t.Fatal("unknown provider should error")
	}
	for _, want := range []string{"gemini", "claude-code", "codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: got %v", want, err)
		}
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

// growCapability seeds a scope+provider capability Connection with
// nSuccess adoptions and nFail reverts, growing it exactly as production
// would (mirrors internal/curiosity's own grow helper).
func growCapability(t *testing.T, s *store.Store, en *core.Engine, scopeKey, provider string, ts int64, nSuccess, nFail int) {
	t.Helper()
	ctx := map[string]string{}
	for _, tok := range core.ParseScopeKey(scopeKey) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			ctx[k] = v
		}
	}
	apply := func(seq int, o core.Outcome) {
		e := &core.Experience{
			ID:        fmt.Sprintf("%s-%s-%d", scopeKey, provider, seq),
			SessionID: fmt.Sprintf("s-%s-%s-%d", scopeKey, provider, seq),
			TS:        ts, Kind: core.KindExecution, ExtractorVer: extractorVer, ExtractorModel: "none",
			Context: ctx, Provider: provider, Outcome: o, Source: "production",
		}
		if err := s.InsertExperience(e); err != nil {
			t.Fatal(err)
		}
		if err := en.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	seq := 0
	for i := 0; i < nSuccess; i++ {
		seq++
		apply(seq, core.Outcome{Adopted: "as-is"})
	}
	for i := 0; i < nFail; i++ {
		seq++
		apply(seq, core.Outcome{Reverted: true})
	}
}

// TestAskWithIONonInteractiveRecordsNothing guards ADR-0007 Decision 3: a
// headless run has no human to interrupt, so it must not even reach the
// budget check, let alone spend it recording a tomo.asked nobody answered.
func TestAskWithIONonInteractiveRecordsNothing(t *testing.T) {
	s := openTestStore(t)
	en := &core.Engine{Repo: s}
	growCapability(t, s, en, "lang=rust", "claude", 1000, 4, 1)
	growCapability(t, s, en, "lang=rust", "codex", 1000, 4, 1)

	askWithIO(s, "do-session", strings.NewReader("1\n"), &bytes.Buffer{}, false, 1000)

	var total int
	if err := s.DB.QueryRow(`SELECT count(*) FROM events`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("non-interactive must record no events at all, got %d", total)
	}
}

// TestAskWithIOInteractiveRecordsTomoAsked exercises the reachable path an
// interactive terminal takes when a Preference Gap is open: HasBudget
// passes, a gap is found, and asking records tomo.asked.
func TestAskWithIOInteractiveRecordsTomoAsked(t *testing.T) {
	s := openTestStore(t)
	en := &core.Engine{Repo: s}
	growCapability(t, s, en, "lang=rust", "claude", 1000, 4, 1)
	growCapability(t, s, en, "lang=rust", "codex", 1000, 4, 1)

	var out bytes.Buffer
	askWithIO(s, "do-session", strings.NewReader("1\n"), &out, true, 1000)

	if n := countEventsOfType(t, s, "tomo.asked"); n != 1 {
		t.Errorf("interactive with an open gap should record exactly one tomo.asked, got %d", n)
	}
}

func countEventsOfType(t *testing.T, s *store.Store, typ string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM events WHERE type = ?`, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

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

// TestBareInvocationDoesNotError exercises the no-args path (ADR-0008
// Consequences: bare `tomobit` is the companion view, not usage) against an
// empty database, where the view has the least to work with.
func TestBareInvocationDoesNotError(t *testing.T) {
	t.Setenv("TOMOBIT_DB", filepath.Join(t.TempDir(), "test.db"))
	if err := run(nil); err != nil {
		t.Fatalf("bare invocation should not error, got %v", err)
	}
}

// TestCompanionViewSkipsAvatarWhenStdoutIsNotATTY guards ADR-0008 Decision
// 4: piped/redirected stdout must stay exactly the old machine-readable
// output — no avatar escape codes, no spoken line.
func TestCompanionViewSkipsAvatarWhenStdoutIsNotATTY(t *testing.T) {
	t.Setenv("TOMOBIT_DB", filepath.Join(t.TempDir(), "test.db"))

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	statusErr := cmdStatus(nil)
	os.Stdout = orig
	w.Close()
	if statusErr != nil {
		t.Fatalf("cmdStatus: %v", statusErr)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("non-TTY stdout must not carry avatar escape codes, got %q", got)
	}
	if want := "no connections yet — record a session and run `tomobit perceive`\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestTruncateExactWidthIsUnchanged guards the boundary: a string exactly at
// the column width must not lose a character (only strings that exceed the
// width should be cut).
func TestTruncateExactWidthIsUnchanged(t *testing.T) {
	s := strings.Repeat("a", 40)
	if got := truncate(s, 40); got != s {
		t.Errorf("40-rune input at width 40: got %q, want unchanged", got)
	}
}

// TestTruncateOneOverWidthGetsEllipsis guards the other side of the same
// boundary: one rune past the width must be cut down to the width, with the
// last rune replaced by an ellipsis so the total display width is preserved.
func TestTruncateOneOverWidthGetsEllipsis(t *testing.T) {
	got := truncate(strings.Repeat("a", 41), 40)
	if n := utf8.RuneCountInString(got); n != 40 {
		t.Errorf("truncated rune count: got %d, want 40", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated string should end with an ellipsis: got %q", got)
	}
}

// TestTruncateIsRuneSafeAcrossMultibyteCharacters guards against a byte-based
// cut splitting a multi-byte rune (e.g. a Japanese scope token) into invalid
// UTF-8.
func TestTruncateIsRuneSafeAcrossMultibyteCharacters(t *testing.T) {
	s := strings.Repeat("あ", 20) + strings.Repeat("a", 21) // 41 runes, mixed width
	got := truncate(s, 40)
	if !utf8.ValidString(got) {
		t.Errorf("truncation must not split a multi-byte rune: got %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 40 {
		t.Errorf("truncated rune count: got %d, want 40", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated string should end with an ellipsis: got %q", got)
	}
}
