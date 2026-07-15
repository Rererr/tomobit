package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEventsRejectUpdateAndDelete(t *testing.T) {
	s := openTest(t)
	if err := s.AppendEvent("sess", "task.started", 100, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE events SET ts = 999`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("update should be rejected as append-only, got %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM events`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("delete should be rejected as append-only, got %v", err)
	}
}

func TestAppendEventAssignsPerSessionSequence(t *testing.T) {
	s := openTest(t)
	for _, typ := range []string{"task.started", "capability.started", "task.finished"} {
		if err := s.AppendEvent("A", typ, 100, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendEvent("B", "task.started", 100, nil); err != nil {
		t.Fatal(err)
	}

	a, _ := s.EventsBySession("A")
	if got := []int64{a[0].Seq, a[1].Seq, a[2].Seq}; got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("session A seq: got %v, want [1 2 3]", got)
	}
	b, _ := s.EventsBySession("B")
	if b[0].Seq != 1 {
		t.Errorf("session B seq restarts at 1, got %d", b[0].Seq)
	}
}

func TestPendingSessions(t *testing.T) {
	s := openTest(t)
	// finished session with no experience -> pending.
	mustAppend(t, s, "finished", "task.started", 100)
	mustAppend(t, s, "finished", "task.finished", 200)
	// cancelled session -> pending.
	mustAppend(t, s, "cancelled", "task.cancelled", 300)
	// running session (never finished) -> not pending.
	mustAppend(t, s, "running", "task.started", 400)
	// already-perceived session -> not pending at same ver.
	mustAppend(t, s, "perceived", "task.finished", 500)
	insertExp(t, s, "x1", "perceived", core.KindExecution, 2)

	got, err := s.PendingSessions(2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"finished", "cancelled"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pending: got %v, want %v (events.id order)", got, want)
	}
}

func TestPendingSessionsRequiresExperienceAtOrAboveVersion(t *testing.T) {
	s := openTest(t)
	mustAppend(t, s, "sess", "task.finished", 100)
	insertExp(t, s, "old", "sess", core.KindExecution, 1)

	if got, _ := s.PendingSessions(2); len(got) != 1 || got[0] != "sess" {
		t.Errorf("stale-version session should still be pending, got %v", got)
	}
	if got, _ := s.PendingSessions(1); len(got) != 0 {
		t.Errorf("session perceived at the queried version is not pending, got %v", got)
	}
}

func TestExperiencesRejectUpdateAndDelete(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "e1", "sess", core.KindExecution, 1)
	if _, err := s.DB.Exec(`UPDATE experiences SET ts = 1`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("update should be rejected, got %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM experiences`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("delete should be rejected, got %v", err)
	}
}

func TestCurrentExperiencesKeepsHighestVersionPerSessionAndKind(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "exec-v1", "sess", core.KindExecution, 1)
	insertExp(t, s, "exec-v2", "sess", core.KindExecution, 2)
	insertExp(t, s, "pref-v1", "sess", core.KindPreference, 1)

	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, e := range cur {
		ids[e.ID] = true
	}
	if ids["exec-v1"] {
		t.Error("superseded execution version should be hidden by the view")
	}
	if !ids["exec-v2"] || !ids["pref-v1"] {
		t.Errorf("current view should keep exec-v2 and pref-v1, got %v", ids)
	}
}

func TestInsertAndReadExperienceRoundTrips(t *testing.T) {
	s := openTest(t)
	e := &core.Experience{
		ID: "e1", SessionID: "sess", TS: 12345, Kind: core.KindExecution,
		ExtractorVer: 3, ExtractorModel: "qwen3:8b",
		Context:  map[string]string{"lang": "rust", "cap": "impl"},
		Provider: "claude",
		Outcome:  core.Outcome{Adopted: "as-is", TestsPassed: boolp(true)},
		Source:   "production",
	}
	if err := s.InsertExperience(e); err != nil {
		t.Fatal(err)
	}
	got := currentByID(t, s, "e1")
	if got.SessionID != e.SessionID || got.TS != e.TS || got.Kind != e.Kind ||
		got.ExtractorVer != e.ExtractorVer || got.ExtractorModel != e.ExtractorModel ||
		got.Provider != e.Provider || got.Source != e.Source {
		t.Errorf("scalar mismatch: got %+v", got)
	}
	if got.Context["lang"] != "rust" || got.Context["cap"] != "impl" {
		t.Errorf("context mismatch: got %v", got.Context)
	}
	if got.Outcome.Adopted != "as-is" || got.Outcome.TestsPassed == nil || !*got.Outcome.TestsPassed {
		t.Errorf("outcome mismatch: got %+v", got.Outcome)
	}
}

