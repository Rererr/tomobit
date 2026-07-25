package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/subtask"
)

// The Feedback question keeps the adopted/reverted payload keys (呼称の統一
// ADR-0028 changed the name, not the schema): 1/2/3 still map to the same
// y-value writes rebuild reads.
func TestFeedbackPayloadOneMeansAsIs(t *testing.T) {
	got := feedbackPayload(bufio.NewReader(strings.NewReader("1\n")), io.Discard)
	if got["adopted"] != "as-is" || got["reverted"] != false {
		t.Errorf("1: got %v", got)
	}
}

func TestFeedbackPayloadTwoMeansWithEdits(t *testing.T) {
	got := feedbackPayload(bufio.NewReader(strings.NewReader("2\n")), io.Discard)
	if got["adopted"] != "with-edits" || got["reverted"] != false {
		t.Errorf("2: got %v", got)
	}
}

func TestFeedbackPayloadThreeMeansReverted(t *testing.T) {
	got := feedbackPayload(bufio.NewReader(strings.NewReader("3\n")), io.Discard)
	if got["adopted"] != "" || got["reverted"] != true {
		t.Errorf("3: got %v", got)
	}
}

// TestFeedbackPayloadEnterCarriesNoSignal pins the deliberate default (案A):
// a bare Enter is "まだ言えない", not top-grade praise — the ledger learns
// nothing rather than being inflated by a mindless keypress.
func TestFeedbackPayloadEnterCarriesNoSignal(t *testing.T) {
	got := feedbackPayload(bufio.NewReader(strings.NewReader("\n")), io.Discard)
	if len(got) != 0 {
		t.Errorf("Enter: got %v, want empty payload", got)
	}
}

func TestFeedbackPayloadUnknownCarriesNoSignal(t *testing.T) {
	got := feedbackPayload(bufio.NewReader(strings.NewReader("x\n")), io.Discard)
	if len(got) != 0 {
		t.Errorf("unknown: got %v, want empty payload", got)
	}
}

// TestFeedbackPayloadEOFCarriesNoSignal guards against EOF (non-interactive
// stdin, e.g. a headless invocation with no terminal attached) fabricating a
// verdict nobody confirmed — it must stay an empty payload, never a grade.
func TestFeedbackPayloadEOFCarriesNoSignal(t *testing.T) {
	got := feedbackPayload(bufio.NewReader(strings.NewReader("")), io.Discard)
	if len(got) != 0 {
		t.Errorf("EOF: got %v, want empty payload", got)
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
	payload, need := providerErrorPayload(fmt.Errorf("timed out"), executor.Result{Started: true})
	if !need || payload["message"] != "timed out" {
		t.Errorf("got need=%v payload=%v", need, payload)
	}
}

// ADR-0043 Decision 3: a run that never launched (Started=false + error) is
// not the provider's failure — recording it as provider.error would perceive
// into outcome.Failed and sink a capability the provider never got to show.
func TestProviderErrorPayloadSkipsARunThatNeverStarted(t *testing.T) {
	_, need := providerErrorPayload(fmt.Errorf("claude: start: executable file not found"), executor.Result{})
	if need {
		t.Error("a run that never started must not be recorded as the provider's error")
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
	semantic     map[string]string
	taskSemantic map[string]string
	taskIntent   string
	taskVocab    map[string][]string
	taskCalls    int
	err          error
}

func (f *fakePerceiveExtractor) ExtractContext([]*store.Event, map[string][]string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.semantic, nil
}

// ExtractTaskContextは渡された入力を記録し、ExtractContextとは別のfixtureを返す。
// 両入口が同じ応答を返すfakeでは、Task Perceptionに配線された呼び出しと
// session perceptionへの退避を区別できないため、この境界を固定する。
func (f *fakePerceiveExtractor) ExtractTaskContext(intent string, vocab map[string][]string) (map[string]string, error) {
	f.taskIntent = intent
	f.taskVocab = vocab
	f.taskCalls++
	if f.err != nil {
		return nil, f.err
	}
	if f.taskSemantic != nil {
		return f.taskSemantic, nil
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

	askWithIO(s, "do-session", bufio.NewReader(strings.NewReader("1\n")), &bytes.Buffer{}, false, 1000)

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
	askWithIO(s, "do-session", bufio.NewReader(strings.NewReader("1\n")), &out, true, 1000)

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

// knowHuman plants a human capability connection at scopeKey — the ADR-0043
// Decision 4 precondition for human to enter auto's candidate set at all.
func knowHuman(t *testing.T, s *store.Store, scopeKey string) {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := s.UpsertConnection(&core.Connection{
		Kind: core.ConnCapability, ScopeKey: scopeKey, Target: "human",
		Alpha: 2, Beta: 1, LastUpdate: now, BornTS: now, PriorA: 1, PriorB: 1,
	}); err != nil {
		t.Fatal(err)
	}
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
	appendFinishedSessionAt(t, s, sid, 1000)
}

// appendFinishedSessionAt is appendFinishedSession with a configurable base
// ts, so tests can place two sessions at deliberately different points in
// time (e.g. ADR-0041's out-of-order perceive scenario, where a session
// perceived later must still carry an earlier ts).
func appendFinishedSessionAt(t *testing.T, s *store.Store, sid string, baseTS int64) {
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
		if err := s.AppendEvent(sid, step.typ, baseTS+int64(i), step.payload); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPerceiveBestEffortAppliesExperiencesLive(t *testing.T) {
	s := openTestStore(t)
	appendFinishedSession(t, s, "sess")

	perceiveBestEffort(s, io.Discard, &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}})

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

	perceiveBestEffort(s, io.Discard, &fakePerceiveExtractor{err: fmt.Errorf("ollama: connection refused")})

	pending, err := s.PendingSessions(extractorVer)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != "sess" {
		t.Errorf("session should stay pending after an extractor failure, got %v", pending)
	}
}

// TestPerceiveBestEffortLogsOnOutOfOrderBatch pins the do/chat tail's
// perception path (finishTask → perceiveBestEffort) to ADR-0041's guard, not
// just cmdPerceive's: a session perceived after a chronologically later one
// already went live must log the one honest rebuild line, not diverge
// silently.
func TestPerceiveBestEffortLogsOnOutOfOrderBatch(t *testing.T) {
	s := openTestStore(t)
	appendFinishedSessionAt(t, s, "recent", 5_000_000)
	perceiveBestEffort(s, io.Discard, &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}})

	appendFinishedSessionAt(t, s, "late", 1_000_000)
	_, stderr := captureStdoutStderr(t, func() {
		perceiveBestEffort(s, io.Discard, &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}})
	})

	if !strings.Contains(stderr, "out-of-order batch — rebuilding projections") {
		t.Errorf("expected the out-of-order rebuild line on stderr, got %q", stderr)
	}
}

