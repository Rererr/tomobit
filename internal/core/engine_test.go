package core

import (
	"fmt"
	"sort"
	"testing"
)

// fakeRepo is an in-memory Repo. It returns copies so the engine can only
// mutate stored state through UpsertConnection, mirroring the SQLite store
// (which also updates alpha/beta/last_update only on conflict).
type fakeRepo struct {
	conns  map[string]*Connection
	ledger map[string]*LedgerEntry
	exps   []*Experience
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{conns: map[string]*Connection{}, ledger: map[string]*LedgerEntry{}}
}

func connKey(kind, scope, target string) string { return kind + "\x00" + scope + "\x00" + target }
func ledKey(kind, scope, target, exp string) string {
	return connKey(kind, scope, target) + "\x00" + exp
}
func ledPrefix(kind, scope, target string) string { return connKey(kind, scope, target) + "\x00" }

func (r *fakeRepo) GetConnection(kind, scopeKey, target string) (*Connection, error) {
	c, ok := r.conns[connKey(kind, scopeKey, target)]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (r *fakeRepo) UpsertConnection(c *Connection) error {
	k := connKey(c.Kind, c.ScopeKey, c.Target)
	if e, ok := r.conns[k]; ok {
		e.Alpha, e.Beta, e.LastUpdate = c.Alpha, c.Beta, c.LastUpdate
		return nil
	}
	cp := *c
	r.conns[k] = &cp
	return nil
}

func (r *fakeRepo) DeleteConnection(kind, scopeKey, target string) error {
	delete(r.conns, connKey(kind, scopeKey, target))
	return nil
}

func (r *fakeRepo) ConnectionsFor(kind, target string) ([]*Connection, error) {
	var out []*Connection
	for _, c := range r.conns {
		if c.Kind == kind && c.Target == target {
			cp := *c
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ScopeKey < out[j].ScopeKey })
	return out, nil
}

func (r *fakeRepo) AllConnections() ([]*Connection, error) {
	var out []*Connection
	for _, c := range r.conns {
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].ScopeKey != out[j].ScopeKey {
			return out[i].ScopeKey < out[j].ScopeKey
		}
		return out[i].Target < out[j].Target
	})
	return out, nil
}

func (r *fakeRepo) InsertLedger(e *LedgerEntry) error {
	cp := *e
	r.ledger[ledKey(e.Kind, e.ScopeKey, e.Target, e.ExperienceID)] = &cp
	return nil
}

