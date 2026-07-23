package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestRecentProviderCosts pins the sample the ADR-0028 gate's cost estimate
// draws from: only provider.finished events with a cost_usd (claude-code
// reports it, codex does not — a null must be excluded), newest first, capped
// at the limit.
func TestRecentProviderCosts(t *testing.T) {
	s := openTest(t)

	// Oldest to newest, interleaved with cost-less finishes (codex) that must
	// not appear in the sample.
	must := func(sid string, ts int64, payload map[string]any) {
		if err := s.AppendEvent(sid, "provider.finished", ts, payload); err != nil {
			t.Fatal(err)
		}
	}
	must("a", 100, map[string]any{"cost_usd": 0.10})
	must("b", 200, map[string]any{"duration_ms": 5}) // codex: no cost_usd → excluded
	must("c", 300, map[string]any{"cost_usd": 0.30})
	must("d", 400, map[string]any{"cost_usd": 0.20})

	got, err := s.RecentProviderCosts(20)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0.20, 0.30, 0.10} // newest first, the cost-less one dropped
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v (null cost excluded)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %v, want %v (newest first)", i, got[i], want[i])
		}
	}

	// The limit caps how many are returned, keeping the newest.
	capped, err := s.RecentProviderCosts(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 || capped[0] != 0.20 || capped[1] != 0.30 {
		t.Errorf("limit 2 should keep the two newest [0.20 0.30], got %v", capped)
	}

	// An empty ledger yields no sample — the gate then honestly says "概算なし".
	empty, err := openTest(t).RecentProviderCosts(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("no provider.finished should yield no sample, got %v", empty)
	}
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

func TestLastEventTSFiltersByType(t *testing.T) {
	s := openTest(t)
	mustAppend(t, s, "sess", "task.started", 100)
	mustAppend(t, s, "sess", "tomo.asked", 200)
	mustAppend(t, s, "sess", "task.finished", 300)

	ts, found, err := s.LastEventTS("tomo.asked")
	if err != nil {
		t.Fatal(err)
	}
	if !found || ts != 200 {
		t.Errorf("got ts=%d found=%v, want ts=200 found=true (other types must not leak in)", ts, found)
	}
}

func TestLastEventTSPicksMaxAcrossSessions(t *testing.T) {
	s := openTest(t)
	mustAppend(t, s, "a", "tomo.asked", 100)
	mustAppend(t, s, "b", "tomo.asked", 300)
	mustAppend(t, s, "c", "tomo.asked", 200)

	ts, found, err := s.LastEventTS("tomo.asked")
	if err != nil {
		t.Fatal(err)
	}
	if !found || ts != 300 {
		t.Errorf("got ts=%d found=%v, want the max ts=300 across every tomo.asked", ts, found)
	}
}

func TestLastEventTSNotFoundWhenTypeAbsent(t *testing.T) {
	s := openTest(t)
	mustAppend(t, s, "sess", "task.started", 100)

	ts, found, err := s.LastEventTS("tomo.asked")
	if err != nil {
		t.Fatal(err)
	}
	if found || ts != 0 {
		t.Errorf("got ts=%d found=%v, want found=false for a type with no events", ts, found)
	}
}

// TestNewIDTSPrefixIsFixedWidthHex pins a format detail internal/core.Engine
// relies on implicitly: its (ts, id) ordering compares id as a plain Go
// string, and that only agrees with numeric ts order if every id's ts
// prefix is padded to the same width — an unpadded %x would let ts=1
// ("1-...") sort after ts=16 ("10-...") lexicographically despite being
// numerically smaller.
func TestNewIDTSPrefixIsFixedWidthHex(t *testing.T) {
	prefix, _, ok := strings.Cut(NewID(42), "-")
	if !ok {
		t.Fatalf("NewID must contain a '-' separating the ts prefix from the random suffix, got %q", NewID(42))
	}
	if len(prefix) != 13 {
		t.Errorf("ts prefix must be zero-padded to a fixed 13 hex digits (room for ts up to 2^52ms, far past the store's lifetime), got %q (%d chars) for ts=42", prefix, len(prefix))
	}
}

// TestNewIDLexicographicOrderMatchesNumericTSOrder pins the guarantee the
// fixed-width prefix exists for: engine.go's (ts, id) comparisons and SQL's
// `ORDER BY ts, id` both assume Go/SQLite string order agrees with numeric
// ts order.
func TestNewIDLexicographicOrderMatchesNumericTSOrder(t *testing.T) {
	earlier, later := NewID(1), NewID(1_700_000_000_000)
	if !(earlier < later) {
		t.Errorf("string order must agree with numeric ts order: NewID(1)=%q should sort before NewID(1_700_000_000_000)=%q", earlier, later)
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

func TestKnownValuesExcludeSupersededExtractorVersions(t *testing.T) {
	s := openTest(t)
	insertExpFull(t, s, &core.Experience{
		ID: "a", SessionID: "s1", TS: 1, Kind: core.KindExecution,
		ExtractorVer: 1, ExtractorModel: "none",
		Context: map[string]string{"framework": "rust"}, Provider: "claude",
		Outcome: core.Outcome{}, Source: "production",
	})
	insertExpFull(t, s, &core.Experience{
		ID: "b", SessionID: "s1", TS: 1, Kind: core.KindExecution,
		ExtractorVer: 2, ExtractorModel: "none",
		Context: map[string]string{"framework": "axum"}, Provider: "claude",
		Outcome: core.Outcome{}, Source: "production",
	})

	got, err := s.KnownValues("framework")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "axum" {
		t.Errorf("superseded vocabulary should not be re-suggested: got %v, want [axum]", got)
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

// TestConnectionPriorRoundTripAndImmutability (ADR-0013): the inherited
// prior persists, and a later posterior upsert can never rewrite it — the
// prior is set at birth, full stop.
func TestConnectionPriorRoundTripAndImmutability(t *testing.T) {
	s := openTest(t)
	first := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=impl|lang=rust", Target: "claude",
		Alpha: 1.6, Beta: 0.4, LastUpdate: 100, BornTS: 100, ParentKey: "cap=impl",
		PriorA: 1.6, PriorB: 0.4,
	}
	if err := s.UpsertConnection(first); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetConnection(core.ConnCapability, "cap=impl|lang=rust", "claude")
	if got.PriorA != 1.6 || got.PriorB != 0.4 {
		t.Errorf("prior round-trip mismatch: %+v", got)
	}

	update := *first
	update.Alpha, update.Beta, update.LastUpdate = 5, 3, 200
	update.PriorA, update.PriorB = 9, 9
	if err := s.UpsertConnection(&update); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConnection(core.ConnCapability, "cap=impl|lang=rust", "claude")
	if got.PriorA != 1.6 || got.PriorB != 0.4 {
		t.Errorf("prior must stay from first insert: %+v", got)
	}

	// A parentless row that never set its prior reads back as the blank
	// Beta(1,1) via the schema default.
	root := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=go", Target: "claude",
		Alpha: 2, Beta: 1, LastUpdate: 100, BornTS: 100,
	}
	if err := s.UpsertConnection(root); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetConnection(core.ConnCapability, "lang=go", "claude")
	if pa, pb := got.Prior(); pa != core.PriorAlpha || pb != core.PriorBeta {
		t.Errorf("parentless prior should be Beta(1,1), got (%v,%v)", pa, pb)
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

// ---- the organ of forgetting (ADR-0033) ----

// TestForgetTriggerDDLMatchesSchema proves the recreated append-only guards are
// byte-identical to the ones Open installs (ADR-0033 Decision 5: schema定数と
// 同一DDL) — the drift guard the forget transaction's correctness rests on.
func TestForgetTriggerDDLMatchesSchema(t *testing.T) {
	if !strings.Contains(schema, eventsNoDeleteTrigger) {
		t.Error("eventsNoDeleteTrigger must match the schema's events_no_delete DDL verbatim")
	}
	if !strings.Contains(schema, experiencesNoDeleteTrigger) {
		t.Error("experiencesNoDeleteTrigger must match the schema's experiences_no_delete DDL verbatim")
	}
}

// TestForgetExperiences (ADR-0033 Decision 4/5): named experiences vanish, each
// affected session gets a user.forgot marker carrying only its own deleted ids
// at the next seq, and the append-only guard is back — a raw DELETE is rejected
// again, proving the trigger was recreated inside the transaction.
func TestForgetExperiences(t *testing.T) {
	s := openTest(t)
	mustAppend(t, s, "s1", "task.started", 100)
	mustAppend(t, s, "s1", "task.finished", 200)
	insertExp(t, s, "e1", "s1", core.KindExecution, 1)
	insertExp(t, s, "e2", "s1", core.KindPreference, 1)
	insertExp(t, s, "e3", "s2", core.KindExecution, 1)

	named, superseded, err := s.ForgetExperiences(500, []string{"e1", "e3"})
	if err != nil {
		t.Fatal(err)
	}
	if named != 2 || superseded != 0 {
		t.Errorf("got named=%d superseded=%d, want named=2 superseded=0 (no lower generations to sweep)", named, superseded)
	}

	remaining := map[string]bool{}
	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cur {
		remaining[e.ID] = true
	}
	if remaining["e1"] || remaining["e3"] {
		t.Errorf("forgotten experiences must be gone, got %v", remaining)
	}
	if !remaining["e2"] {
		t.Error("an untouched experience must survive")
	}

	// s1's marker carries only e1, at the seq after its two events.
	s1ev := lastEventOfType(t, s, "s1", "user.forgot")
	if s1ev.Seq != 3 {
		t.Errorf("s1 user.forgot seq: got %d, want 3 (after task.started/finished)", s1ev.Seq)
	}
	if got := forgotIDs(t, s1ev.Payload); strings.Join(got, ",") != "e1" {
		t.Errorf("s1 marker ids: got %v, want [e1] (only that session's deletion)", got)
	}
	// s2 had no prior events, so its marker starts the sequence.
	s2ev := lastEventOfType(t, s, "s2", "user.forgot")
	if s2ev.Seq != 1 {
		t.Errorf("s2 user.forgot seq: got %d, want 1", s2ev.Seq)
	}
	if got := forgotIDs(t, s2ev.Payload); strings.Join(got, ",") != "e3" {
		t.Errorf("s2 marker ids: got %v, want [e3]", got)
	}

	if _, err := s.DB.Exec(`DELETE FROM experiences WHERE id = 'e2'`); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Errorf("the append-only guard must be recreated after forget, got %v", err)
	}
}

// TestForgetExperiencesUnknownIDRollsBackEverything (ADR-0033 Decision 5): one
// missing id aborts the whole batch — no row deleted, no marker written — so a
// typo can never forge a partial "忘れたつもり".
func TestForgetExperiencesUnknownIDRollsBackEverything(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "e1", "s1", core.KindExecution, 1)

	_, _, err := s.ForgetExperiences(500, []string{"e1", "ghost"})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("a missing id must error naming it, got %v", err)
	}

	var exps int
	if err := s.DB.QueryRow(`SELECT count(*) FROM experiences`).Scan(&exps); err != nil {
		t.Fatal(err)
	}
	if exps != 1 {
		t.Errorf("no experience may be deleted on rollback, got %d", exps)
	}
	if n := countType(t, s, "user.forgot"); n != 0 {
		t.Errorf("no marker may be written on rollback, got %d", n)
	}
}

// TestForgetExperiencesDeduplicatesIDs (ADR-0033): a repeated --id counts once
// and the marker lists it once — no double-delete illusion, no duplicated ids.
func TestForgetExperiencesDeduplicatesIDs(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "e1", "s1", core.KindExecution, 1)

	named, superseded, err := s.ForgetExperiences(500, []string{"e1", "e1"})
	if err != nil {
		t.Fatal(err)
	}
	if named != 1 || superseded != 0 {
		t.Errorf("a duplicate id must count once, got named=%d superseded=%d", named, superseded)
	}
	ev := lastEventOfType(t, s, "s1", "user.forgot")
	if got := forgotIDs(t, ev.Payload); strings.Join(got, ",") != "e1" {
		t.Errorf("marker ids must be deduplicated, got %v", got)
	}
}

// TestForgetSweepsLowerGenerationsInSameSessionKind (ADR-0034 Decision 1):
// forgetting the current generation's row also removes every row of the same
// (session, kind) at a lower extractor_ver — otherwise experiences_current's
// max(extractor_ver) selection would fall through to the superseded
// generation the moment its successor is gone, resurrecting a perception the
// owner had already moved past. A current sibling that was not named
// survives, and the marker names only the id the caller gave, not the rows
// swept along with it.
func TestForgetSweepsLowerGenerationsInSameSessionKind(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "a1", "sess", core.KindExecution, 1)
	insertExp(t, s, "b1", "sess", core.KindExecution, 1)
	insertExp(t, s, "a2", "sess", core.KindExecution, 2)
	insertExp(t, s, "b2", "sess", core.KindExecution, 2)

	named, superseded, err := s.ForgetExperiences(500, []string{"a2"})
	if err != nil {
		t.Fatal(err)
	}
	if named != 1 || superseded != 2 {
		t.Errorf("got named=%d superseded=%d, want named=1 superseded=2 (a1,b1 swept)", named, superseded)
	}

	var total int
	if err := s.DB.QueryRow(`SELECT count(*) FROM experiences`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("only the untouched current sibling should remain, got %d rows", total)
	}
	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 1 || cur[0].ID != "b2" {
		t.Errorf("the current view must still show the untouched sibling b2, got %v", cur)
	}

	ev := lastEventOfType(t, s, "sess", "user.forgot")
	if got := forgotIDs(t, ev.Payload); strings.Join(got, ",") != "a2" {
		t.Errorf("marker must name only the requested id, not the swept superseded rows, got %v", got)
	}
}