// TestPerceiveLiveFailsFastWhenCurrentExperiencesErrored pins the fix for a
// bug review caught in cmdPerceive: it used to ignore the error from the
// CurrentExperiences() call that produces `known` and hand PerceiveBatch a
// nil/incomplete slice anyway, silently disabling both of ADR-0041's guards.
// perceiveLive is the one shared path cmdPerceive and perceiveBestEffort now
// both route through, so pinning it here covers both call sites at once.
func TestPerceiveLiveFailsFastWhenCurrentExperiencesErrored(t *testing.T) {
	s := openTestStore(t)
	en := &core.Engine{Repo: s}
	batch := []*core.Experience{{
		ID: "e1", SessionID: "sess", TS: 1000, Kind: core.KindExecution,
		ExtractorVer: extractorVer, ExtractorModel: "none",
		Context: map[string]string{"cap": "impl"}, Provider: "claude-code",
		Outcome: core.Outcome{Adopted: "as-is"}, Source: "production",
	}}
	if err := s.InsertExperiences(batch); err != nil {
		t.Fatal(err)
	}

	if err := perceiveLive(en, batch, nil, fmt.Errorf("db unavailable")); err == nil {
		t.Fatal("perceiveLive must fail when the CurrentExperiences fetch behind `known` errored")
	}

	conns, err := s.AllConnections()
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 0 {
		t.Errorf("must not fold the batch into the live projection when the order-check's `known` was unavailable, got %v", conns)
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

// TestAutoDecideRecordsReplayableSeed (ADR-0012 Decision 5): the decision
// audit lands in events with the seed as a string — UnixNano does not fit
// JSON's exact float64 integers, and a rounded seed cannot replay.
func TestAutoDecideRecordsReplayableSeed(t *testing.T) {
	registerRunnableFakeProvider(t, "fake-runnable")
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AppendEvent("sess", "task.started", 1, nil); err != nil {
		t.Fatal(err)
	}

	dec, err := autoDecide(s, io.Discard, "sess", "implement", "large", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := providers[dec.Provider]; !ok && dec.Provider != "human" {
		t.Fatalf("decided unregistered provider %q", dec.Provider)
	}
	if dec.N != 5 {
		t.Errorf("size=large should decide with n=5, got %d", dec.N)
	}

	evs, err := s.EventsBySession("sess")
	if err != nil {
		t.Fatal(err)
	}
	var decided map[string]any
	for _, e := range evs {
		if e.Type == "tomo.decided" {
			decided = e.Payload
		}
	}
	if decided == nil {
		t.Fatal("no tomo.decided event recorded")
	}
	seed, ok := decided["seed"].(string)
	if !ok || seed == "" {
		t.Fatalf("seed must be a non-empty string, got %v", decided["seed"])
	}
	if decided["provider"] != dec.Provider {
		t.Errorf("payload provider %v != decision %q", decided["provider"], dec.Provider)
	}
	// Every launchable adapter is audited (ADR-0043 Decision 2); human is
	// absent — a blank ledger knows no human here (ADR-0043 Decision 4).
	if cands, ok := decided["candidates"].([]any); !ok || len(cands) != len(availableProviderNames()) {
		t.Errorf("payload should audit every launchable candidate, got %v", decided["candidates"])
	}
}

// TestGreetIfReturned (ADR-0019 Decision 2): three quiet days make a
// return; the greeting names the island whose confidence faded the most,
// and the recorded tomo.greeted closes the gap so one return greets once.
func TestGreetIfReturned(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := int64(1_800_000_000_000)
	last := now - 30*24*3600*1000 // a month of silence
	if err := s.AppendEvent("old", "task.finished", last, nil); err != nil {
		t.Fatal(err)
	}
	conns := []*core.Connection{{
		Kind: core.ConnCapability, ScopeKey: "lang=go", Target: "claude",
		Alpha: 11, Beta: 1, LastUpdate: last, BornTS: last,
	}}

	var out bytes.Buffer
	greetIfReturned(&out, s, conns, now)
	if !strings.Contains(out.String(), "おかえり") || !strings.Contains(out.String(), "go") {
		t.Errorf("want an okaeri naming the faded island, got %q", out.String())
	}

	out.Reset()
	greetIfReturned(&out, s, conns, now+1000)
	if out.Len() != 0 {
		t.Errorf("the same return must greet once, got %q", out.String())
	}

	// A short gap is not an absence.
	s2, err := store.Open(filepath.Join(t.TempDir(), "t2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.AppendEvent("x", "task.finished", now-3600*1000, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	greetIfReturned(&out, s2, nil, now)
	if out.Len() != 0 {
		t.Errorf("an hour is no absence, got %q", out.String())
	}
}

// TestIsTTYRejectsDevNull guards the whole non-interactive story: /dev/null is
// a character device, so a file-mode check calls `tomobit ... < /dev/null` an
// interactive terminal — and Tomo spends its one question a day (ADR-0007) on
// nobody, recording a tomo.asked no human ever saw.
func TestIsTTYRejectsDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Error("/dev/null is not a terminal")
	}
}

// A regular file is the other half: `tomobit do ... < script.txt`.
func TestIsTTYRejectsARegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(path, []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Error("a regular file is not a terminal")
	}
}

// unsetClaudeProfile makes this process look like a machine where no profile
// has ever been chosen, and puts the wiring back afterwards. HOME is
// redirected too: answering the question saves, and a test must never write
// the real ~/.tomobit/config.json.
func unsetClaudeProfile(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if v, ok := os.LookupEnv("TOMOBIT_CLAUDE_CONFIG_DIR"); ok {
		os.Unsetenv("TOMOBIT_CLAUDE_CONFIG_DIR")
		t.Cleanup(func() { os.Setenv("TOMOBIT_CLAUDE_CONFIG_DIR", v) })
	}
	oldCfg, oldErr := cfg, cfgErr
	cfg, cfgErr = config.Config{}, nil
	t.Cleanup(func() {
		cfg, cfgErr = oldCfg, oldErr
		wireClaude()
	})
	wireClaude()
	if claudeProfileSet {
		t.Fatal("setup: the profile should look unchosen")
	}
}

// The missing choice becomes the question (ADR-0021 Decision 4), and the
// question reads from the reader it was handed — the one a chat also reads
// its next turn from. Taking one byte more than its own line would swallow
// whatever the user had already typed ahead.
func TestEnsureClaudeProfileAsksOnTheGivenReaderAndTakesOnlyItsLine(t *testing.T) {
	unsetClaudeProfile(t)
	in := bufio.NewReader(strings.NewReader("0\n次のタスク\n"))
	var out bytes.Buffer

	if err := ensureClaudeProfileIO(in, &out, "claude-code", true); err != nil {
		t.Fatal(err)
	}
	if !claudeProfileSet {
		t.Error("answering must settle the choice")
	}
	if !strings.Contains(out.String(), "プロファイル") {
		t.Errorf("the question must be asked: %q", out.String())
	}
	rest, err := in.ReadString('\n')
	if err != nil || strings.TrimSpace(rest) != "次のタスク" {
		t.Errorf("the rest of the reader must survive the question: got %q (%v)", rest, err)
	}
}

// Headless (daemon, cron, pipe) it stays a hard error: starting a dialogue
// inside automation is the accident ADR-0021 refuses, so nothing is read and
// nothing is saved.
func TestEnsureClaudeProfileHeadlessRefusesWithoutReading(t *testing.T) {
	unsetClaudeProfile(t)
	in := bufio.NewReader(strings.NewReader("0\n"))
	var out bytes.Buffer

	err := ensureClaudeProfileIO(in, &out, "claude-code", false)
	if err == nil {
		t.Fatal("a headless run with no profile chosen must fail")
	}
	if !strings.Contains(err.Error(), "tomobit setup") {
		t.Errorf("the error must say how to fix it: %v", err)
	}
	if claudeProfileSet {
		t.Error("nothing may be settled without an answer")
	}
	if line, _ := in.ReadString('\n'); strings.TrimSpace(line) != "0" {
		t.Errorf("the input must be untouched: got %q", line)
	}
}

// auto can pick claude-code, so it carries the same gate; a provider that
// cannot be claude-code never triggers the question.
func TestEnsureClaudeProfileGatesAutoButNotOtherProviders(t *testing.T) {
	unsetClaudeProfile(t)
	var out bytes.Buffer
	if err := ensureClaudeProfileIO(bufio.NewReader(strings.NewReader("")), &out, "auto", false); err == nil {
		t.Error("auto may launch claude-code, so it must be gated too")
	}
	for _, name := range []string{"codex", "human"} {
		if err := ensureClaudeProfileIO(bufio.NewReader(strings.NewReader("")), &out, name, false); err != nil {
			t.Errorf("%s needs no claude profile: %v", name, err)
		}
	}
}

func TestSplitArgsMatchesFieldsWhenThereIsNoQuotingOrEscaping(t *testing.T) {
	for _, s := range []string{
		"",
		"   ",
		"--foo",
		"--foo bar",
		"  --foo   bar  baz ",
		"a\tb\nc\r\nd",
	} {
		got := splitArgs(s)
		want := strings.Fields(s)
		if !slicesEqual(got, want) {
			t.Errorf("splitArgs(%q) = %v, want %v (strings.Fields)", s, got, want)
		}
	}
}

func TestSplitArgsPreservesWhitespaceInsideDoubleQuotes(t *testing.T) {
	got := splitArgs(`--append-system-prompt "be terse and direct"`)
	want := []string{"--append-system-prompt", "be terse and direct"}
	if !slicesEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitArgsPreservesWhitespaceInsideSingleQuotes(t *testing.T) {
	got := splitArgs(`-x 'a b c'`)
	want := []string{"-x", "a b c"}
	if !slicesEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitArgsMergesAQuoteInTheMiddleOfATokenIntoOneArg(t *testing.T) {
	got := splitArgs(`ab"c d"e`)
	want := []string{"abc de"}
	if !slicesEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitArgsUnquotedBackslashEscapesTheNextRune(t *testing.T) {
	got := splitArgs(`a\ b \"x\\y`)
	want := []string{"a b", `"x\y`}
	if !slicesEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitArgsSingleQuoteTakesBackslashLiterally(t *testing.T) {
	got := splitArgs(`'a\b'`)
	want := []string{`a\b`}
	if !slicesEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestSplitArgsUnterminatedQuoteIsDeterministicNotAnError pins the contract
// a GUI-supplied string relies on: a stray quote must never abort the
// launch, it only pulls the remainder into one final token and warns once.
func TestSplitArgsUnterminatedQuoteIsDeterministicNotAnError(t *testing.T) {
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	got := splitArgs(`--foo "bar baz`)
	w.Close()
	os.Stderr = oldStderr
	var warned bytes.Buffer
	if _, err := io.Copy(&warned, r); err != nil {
		t.Fatal(err)
	}

	want := []string{"--foo", "bar baz"}
	if !slicesEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if warned.Len() == 0 {
		t.Error("an unterminated quote must warn on stderr")
	}
}

func TestSplitArgsEmptyStringYieldsNoArgs(t *testing.T) {
	if got := splitArgs(""); len(got) != 0 {
		t.Errorf("splitArgs(\"\") = %v, want empty", got)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// withClaudeArgsEnv saves and restores TOMOBIT_CLAUDE_ARGS,
// TOMOBIT_CLAUDE_ARGS_APPEND and cfg around a wireClaude test, and rewires
// the adapter back to the ambient machine state afterwards so a later test
// never inherits a fixture's resolution.
func withClaudeArgsEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"TOMOBIT_CLAUDE_ARGS", "TOMOBIT_CLAUDE_ARGS_APPEND"} {
		if v, ok := os.LookupEnv(name); ok {
			os.Unsetenv(name)
			t.Cleanup(func() { os.Setenv(name, v) })
		}
	}
	oldCfg := cfg
	t.Cleanup(func() {
		cfg = oldCfg
		wireClaude()
	})
}

func TestWireClaudeEnvArgsReplaceConfigArgs(t *testing.T) {
	withClaudeArgsEnv(t)
	cfg.ClaudeArgs = []string{"--from-config"}
	t.Setenv("TOMOBIT_CLAUDE_ARGS", "--from-env")
	wireClaude()
	want := []string{"--from-env"}
	if !slicesEqual(claudeAdapter.ExtraArgs, want) {
		t.Errorf("env must replace config args entirely: got %v, want %v", claudeAdapter.ExtraArgs, want)
	}
}

func TestWireClaudeAppendAddsAfterEnvResolution(t *testing.T) {
	withClaudeArgsEnv(t)
	t.Setenv("TOMOBIT_CLAUDE_ARGS", "--from-env")
	t.Setenv("TOMOBIT_CLAUDE_ARGS_APPEND", `--append-system-prompt "be terse"`)
	wireClaude()
	want := []string{"--from-env", "--append-system-prompt", "be terse"}
	if !slicesEqual(claudeAdapter.ExtraArgs, want) {
		t.Errorf("got %v, want %v", claudeAdapter.ExtraArgs, want)
	}
}

func TestWireClaudeAppendAddsAfterConfigResolution(t *testing.T) {
	withClaudeArgsEnv(t)
	cfg.ClaudeArgs = []string{"--exclude-dynamic-system-prompt-sections"}
	t.Setenv("TOMOBIT_CLAUDE_ARGS_APPEND", `--append-system-prompt "be terse"`)
	wireClaude()
	want := []string{"--exclude-dynamic-system-prompt-sections", "--append-system-prompt", "be terse"}
	if !slicesEqual(claudeAdapter.ExtraArgs, want) {
		t.Errorf("got %v, want %v", claudeAdapter.ExtraArgs, want)
	}
}

// TestWireClaudeAppendNeverMutatesConfigArgsBackingArray guards the
// aliasing bug an in-place append onto cfg.ClaudeArgs would reintroduce: a
// later read of cfg (e.g. `tomobit setup` re-saving it) must still see the
// config's own args, untouched by what APPEND tacked on.
func TestWireClaudeAppendNeverMutatesConfigArgsBackingArray(t *testing.T) {
	withClaudeArgsEnv(t)
	original := make([]string, 2, 5) // spare capacity: an in-place append would silently reuse it
	original[0] = "a"
	original[1] = "b"
	cfg.ClaudeArgs = original
	t.Setenv("TOMOBIT_CLAUDE_ARGS_APPEND", "c d")
	wireClaude()

	want := []string{"a", "b", "c", "d"}
	if !slicesEqual(claudeAdapter.ExtraArgs, want) {
		t.Errorf("got %v, want %v", claudeAdapter.ExtraArgs, want)
	}
	if len(original) != 2 || original[0] != "a" || original[1] != "b" {
		t.Errorf("cfg.ClaudeArgs must be unchanged: %v", original)
	}
	if cap(original) >= 3 && original[:3][2] != "" {
		t.Errorf("append must not spill into cfg.ClaudeArgs's backing array: %v", original[:3])
	}
}

// fakeSplitAdapter is a test-only executor.Adapter: Command launches a real
// but trivial child (`sh -c`) so the Executor's actual process lifecycle
// runs, and Translate maps every stdout line straight to a provider.output.
// exitCode lets a test simulate a subtask that runs and then fails, without
// touching any real provider CLI.
type fakeSplitAdapter struct {
	name     string
	line     string
	exitCode int
}

func (f *fakeSplitAdapter) Name() string { return f.name }

func (f *fakeSplitAdapter) Command(executor.Request) (string, []string, []string) {
	script := fmt.Sprintf("echo %s; exit %d", shellQuote(f.line), f.exitCode)
	return "sh", []string{"-c", script}, nil
}

func (f *fakeSplitAdapter) Translate(line []byte) ([]executor.Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	return []executor.Event{{
		Type:    executor.EventProviderOutput,
		Payload: map[string]any{"text": string(line)},
	}}, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// registerFakeProvider adds a test adapter to the live providers map for the
// duration of the test — runSplit resolves an explicit --provider name
// straight out of this package-level map — and restores it on cleanup so
// TestAutoDecideRecordsReplayableSeed's len(providers) count is never left
// stale for a later test.
func registerFakeProvider(t *testing.T, name string, a executor.Adapter) {
	t.Helper()
	providers[name] = a
	t.Cleanup(func() { delete(providers, name) })
}

// registerRunnableFakeProvider registers a provider that auto will actually
// consider, on any machine. availableProviderNames filters the registry by
// whether the adapter's executable is on PATH (ADR-0043 Decision 2), so a test
// calling autoDecide without one of these decides nothing wherever the real
// claude/codex CLIs are absent — it passes on a developer's laptop and fails
// in CI, which is a test that measures the machine rather than the code.
// fakeSplitAdapter's Command names `sh`, which is on PATH everywhere this
// suite runs, so the candidate is runnable for real rather than by stubbing
// the PATH lookup.
func registerRunnableFakeProvider(t *testing.T, name string) {
	t.Helper()
	registerFakeProvider(t, name, &fakeSplitAdapter{name: name, line: "ok"})
}

// subtaskSessionIDs returns the distinct session ids whose task.started names
// parentSID as parent, in the order they were recorded.
func subtaskSessionIDs(t *testing.T, s *store.Store, parentSID string) []string {
	t.Helper()
	rows, err := s.DB.Query(`
		SELECT session_id FROM events
		WHERE type = 'task.started' AND json_extract(payload, '$.parent') = ?
		ORDER BY id`, parentSID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func countEventsOfTypeInSession(t *testing.T, s *store.Store, sid, typ string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM events WHERE session_id = ? AND type = ?`,
		sid, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestProviderSinkCollectsTextAcrossEventsForSplitParsing exercises the
// cmdDo seam runSplit's tests cannot reach: provider.output text is gathered
// event by event (tool-only outputs skipped), and the "\n"-joined result is
// what subtask.Parse reads a proposal from — prose in one event, the fenced
// marker in another.
func TestProviderSinkCollectsTextAcrossEventsForSplitParsing(t *testing.T) {
	s := openTestStore(t)
	var texts []string
	sink := providerSink(s, "sess", io.Discard, &texts)

	events := []executor.Event{
		{Type: executor.EventProviderOutput, Payload: map[string]any{"text": "分割を提案する。"}},
		{Type: executor.EventProviderOutput, Payload: map[string]any{"tool": "Bash"}},
		{Type: executor.EventProviderOutput, Payload: map[string]any{"text": "```json\n{\"tomobit_split\": [\"part one\", \"part two\"]}\n```"}},
	}
	for i, ev := range events {
		if err := sink(ev, int64(1000+i)); err != nil {
			t.Fatal(err)
		}
	}

	if len(texts) != 2 {
		t.Fatalf("only text-bearing outputs should be collected, got %d: %v", len(texts), texts)
	}
	groups, err := subtask.Parse(strings.Join(texts, "\n"))
	if err != nil {
		t.Fatalf("joined collection should parse as a proposal: %v", err)
	}
	if len(groups) != 2 || groups[0][0] != "part one" {
		t.Fatalf("got %v, want the proposed subtasks", groups)
	}
	if n := countEventsOfTypeInSession(t, s, "sess", "provider.output"); n != 3 {
		t.Errorf("every event must still be recorded regardless of collection, got %d", n)
	}
}

// ADR-0024 Decision 6: do's sink strips view-only keys the same way chat's
// does — the two paths must record one shape for the same provider stream.
func TestProviderSinkKeepsViewOnlyDetailOutOfTheLedger(t *testing.T) {
	s := openTestStore(t)
	sink := providerSink(s, "sess", io.Discard, nil)

	if err := sink(executor.Event{Type: executor.EventProviderOutput,
		Payload: map[string]any{"tool": "Bash", executor.PayloadDetail: "git status"}}, 1000); err != nil {
		t.Fatal(err)
	}

	p := payloadOf(t, s, "provider.output")
	if p["tool"] != "Bash" {
		t.Errorf("tool name is the record: got %v", p)
	}
	if _, ok := p["detail"]; ok {
		t.Errorf("view-only detail must not be recorded: %v", p)
	}
}

// TestRunSplitNormalFlowRecordsParentAndPerSubtaskLedger exercises the happy
// path: the parent gets task.split and a Feedback-free task.finished, while
// each subtask is its own session linked to the parent. Under ADR-0028
// Decision 5 the subtask's task.finished is empty too — no per-subtask
// subjective Feedback — so only objective signals become its experience.
func TestRunSplitNormalFlowRecordsParentAndPerSubtaskLedger(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "done"})

	const parentSID = "parent-ok"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	groups := [][]string{{"subtask A"}, {"subtask B"}}
	in := bufio.NewReader(strings.NewReader("")) // no per-subtask Feedback is asked
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	err := runSplit(context.Background(), s, parentSID, groups, "big task", "fake-split",
		"implement", "", "", 0, in, &out, false, extractor, nil)
	if err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	if n := countEventsOfTypeInSession(t, s, parentSID, "task.split"); n != 1 {
		t.Errorf("parent should record exactly one task.split, got %d", n)
	}
	parentEvs, err := s.EventsBySession(parentSID)
	if err != nil {
		t.Fatal(err)
	}
	var parentFinished map[string]any
	var sawParentFinished bool
	for _, e := range parentEvs {
		if e.Type == "task.finished" {
			sawParentFinished = true
			parentFinished = e.Payload
		}
	}
	if !sawParentFinished || len(parentFinished) != 0 {
		t.Errorf("parent task.finished should carry no Feedback key (the artifact was the proposal, not work to judge), got %v", parentFinished)
	}

	subSIDs := subtaskSessionIDs(t, s, parentSID)
	if len(subSIDs) != 2 {
		t.Fatalf("expected 2 subtask sessions, got %d", len(subSIDs))
	}
	for i, sid := range subSIDs {
		evs, err := s.EventsBySession(sid)
		if err != nil {
			t.Fatal(err)
		}
		var started, finished map[string]any
		var sawFinished bool
		for _, e := range evs {
			switch e.Type {
			case "task.started":
				started = e.Payload
			case "task.finished":
				sawFinished = true
				finished = e.Payload
			}
		}
		if started == nil || started["parent"] != parentSID {
			t.Errorf("subtask %d task.started.parent = %v, want %q", i, started["parent"], parentSID)
		}
		if !sawFinished || len(finished) != 0 {
			t.Errorf("subtask %d task.finished should be empty (no subjective Feedback, ADR-0028), got %v", i, finished)
		}
	}
}

// TestRunSplitFlattensGroupsAndRecordsIndexGroups pins the Phase-1 execution
// model (ADR-0028): a mixed group proposal is flattened to a single sequential
// run, and task.split records the flat subtasks plus the index groups
// ([[0],[1,2]]) so the independence declaration stays auditable.
func TestRunSplitFlattensGroupsAndRecordsIndexGroups(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "done"})

	const parentSID = "parent-groups"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	groups := [][]string{{"lone"}, {"para A", "para B"}}
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	err := runSplit(context.Background(), s, parentSID, groups, "big task", "fake-split",
		"implement", "", "", 0, bufio.NewReader(strings.NewReader("")), &out, false, extractor, nil)
	if err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	if n := len(subtaskSessionIDs(t, s, parentSID)); n != 3 {
		t.Fatalf("three flattened subtasks should each open a session, got %d", n)
	}

	split := payloadOf(t, s, "task.split") // the parent's is the only one
	subs, ok := split["subtasks"].([]any)
	if !ok || len(subs) != 3 || subs[0] != "lone" || subs[1] != "para A" || subs[2] != "para B" {
		t.Fatalf("task.split.subtasks should be the flat proposal order, got %v", split["subtasks"])
	}
	idx, ok := split["groups"].([]any)
	if !ok || len(idx) != 2 {
		t.Fatalf("task.split.groups should hold two index groups, got %v", split["groups"])
	}
	first, _ := idx[0].([]any)
	second, _ := idx[1].([]any)
	if len(first) != 1 || first[0].(float64) != 0 {
		t.Errorf("group 0 should index [0], got %v", idx[0])
	}
	if len(second) != 2 || second[0].(float64) != 1 || second[1].(float64) != 2 {
		t.Errorf("group 1 should index [1,2], got %v", idx[1])
	}
}

// TestRunSplitFlatProposalOmitsGroups pins declaresGroups' spec (SCHEMA.md R4,
// ADR-0028): a flat proposal (every element a lone subtask) declares no
// independence, so task.split records subtasks but omits the redundant groups
// index — its absence is the audit signal that no parallelism was declared.
func TestRunSplitFlatProposalOmitsGroups(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "done"})

	const parentSID = "parent-flat"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	groups := [][]string{{"one"}, {"two"}}
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}
	if err := runSplit(context.Background(), s, parentSID, groups, "big task", "fake-split",
		"implement", "", "", 0, bufio.NewReader(strings.NewReader("")), &out, false, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	split := payloadOf(t, s, "task.split")
	if _, ok := split["subtasks"]; !ok {
		t.Errorf("subtasks must always be recorded, got %v", split)
	}
	if _, ok := split["groups"]; ok {
		t.Errorf("a flat proposal must omit groups, got %v", split["groups"])
	}
}

// TestSplitProtocolEligible: the protocol rides a vanilla do, but not a plan
// run (a step's output is not the proposal), a human run (no provider stream),
// nor any run when the kill switch is off (config split_protocol=false) —
// ADR-0028 Decision 1, symmetric with duelEligible.
func TestSplitProtocolEligible(t *testing.T) {
	cases := []struct {
		name     string
		enabled  bool
		human    bool
		planName string
		want     bool
	}{
		{"vanilla do", true, false, "", true},
		{"plan run", true, false, "full", false},
		{"human run", true, true, "", false},
		{"kill switch off", false, false, "", false},
		{"kill switch off, otherwise eligible", false, false, "", false},
	}
	for _, c := range cases {
		if got := splitProtocolEligible(c.enabled, c.human, c.planName); got != c.want {
			t.Errorf("%s: splitProtocolEligible(%v,%v,%q) = %v, want %v", c.name, c.enabled, c.human, c.planName, got, c.want)
		}
	}
}

// TestSplitProtocolEnabledDefaultsOnWhenAbsent pins the kill switch's default
// (ADR-0028 Decision 1): a nil pointer (key absent from config) reads as on, so
// a config predating the key never silently disables the always-on protocol; an
// explicit false is the only way off.
func TestSplitProtocolEnabledDefaultsOnWhenAbsent(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })

	cfg = config.Config{SplitProtocol: nil}
	if !splitProtocolEnabled() {
		t.Error("an absent split_protocol key must default to on")
	}
	no := false
	cfg = config.Config{SplitProtocol: &no}
	if splitProtocolEnabled() {
		t.Error("an explicit false must disable the protocol")
	}
	yes := true
	cfg = config.Config{SplitProtocol: &yes}
	if !splitProtocolEnabled() {
		t.Error("an explicit true must enable the protocol")
	}
}

