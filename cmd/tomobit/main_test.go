package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/subtask"
)

func TestAdoptionPayloadOneMeansAsIs(t *testing.T) {
	got := adoptionPayload(bufio.NewReader(strings.NewReader("1\n")), io.Discard)
	if got["adopted"] != "as-is" || got["reverted"] != false {
		t.Errorf("1: got %v", got)
	}
}

func TestAdoptionPayloadTwoMeansWithEdits(t *testing.T) {
	got := adoptionPayload(bufio.NewReader(strings.NewReader("2\n")), io.Discard)
	if got["adopted"] != "with-edits" || got["reverted"] != false {
		t.Errorf("2: got %v", got)
	}
}

func TestAdoptionPayloadThreeMeansReverted(t *testing.T) {
	got := adoptionPayload(bufio.NewReader(strings.NewReader("3\n")), io.Discard)
	if got["adopted"] != "" || got["reverted"] != true {
		t.Errorf("3: got %v", got)
	}
}

// TestAdoptionPayloadEnterCarriesNoSignal pins the deliberate default (案A):
// a bare Enter is "まだ言えない", not top-grade praise — the ledger learns
// nothing rather than being inflated by a mindless keypress.
func TestAdoptionPayloadEnterCarriesNoSignal(t *testing.T) {
	got := adoptionPayload(bufio.NewReader(strings.NewReader("\n")), io.Discard)
	if len(got) != 0 {
		t.Errorf("Enter: got %v, want empty payload", got)
	}
}

func TestAdoptionPayloadUnknownCarriesNoSignal(t *testing.T) {
	got := adoptionPayload(bufio.NewReader(strings.NewReader("x\n")), io.Discard)
	if len(got) != 0 {
		t.Errorf("unknown: got %v, want empty payload", got)
	}
}

// TestAdoptionPayloadEOFCarriesNoSignal guards against EOF (non-interactive
// stdin, e.g. a headless invocation with no terminal attached) fabricating a
// verdict nobody confirmed — it must stay an empty payload, never a grade.
func TestAdoptionPayloadEOFCarriesNoSignal(t *testing.T) {
	got := adoptionPayload(bufio.NewReader(strings.NewReader("")), io.Discard)
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

// TestAutoDecideRecordsReplayableSeed (ADR-0012 Decision 5): the decision
// audit lands in events with the seed as a string — UnixNano does not fit
// JSON's exact float64 integers, and a rounded seed cannot replay.
func TestAutoDecideRecordsReplayableSeed(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AppendEvent("sess", "task.started", 1, nil); err != nil {
		t.Fatal(err)
	}

	dec, err := autoDecide(s, "sess", "implement", "large")
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
	// Every registered adapter plus human (ADR-0018 Decision 2) is audited.
	if cands, ok := decided["candidates"].([]any); !ok || len(cands) != len(providers)+1 {
		t.Errorf("payload should audit every candidate, got %v", decided["candidates"])
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

// TestSplitFlagRejectsPlanCombination and TestSplitFlagRejectsHumanProvider
// guard ADR-0023 Decision 3's mutually-exclusive combinations. cmdDo checks
// these before the store is even opened, so a plain function call — no flag
// parsing, no DB — is enough to exercise the same condition it evaluates.
func TestSplitFlagRejectsPlanCombination(t *testing.T) {
	if err := splitCombinationError(true, "auto", "full"); err == nil {
		t.Error("--split with --plan should be rejected")
	}
	if err := splitCombinationError(true, "auto", ""); err != nil {
		t.Errorf("--split alone should be fine, got %v", err)
	}
}

func TestSplitFlagRejectsHumanProvider(t *testing.T) {
	if err := splitCombinationError(true, "human", ""); err == nil {
		t.Error("--split with --provider human should be rejected")
	}
	if err := splitCombinationError(true, "claude-code", ""); err != nil {
		t.Errorf("--split with an explicit non-human provider should be fine, got %v", err)
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
	subs, err := subtask.Parse(strings.Join(texts, "\n"))
	if err != nil {
		t.Fatalf("joined collection should parse as a proposal: %v", err)
	}
	if len(subs) != 2 || subs[0] != "part one" {
		t.Fatalf("got %v, want the proposed subtasks", subs)
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
// path (ADR-0023 Decision 5): the parent gets task.split and an
// adoption-free task.finished, while each subtask is its own session, linked
// to the parent, with its own adoption confirmation.
func TestRunSplitNormalFlowRecordsParentAndPerSubtaskLedger(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "done"})

	const parentSID = "parent-ok"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	subs := []string{"subtask A", "subtask B"}
	in := bufio.NewReader(strings.NewReader("1\n1\n")) // adoption "1"=文句なし, once per subtask
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	err := runSplit(context.Background(), s, parentSID, subs, "big task", "fake-split",
		"implement", "", "", 0, in, &out, extractor)
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
		t.Errorf("parent task.finished should carry no adoption key (the artifact was the proposal, not work to judge), got %v", parentFinished)
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
		for _, e := range evs {
			switch e.Type {
			case "task.started":
				started = e.Payload
			case "task.finished":
				finished = e.Payload
			}
		}
		if started == nil || started["parent"] != parentSID {
			t.Errorf("subtask %d task.started.parent = %v, want %q", i, started["parent"], parentSID)
		}
		if finished == nil || finished["adopted"] != "as-is" {
			t.Errorf("subtask %d task.finished.adopted = %v, want \"as-is\"", i, finished["adopted"])
		}
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

	subs := []string{"subtask A", "subtask B"}
	in := bufio.NewReader(strings.NewReader("\n\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	err := runSplit(context.Background(), s, parentSID, subs, "big task", "fail-split",
		"implement", "", "", 0, in, &out, extractor)
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

	subs := []string{"subtask A", "subtask B"}
	// Enough lines to cover runHuman (1 line) + adoptionPayload (1 line) for
	// both subtasks, in case auto ever routes either one to the human
	// candidate — that must not stop the run.
	in := bufio.NewReader(strings.NewReader(strings.Repeat("\n", 8)))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	err := runSplit(context.Background(), s, parentSID, subs, "big task", "auto",
		"implement", "", "", 0, in, &out, extractor)
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