// TestForgetRejectsSupersededID (ADR-0034 Decision 2): naming a row that is
// not the current generation is refused — the same discipline ADR-0033
// Decision 3 already puts on amend. Its content still lives on in the
// current generation's copy-forward, so deleting it alone would misreport
// what was erased. The whole batch is rejected, nothing deleted.
func TestForgetRejectsSupersededID(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "a1", "sess", core.KindExecution, 1)
	insertExp(t, s, "a2", "sess", core.KindExecution, 2)

	_, _, err := s.ForgetExperiences(500, []string{"a1"})
	if err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("a superseded id must be rejected, got %v", err)
	}

	var total int
	if err := s.DB.QueryRow(`SELECT count(*) FROM experiences`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("a rejected forget must delete nothing, got %d rows", total)
	}
}

// TestForgetSession (ADR-0033 Decision 2/4): a whole session's events and
// experiences are erased, no marker is written (the session leaves the events
// the queue derives from), an unknown session errors, and both guards return.
func TestForgetSession(t *testing.T) {
	s := openTest(t)
	mustAppend(t, s, "s", "task.started", 100)
	mustAppend(t, s, "s", "task.finished", 200)
	insertExp(t, s, "e1", "s", core.KindExecution, 1)
	insertExp(t, s, "e2", "s", core.KindPreference, 1)
	// A second session keeps a row in each table so the recreated guards have
	// something to fire on afterwards.
	mustAppend(t, s, "keep", "task.started", 300)
	insertExp(t, s, "k1", "keep", core.KindExecution, 1)

	events, exps, err := s.ForgetSession("s")
	if err != nil {
		t.Fatal(err)
	}
	if events != 2 || exps != 2 {
		t.Errorf("deleted counts: got (%d events, %d experiences), want (2, 2)", events, exps)
	}
	if got, _ := s.EventsBySession("s"); len(got) != 0 {
		t.Errorf("session events must be gone, got %v", got)
	}
	if n := countType(t, s, "user.forgot"); n != 0 {
		t.Errorf("forget --session writes no marker (events vanish), got %d", n)
	}

	if _, _, err := s.ForgetSession("nope"); err == nil ||
		!strings.Contains(err.Error(), "unknown session") {
		t.Errorf("an unknown session must error, got %v", err)
	}

	if _, err := s.DB.Exec(`DELETE FROM events WHERE session_id = 'keep'`); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Errorf("events guard must be recreated, got %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM experiences WHERE id = 'k1'`); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Errorf("experiences guard must be recreated, got %v", err)
	}
}