// TestNewExtractorFillsFlagThenConfigThenHardcodedDefault pins the ADR-0029
// Decision 4 precedence: an explicit flag value always wins, an unset one
// falls to the resolved backend's config key, and an unconfigured key falls
// to the ADR-0029 Decision 3 hardcoded default.
func TestNewExtractorFillsFlagThenConfigThenHardcodedDefault(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })

	cfg = config.Config{}
	ex, err := newExtractor("ollama", "", "")
	if err != nil {
		t.Fatal(err)
	}
	o, ok := ex.(*perceive.Ollama)
	if !ok {
		t.Fatalf("backend ollama must build a *perceive.Ollama, got %T", ex)
	}
	if o.URL != defaultOllamaURL || o.Model != defaultOllamaModel {
		t.Errorf("unconfigured ollama must fall to the hardcoded default: %+v", o)
	}

	cfg = config.Config{OllamaURL: "http://cfg:1", OllamaModel: "cfg-model"}
	ex, err = newExtractor("ollama", "", "")
	if err != nil {
		t.Fatal(err)
	}
	o = ex.(*perceive.Ollama)
	if o.URL != "http://cfg:1" || o.Model != "cfg-model" {
		t.Errorf("config must fill an empty flag: %+v", o)
	}

	ex, err = newExtractor("ollama", "http://flag:2", "flag-model")
	if err != nil {
		t.Fatal(err)
	}
	o = ex.(*perceive.Ollama)
	if o.URL != "http://flag:2" || o.Model != "flag-model" {
		t.Errorf("an explicit flag must win over config: %+v", o)
	}

	cfg = config.Config{}
	ex, err = newExtractor("mlx-lm", "", "")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := ex.(*perceive.MLXLM)
	if !ok {
		t.Fatalf("backend mlx-lm must build a *perceive.MLXLM, got %T", ex)
	}
	if m.URL != defaultMLXURL || m.Model != defaultMLXModel {
		t.Errorf("unconfigured mlx-lm must fall to the hardcoded default: %+v", m)
	}
}