func (r *fakeRepo) LedgerFor(kind, scopeKey, target string) ([]*LedgerEntry, error) {
	pre := ledPrefix(kind, scopeKey, target)
	var out []*LedgerEntry
	for k, e := range r.ledger {
		if hasPrefix(k, pre) {
			cp := *e
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out, nil
}

func (r *fakeRepo) DeleteLedgerFor(kind, scopeKey, target string) error {
	pre := ledPrefix(kind, scopeKey, target)
	for k := range r.ledger {
		if hasPrefix(k, pre) {
			delete(r.ledger, k)
		}
	}
	return nil
}

func (r *fakeRepo) CurrentExperiences() ([]*Experience, error) {
	out := make([]*Experience, len(r.exps))
	copy(out, r.exps)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS < out[j].TS
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *fakeRepo) ClearProjections() error {
	r.conns = map[string]*Connection{}
	r.ledger = map[string]*LedgerEntry{}
	return nil
}

func hasPrefix(s, pre string) bool { return len(s) >= len(pre) && s[:len(pre)] == pre }

func mustAll(t *testing.T, r *fakeRepo) []*Connection {
	t.Helper()
	conns, err := r.AllConnections()
	if err != nil {
		t.Fatal(err)
	}
	return conns
}

func execExp(id string, ts int64, provider string, ctx map[string]string, o Outcome) *Experience {
	return &Experience{
		ID: id, SessionID: "sess", TS: ts, Kind: KindExecution,
		ExtractorVer: 1, ExtractorModel: "none",
		Context: ctx, Provider: provider, Outcome: o, Source: "production",
	}
}

func TestApplyIgnoresExperiencesWithoutOutcomeSignal(t *testing.T) {
	tests := []struct {
		name string
		exp  *Experience
	}{
		{"cancelled", execExp("e1", 100, "claude", map[string]string{"lang": "rust"}, Outcome{Cancelled: true})},
		{"empty target", execExp("e2", 100, "", map[string]string{"lang": "rust"}, Outcome{Adopted: "as-is"})},
		{"empty tokens", execExp("e3", 100, "claude", map[string]string{}, Outcome{Adopted: "as-is"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFakeRepo()
			en := &Engine{Repo: r}
			r.exps = []*Experience{tt.exp}
			if err := en.Apply(tt.exp); err != nil {
				t.Fatal(err)
			}
			conns, _ := r.AllConnections()
			if len(conns) != 0 {
				t.Errorf("expected no connections, got %d", len(conns))
			}
			if len(r.ledger) != 0 {
				t.Errorf("expected no ledger entries, got %d", len(r.ledger))
			}
		})
	}
}

func TestApplyBirthsGranularityOneConnectionPerAttribute(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	exp := execExp("e1", 500, "claude", map[string]string{"lang": "rust", "cap": "impl"}, Outcome{TestsPassed: boolp(true)})
	r.exps = []*Experience{exp}
	if err := en.Apply(exp); err != nil {
		t.Fatal(err)
	}

	conns, _ := r.AllConnections()
	if len(conns) != 2 {
		t.Fatalf("expected 2 granularity-1 connections, got %d", len(conns))
	}
	for _, c := range conns {
		if got := ParseScopeKey(c.ScopeKey); len(got) != 1 {
			t.Errorf("%s: expected single-token scope, got %v", c.ScopeKey, got)
		}
		if c.ParentKey != "" {
			t.Errorf("%s: born connection should have no parent", c.ScopeKey)
		}
		if c.BornTS != exp.TS || c.LastUpdate != exp.TS {
			t.Errorf("%s: born/last should equal exp.TS %d, got born=%d last=%d", c.ScopeKey, exp.TS, c.BornTS, c.LastUpdate)
		}
		almostEqual(t, c.Alpha, PriorAlpha+0.9, 1e-12, c.ScopeKey+" alpha = prior + y")
		almostEqual(t, c.Beta, PriorBeta+0.1, 1e-12, c.ScopeKey+" beta = prior + (1-y)")
	}
}

func TestApplyRecordsSurpriseOnlyOnSubsetMatchingConnections(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	child := &Connection{
		Kind: ConnCapability, ScopeKey: NewScope("cap=impl", "lang=rust").Key(), Target: "claude",
		Alpha: PriorAlpha, Beta: PriorBeta, LastUpdate: 0, BornTS: 0,
	}
	if err := r.UpsertConnection(child); err != nil {
		t.Fatal(err)
	}

	miss := execExp("a", 100, "claude", map[string]string{"cap": "impl", "lang": "go"}, Outcome{Adopted: "as-is"})
	hit := execExp("b", 200, "claude", map[string]string{"cap": "impl", "lang": "rust"}, Outcome{Adopted: "as-is"})
	r.exps = []*Experience{miss, hit}
	for _, e := range r.exps {
		if err := en.Apply(e); err != nil {
			t.Fatal(err)
		}
	}

	childLedger, _ := r.LedgerFor(ConnCapability, child.ScopeKey, "claude")
	if len(childLedger) != 1 {
		t.Fatalf("child should only record the rust experience, got %d entries", len(childLedger))
	}
	if childLedger[0].ExperienceID != "b" {
		t.Errorf("child ledger entry: got %q, want %q", childLedger[0].ExperienceID, "b")
	}
	got, _ := r.GetConnection(ConnCapability, child.ScopeKey, "claude")
	almostEqual(t, got.Alpha, PriorAlpha+1.0, 1e-12, "child observed one success")

	capLedger, _ := r.LedgerFor(ConnCapability, NewScope("cap=impl").Key(), "claude")
	if len(capLedger) != 2 {
		t.Errorf("coarse cap connection should record both experiences, got %d", len(capLedger))
	}
}

// splitScenario: many successes across varied languages, then a run of rust
// failures, so only lang=rust cleanly partitions cap=impl's world.
func splitScenario() []*Experience {
	var exps []*Experience
	var ts int64 = 1_700_000_000_000
	for i := 0; i < 2; i++ {
		for _, l := range []string{"go", "python", "java", "typescript"} {
			ts += 60000
			exps = append(exps, execExp(fmt.Sprintf("s-%s-%d", l, i), ts,
				"claude", map[string]string{"cap": "impl", "lang": l}, Outcome{Adopted: "as-is"}))
		}
	}
	for i := 0; i < 10; i++ {
		ts += 60000
		exps = append(exps, execExp(fmt.Sprintf("f-%d", i), ts,
			"claude", map[string]string{"cap": "impl", "lang": "rust"}, Outcome{Reverted: true}))
	}
	return exps
}

func TestApplySplitsOnTheDistinguishingTokenAndBornsChildWithHistory(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	childKey := NewScope("cap=impl", "lang=rust").Key()
	parentKey := NewScope("cap=impl").Key()

	var child *Connection
	var triggerTS int64
	for _, e := range splitScenario() {
		r.exps = append(r.exps, e)
		if err := en.Apply(e); err != nil {
			t.Fatal(err)
		}
		if c, _ := r.GetConnection(ConnCapability, childKey, "claude"); c != nil {
			child = c
			triggerTS = e.TS
			break
		}
	}
	if child == nil {
		t.Fatal("split never produced a child connection")
	}

	if child.ParentKey != parentKey {
		t.Errorf("child parent: got %q, want %q", child.ParentKey, parentKey)
	}
	if child.BornTS != triggerTS {
		t.Errorf("child BornTS: got %d, want %d (exp.TS at split)", child.BornTS, triggerTS)
	}
	if child.Mean(child.LastUpdate) >= 0.5 {
		t.Errorf("child born with history should predict failure, mean=%v", child.Mean(child.LastUpdate))
	}
	if child.Evidence(child.LastUpdate) < 3 {
		t.Errorf("child should carry replayed evidence, got %v", child.Evidence(child.LastUpdate))
	}
	if parentLedger, _ := r.LedgerFor(ConnCapability, parentKey, "claude"); len(parentLedger) != 0 {
		t.Errorf("parent ledger should reset after split, got %d entries", len(parentLedger))
	}
}

func TestApplyDoesNotSplitWhenNoTokenJustifiesIt(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	var exps []*Experience
	var ts int64 = 1_700_000_000_000
	for i := 0; i < 8; i++ {
		ts += 60000
		exps = append(exps, execExp(fmt.Sprintf("s%d", i), ts,
			"claude", map[string]string{"cap": "impl", "lang": "rust"}, Outcome{Adopted: "as-is"}))
	}
	for i := 0; i < 5; i++ {
		ts += 60000
		exps = append(exps, execExp(fmt.Sprintf("f%d", i), ts,
			"claude", map[string]string{"cap": "impl", "lang": "rust"}, Outcome{Reverted: true}))
	}
	for _, e := range exps {
		r.exps = append(r.exps, e)
		if err := en.Apply(e); err != nil {
			t.Fatal(err)
		}
	}

	conns, _ := r.AllConnections()
	for _, c := range conns {
		if c.ParentKey != "" {
			t.Errorf("no child should be born, found %s", c.ScopeKey)
		}
	}
	capc, _ := r.GetConnection(ConnCapability, NewScope("cap=impl").Key(), "claude")
	sum, _ := en.LedgerSum(capc, ts)
	if sum <= ThetaTrigger {
		t.Errorf("surprise should have accumulated past the trigger, got %v", sum)
	}
}

// binaryScenario: a single attribute cleanly splits cap=impl into a winning
// language (go, always adopted) and a losing one (rust, always reverted).
func binaryScenario() []*Experience {
	var exps []*Experience
	var ts int64 = 1_700_000_000_000
	for i := 0; i < 8; i++ {
		ts += 60000
		exps = append(exps, execExp(fmt.Sprintf("g-%d", i), ts,
			"claude", map[string]string{"cap": "impl", "lang": "go"}, Outcome{Adopted: "as-is"}))
	}
	for i := 0; i < 10; i++ {
		ts += 60000
		exps = append(exps, execExp(fmt.Sprintf("r-%d", i), ts,
			"claude", map[string]string{"cap": "impl", "lang": "rust"}, Outcome{Reverted: true}))
	}
	return exps
}

func TestSplitBreaksTiesTowardTheLexicalWinnerAndKeepsTheDiscoveryAtGranularityOne(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	for _, e := range binaryScenario() {
		r.exps = append(r.exps, e)
		if err := en.Apply(e); err != nil {
			t.Fatal(err)
		}
	}

	var children []*Connection
	for _, c := range mustAll(t, r) {
		if c.ParentKey != "" {
			children = append(children, c)
		}
	}
	if len(children) != 1 {
		t.Fatalf("the tie must birth exactly one child, got %d: %v", len(children), children)
	}
	if got, want := children[0].ScopeKey, NewScope("cap=impl", "lang=go").Key(); got != want {
		t.Errorf("tie-break should pick the lexical winner: got child %q, want %q", got, want)
	}

	rust, _ := r.GetConnection(ConnCapability, NewScope("lang=rust").Key(), "claude")
	if rust == nil {
		t.Fatal("granularity-1 lang=rust connection should exist")
	}
	if rust.Mean(rust.LastUpdate) >= 0.5 {
		t.Errorf("granularity-1 lang=rust should hold the failures, mean=%v", rust.Mean(rust.LastUpdate))
	}
	if child, _ := r.GetConnection(ConnCapability, NewScope("cap=impl", "lang=rust").Key(), "claude"); child != nil {
		t.Error("the losing side stays at granularity 1; no cap=impl|lang=rust child should be born")
	}
}

func TestLiveApplyMatchesRebuildAcrossASplit(t *testing.T) {
	exps := splitScenario() // already in strict (ts, id) order

	live := newFakeRepo()
	enLive := &Engine{Repo: live}
	for _, e := range exps {
		live.exps = append(live.exps, e)
		if err := enLive.Apply(e); err != nil {
			t.Fatal(err)
		}
	}

	rebuilt := newFakeRepo()
	rebuilt.exps = append(rebuilt.exps, exps...)
	enRebuilt := &Engine{Repo: rebuilt}
	if err := enRebuilt.Rebuild(); err != nil {
		t.Fatal(err)
	}

	lc, rc := mustAll(t, live), mustAll(t, rebuilt)
	if len(lc) == 0 {
		t.Fatal("scenario should have produced connections including a split child")
	}
	if hasChild := func(cs []*Connection) bool {
		for _, c := range cs {
			if c.ParentKey != "" {
				return true
			}
		}
		return false
	}; !hasChild(lc) {
		t.Fatal("scenario should have fired a split")
	}
	if len(lc) != len(rc) {
		t.Fatalf("connection count differs: live %d, rebuild %d", len(lc), len(rc))
	}
	for i := range lc {
		a, b := lc[i], rc[i]
		if a.Kind != b.Kind || a.ScopeKey != b.ScopeKey || a.Target != b.Target ||
			a.Alpha != b.Alpha || a.Beta != b.Beta || a.LastUpdate != b.LastUpdate ||
			a.BornTS != b.BornTS || a.ParentKey != b.ParentKey {
			t.Errorf("connection %d differs:\n live=%+v\n rebuild=%+v", i, a, b)
		}
	}

	ll, rl := snapshotLedger(live), snapshotLedger(rebuilt)
	if len(ll) != len(rl) {
		t.Fatalf("ledger size differs: live %d, rebuild %d", len(ll), len(rl))
	}
	for k, e1 := range ll {
		e2, ok := rl[k]
		if !ok {
			t.Errorf("ledger entry %q missing from rebuild", k)
			continue
		}
		if e1.P != e2.P || e1.Y != e2.Y || e1.SExcess != e2.SExcess || e1.TS != e2.TS {
			t.Errorf("ledger entry %q differs: live=%+v rebuild=%+v", k, e1, e2)
		}
	}
}

func TestJudgeMergesChildWhenDistinguishingTokenLosesSignal(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	parentKey := NewScope("cap=impl").Key()
	childKey := NewScope("cap=impl", "lang=rust").Key()

	var ts int64 = 1_700_000_000_000
	add := func(id, lang string, o Outcome) {
		ts += 60000
		r.exps = append(r.exps, execExp(id, ts, "claude", map[string]string{"cap": "impl", "lang": lang}, o))
	}
	add("r1", "rust", Outcome{Adopted: "as-is"})
	add("r2", "rust", Outcome{Adopted: "as-is"})
	add("r3", "rust", Outcome{Reverted: true})
	add("r4", "rust", Outcome{Reverted: true})
	add("g1", "go", Outcome{Adopted: "as-is"})
	add("g2", "go", Outcome{Adopted: "as-is"})
	add("g3", "go", Outcome{Reverted: true})
	add("g4", "go", Outcome{Reverted: true})

	child := &Connection{
		Kind: ConnCapability, ScopeKey: childKey, Target: "claude",
		Alpha: 3, Beta: 3, LastUpdate: ts, BornTS: 1_700_000_000_000, ParentKey: parentKey,
	}
	if err := r.UpsertConnection(child); err != nil {
		t.Fatal(err)
	}
	if err := r.InsertLedger(&LedgerEntry{Kind: ConnCapability, ScopeKey: childKey, Target: "claude", ExperienceID: "r1", TS: ts, P: 0.5, Y: 1, SExcess: 0}); err != nil {
		t.Fatal(err)
	}

	if err := en.judge(child, ts); err != nil {
		t.Fatal(err)
	}

	if c, _ := r.GetConnection(ConnCapability, childKey, "claude"); c != nil {
		t.Error("child should be merged away when its token stops distinguishing")
	}
	if led, _ := r.LedgerFor(ConnCapability, childKey, "claude"); len(led) != 0 {
		t.Errorf("child ledger should be deleted on merge, got %d entries", len(led))
	}
}

func TestRebuildIsIdempotentAndWallClockIndependent(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	r.exps = splitScenario()

	if err := en.Rebuild(); err != nil {
		t.Fatal(err)
	}
	conns1, _ := r.AllConnections()
	ledger1 := snapshotLedger(r)

	if err := en.Rebuild(); err != nil {
		t.Fatal(err)
	}
	conns2, _ := r.AllConnections()
	ledger2 := snapshotLedger(r)

	if len(conns1) != len(conns2) {
		t.Fatalf("connection count changed across rebuilds: %d vs %d", len(conns1), len(conns2))
	}
	for i := range conns1 {
		a, b := conns1[i], conns2[i]
		if a.Kind != b.Kind || a.ScopeKey != b.ScopeKey || a.Target != b.Target ||
			a.Alpha != b.Alpha || a.Beta != b.Beta || a.LastUpdate != b.LastUpdate ||
			a.BornTS != b.BornTS || a.ParentKey != b.ParentKey {
			t.Errorf("connection %d differs: %+v vs %+v", i, a, b)
		}
	}
	if len(ledger1) != len(ledger2) {
		t.Fatalf("ledger size changed across rebuilds: %d vs %d", len(ledger1), len(ledger2))
	}
	for k, e1 := range ledger1 {
		e2, ok := ledger2[k]
		if !ok {
			t.Errorf("ledger entry %q missing after second rebuild", k)
			continue
		}
		if e1.P != e2.P || e1.Y != e2.Y || e1.SExcess != e2.SExcess || e1.TS != e2.TS {
			t.Errorf("ledger entry %q differs: %+v vs %+v", k, e1, e2)
		}
	}
	if len(conns1) == 0 {
		t.Fatal("scenario should have produced connections")
	}
}

func snapshotLedger(r *fakeRepo) map[string]LedgerEntry {
	out := map[string]LedgerEntry{}
	for k, e := range r.ledger {
		out[k] = *e
	}
	return out
}