// TestPendingSessionsExcludesForgottenAndAmended (ADR-0033 Decision 4): a
// session the owner forgot or amended is dropped from the Deferred Perception
// queue permanently — even a higher extractor_ver query, which would otherwise
// re-pend it, must not resurrect the machine's re-perception of it.
func TestPendingSessionsExcludesForgottenAndAmended(t *testing.T) {
	s := openTest(t)
	for _, sess := range []string{"forgot", "amended", "normal"} {
		mustAppend(t, s, sess, "task.finished", 100)
		insertExp(t, s, "x-"+sess, sess, core.KindExecution, 1)
	}
	mustAppend(t, s, "forgot", "user.forgot", 200)
	mustAppend(t, s, "amended", "user.amended", 200)

	got, err := s.PendingSessions(5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "normal" {
		t.Errorf("only the unmarked session stays pending at a higher ver, got %v", got)
	}
}

// TestAmendExperienceAppendsNewGeneration (ADR-0033 Decision 3): the amended
// generation is added under fresh ids, the current view returns the whole
// sibling set — the edited row plus the untouched one, which keeps its own
// extractor_model — while the superseded originals remain in the truth
// table, and the user.amended marker lands in the session.
func TestAmendExperienceAppendsNewGeneration(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "e1", "s", core.KindExecution, 1)
	insertExp(t, s, "e2", "s", core.KindExecution, 1)

	newVer, err := s.AmendExperience("e1", 300, func(target *core.Experience) error {
		target.Context = map[string]string{"lang": "go"}
		target.Outcome = core.Outcome{Adopted: "as-is"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if newVer != 2 {
		t.Errorf("newVer: got %d, want 2", newVer)
	}

	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 2 {
		t.Fatalf("current view must return the whole new generation, got %d rows: %+v", len(cur), cur)
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
	if amended.Context["lang"] != "go" || amended.Outcome.Adopted != "as-is" {
		t.Errorf("the amended row must carry the edit, got %+v", amended)
	}
	if carried.ExtractorModel != "none" {
		t.Errorf("the carried sibling must keep its own extractor_model, got %q", carried.ExtractorModel)
	}
	if amended.ExtractorVer != 2 || carried.ExtractorVer != 2 {
		t.Errorf("both rows must share the bumped ver, got %d / %d", amended.ExtractorVer, carried.ExtractorVer)
	}

	var total int
	if err := s.DB.QueryRow(`SELECT count(*) FROM experiences`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("the superseded originals must remain as history, got %d rows", total)
	}
	if n := countTypeInSession(t, s, "s", "user.amended"); n != 1 {
		t.Errorf("amend must record one user.amended, got %d", n)
	}
}

// TestAmendExperienceRejectsUnknownID: no such row in any generation.
func TestAmendExperienceRejectsUnknownID(t *testing.T) {
	s := openTest(t)
	_, err := s.AmendExperience("ghost", 300, func(*core.Experience) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "no such experience") {
		t.Errorf("an unknown id must error, got %v", err)
	}
}

// TestAmendExperienceRejectsSupersededID (ADR-0033 Decision 3): only the
// current generation can be amended — a past perception is history.
func TestAmendExperienceRejectsSupersededID(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "old", "s", core.KindExecution, 1)
	insertExp(t, s, "new", "s", core.KindExecution, 2)

	_, err := s.AmendExperience("old", 300, func(*core.Experience) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Errorf("a superseded id must error, got %v", err)
	}
}

// TestAmendExperienceRollsBackOnApplyError (修正3): apply's error aborts the
// whole read-modify-write — no new generation, no marker — proving the write
// only happens once the caller's own validation (e.g. cmdAmend's
// --provider/kind check) has accepted the edit inside the same transaction
// the read came from.
func TestAmendExperienceRollsBackOnApplyError(t *testing.T) {
	s := openTest(t)
	insertExp(t, s, "e1", "s", core.KindExecution, 1)

	_, err := s.AmendExperience("e1", 300, func(*core.Experience) error {
		return errors.New("apply rejected")
	})
	if err == nil || !strings.Contains(err.Error(), "apply rejected") {
		t.Errorf("apply's error must propagate, got %v", err)
	}
	var total int
	if err := s.DB.QueryRow(`SELECT count(*) FROM experiences`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("a rejected apply must write nothing, got %d rows", total)
	}
	if n := countTypeInSession(t, s, "s", "user.amended"); n != 0 {
		t.Errorf("a rejected apply must write no marker, got %d", n)
	}
}

// TestConcurrentAmendsOfSiblingsDoNotRace (修正3+4): two Stores opened on the
// same file — the shape of two separate `tomobit amend` processes, not two
// callers sharing one connection — amend different siblings of the same
// (session, kind) group at the same moment. Whichever transaction's BEGIN
// IMMEDIATE (修正4) claims the write lock first runs to completion; the
// other's Begin blocks on it rather than racing it with a now-stale read
// snapshot, so it always sees the winner's committed generation once it
// proceeds — and finds its own target id already superseded by the
// copy-forward, the same everyday rejection amend gives a human who names a
// row twice. Neither goroutine may surface a raw SQLite error: that would
// mean the race broke through to the database instead of being serialized.
func TestConcurrentAmendsOfSiblingsDoNotRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	insertExp(t, seed, "e1", "sess", core.KindExecution, 1)
	insertExp(t, seed, "e2", "sess", core.KindExecution, 1)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	ready := make(chan struct{})
	results := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-ready
		_, results[0] = s1.AmendExperience("e1", 100, func(target *core.Experience) error {
			target.Context = map[string]string{"lang": "go"}
			return nil
		})
	}()
	go func() {
		defer wg.Done()
		<-ready
		_, results[1] = s2.AmendExperience("e2", 100, func(target *core.Experience) error {
			target.Context = map[string]string{"lang": "rust"}
			return nil
		})
	}()
	close(ready)
	wg.Wait()

	var wins, superseded int
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case strings.Contains(err.Error(), "superseded"):
			superseded++
		default:
			t.Errorf("a raced amend must fail as the ordinary superseded rejection, not a raw SQLite error: %v", err)
		}
	}
	if wins != 1 || superseded != 1 {
		t.Errorf("want exactly one winner and one superseded loser, got wins=%d superseded=%d (results=%v)", wins, superseded, results)
	}

	cur, err := s1.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 2 {
		t.Fatalf("the winner's generation must still carry both siblings forward, got %d: %+v", len(cur), cur)
	}
}