// TestNewExtractorEmptyBackendResolvesFromConfig pins that an empty --backend
// hands the choice to cfg.ResolveBackend rather than picking a fixed backend
// itself — newExtractor must not duplicate that decision.
func TestNewExtractorEmptyBackendResolvesFromConfig(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })

	cfg = config.Config{PerceiveBackend: "mlx-lm"}
	ex, err := newExtractor("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ex.(*perceive.MLXLM); !ok {
		t.Errorf("empty --backend must resolve via config, got %T", ex)
	}
}

func TestNewExtractorRejectsAnUnknownBackend(t *testing.T) {
	if _, err := newExtractor("bogus", "", ""); err == nil {
		t.Error("an unknown --backend must error, not silently default")
	}
}

func TestNewExtractorPropagatesAnInvalidConfigBackend(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })

	cfg = config.Config{PerceiveBackend: "bogus"}
	if _, err := newExtractor("", "", ""); err == nil {
		t.Error("an invalid config perceive_backend must error, not silently default")
	}
}

// TestRunSplitStopsAfterAFailedSubtask guards ADR-0023 Decision 4: a failed
// subtask records provider.error and the loop stops before opening the next
// subtask's session — a task that never started must never appear in the
// ledger.
func TestRunSplitStopsAfterAFailedSubtask(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fail-split", &fakeSplitAdapter{name: "fail-split", line: "broken", exitCode: 3})

	const parentSID = "parent-fail"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	groups := [][]string{{"subtask A"}, {"subtask B"}}
	in := bufio.NewReader(strings.NewReader(""))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	err := runSplit(context.Background(), s, parentSID, groups, "big task", "fail-split",
		"implement", "", "", 0, in, &out, false, extractor, nil)
	if err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	subSIDs := subtaskSessionIDs(t, s, parentSID)
	if len(subSIDs) != 1 {
		t.Fatalf("only the first subtask should ever start, got %d sessions: %v", len(subSIDs), subSIDs)
	}
	if n := countEventsOfTypeInSession(t, s, subSIDs[0], "provider.error"); n != 1 {
		t.Errorf("the failed subtask should record provider.error, got %d", n)
	}
}

