package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

// seedForgetDB creates a db at a fresh path, seeds it through seed, and closes
// the handle so the command under test opens its own — mirroring how forget /
// amend run as their own process against the file.
func seedForgetDB(t *testing.T, seed func(s *store.Store)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seed(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func reopenForget(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// insertExecution seeds one execution experience and rebuilds so its capability
// connection exists before the command runs.
func insertExecution(t *testing.T, s *store.Store, id, session string, ctx map[string]string, provider string, o core.Outcome) {
	t.Helper()
	e := &core.Experience{
		ID: id, SessionID: session, TS: 1000, Kind: core.KindExecution,
		ExtractorVer: extractorVer, ExtractorModel: "none",
		Context: ctx, Provider: provider, Outcome: o, Source: "production",
	}
	if err := s.InsertExperience(e); err != nil {
		t.Fatal(err)
	}
	if err := (&core.Engine{Repo: s}).Rebuild(); err != nil {
		t.Fatal(err)
	}
}

// TestForgetConfirmedGate (ADR-0033 Decision 2): --yes proceeds without asking,
// a non-interactive run without --yes is refused (never deletes on a default),
// and an interactive y/N is honored either way.
func TestForgetConfirmedGate(t *testing.T) {
	// Non-interactive, no --yes: hard error naming the flag.
	if _, err := forgetConfirmed(bufio.NewReader(strings.NewReader("")), io.Discard, false, false, idList{"e1"}, ""); err == nil ||
		!strings.Contains(err.Error(), "--yes") {
		t.Errorf("non-interactive without --yes must demand --yes, got %v", err)
	}
	// --yes: proceeds without reading.
	if ok, err := forgetConfirmed(bufio.NewReader(strings.NewReader("")), io.Discard, false, true, idList{"e1"}, ""); err != nil || !ok {
		t.Errorf("--yes must proceed, got ok=%v err=%v", ok, err)
	}
	// Interactive yes / no.
	if ok, err := forgetConfirmed(bufio.NewReader(strings.NewReader("y\n")), io.Discard, true, false, idList{"e1"}, ""); err != nil || !ok {
		t.Errorf("interactive y must proceed, got ok=%v err=%v", ok, err)
	}
	if ok, err := forgetConfirmed(bufio.NewReader(strings.NewReader("n\n")), io.Discard, true, false, idList{"e1"}, ""); err != nil || ok {
		t.Errorf("interactive n must abort, got ok=%v err=%v", ok, err)
	}
}

// TestForgetExperienceRebuildsAwayConnection (ADR-0033 Decision 6): forgetting
// the only experience feeding a connection deletes the row and the same command
// rebuilds the projection, so the connection it fed is gone with it.
func TestForgetExperienceRebuildsAwayConnection(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		insertExecution(t, s, "e1", "s1", map[string]string{"lang": "go"}, "claude-code",
			core.Outcome{Adopted: "as-is"})
		if conns, _ := s.AllConnections(); len(conns) == 0 {
			t.Fatal("seed must leave a connection to remove")
		}
	})

	if err := cmdForget([]string{"--db", path, "--id", "e1", "--yes"}); err != nil {
		t.Fatalf("cmdForget: %v", err)
	}

	s := reopenForget(t, path)
	if cur, _ := s.CurrentExperiences(); len(cur) != 0 {
		t.Errorf("the experience must be gone, got %v", cur)
	}
	if conns, _ := s.AllConnections(); len(conns) != 0 {
		t.Errorf("the connection must be rebuilt away, got %v", conns)
	}
}

// TestForgetSessionRemovesEvents (ADR-0033 Decision 2): forget --session erases
// the raw log too — the only verb that reaches events.
func TestForgetSessionRemovesEvents(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		if err := s.AppendEvent("s1", "task.started", 1000, map[string]any{"intent": "x"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("s1", "task.finished", 1100, nil); err != nil {
			t.Fatal(err)
		}
		insertExecution(t, s, "e1", "s1", map[string]string{"lang": "go"}, "claude-code",
			core.Outcome{Adopted: "as-is"})
	})

	if err := cmdForget([]string{"--db", path, "--session", "s1", "--yes"}); err != nil {
		t.Fatalf("cmdForget: %v", err)
	}

	s := reopenForget(t, path)
	if evs, _ := s.EventsBySession("s1"); len(evs) != 0 {
		t.Errorf("session events must be gone, got %v", evs)
	}
	if cur, _ := s.CurrentExperiences(); len(cur) != 0 {
		t.Errorf("session experiences must be gone, got %v", cur)
	}
}

// TestForgetSummaryReportsSupersededRows (ADR-0034 Decision 1/3): forgetting
// a current-generation experience whose (session, kind) has a lower
// generation beneath it sweeps that generation along and reports the swept
// count in the one-line summary — the stdout contract a GUI parses
// (ADR-0033 Decision 6) must not hide the extra cost.
func TestForgetSummaryReportsSupersededRows(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		mustInsert(t, s, &core.Experience{
			ID: "a1", SessionID: "s1", TS: 1000, Kind: core.KindExecution,
			ExtractorVer: 1, ExtractorModel: "none",
			Context: map[string]string{"lang": "go"}, Provider: "claude-code",
			Outcome: core.Outcome{}, Source: "production",
		})
		mustInsert(t, s, &core.Experience{
			ID: "a2", SessionID: "s1", TS: 1000, Kind: core.KindExecution,
			ExtractorVer: 2, ExtractorModel: "none",
			Context: map[string]string{"lang": "go"}, Provider: "claude-code",
			Outcome: core.Outcome{}, Source: "production",
		})
	})

	stdout, _ := captureStdoutStderr(t, func() {
		if err := cmdForget([]string{"--db", path, "--id", "a2", "--yes"}); err != nil {
			t.Fatalf("cmdForget: %v", err)
		}
	})

	if !strings.Contains(stdout, "forgot: 1 experiences (+1 superseded rows)") {
		t.Errorf("stdout must report the swept superseded row, got %q", stdout)
	}
}

