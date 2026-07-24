package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/face"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

// statusJSONFixture seeds one capability Connection with enough evidence to
// clear StageFrom's S2 quantity gate but no ledger entries, so isCalibrated
// (undefined without ledger data) reliably lands the stage at StageChild —
// a value stable across the few milliseconds between seeding and the json
// view's own time.Now() call, since HalfLifeMs is measured in days.
func statusJSONFixture(t *testing.T, dbPath string, now int64) {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conn := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=go", Target: "claude-code",
		Alpha: 5, Beta: 1, LastUpdate: now, BornTS: now,
	}
	if err := s.UpsertConnection(conn); err != nil {
		t.Fatal(err)
	}
}

func decodeStatusJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--view json output is not valid JSON: %v (%q)", err, out)
	}
	return got
}

// TestStatusJSONMatchesFaceDerivationWhenLedgerExists guards ADR-0039
// Decision 1: the GUI drops its 570-line stage.go port and trusts this view
// only if it is the same derivation `face` itself produces — not a
// reimplementation that can drift.
func TestStatusJSONMatchesFaceDerivationWhenLedgerExists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONFixture(t, dbPath, now)

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wantStage, err := face.StageFrom(s, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	wantStageName := face.StageName(wantStage)

	if got["type"] != "status" {
		t.Errorf(`type = %v, want "status"`, got["type"])
	}
	if got["exists"] != true {
		t.Errorf("exists = %v, want true", got["exists"])
	}
	if gotStage, ok := got["stage"].(float64); !ok || int(gotStage) != wantStage {
		t.Errorf("stage = %v, want %d (face.StageFrom)", got["stage"], wantStage)
	}
	if got["stage_name"] != wantStageName {
		t.Errorf("stage_name = %v, want %q (face.StageName)", got["stage_name"], wantStageName)
	}
}

// TestStatusJSONMoodMatchesFaceMood guards the mood field against the same
// drift risk as stage: it must be face.Mood's own output over the same
// per-Connection states showStatus computes, not a second opinion.
func TestStatusJSONMoodMatchesFaceMood(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONFixture(t, dbPath, now)

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nowVerify := time.Now().UnixMilli()
	conns, err := s.AllConnections()
	if err != nil {
		t.Fatal(err)
	}
	en := &core.Engine{Repo: s}
	states := make([]string, len(conns))
	cands := make([]voice.Candidate, len(conns))
	for i, c := range conns {
		sum, err := en.LedgerSum(c, nowVerify)
		if err != nil {
			t.Fatal(err)
		}
		states[i] = c.State(nowVerify, sum)
		cands[i] = voice.Candidate{Conn: c, State: states[i], LedgerSum: sum}
	}
	wantName, wantMarker := face.Mood(states)

	mood, ok := got["mood"].(map[string]any)
	if !ok {
		t.Fatalf("mood is not an object: %v", got["mood"])
	}
	if mood["name"] != wantName {
		t.Errorf("mood.name = %v, want %q (face.Mood)", mood["name"], wantName)
	}
	if mood["marker"] != wantMarker {
		t.Errorf("mood.marker = %v, want %q (face.Mood)", mood["marker"], wantMarker)
	}

	wantSpeak, wantOK := voice.Suggest(cands, nowVerify)
	gotSpeak, hasSpeak := got["speak"]
	if wantOK != hasSpeak {
		t.Errorf("speak presence = %v, want %v (voice.Suggest ok)", hasSpeak, wantOK)
	}
	if wantOK && gotSpeak != wantSpeak {
		t.Errorf("speak = %v, want %q (voice.Suggest)", gotSpeak, wantSpeak)
	}
}

// TestStatusJSONNoLedgerReportsAbsenceWithoutCreatingOne guards ADR-0039
// Decision 1's "台帳が無ければ作らない": a machine reader merely looking at
// status must not be the reason a ledger — and its parent directory — comes
// into being. openStore's MkdirAll+store.Open would otherwise do exactly
// that, unlike the human view which has always been fine drawing one.
func TestStatusJSONNoLedgerReportsAbsenceWithoutCreatingOne(t *testing.T) {
	dbDir := filepath.Join(t.TempDir(), "nonexistent")
	dbPath := filepath.Join(dbDir, "t.db")

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)
	if got["exists"] != false {
		t.Errorf("exists = %v, want false", got["exists"])
	}
	if _, hasStage := got["stage"]; hasStage {
		t.Errorf("absent ledger must carry no other fields, got stage=%v", got["stage"])
	}
	if _, hasMood := got["mood"]; hasMood {
		t.Errorf("absent ledger must carry no other fields, got mood=%v", got["mood"])
	}

	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("--view json must not create the database file, os.Stat err = %v", err)
	}
	if _, err := os.Stat(dbDir); !os.IsNotExist(err) {
		t.Errorf("--view json must not create the database's parent directory, os.Stat err = %v", err)
	}
}