// TestRunSplitAutoInheritsDecisionEnginePerSubtask guards ADR-0023 Decision
// 3: a parent run with --provider auto has each subtask decided separately.
// The candidate pool is swapped to adapters this test controls — real
// claude-code/codex adapters must never be launched by a unit test — so
// whichever candidate wins, the run stays safe to finish; only the recorded
// tomo.decided is asserted, not who won.
func TestRunSplitAutoInheritsDecisionEnginePerSubtask(t *testing.T) {
	s := openTestStore(t)

	saved := providers
	providers = map[string]executor.Adapter{
		"fake-a": &fakeSplitAdapter{name: "fake-a", line: "did a"},
		"fake-b": &fakeSplitAdapter{name: "fake-b", line: "did b"},
	}
	t.Cleanup(func() { providers = saved })

	const parentSID = "parent-auto"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	groups := [][]string{{"subtask A"}, {"subtask B"}}
	// Enough lines to cover runHuman (1 line per subtask), in case auto ever
	// routes either one to the human candidate — that must not stop the run.
	// No per-subtask Feedback is asked anymore (ADR-0028 Decision 5).
	in := bufio.NewReader(strings.NewReader(strings.Repeat("\n", 8)))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	err := runSplit(context.Background(), s, parentSID, groups, "big task", "auto",
		"implement", "", "", 0, in, &out, false, extractor, nil)
	if err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	subSIDs := subtaskSessionIDs(t, s, parentSID)
	if len(subSIDs) != 2 {
		t.Fatalf("expected 2 subtask sessions, got %d", len(subSIDs))
	}
	for i, sid := range subSIDs {
		if n := countEventsOfTypeInSession(t, s, sid, "tomo.decided"); n != 1 {
			t.Errorf("subtask %d should record tomo.decided under --provider auto, got %d", i, n)
		}
	}
}