// TestForgetRejectsSupersededID (ADR-0034 Decision 2, CLI level): naming a
// superseded row through the CLI is refused just like a nonexistent one —
// the discipline holds at the entrypoint the GUI actually calls, not just at
// the store's own API.
func TestForgetRejectsSupersededID(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		mustInsert(t, s, &core.Experience{
			ID: "a1", SessionID: "s1", TS: 1000, Kind: core.KindExecution,
			ExtractorVer: 1, ExtractorModel: "none",
			Context: map[string]string{"lang": "go"}, Provider: "claude-code",
			Outcome: core.Outcome{}, Source: "production",
		})
		mustInsert(t, s, &core.Experience{
			ID: "a2", SessionID: "s1", TS: 1000, Kind: core.KindExecution,
			ExtractorVer: 2, ExtractorModel: "none",
			Context: map[string]string{"lang": "go"}, Provider: "claude-code",
			Outcome: core.Outcome{}, Source: "production",
		})
	})

	err := cmdForget([]string{"--db", path, "--id", "a1", "--yes"})
	if err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Errorf("a superseded id must be rejected, got %v", err)
	}
}

// TestForgetRejectsBothOrNeitherSelector: --id and --session are exclusive and
// one is required.
func TestForgetRejectsBothOrNeitherSelector(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {})
	if err := cmdForget([]string{"--db", path, "--yes"}); err == nil {
		t.Error("neither --id nor --session must error")
	}
	if err := cmdForget([]string{"--db", path, "--id", "e1", "--session", "s1", "--yes"}); err == nil {
		t.Error("both --id and --session must error")
	}
}

// ---- amend ----