// ---- forgetting test helpers ----

func lastEventOfType(t *testing.T, s *Store, session, typ string) *Event {
	t.Helper()
	evs, err := s.EventsBySession(session)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == typ {
			return evs[i]
		}
	}
	t.Fatalf("session %q has no %s event", session, typ)
	return nil
}

func forgotIDs(t *testing.T, payload map[string]any) []string {
	t.Helper()
	raw, ok := payload["ids"].([]any)
	if !ok {
		t.Fatalf("user.forgot payload has no ids array: %v", payload)
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i], _ = v.(string)
	}
	return out
}

func countType(t *testing.T, s *Store, typ string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM events WHERE type = ?`, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countTypeInSession(t *testing.T, s *Store, session, typ string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM events WHERE session_id = ? AND type = ?`,
		session, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestMigrationRebuildsPreReflectionExperiences (ADR-0015): an old database
// whose CHECK predates kind='reflection' is rebuilt in place — truth rows
// survive verbatim, the append-only triggers come back, and reflection
// experiences insert cleanly afterwards.
func TestMigrationRebuildsPreReflectionExperiences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Regress the schema to the pre-ADR-0015 CHECK with one row in it.
	_, err = s.DB.Exec(`
		BEGIN;
		DROP VIEW experiences_current;
		DROP TABLE experiences;
		CREATE TABLE experiences (
		  id TEXT PRIMARY KEY, session_id TEXT NOT NULL, ts INTEGER NOT NULL,
		  kind TEXT NOT NULL CHECK (kind IN ('execution','preference')),
		  extractor_ver INTEGER NOT NULL, extractor_model TEXT NOT NULL,
		  context TEXT NOT NULL CHECK (json_valid(context)),
		  provider TEXT, outcome TEXT NOT NULL CHECK (json_valid(outcome)),
		  source TEXT NOT NULL DEFAULT 'production' CHECK (source IN ('production','learning'))
		);
		INSERT INTO experiences VALUES ('e1','sess',100,'execution',1,'m','{}','claude','{}','production');
		COMMIT;`)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen with migration: %v", err)
	}
	defer s.Close()

	exps, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	if len(exps) != 1 || exps[0].ID != "e1" {
		t.Fatalf("truth rows must survive the rebuild: %+v", exps)
	}
	if err := s.InsertExperience(&core.Experience{
		ID: "r1", SessionID: "refl", TS: 200, Kind: core.KindReflection,
		ExtractorVer: 1, ExtractorModel: "deterministic",
		Context: map[string]string{}, Outcome: core.Outcome{Reaction: "unexpected"},
		Source: "learning",
	}); err != nil {
		t.Fatalf("reflection insert after migration: %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM experiences WHERE id='e1'`); err == nil {
		t.Fatal("append-only trigger must be recreated after the rebuild")
	}
}