// TestRunSplitSharesOneTaskPerceptionHolderAcrossChildren exercises the full
// runSplit → executeSplit → runSubtasksSequential/runGroupParallel →
// openSubtask threading (ADR-0036 Decision 2b), not just openSubtask in
// isolation: both --provider auto subtasks must share the parent's holder,
// so a regression that drops tp anywhere along that chain (e.g. a call site
// silently passing nil instead of threading it through) shows up here as a
// second extraction.
func TestRunSplitSharesOneTaskPerceptionHolderAcrossChildren(t *testing.T) {
	s := openTestStore(t)

	saved := providers
	providers = map[string]executor.Adapter{
		"fake-a": &fakeSplitAdapter{name: "fake-a", line: "did a"},
		"fake-b": &fakeSplitAdapter{name: "fake-b", line: "did b"},
	}
	t.Cleanup(func() { providers = saved })

	const parentSID = "parent-auto-tp"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	groups := [][]string{{"subtask A"}, {"subtask B"}}
	in := bufio.NewReader(strings.NewReader(strings.Repeat("\n", 8)))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}
	fake := &countingTaskExtract{semantic: map[string]string{"lang": "go"}}
	tp := newTaskPerception("big task", fake.fn)

	err := runSplit(context.Background(), s, parentSID, groups, "big task", "auto",
		"implement", "", "", 0, in, &out, false, extractor, tp)
	if err != nil {
		t.Fatalf("runSplit: %v", err)
	}
	if len(subtaskSessionIDs(t, s, parentSID)) != 2 {
		t.Fatalf("expected 2 subtask sessions")
	}
	if fake.calls != 1 {
		t.Errorf("two --provider auto subtasks under one split should share one extraction, got %d calls", fake.calls)
	}
}

// The Decision Engine must read the finest Connection the harness can name
// without guessing (ADR-0013 Decision 2, ADR-0036 Decision 1). size is
// declared by the caller, so a Connection born at that granularity is part of
// the context — not structure the judgment grows and then cannot read.
func TestDecisionReadsTheSizeScopedConnection(t *testing.T) {
	registerFakeProvider(t, "prov-a", &fakeSplitAdapter{name: "prov-a"})
	s := openTestStore(t)
	en := &core.Engine{Repo: s}
	now := time.Now().UnixMilli()
	growCapability(t, s, en, "cap=implement", "prov-a", now, 4, 1)
	// The child Split births once size explains the parent's surprise
	// (ADR-0001/0002): a 2-token scope no single-token match can stand in for.
	child := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=implement|size=large", Target: "prov-a",
		Alpha: 5, Beta: 2, LastUpdate: now, BornTS: now,
		ParentKey: "cap=implement", PriorA: 1, PriorB: 1,
	}
	if err := s.UpsertConnection(child); err != nil {
		t.Fatal(err)
	}

	dec, err := autoDecide(s, io.Discard, "s-decide", "implement", "large", nil)
	if err != nil {
		t.Fatalf("autoDecide: %v", err)
	}
	var got string
	for _, c := range dec.Candidates {
		if c.Provider == "prov-a" {
			got = c.ScopeKey
		}
	}
	if want := "cap=implement|size=large"; got != want {
		t.Errorf("connection read for prov-a = %q, want %q — a size-scoped Connection is unreachable from the decision", got, want)
	}
}

// An absent --size is not a token: Experience.Tokens() skips empty values, so
// "size=" would name a scope no experience can ever have written.
func TestDecisionTokensDropTheUndeclaredSize(t *testing.T) {
	if got, want := decisionTokens("implement", ""), []string{"cap=implement"}; !reflect.DeepEqual(got, want) {
		t.Errorf("decisionTokens(implement, \"\") = %v, want %v", got, want)
	}
	if got, want := decisionTokens("implement", "Large"), []string{"cap=implement", "size=large"}; !reflect.DeepEqual(got, want) {
		t.Errorf("decisionTokens(implement, Large) = %v, want %v", got, want)
	}
}