func TestInsertExperienceStoresEmptyProviderAsNull(t *testing.T) {
	s := openTest(t)
	e := &core.Experience{
		ID: "e1", SessionID: "sess", TS: 1, Kind: core.KindPreference,
		ExtractorVer: 1, ExtractorModel: "none",
		Context: map[string]string{}, Provider: "",
		Outcome: core.Outcome{Preferred: "react", Over: "vue"}, Source: "learning",
	}
	if err := s.InsertExperience(e); err != nil {
		t.Fatal(err)
	}
	var provider any
	if err := s.DB.QueryRow(`SELECT provider FROM experiences WHERE id='e1'`).Scan(&provider); err != nil {
		t.Fatal(err)
	}
	if provider != nil {
		t.Errorf("empty provider should be NULL, got %v", provider)
	}
	if got := currentByID(t, s, "e1"); got.Provider != "" {
		t.Errorf("read-back provider should be empty, got %q", got.Provider)
	}
}

func TestInsertExperiencesIsAtomicOnFailure(t *testing.T) {
	s := openTest(t)
	good := &core.Experience{
		ID: "e1", SessionID: "s", TS: 1, Kind: core.KindExecution,
		ExtractorVer: 1, ExtractorModel: "none",
		Context: map[string]string{}, Outcome: core.Outcome{}, Source: "production",
	}
	bad := &core.Experience{
		ID: "e2", SessionID: "s", TS: 1, Kind: "bogus",
		ExtractorVer: 1, ExtractorModel: "none",
		Context: map[string]string{}, Outcome: core.Outcome{}, Source: "production",
	}

	err := s.InsertExperiences([]*core.Experience{good, bad})
	if err == nil {
		t.Fatal("batch with a CHECK violation should error")
	}
	if !strings.Contains(err.Error(), "e2") || !strings.Contains(err.Error(), "2/2") {
		t.Errorf("error should name the failing position and id, got %v", err)
	}

	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM experiences`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("no rows should survive the rollback, got %d", n)
	}
}

func TestKnownValuesAreDistinctNonEmptyAndSorted(t *testing.T) {
	s := openTest(t)
	insertExpCtx(t, s, "a", "s1", map[string]string{"lang": "rust"})
	insertExpCtx(t, s, "b", "s2", map[string]string{"lang": "go"})
	insertExpCtx(t, s, "c", "s3", map[string]string{"lang": "rust"})
	insertExpCtx(t, s, "d", "s4", map[string]string{"lang": ""})
	insertExpCtx(t, s, "e", "s5", map[string]string{"cap": "impl"})

	got, err := s.KnownValues("lang")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "go,rust" {
		t.Errorf("got %v, want [go rust]", got)
	}
}

func TestConnectionCRUD(t *testing.T) {
	s := openTest(t)
	c := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=rust", Target: "claude",
		Alpha: 2, Beta: 1, LastUpdate: 100, BornTS: 100,
	}
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetConnection(core.ConnCapability, "lang=rust", "claude")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.Alpha != 2 || got.Beta != 1 || got.BornTS != 100 || got.ParentKey != "" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	if err := s.DeleteConnection(core.ConnCapability, "lang=rust", "claude"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetConnection(core.ConnCapability, "lang=rust", "claude"); got != nil {
		t.Error("connection should be gone after delete")
	}
}

func TestUpsertConnectionUpdatesOnlyPosteriorFields(t *testing.T) {
	s := openTest(t)
	first := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=impl|lang=rust", Target: "claude",
		Alpha: 1, Beta: 1, LastUpdate: 100, BornTS: 100, ParentKey: "cap=impl",
	}
	if err := s.UpsertConnection(first); err != nil {
		t.Fatal(err)
	}
	update := *first
	update.Alpha, update.Beta, update.LastUpdate = 5, 3, 200
	update.BornTS, update.ParentKey = 999, "changed"
	if err := s.UpsertConnection(&update); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetConnection(core.ConnCapability, "cap=impl|lang=rust", "claude")
	if got.Alpha != 5 || got.Beta != 3 || got.LastUpdate != 200 {
		t.Errorf("posterior fields should update: %+v", got)
	}
	if got.BornTS != 100 || got.ParentKey != "cap=impl" {
		t.Errorf("born_ts/parent_key must stay from first insert: %+v", got)
	}
}

func TestLedgerCRUDAndClearProjections(t *testing.T) {
	s := openTest(t)
	c := &core.Connection{Kind: core.ConnCapability, ScopeKey: "lang=rust", Target: "claude", Alpha: 1, Beta: 1, LastUpdate: 1, BornTS: 1}
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	entry := &core.LedgerEntry{Kind: core.ConnCapability, ScopeKey: "lang=rust", Target: "claude", ExperienceID: "e1", TS: 10, P: 0.5, Y: 1, SExcess: 0.3}
	if err := s.InsertLedger(entry); err != nil {
		t.Fatal(err)
	}
	got, _ := s.LedgerFor(core.ConnCapability, "lang=rust", "claude")
	if len(got) != 1 || got[0].SExcess != 0.3 {
		t.Fatalf("ledger round-trip: %+v", got)
	}

	if err := s.DeleteLedgerFor(core.ConnCapability, "lang=rust", "claude"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LedgerFor(core.ConnCapability, "lang=rust", "claude"); len(got) != 0 {
		t.Error("ledger should be empty after DeleteLedgerFor")
	}

	if err := s.InsertLedger(entry); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearProjections(); err != nil {
		t.Fatal(err)
	}
	conns, _ := s.AllConnections()
	led, _ := s.LedgerFor(core.ConnCapability, "lang=rust", "claude")
	if len(conns) != 0 || len(led) != 0 {
		t.Errorf("ClearProjections should wipe connections and ledger, got %d conns %d ledger", len(conns), len(led))
	}
}

func TestCheckConstraintsRejectUnknownEnumValues(t *testing.T) {
	s := openTest(t)
	badKind := &core.Experience{ID: "e1", SessionID: "s", TS: 1, Kind: "bogus", ExtractorVer: 1, ExtractorModel: "none", Context: map[string]string{}, Outcome: core.Outcome{}, Source: "production"}
	if err := s.InsertExperience(badKind); err == nil {
		t.Error("invalid experience kind should violate CHECK")
	}
	badSource := &core.Experience{ID: "e2", SessionID: "s", TS: 1, Kind: core.KindExecution, ExtractorVer: 1, ExtractorModel: "none", Context: map[string]string{}, Outcome: core.Outcome{}, Source: "bogus"}
	if err := s.InsertExperience(badSource); err == nil {
		t.Error("invalid experience source should violate CHECK")
	}
	if _, err := s.DB.Exec(`INSERT INTO connections (kind, scope_key, target, alpha, beta, last_update, born_ts)
		VALUES ('bogus','lang=rust','claude',1,1,1,1)`); err == nil {
		t.Error("invalid connection kind should violate CHECK")
	}
	if _, err := s.DB.Exec(`INSERT INTO curiosity_queue (id, created_ts, signal, payload, priority, status)
		VALUES ('q1',1,'sig','{}',1.0,'bogus')`); err == nil {
		t.Error("invalid curiosity status should violate CHECK")
	}
}

// ---- helpers ----

func boolp(b bool) *bool { return &b }

func mustAppend(t *testing.T, s *Store, session, typ string, ts int64) {
	t.Helper()
	if err := s.AppendEvent(session, typ, ts, nil); err != nil {
		t.Fatal(err)
	}
}

func insertExp(t *testing.T, s *Store, id, session, kind string, ver int) {
	t.Helper()
	insertExpFull(t, s, &core.Experience{
		ID: id, SessionID: session, TS: 1, Kind: kind,
		ExtractorVer: ver, ExtractorModel: "none",
		Context: map[string]string{}, Outcome: core.Outcome{}, Source: "production",
	})
}

func insertExpCtx(t *testing.T, s *Store, id, session string, ctx map[string]string) {
	t.Helper()
	insertExpFull(t, s, &core.Experience{
		ID: id, SessionID: session, TS: 1, Kind: core.KindExecution,
		ExtractorVer: 1, ExtractorModel: "none",
		Context: ctx, Provider: "claude", Outcome: core.Outcome{}, Source: "production",
	})
}

func insertExpFull(t *testing.T, s *Store, e *core.Experience) {
	t.Helper()
	if err := s.InsertExperience(e); err != nil {
		t.Fatal(err)
	}
}

func currentByID(t *testing.T, s *Store, id string) *core.Experience {
	t.Helper()
	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cur {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("experience %q not in current view", id)
	return nil
}