// TestAmendRejectsUnknownContextKey (ADR-0033 Decision 3): the key set is
// closed — a human re-canonicalizes values but cannot invent a key.
func TestAmendRejectsUnknownContextKey(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		insertExecution(t, s, "e1", "s1", map[string]string{"lang": "go"}, "claude-code", core.Outcome{})
	})
	err := cmdAmend([]string{"--db", path, "--id", "e1", "--context", `{"bogus":"x"}`})
	if err == nil || !strings.Contains(err.Error(), "unknown context key") {
		t.Errorf("an unknown context key must error, got %v", err)
	}
}

// TestAmendRejectsEmptyContextValue: a value that canonicalizes to empty is an
// error — dropping a key means omitting it from the JSON (context is a full
// replace).
func TestAmendRejectsEmptyContextValue(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		insertExecution(t, s, "e1", "s1", map[string]string{"lang": "go"}, "claude-code", core.Outcome{})
	})
	err := cmdAmend([]string{"--db", path, "--id", "e1", "--context", `{"lang":"  "}`})
	if err == nil || !strings.Contains(err.Error(), "empty value") {
		t.Errorf("an empty context value must error, got %v", err)
	}
}

// TestAmendRejectsUnknownProvider (ADR-0033 Decision 3 / SCHEMA.md R3): only a
// registered adapter name or human is accepted.
func TestAmendRejectsUnknownProvider(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		insertExecution(t, s, "e1", "s1", map[string]string{"lang": "go"}, "claude-code", core.Outcome{})
	})
	err := cmdAmend([]string{"--db", path, "--id", "e1", "--provider", "gemini"})
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("an unknown provider must error, got %v", err)
	}
}

// TestAmendRejectsUnknownOutcomeField (ADR-0033 Decision 3): outcome decodes
// strictly — a typo'd field is an error, not a silently dropped correction.
func TestAmendRejectsUnknownOutcomeField(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		insertExecution(t, s, "e1", "s1", map[string]string{"lang": "go"}, "claude-code", core.Outcome{})
	})
	err := cmdAmend([]string{"--db", path, "--id", "e1", "--outcome", `{"bogus":true}`})
	if err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Errorf("an unknown outcome field must error, got %v", err)
	}
}

// TestAmendRejectsSupersededID (ADR-0033 Decision 3): only the current
// generation can be amended — a past perception is history.
func TestAmendRejectsSupersededID(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		mustInsert(t, s, &core.Experience{
			ID: "old", SessionID: "s1", TS: 1000, Kind: core.KindExecution,
			ExtractorVer: 1, ExtractorModel: "none",
			Context: map[string]string{"lang": "go"}, Provider: "claude-code",
			Outcome: core.Outcome{}, Source: "production",
		})
		mustInsert(t, s, &core.Experience{
			ID: "new", SessionID: "s1", TS: 1000, Kind: core.KindExecution,
			ExtractorVer: 2, ExtractorModel: "none",
			Context: map[string]string{"lang": "go"}, Provider: "claude-code",
			Outcome: core.Outcome{}, Source: "production",
		})
	})
	err := cmdAmend([]string{"--db", path, "--id", "old", "--outcome", `{"adopted":"as-is"}`})
	if err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Errorf("a superseded id must error, got %v", err)
	}
}

// TestAmendRejectsProviderOnPreference (ADR-0033 Decision 3): preference and
// reflection experiences carry no provider, so --provider on one is an error.
func TestAmendRejectsProviderOnPreference(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		mustInsert(t, s, &core.Experience{
			ID: "p1", SessionID: "s1", TS: 1000, Kind: core.KindPreference,
			ExtractorVer: 1, ExtractorModel: "none",
			Context: map[string]string{}, Outcome: core.Outcome{Preferred: "codex", Over: "claude-code"},
			Source: "learning",
		})
	})
	err := cmdAmend([]string{"--db", path, "--id", "p1", "--provider", "claude-code"})
	if err == nil || !strings.Contains(err.Error(), "execution") {
		t.Errorf("--provider on a preference must error, got %v", err)
	}
}