// ADR-0036 Decision 2c: an extractor's own guess at cap or size must lose to
// what the harness already knows for certain — deciding on a model's guess
// when the machine already has the answer would be strictly worse, never
// better, than the deterministic value.
func TestPerceptionTokensDeterministicTokensWinOverSemanticGuesses(t *testing.T) {
	got := perceptionTokens("implement", "large", []string{"cap=refactor", "size=small", "lang=go"})
	want := []string{"cap=implement", "size=large", "lang=go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// No semantic tokens (a nil holder, or one that never extracted anything)
// must fold to exactly decisionTokens' own output — Task Perception adds,
// it never changes, the deterministic baseline.
func TestPerceptionTokensWithNoSemanticMatchesDecisionTokens(t *testing.T) {
	got := perceptionTokens("implement", "", nil)
	want := decisionTokens("implement", "")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// countingTaskExtract is a taskExtractFunc source that counts its own calls
// and answers with a fixed semantic map or a fixed error — the seam every
// Task Perception test below uses instead of ever reaching a real LLM
// backend.
type countingTaskExtract struct {
	calls    int
	semantic map[string]string
	err      error
}

func (c *countingTaskExtract) fn(string) (map[string]string, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.semantic, nil
}

// TestTaskPerceptionExtractsOnceAndCachesAcrossCalls pins ADR-0036 Decision
// 2b's core guarantee: whichever of the three decision paths asks first
// perceives, and every asker after that — standing in for autoDecide /
// ChoosePlan / pickDuelGap here — reads the identical cached result.
func TestTaskPerceptionExtractsOnceAndCachesAcrossCalls(t *testing.T) {
	fake := &countingTaskExtract{semantic: map[string]string{"lang": "go"}}
	tp := newTaskPerception("fix the go handler", fake.fn)

	want := []string{"lang=go"}
	for i := 0; i < 3; i++ {
		if got := tp.semanticTokens(io.Discard); !reflect.DeepEqual(got, want) {
			t.Errorf("call %d: got %v, want %v", i+1, got, want)
		}
	}
	if fake.calls != 1 {
		t.Errorf("extraction should run once across 3 askers, got %d calls", fake.calls)
	}
}

// A holder nobody asks must never extract (ADR-0036 Decision 2b: the
// cost Decision 2 measured is spent only when a caller actually wants the
// tokens).
func TestTaskPerceptionNeverExtractsIfNeverAsked(t *testing.T) {
	fake := &countingTaskExtract{semantic: map[string]string{"lang": "go"}}
	newTaskPerception("fix the go handler", fake.fn)
	if fake.calls != 0 {
		t.Errorf("a holder nobody asked must never extract, got %d calls", fake.calls)
	}
}

// A failing extractor still lets the decision proceed on Decision 1's
// deterministic tokens, and the failure is confessed exactly once — not
// retried on every later asker, not swallowed into silence (ADR-0009: the
// machine's own channel).
func TestTaskPerceptionDegradesOnceAndLogsOnceOnExtractionFailure(t *testing.T) {
	fake := &countingTaskExtract{err: fmt.Errorf("mlx-lm: connection refused")}
	tp := newTaskPerception("fix it", fake.fn)

	var out bytes.Buffer
	for i := 0; i < 3; i++ {
		if got := tp.semanticTokens(&out); got != nil {
			t.Errorf("call %d: a failed extraction should yield no semantic tokens, got %v", i+1, got)
		}
	}
	if fake.calls != 1 {
		t.Errorf("a failing extractor must still only be tried once per task, got %d calls", fake.calls)
	}
	if n := strings.Count(out.String(), "\n"); n != 1 {
		t.Errorf("degradation must log exactly one operational line across 3 askers, got %d: %q", n, out.String())
	}
	if !strings.Contains(out.String(), "connection refused") {
		t.Errorf("the log line should name the failure reason: %q", out.String())
	}
	if tp.degradedReason() == "" {
		t.Error("degradedReason should carry the failure for the decision audit")
	}
}

// A nil extract func (a taskPerception built with no backend at all — the
// state a holder is in if a future caller ever legitimately omits one) is
// the same "黙って劣化するな" contract as a failing one: a reason is logged
// and recorded, not silently absorbed.
func TestTaskPerceptionDegradesWhenNoExtractorWired(t *testing.T) {
	tp := newTaskPerception("fix it", nil)
	var out bytes.Buffer
	if got := tp.semanticTokens(&out); got != nil {
		t.Errorf("no extractor wired should yield no semantic tokens, got %v", got)
	}
	if out.Len() == 0 {
		t.Error("an unwired extractor must still log once, not silently degrade")
	}
	if tp.degradedReason() == "" {
		t.Error("degradedReason should be non-empty when nothing was wired")
	}
}

// openTask's non-auto path never reaches autoDecide, so a task pinned to a
// concrete provider must never trigger Task Perception at all (ADR-0036
// Decision 2b: "誰も要求しなければ知覚は走らない").
func TestOpenTaskSkipsTaskPerceptionForAnExplicitProvider(t *testing.T) {
	s := openTestStore(t)
	fake := &countingTaskExtract{semantic: map[string]string{"lang": "go"}}
	tp := newTaskPerception("fix the go handler", fake.fn)

	if _, _, _, err := openTask(s, io.Discard, "claude-code", "implement", "", "fix the go handler", tp); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Errorf("an explicit provider never reaches autoDecide, so extraction must not run: got %d calls", fake.calls)
	}
}

// duelOffer returns before ever reading a gap when non-interactive — another
// "nobody asked" path (ADR-0036 Decision 2b), since a headless run has no
// terminal to offer the experiment on in the first place.
func TestDuelOfferNonInteractiveNeverAsksTaskPerception(t *testing.T) {
	s := openTestStore(t)
	fake := &countingTaskExtract{semantic: map[string]string{"lang": "go"}}
	tp := newTaskPerception("fix the go handler", fake.fn)

	duelOffer(s, "implement", "", bufio.NewReader(strings.NewReader("")), io.Discard, false, 1000, tp)
	if fake.calls != 0 {
		t.Errorf("a non-interactive run must never ask Task Perception, got %d calls", fake.calls)
	}
}

// A split child is the parent task's own decomposition, not a second task
// (ADR-0036 Decision 2b): both subtasks below share one holder, and only the
// first to open reaches into it.
func TestOpenSubtaskReusesParentHolderWithoutReperceiving(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-a", &fakeSplitAdapter{name: "fake-a"})
	registerFakeProvider(t, "fake-b", &fakeSplitAdapter{name: "fake-b"})

	fake := &countingTaskExtract{semantic: map[string]string{"lang": "go"}}
	tp := newTaskPerception("split this up", fake.fn)

	if _, _, _, err := openSubtask(s, io.Discard, "auto", "implement", "", "sub one", "parent", tp); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := openSubtask(s, io.Discard, "auto", "implement", "", "sub two", "parent", tp); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Errorf("both children share the parent's holder — extraction should run once, got %d calls", fake.calls)
	}
}

// End to end: a real Connection scoped on a Task Perception token (lang) is
// only reachable once the semantic tokens actually reach decide.Choose, and
// the extractor's own cap/size guesses must be dropped in favor of the
// deterministic ones already known before the run (ADR-0036 Decision 2c).
func TestAutoDecideMergesSemanticTokensWithDeterministicPrecedence(t *testing.T) {
	registerFakeProvider(t, "prov-a", &fakeSplitAdapter{name: "prov-a"})
	s := openTestStore(t)
	en := &core.Engine{Repo: s}
	now := time.Now().UnixMilli()
	growCapability(t, s, en, "cap=implement", "prov-a", now, 4, 1)
	child := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=implement|lang=go", Target: "prov-a",
		Alpha: 9, Beta: 1, LastUpdate: now, BornTS: now,
		ParentKey: "cap=implement", PriorA: 1, PriorB: 1,
	}
	if err := s.UpsertConnection(child); err != nil {
		t.Fatal(err)
	}

	fake := &countingTaskExtract{semantic: map[string]string{"lang": "go", "cap": "refactor", "size": "small"}}
	tp := newTaskPerception("improve the go handler", fake.fn)

	dec, err := autoDecide(s, io.Discard, "s-decide", "implement", "large", tp)
	if err != nil {
		t.Fatalf("autoDecide: %v", err)
	}
	var got string
	for _, c := range dec.Candidates {
		if c.Provider == "prov-a" {
			got = c.ScopeKey
		}
	}
	if want := "cap=implement|lang=go"; got != want {
		t.Errorf("connection read for prov-a = %q, want %q — Task Perception's lang token should reach the decision", got, want)
	}

	rawTokens, ok := payloadOf(t, s, "tomo.decided")["tokens"].([]any)
	if !ok {
		t.Fatal("tomo.decided should record the tokens actually used for the decision (ADR-0036 Decision 2d)")
	}
	gotTokens := make([]string, len(rawTokens))
	for i, tok := range rawTokens {
		gotTokens[i] = tok.(string)
	}
	sort.Strings(gotTokens)
	wantTokens := []string{"cap=implement", "lang=go", "size=large"}
	if !reflect.DeepEqual(gotTokens, wantTokens) {
		t.Errorf("recorded tokens = %v, want %v (the extractor's own cap/size must be dropped)", gotTokens, wantTokens)
	}
}