// TestStatusJSONDoesNotRecordTheReturnGreeting guards ADR-0039 Decision 1's
// "機械viewは対人ではない": a return absence old enough to make the human view
// speak `おかえり` and record tomo.greeted (ADR-0019 Decision 2) must pass
// through --view json silently, since nobody is there to greet.
func TestStatusJSONDoesNotRecordTheReturnGreeting(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	last := now - 4*24*3600*1000 // four quiet days clears the 72h absence gate
	statusJSONFixture(t, dbPath, last)

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent("old", "task.finished", last, nil); err != nil {
		t.Fatal(err)
	}
	s.Close()

	captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})

	s2, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// The greeting is recorded under a fresh session id (greetIfReturned uses
	// store.NewID), so a per-session query would pass no matter what the code
	// does — only a cross-session scan can catch the event sneaking back in.
	evs, err := s2.EventsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == "tomo.greeted" {
			t.Errorf("--view json must not record tomo.greeted, got event %+v", e)
		}
	}
}

// TestStatusJSONProvidersMatchesTheAggregationFunction guards this feature's
// Decision 3: `status --view json` must carry the same providers rows
// providerUsageSummary itself produces over experiences_current — no second,
// hand-rolled reduction for the machine view to drift from.
func TestStatusJSONProvidersMatchesTheAggregationFunction(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONFixture(t, dbPath, now)

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	mustInsert(t, s, &core.Experience{
		ID: "e1", SessionID: "s1", TS: now, Kind: core.KindExecution,
		Provider: "claude-code", Source: "production", Outcome: core.Outcome{Verdict: "up"},
	})
	mustInsert(t, s, &core.Experience{
		ID: "e2", SessionID: "s2", TS: now, Kind: core.KindExecution,
		Provider: "", Source: "production", Outcome: core.Outcome{Cancelled: true}, // duel no-signal row
	})
	s.Close()

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)

	providers, ok := got["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("providers = %v, want exactly one row (provider=\"\" excluded)", got["providers"])
	}
	row, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatalf("providers[0] is not an object: %v", providers[0])
	}
	if row["provider"] != "claude-code" || row["runs"] != float64(1) {
		t.Errorf("providers[0] = %v, want provider=claude-code runs=1", row)
	}
	if row["success"] != float64(1) || row["scored"] != float64(1) {
		t.Errorf("providers[0] = %v, want success=1 scored=1 (a verdict=up run)", row)
	}
}

// TestStatusJSONOmitsProvidersWhenNoUsageExists guards the field's
// omitempty contract: a ledger with connections but no execution experience
// yet must not carry an empty providers array.
func TestStatusJSONOmitsProvidersWhenNoUsageExists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONFixture(t, dbPath, now)

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)
	if _, hasProviders := got["providers"]; hasProviders {
		t.Errorf("providers = %v, want the key entirely absent", got["providers"])
	}
}

// TestStatusJSONNoLedgerOmitsProviders guards the absent-ledger contract
// (exists:false carries no other fields, providers included).
func TestStatusJSONNoLedgerOmitsProviders(t *testing.T) {
	dbDir := filepath.Join(t.TempDir(), "nonexistent")
	dbPath := filepath.Join(dbDir, "t.db")

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)
	if _, hasProviders := got["providers"]; hasProviders {
		t.Errorf("providers = %v, want absent when exists:false", got["providers"])
	}
}

// TestStatusJSONRejectsUnknownView guards the flag's closed vocabulary
// (ADR-0039 Decision 1: human default, json the only machine view).
func TestStatusJSONRejectsUnknownView(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	captureStdoutStderr(t, func() {
		err := cmdStatus([]string{"--db", dbPath, "--view", "yaml"})
		if err == nil {
			t.Error("cmdStatus with --view yaml should error, got nil")
		}
	})
}