// TestAmendAppliesNewOutcomeToProjection (ADR-0033 Decision 3/6): a corrected
// outcome becomes the current generation (extractor_model human) and the
// command's rebuild re-derives the projection from it — a reverted result
// amended to adopted flips the connection from failure-leaning to success.
func TestAmendAppliesNewOutcomeToProjection(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		insertExecution(t, s, "e1", "s1", map[string]string{"lang": "go"}, "claude-code",
			core.Outcome{Reverted: true})
		c, _ := s.GetConnection(core.ConnCapability, "lang=go", "claude-code")
		if c == nil || c.Alpha >= c.Beta {
			t.Fatalf("seed must leave a failure-leaning connection, got %+v", c)
		}
	})

	if err := cmdAmend([]string{"--db", path, "--id", "e1", "--outcome", `{"adopted":"as-is"}`}); err != nil {
		t.Fatalf("cmdAmend: %v", err)
	}

	s := reopenForget(t, path)
	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 1 {
		t.Fatalf("current view must hold one amended row, got %d", len(cur))
	}
	got := cur[0]
	if got.Outcome.Adopted != "as-is" || got.Outcome.Reverted {
		t.Errorf("the current outcome must be the amended one, got %+v", got.Outcome)
	}
	if got.ExtractorModel != "human" {
		t.Errorf("the amended row's extractor_model must be human, got %q", got.ExtractorModel)
	}
	if got.ExtractorVer != extractorVer+1 {
		t.Errorf("the amended generation must bump ver to %d, got %d", extractorVer+1, got.ExtractorVer)
	}
	c, _ := s.GetConnection(core.ConnCapability, "lang=go", "claude-code")
	if c == nil || c.Alpha <= c.Beta {
		t.Errorf("the rebuilt projection must reflect the adopted outcome (alpha>beta), got %+v", c)
	}
}