// A failed extraction must not fail the decision — it proceeds on Decision
// 1's deterministic tokens alone — and the reason is audited on tomo.decided
// so it is measurable, not merely logged and forgotten (ADR-0036 Decision
// 2b/2d).
func TestAutoDecideRecordsPerceptionDegradedReasonWhenExtractionFails(t *testing.T) {
	registerRunnableFakeProvider(t, "fake-runnable")
	s := openTestStore(t)
	fake := &countingTaskExtract{err: fmt.Errorf("mlx-lm: connection refused")}
	tp := newTaskPerception("fix it", fake.fn)

	dec, err := autoDecide(s, io.Discard, "s-decide", "implement", "", tp)
	if err != nil {
		t.Fatalf("a failed extraction must not fail the decision: %v", err)
	}
	if _, ok := providers[dec.Provider]; !ok && dec.Provider != "human" {
		t.Fatalf("decided unregistered provider %q", dec.Provider)
	}

	payload := payloadOf(t, s, "tomo.decided")
	reason, ok := payload["perception_degraded"].(string)
	if !ok || reason == "" {
		t.Errorf("a degraded extraction must be audited on tomo.decided, got %v", payload)
	}
	if !strings.Contains(reason, "connection refused") {
		t.Errorf("the recorded reason should name the failure, got %q", reason)
	}
	tokens, _ := payload["tokens"].([]any)
	if len(tokens) != 1 || tokens[0] != "cap=implement" {
		t.Errorf("a degraded extraction should leave only the deterministic cap token, got %v", tokens)
	}
}

// resolvePlan's auto branch is the same wiring as autoDecide's — this pins
// that ChoosePlan's plan.selected carries the same audit contract (ADR-0036
// Decision 2d), not just the provider-decision path.
func TestResolvePlanRecordsTokensAndDegradedReason(t *testing.T) {
	s := openTestStore(t)
	fake := &countingTaskExtract{err: fmt.Errorf("ollama: connection refused")}
	tp := newTaskPerception("fix it", fake.fn)

	if _, err := resolvePlan(s, "sid", "auto", "implement", "medium", tp); err != nil {
		t.Fatalf("resolvePlan: %v", err)
	}
	payload := payloadOf(t, s, "plan.selected")
	if reason, ok := payload["perception_degraded"].(string); !ok || reason == "" {
		t.Errorf("plan.selected should audit the degraded reason, got %v", payload)
	}
	tokens, ok := payload["tokens"].([]any)
	if !ok || len(tokens) != 2 {
		t.Errorf("plan.selected should record the tokens used (cap+size), got %v", payload["tokens"])
	}
}

// finestMatch's tie-break among equally-specific Connections is lexical on
// scope_key (internal/decide), which today means "cap" always wins — not by
// design, but because it alphabetically precedes every other token key Task
// Perception can now add (ADR-0036 Consequences: c<f<l<s<t). This pins that
// accident so a future vocabulary addition that sorts earlier is caught
// here, not discovered silently in production.
func TestFinestMatchGranularityOneTieBreaksToCapAcrossFullVocabulary(t *testing.T) {
	registerFakeProvider(t, "prov-a", &fakeSplitAdapter{name: "prov-a"})
	s := openTestStore(t)
	now := time.Now().UnixMilli()
	// 判定に届くgranularity-1のConnectionを、cap/size（ADR-0036 Decision 1）と
	// Decision 2が配線するsemantic keyごとに1つずつ置く。key一覧は
	// perceive.SemanticKeysから作る。手書きだと、"cap"より辞書順で前のkeyが
	// 追加された日に、本番の勝者だけが静かに変わってもテストが通ってしまう。
	semanticValue := map[string]string{
		"lang": "go", "framework": "stdlib", "topic": "refactor", "size": "medium",
	}
	scopeKeys := []string{"cap=implement"}
	for _, k := range perceive.SemanticKeys {
		v, ok := semanticValue[k]
		if !ok {
			t.Fatalf("semantic key %q has no fixture value — a new key joined the vocabulary and this test must decide what it means for the tie-break", k)
		}
		scopeKeys = append(scopeKeys, core.CanonToken(k, v))
	}
	for _, scopeKey := range scopeKeys {
		conn := &core.Connection{
			Kind: core.ConnCapability, ScopeKey: scopeKey, Target: "prov-a",
			Alpha: 5, Beta: 1, LastUpdate: now, BornTS: now,
			PriorA: 1, PriorB: 1,
		}
		if err := s.UpsertConnection(conn); err != nil {
			t.Fatal(err)
		}
	}

	fake := &countingTaskExtract{semantic: semanticValue}
	tp := newTaskPerception("refactor the go stdlib usage", fake.fn)

	dec, err := autoDecide(s, io.Discard, "s-decide", "implement", "medium", tp)
	if err != nil {
		t.Fatalf("autoDecide: %v", err)
	}
	var got string
	for _, c := range dec.Candidates {
		if c.Provider == "prov-a" {
			got = c.ScopeKey
		}
	}
	if want := "cap=implement"; got != want {
		t.Errorf("granularity-1 tie-break = %q, want %q (alphabetically first token key among cap/framework/lang/size/topic)", got, want)
	}
}

// Task Perception must be shown the same vocabulary session perception is
// (ADR-0036 Decision 2c/2e): one ranking, one cap, one set of spellings. A
// caller that built its own slice of the known values would make a T_pre /
// T_post divergence unreadable — it could mean the extractor drifted, or just
// that the two calls saw different words.
func TestTaskPerceptionIsShownTheSameVocabularyAsSessionPerception(t *testing.T) {
	s := openTestStore(t)
	empty, err := perceive.Vocab(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != len(perceive.SemanticKeys) {
		t.Errorf("an empty ledger should still cover every semantic key, got %v", empty)
	}

	exp := &core.Experience{
		ID: "exp-1", SessionID: "sess-1", TS: 1000, Kind: core.KindExecution,
		ExtractorVer: extractorVer, ExtractorModel: "none",
		Context:  map[string]string{"lang": "rust", "framework": "axum"},
		Provider: "prov-a", Source: "production",
	}
	if err := s.InsertExperience(exp); err != nil {
		t.Fatal(err)
	}

	got, err := perceive.Vocab(s)
	if err != nil {
		t.Fatal(err)
	}
	if !slicesEqual(got["lang"], []string{"rust"}) {
		t.Errorf("lang vocab = %v, want [rust]", got["lang"])
	}
	if !slicesEqual(got["framework"], []string{"axum"}) {
		t.Errorf("framework vocab = %v, want [axum]", got["framework"])
	}
}

// taskExtractFuncFor is the seam between a taskPerception holder and the real
// perceive.Extractor (ADR-0036 Decision 2c) — this pins that it actually
// calls through to ExtractTaskContext rather than, say, ExtractContext (the
// session-based sibling), using a fake Extractor so no test here reaches a
// real LLM backend.
func TestTaskExtractFuncForCallsExtractTaskContext(t *testing.T) {
	s := openTestStore(t)
	exp := &core.Experience{
		ID: "exp-v", SessionID: "sess-v", TS: 1000, Kind: core.KindExecution,
		ExtractorVer: extractorVer, ExtractorModel: "none",
		Context:  map[string]string{"lang": "rust"},
		Provider: "prov-a", Source: "production",
	}
	if err := s.InsertExperience(exp); err != nil {
		t.Fatal(err)
	}
	// lang=goを返せるのはtask入口だけ、というfixtureにする。
	extractor := &fakePerceiveExtractor{
		semantic:     map[string]string{"lang": "session-not-task"},
		taskSemantic: map[string]string{"lang": "go"},
	}
	extract := taskExtractFuncFor(s, extractor)

	got, err := extract("improve the go handler")
	if err != nil {
		t.Fatal(err)
	}
	if got["lang"] != "go" {
		t.Errorf("got %v, want lang=go — the task entry point, not ExtractContext", got)
	}
	if extractor.taskIntent != "improve the go handler" {
		t.Errorf("intent handed to the extractor = %q, want the task description verbatim", extractor.taskIntent)
	}
	if !slicesEqual(extractor.taskVocab["lang"], []string{"rust"}) {
		t.Errorf("vocab handed to the extractor = %v, want the ledger's known values", extractor.taskVocab)
	}
}