// ---- Vacuum (ADR-0033 Decision 5 / 修正2) ----

// TestVacuumErasesForgottenSecretFromTheDBFile: VACUUM must run before the
// WAL checkpoint — in WAL mode VACUUM's rewritten, freed-page-free pages
// land in WAL frames, not the main file, so checkpointing first (the
// pre-fix order) leaves the pre-vacuum, still-secret-bearing pages on disk
// regardless. Reading the .db file as raw bytes is the only check that can
// tell "logically deleted" from "physically gone" apart.
func TestVacuumErasesForgottenSecretFromTheDBFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const secret = "UNIQUELY-IDENTIFIABLE-SECRET-9f3c1a"
	insertExpCtx(t, s, "e1", "sess", map[string]string{"lang": secret})

	if _, _, err := s.ForgetExperiences(1000, []string{"e1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Vacuum(); err != nil {
		t.Fatalf("Vacuum: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Error("forgotten content must not survive on disk after Vacuum")
	}
}

// TestVacuumReportsIncompleteWhileAReaderHoldsTheWAL: a held read-only
// transaction — the face window's own shape (ADR-0020: a mode=ro connection
// kept open across polls instead of opened and closed each time) — pins a
// WAL snapshot that blocks the checkpoint's TRUNCATE step specifically.
// VACUUM's own commit is large enough to trigger SQLite's automatic
// checkpoint regardless (measured: it backfills the main file even with the
// reader present), but that automatic checkpoint cannot reset — the reader
// holds it open — so the WAL file itself, holding the original INSERT's
// frame among the ones it never got to erase, is exactly the residue
// ADR-0033 Decision 5 names ("WALと空きページに残る痕跡ごと消す"). Vacuum
// must report that residue as an error rather than claim success, and it
// must still be sitting in the WAL file even after the writer's own
// Store.Close(): proof this is Vacuum's own honest report, not something a
// bare Close() (SQLite checkpoints on the very last connection's close)
// would have quietly cleaned up once the reader let go.
func TestVacuumReportsIncompleteWhileAReaderHoldsTheWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	const secret = "UNIQUELY-IDENTIFIABLE-SECRET-b71e0d"
	insertExpCtx(t, s, "e1", "sess", map[string]string{"lang": secret})
	if _, _, err := s.ForgetExperiences(1000, []string{"e1"}); err != nil {
		t.Fatal(err)
	}

	readerDB, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer readerDB.Close()
	readerTx, err := readerDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer readerTx.Rollback()
	var probe int
	if err := readerTx.QueryRow(`SELECT count(*) FROM events`).Scan(&probe); err != nil {
		t.Fatal(err)
	}
	// readerTx stays open (no Commit/Rollback yet) — pinning its snapshot for
	// the rest of the test, matching the face window's held connection.

	if err := s.Vacuum(); err == nil {
		t.Error("Vacuum should report incomplete physical erasure while a reader holds the WAL")
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	walRaw, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(walRaw, []byte(secret)) {
		t.Error("without a completed checkpoint the WAL file should still hold the un-truncated residue — Vacuum must not have silently claimed success")
	}
}