// TestAmendCopiesSiblingsForward (ADR-0033 Decision 3): when a (session, kind)
// has several current-generation rows, amending one carries the whole set to the
// new ver — experiences_current picks the max ver per (session, kind), so a row
// left behind would vanish. The untouched sibling rides forward unchanged and
// keeps its own extractor_model; only the target's becomes human.
func TestAmendCopiesSiblingsForward(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		mustInsert(t, s, &core.Experience{
			ID: "target", SessionID: "s1", TS: 1000, Kind: core.KindExecution,
			ExtractorVer: extractorVer, ExtractorModel: "qwen3:8b",
			Context: map[string]string{"lang": "go"}, Provider: "claude-code",
			Outcome: core.Outcome{Reverted: true}, Source: "production",
		})
		mustInsert(t, s, &core.Experience{
			ID: "sibling", SessionID: "s1", TS: 1000, Kind: core.KindExecution,
			ExtractorVer: extractorVer, ExtractorModel: "qwen3:8b",
			Context: map[string]string{"framework": "axum"}, Provider: "codex",
			Outcome: core.Outcome{Adopted: "as-is"}, Source: "production",
		})
	})

	if err := cmdAmend([]string{"--db", path, "--id", "target", "--outcome", `{"adopted":"as-is"}`}); err != nil {
		t.Fatalf("cmdAmend: %v", err)
	}

	s := reopenForget(t, path)
	cur, err := s.CurrentExperiencesBySessionKind("s1", core.KindExecution)
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 2 {
		t.Fatalf("both siblings must be carried into the current generation, got %d", len(cur))
	}
	var amended, carried *core.Experience
	for _, e := range cur {
		if e.ExtractorModel == "human" {
			amended = e
		} else {
			carried = e
		}
	}
	if amended == nil || carried == nil {
		t.Fatalf("want one human-amended row and one carried sibling, got %+v", cur)
	}
	// (a) the untouched sibling rides forward unchanged and stays in the view.
	if carried.Context["framework"] != "axum" || carried.Provider != "codex" || carried.Outcome.Adopted != "as-is" {
		t.Errorf("carried sibling content must be unchanged, got %+v", carried)
	}
	// (b) it keeps its own extractor_model — only the target's becomes human.
	if carried.ExtractorModel != "qwen3:8b" {
		t.Errorf("carried sibling must keep its origin model, got %q", carried.ExtractorModel)
	}
	if amended.Context["lang"] != "go" || amended.Outcome.Adopted != "as-is" || amended.Outcome.Reverted {
		t.Errorf("the amended row must apply the edit to the target, got %+v", amended)
	}
	if amended.ExtractorVer != extractorVer+1 || carried.ExtractorVer != extractorVer+1 {
		t.Errorf("both rows must share the bumped ver, got %d / %d", amended.ExtractorVer, carried.ExtractorVer)
	}
	var total int
	if err := s.DB.QueryRow(`SELECT count(*) FROM experiences WHERE session_id = 's1'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("the superseded originals must remain as history, want 4 rows, got %d", total)
	}
}

// TestForgetSessionChildNoticeGoesToStderr (ADR-0033 Decision 2/6): the surviving
// -children notice is a human hint on stderr; stdout is reserved for the one-line
// summary a GUI parses, so it must not carry the notice.
func TestForgetSessionChildNoticeGoesToStderr(t *testing.T) {
	path := seedForgetDB(t, func(s *store.Store) {
		if err := s.AppendEvent("parent", "task.started", 1000, map[string]any{"intent": "big"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("parent", "task.finished", 1100, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("childA", "task.started", 1200,
			map[string]any{"intent": "a", "parent": "parent"}); err != nil {
			t.Fatal(err)
		}
	})

	stdout, stderr := captureStdoutStderr(t, func() {
		if err := cmdForget([]string{"--db", path, "--session", "parent", "--yes"}); err != nil {
			t.Fatalf("cmdForget: %v", err)
		}
	})

	if !strings.Contains(stderr, "子セッション") || !strings.Contains(stderr, "childA") {
		t.Errorf("the child notice must go to stderr, got stderr=%q", stderr)
	}
	if strings.Contains(stdout, "子セッション") {
		t.Errorf("stdout must stay the summary contract, not the notice, got stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "forgot: session parent") {
		t.Errorf("stdout must carry the one-line summary, got %q", stdout)
	}
}

// TestParseAmendRejectsTrailingData (ADR-0033 Decision 3): a JSON decoder reads
// only the first value, so `{...},{...}` would silently apply the first and drop
// the rest. Both parsers must reject the trailing data instead.
func TestParseAmendRejectsTrailingData(t *testing.T) {
	if _, err := parseAmendContext(`{"lang":"go"},{"lang":"rust"}`); err == nil ||
		!strings.Contains(err.Error(), "single JSON object") {
		t.Errorf("context with trailing data must error, got %v", err)
	}
	if _, err := parseAmendOutcome(`{"adopted":"as-is"} garbage`); err == nil ||
		!strings.Contains(err.Error(), "single JSON object") {
		t.Errorf("outcome with trailing data must error, got %v", err)
	}
	// A clean single object still parses.
	if _, err := parseAmendContext(`{"lang":"go"}`); err != nil {
		t.Errorf("a single object must parse, got %v", err)
	}
	if _, err := parseAmendOutcome(`{"adopted":"as-is"}`); err != nil {
		t.Errorf("a single object must parse, got %v", err)
	}
}

func mustInsert(t *testing.T, s *store.Store, e *core.Experience) {
	t.Helper()
	if err := s.InsertExperience(e); err != nil {
		t.Fatal(err)
	}
}

// captureStdoutStderr redirects the process stdout/stderr around fn and returns
// what each collected. Output here is a line or two — well under the pipe
// buffer — so a plain ReadAll after close needs no draining goroutine.
func captureStdoutStderr(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	fn()
	os.Stdout, os.Stderr = origOut, origErr
	outW.Close()
	errW.Close()
	ob, _ := io.ReadAll(outR)
	eb, _ := io.ReadAll(errR)
	return string(ob), string(eb)
}
