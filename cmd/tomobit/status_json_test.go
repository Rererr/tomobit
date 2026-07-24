package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
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

// statusJSONSoloGrownFixture seeds a single-provider network past the
// calibration gates (10 fresh zero-excess ledger rows on one frequent
// island) but with nobody to contest that island: StageFrom lands on
// わかもの with sharpness 測定不能 (ADR-0045 Decision 1).
func statusJSONSoloGrownFixture(t *testing.T, dbPath string, now int64) {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conn := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=impl", Target: "claude",
		Alpha: 11, Beta: 1, LastUpdate: now, BornTS: now,
	}
	if err := s.UpsertConnection(conn); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := s.InsertLedger(&core.LedgerEntry{
			Kind: core.ConnCapability, ScopeKey: "cap=impl", Target: "claude",
			ExperienceID: fmt.Sprintf("led-%d", i), TS: now, SExcess: 0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		mustInsert(t, s, &core.Experience{
			ID: fmt.Sprintf("exp-%d", i), SessionID: "s1", TS: now, Kind: core.KindExecution,
			Context: map[string]string{"cap": "impl"}, Provider: "claude", Source: "production",
		})
	}
}

// statusJSONPartnerFixture extends the solo network into a full あいぼう:
// a rival with the pairwise preference settled (contested island, still
// sharp) plus human-pair preference evidence above the re-ask ceiling.
func statusJSONPartnerFixture(t *testing.T, dbPath string, now int64) {
	t.Helper()
	statusJSONSoloGrownFixture(t, dbPath, now)
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, conn := range []*core.Connection{
		{Kind: core.ConnCapability, ScopeKey: "cap=impl", Target: "codex",
			Alpha: 11, Beta: 1, LastUpdate: now, BornTS: now},
		{Kind: core.ConnPreference, ScopeKey: "cap=impl", Target: "claude~codex",
			Alpha: 30, Beta: 1, LastUpdate: now, BornTS: now},
		{Kind: core.ConnPreference, ScopeKey: "cap=impl", Target: "claude~human",
			Alpha: 3, Beta: 1, LastUpdate: now, BornTS: now},
	} {
		if err := s.UpsertConnection(conn); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStatusJSONGrowthMatchesFaceDerivation guards ADR-0046 Decision 1 the
// same way the stage/mood tests guard theirs: the growth field must be
// face.GrowthFrom's own evaluation — names, met flags, null-ness, hints —
// not a second derivation that can drift.
func TestStatusJSONGrowthMatchesFaceDerivation(t *testing.T) {
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
	want, err := face.GrowthFrom(s, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	wantNext, ok := want.Next()
	if !ok {
		t.Fatal("fixture unexpectedly at the top stage")
	}

	growth, ok := got["growth"].(map[string]any)
	if !ok {
		t.Fatalf("growth is not an object: %v", got["growth"])
	}
	if next, _ := growth["next"].(float64); int(next) != wantNext {
		t.Errorf("growth.next = %v, want %d", growth["next"], wantNext)
	}
	if growth["next_name"] != face.StageName(wantNext) {
		t.Errorf("growth.next_name = %v, want %q", growth["next_name"], face.StageName(wantNext))
	}
	gates, ok := growth["gates"].([]any)
	if !ok || len(gates) != len(want.Gates) {
		t.Fatalf("growth.gates = %v, want %d gates", growth["gates"], len(want.Gates))
	}
	for i, raw := range gates {
		gate, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("gates[%d] is not an object: %v", i, raw)
		}
		w := want.Gates[i]
		if gate["name"] != w.Name {
			t.Errorf("gates[%d].name = %v, want %q", i, gate["name"], w.Name)
		}
		if gate["met"] != w.Met {
			t.Errorf("gates[%d].met = %v, want %v", i, gate["met"], w.Met)
		}
		if th, _ := gate["threshold"].(float64); th != w.Threshold {
			t.Errorf("gates[%d].threshold = %v, want %v", i, gate["threshold"], w.Threshold)
		}
		if (gate["value"] == nil) != (w.Value == nil) {
			t.Errorf("gates[%d].value = %v, want null-ness %v (face.GrowthFrom)", i, gate["value"], w.Value == nil)
		}
		// 1e-4, not tighter: cmdStatus and this verification call time.Now()
		// a moment apart, and evidence≈4 decays ~3.6e-7/s under the 90-day
		// half-life — 1e-6 flakes on a slow CI while real derivation drift
		// stays orders of magnitude above 1e-4. met/null-ness/name stay exact.
		if v, isNum := gate["value"].(float64); isNum && w.Value != nil && math.Abs(v-*w.Value) > 1e-4 {
			t.Errorf("gates[%d].value = %v, want %v", i, v, *w.Value)
		}
		wantHint := w.Hint()
		gotHint, hasHint := gate["hint"]
		if (wantHint != "") != hasHint || (hasHint && gotHint != wantHint) {
			t.Errorf("gates[%d].hint = %v, want %q (Gate.Hint, omitted when met)", i, gotHint, wantHint)
		}
	}
}

// TestStatusJSONGrowthSharpnessUnmeasurableIsNull guards the line ADR-0046
// calls the implementation's pass/fail: a single-provider network's
// sharpness serializes as an explicit null (測定不能), not a zero or a
// missing key, and its hint is the meeting-a-rival move — not the duel one
// an actually-wobbling lottery would get.
func TestStatusJSONGrowthSharpnessUnmeasurableIsNull(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONSoloGrownFixture(t, dbPath, now)

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)
	if stage, _ := got["stage"].(float64); int(stage) != face.StageYouth {
		t.Fatalf("stage = %v, want わかもの (%d) — fixture drifted", got["stage"], face.StageYouth)
	}

	growth, ok := got["growth"].(map[string]any)
	if !ok {
		t.Fatalf("growth is not an object: %v", got["growth"])
	}
	gates, _ := growth["gates"].([]any)
	var sharpness map[string]any
	for _, raw := range gates {
		if gate, ok := raw.(map[string]any); ok && gate["name"] == face.GateSharpness {
			sharpness = gate
		}
	}
	if sharpness == nil {
		t.Fatalf("no sharpness gate in %v", growth["gates"])
	}
	v, present := sharpness["value"]
	if !present {
		t.Error("sharpness.value key is absent, want an explicit null — 測定不能を沈黙にしない")
	}
	if v != nil {
		t.Errorf("sharpness.value = %v, want null — 測定不能は0でも未達でもない", v)
	}
	if sharpness["met"] != false {
		t.Errorf("sharpness.met = %v, want false", sharpness["met"])
	}
	if sharpness["hint"] != "二人目のProviderに会わせる（autoに任せる）" {
		t.Errorf("sharpness.hint = %v, want the meet-a-rival move (測定不能と未達で一手が違う)", sharpness["hint"])
	}
}

// TestStatusJSONGrowthAbsentAtTheTop guards ADR-0046 Decision 1: あいぼう
// has no next stage, and the payload carries no growth at all rather than
// an all-met list dressed up as 100%.
func TestStatusJSONGrowthAbsentAtTheTop(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONPartnerFixture(t, dbPath, now)

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)
	if stage, _ := got["stage"].(float64); int(stage) != face.StagePartner {
		t.Fatalf("stage = %v, want あいぼう (%d) — fixture drifted", got["stage"], face.StagePartner)
	}
	if _, hasGrowth := got["growth"]; hasGrowth {
		t.Errorf("growth = %v, want the key entirely absent at the top stage", got["growth"])
	}
}

// TestStatusJSONNoLedgerOmitsGrowth guards the absent-ledger contract
// (exists:false carries no other fields, growth included).
func TestStatusJSONNoLedgerOmitsGrowth(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nonexistent", "t.db")
	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)
	if _, hasGrowth := got["growth"]; hasGrowth {
		t.Errorf("growth = %v, want absent when exists:false", got["growth"])
	}
}

// TestStatusHumanViewCarriesNoGrowthNumbers guards ADR-0046 Decision 2: the
// terminal is the voice's place (ADR-0025) — the human view gains neither
// gate names nor next-move templates; whoever wants the numbers has the
// machine view.
func TestStatusHumanViewCarriesNoGrowthNumbers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONSoloGrownFixture(t, dbPath, now)

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "human"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	for _, banned := range []string{face.GateCalibrationSample, face.GateSharpness, "二人目のProviderに会わせる", "もっと一緒に仕事をする"} {
		if strings.Contains(out, banned) {
			t.Errorf("human status output contains %q — 端末は声の場 (ADR-0025)", banned)
		}
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
