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

	// clearCalls counts ClearProjections invocations — Rebuild's one
	// defining move that live Apply never makes — so tests can tell the two
	// paths apart directly instead of inferring it from projection numbers
	// or log output (ADR-0041).
	clearCalls int
	// clearErr, when set, makes ClearProjections fail — the deterministic
	// way to force Rebuild to fail, so tests can pin PerceiveBatch's
	// "attempted a rebuild" vs "the rebuild actually succeeded" distinction
	// (ADR-0041).
	clearErr error
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
	r.clearCalls++
	if r.clearErr != nil {
		return r.clearErr
	}
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

// TestSplitChildInheritsMeanOnlyPrior (ADR-0013 Decision 1): the child's
// prior is Beta(μ·m₀, (1−μ)·m₀) — the parent's opinion at fixed baby mass.
// 平均だけ継ぎ、確信は継がない。
func TestSplitChildInheritsMeanOnlyPrior(t *testing.T) {
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

	almostEqual(t, child.PriorA+child.PriorB, InheritM0, 1e-9,
		"child prior mass is exactly m₀")
	parent, _ := r.GetConnection(ConnCapability, parentKey, "claude")
	mu := parent.Mean(triggerTS)
	almostEqual(t, child.PriorA, mu*InheritM0, 1e-9,
		"child prior mean is the parent's posterior mean at split time")
	if mu <= 0.5 {
		t.Fatalf("scenario broken: parent should still lean success, mean=%v", mu)
	}
	// Forgetting sinks the child back to the parent's opinion, not to 0.5
	// (Decision 2: 継承こそがbackoff).
	almostEqual(t, child.Mean(triggerTS+1000*HalfLifeMs), mu, 1e-9,
		"fully decayed child returns to inherited μ")
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

// TestReconcileMergesReachesAnUntouchedChildButOnlyMergesOnceEvidenceNumericallyVanishes
// (ADR-0037 Decision 2, corrected by the ADR's own 実測による訂正): a child
// that stops receiving experiences also stops being judged — production
// only calls judge on connections an incoming experience's scope touches.
// ReconcileMerges reaches it independently of judge/Apply, but the ln BF of
// its distinguishing token decays toward ThetaMerge=0 from above at O(d²)
// and never crosses it in finite time; only once decay has driven every
// term to float64's exact zero (measured: ~55-60 half-lives for this
// scenario) does mergeCheck's `bf <= ThetaMerge` fire. The ADR-0012
// calibrated timescale (~3 half-lives, decide.go's margin comment) is far
// short of that and must not merge — decay's rescue here is numerical, not
// the ADR-0012 Decision 3 "forgetting" story (that story is merge's, not
// this test's — see ADR-0038).
func TestReconcileMergesReachesAnUntouchedChildButOnlyMergesOnceEvidenceNumericallyVanishes(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	childKey := NewScope("cap=impl", "lang=rust").Key()

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

	// No further Apply/judge call ever touches the child from here on —
	// this is the gated-out lineage ADR-0037 describes. Reconciling right
	// at birth must find the distinguishing evidence still fresh and leave
	// it alone.
	if err := en.ReconcileMerges(triggerTS); err != nil {
		t.Fatal(err)
	}
	if c, _ := r.GetConnection(ConnCapability, childKey, "claude"); c == nil {
		t.Fatal("fresh evidence should not merge the child yet")
	}

	// ADR-0012's own calibrated forgetting timescale (~3 half-lives) leaves
	// ln BF at ~0.22 for this scenario (measured) — nowhere near ThetaMerge.
	calibrated := triggerTS + 3*HalfLifeMs
	if err := en.ReconcileMerges(calibrated); err != nil {
		t.Fatal(err)
	}
	if c, _ := r.GetConnection(ConnCapability, childKey, "claude"); c == nil {
		t.Fatal("ADR-0012's calibrated decay timescale should not merge the child (ADR-0037 実測による訂正)")
	}

	// Only once decay has run long enough for the distinguishing token's
	// evidence to underflow to an exact float64 zero does the child fold
	// back — reached purely through ReconcileMerges, judge is never called
	// on it.
	numericallyVanished := triggerTS + 1000*HalfLifeMs
	if err := en.ReconcileMerges(numericallyVanished); err != nil {
		t.Fatal(err)
	}
	if c, _ := r.GetConnection(ConnCapability, childKey, "claude"); c != nil {
		t.Error("numerically vanished evidence should merge the child away even though judge never touched it")
	}
	if led, _ := r.LedgerFor(ConnCapability, childKey, "claude"); len(led) != 0 {
		t.Errorf("child ledger should be deleted on reconciled merge, got %d entries", len(led))
	}
}

// gatedChildScenario trims splitScenario() to the experience that triggers
// the cap=impl|lang=rust split — the scenario's own tail (the remaining
// rust failures) would otherwise keep touching the child right after birth,
// muddying which mechanism (judge's touched path vs. a reconciliation
// sweep) reached it — then appends one experience in an unrelated scope
// (cap=doc, never lang=rust) advance milliseconds later. That experience
// advances the log's own clock without ever touching the child, producing
// the gated-out lineage ADR-0037 Decision 2 describes.
func gatedChildScenario(t *testing.T, advance int64) []*Experience {
	t.Helper()
	childKey := NewScope("cap=impl", "lang=rust").Key()

	var child *Connection
	var triggerIdx int
	scan := newFakeRepo()
	enScan := &Engine{Repo: scan}
	for i, e := range splitScenario() {
		scan.exps = append(scan.exps, e)
		if err := enScan.Apply(e); err != nil {
			t.Fatal(err)
		}
		if c, _ := scan.GetConnection(ConnCapability, childKey, "claude"); c != nil {
			child, triggerIdx = c, i
			break
		}
	}
	if child == nil {
		t.Fatal("split never produced a child connection")
	}

	exps := splitScenario()[:triggerIdx+1]
	last := exps[len(exps)-1]
	exps = append(exps, execExp("advance-clock", last.TS+advance,
		"claude", map[string]string{"cap": "doc"}, Outcome{Adopted: "as-is"}))
	return exps
}

// TestRebuildReconcilesMergesForAChildNoLaterExperienceTouches (ADR-0037
// Decision 2 / "Rebuild で1回"): a child born mid-replay and never touched
// by any later experience in the log — the gated-out lineage the ADR
// describes — must still be folded back once the log's own last timestamp
// has carried its evidence to numerically vanish (ADR-0037 実測による訂正).
// The one reconciliation sweep at the end of Rebuild is the only path that
// can reach it; judge's touched-connection path never runs on this child
// again after birth.
func TestRebuildReconcilesMergesForAChildNoLaterExperienceTouches(t *testing.T) {
	childKey := NewScope("cap=impl", "lang=rust").Key()
	exps := gatedChildScenario(t, 1000*HalfLifeMs)

	r := newFakeRepo()
	r.exps = exps
	en := &Engine{Repo: r}
	if err := en.Rebuild(); err != nil {
		t.Fatal(err)
	}

	if c, _ := r.GetConnection(ConnCapability, childKey, "claude"); c != nil {
		t.Error("Rebuild's end-of-replay reconciliation should have merged the untouched child away")
	}
}

// TestLiveApplyAloneDoesNotReproduceRebuildForAGatedChild measures the gap
// Apply's doc comment now names: live Apply calls never sweep, so the same
// gated-out child Rebuild folds back (previous test) is still present after
// an equivalent live-only replay.
func TestLiveApplyAloneDoesNotReproduceRebuildForAGatedChild(t *testing.T) {
	childKey := NewScope("cap=impl", "lang=rust").Key()
	exps := gatedChildScenario(t, 1000*HalfLifeMs)

	live := newFakeRepo()
	enLive := &Engine{Repo: live}
	for _, e := range exps {
		live.exps = append(live.exps, e)
		if err := enLive.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	// No ReconcileMerges call — this is Apply alone, the way a caller that
	// forgets ADR-0037 Decision 2's batch-boundary responsibility would run.

	rebuilt := newFakeRepo()
	rebuilt.exps = append(rebuilt.exps, exps...)
	enRebuilt := &Engine{Repo: rebuilt}
	if err := enRebuilt.Rebuild(); err != nil {
		t.Fatal(err)
	}

	liveChild, _ := live.GetConnection(ConnCapability, childKey, "claude")
	rebuiltChild, _ := rebuilt.GetConnection(ConnCapability, childKey, "claude")
	if liveChild == nil {
		t.Fatal("scenario broken: live Apply alone should still hold the gated child — that gap is what this test exists to pin")
	}
	if rebuiltChild != nil {
		t.Fatal("scenario broken: Rebuild should have reconciled the gated child away")
	}
}

// TestLiveApplyWithReconcileMergesAtBatchBoundaryMatchesRebuildForAGatedChild
// confirms the invariant Apply's doc comment now describes: the gap the
// previous test pins closes once the caller also calls ReconcileMerges once
// at its batch boundary — the responsibility cmd/tomobit, internal/curiosity
// and internal/reflection each hold after their ADR-0037 Decision 2 wiring.
func TestLiveApplyWithReconcileMergesAtBatchBoundaryMatchesRebuildForAGatedChild(t *testing.T) {
	exps := gatedChildScenario(t, 1000*HalfLifeMs)

	live := newFakeRepo()
	enLive := &Engine{Repo: live}
	for _, e := range exps {
		live.exps = append(live.exps, e)
		if err := enLive.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := enLive.ReconcileMerges(exps[len(exps)-1].TS); err != nil {
		t.Fatal(err)
	}

	rebuilt := newFakeRepo()
	rebuilt.exps = append(rebuilt.exps, exps...)
	enRebuilt := &Engine{Repo: rebuilt}
	if err := enRebuilt.Rebuild(); err != nil {
		t.Fatal(err)
	}

	lc, rc := mustAll(t, live), mustAll(t, rebuilt)
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

// connsEqual compares two AllConnections snapshots the same way
// TestLiveApplyMatchesRebuildAcrossASplit and
// TestRebuildIsIdempotentAndWallClockIndependent do: same fields, same
// order (both come from the (kind, scope_key, target)-sorted fakeRepo).
func connsEqual(t *testing.T, got, want []*Connection) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("connection count differs: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		a, b := got[i], want[i]
		if a.Kind != b.Kind || a.ScopeKey != b.ScopeKey || a.Target != b.Target ||
			a.Alpha != b.Alpha || a.Beta != b.Beta || a.LastUpdate != b.LastUpdate ||
			a.BornTS != b.BornTS || a.ParentKey != b.ParentKey {
			t.Errorf("connection %d differs:\n got =%+v\n want=%+v", i, a, b)
		}
	}
}

// TestPerceiveBatchInOrderAppliesLiveWithoutRebuilding pins ADR-0041's
// no-op case: a batch that stays newer than everything already perceived
// must take the live Apply+ReconcileMerges path untouched — proven here by
// ClearProjections' call count, Rebuild's one defining move, rather than by
// log output or by projection values that could coincidentally agree.
func TestPerceiveBatchInOrderAppliesLiveWithoutRebuilding(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}

	known := execExp("known", 1000, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	known.SessionID = "sess-known"
	r.exps = append(r.exps, known)
	if err := en.Apply(known); err != nil {
		t.Fatal(err)
	}
	if err := en.ReconcileMerges(known.TS); err != nil {
		t.Fatal(err)
	}
	beforeCurrent, err := r.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}

	// A distinct session (a session is perceived into at most one execution
	// experience, so ordinary new evidence never shares (session_id, kind)
	// with something already known — only re-perception does).
	next := execExp("next", 2000, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	next.SessionID = "sess-next"
	r.exps = append(r.exps, next)
	batch := []*Experience{next}

	r.clearCalls = 0
	rebuilt, err := en.PerceiveBatch(batch, beforeCurrent, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt {
		t.Error("an in-order batch must not report a rebuild")
	}
	if r.clearCalls != 0 {
		t.Errorf("an in-order batch must never clear projections, got %d ClearProjections calls", r.clearCalls)
	}
}

// TestPerceiveBatchOutOfOrderRebuildsMatchingCanonicalProjection reproduces,
// minimized, the dogfood divergence ADR-0041 measured (live α=7.6604 vs
// rebuild 7.4789): a late-arriving batch older than what live Apply has
// already folded in adds its evidence at decay weight 1.0 instead of the
// weight true chronological replay would give it (Observe's own doc comment
// names this: "an out-of-order observation ... already adds undecayed via
// PosteriorAt"). PerceiveBatch must detect this and hand the projection to
// Rebuild instead, landing on exactly what an independent rebuild of the
// same two experiences produces.
func TestPerceiveBatchOutOfOrderRebuildsMatchingCanonicalProjection(t *testing.T) {
	const base int64 = 1000

	r := newFakeRepo()
	en := &Engine{Repo: r}

	// Perceived first, while the backend was healthy — chronologically the
	// *later* of the two experiences (one half-life after base). A distinct
	// session from `late` below: this test isolates the out-of-order
	// condition alone, not the (session_id, kind) re-perception one.
	known := execExp("known", base+HalfLifeMs, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	known.SessionID = "sess-known"
	r.exps = append(r.exps, known)
	if err := en.Apply(known); err != nil {
		t.Fatal(err)
	}
	if err := en.ReconcileMerges(known.TS); err != nil {
		t.Fatal(err)
	}
	beforeCurrent, err := r.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}

	// Perceived late (ADR-0029: the backend was down when this session
	// happened) — chronologically the *earlier* experience, arriving after
	// `known` has already been live-applied.
	late := execExp("late", base, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	late.SessionID = "sess-late"
	r.exps = append(r.exps, late)
	batch := []*Experience{late}

	rebuilt, err := en.PerceiveBatch(batch, beforeCurrent, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rebuilt {
		t.Fatal("a batch older than already-perceived evidence must trigger a rebuild")
	}
	got := mustAll(t, r)

	canon := newFakeRepo()
	canon.exps = append(canon.exps, known, late)
	enCanon := &Engine{Repo: canon}
	if err := enCanon.Rebuild(); err != nil {
		t.Fatal(err)
	}
	want := mustAll(t, canon)

	connsEqual(t, got, want)

	// The regression this guards: applying `late` live and out of order
	// (skipping PerceiveBatch's guard entirely) does diverge from the same
	// canonical projection — confirming the scenario actually exercises the
	// bug ADR-0041 describes, not a vacuously-passing one.
	diverged := newFakeRepo()
	diverged.exps = append(diverged.exps, known, late)
	enDiverged := &Engine{Repo: diverged}
	if err := enDiverged.Apply(known); err != nil {
		t.Fatal(err)
	}
	if err := enDiverged.Apply(late); err != nil {
		t.Fatal(err)
	}
	divergedConns := mustAll(t, diverged)
	if len(divergedConns) != len(want) {
		t.Fatalf("setup error: diverged scenario connection count = %d, want %d", len(divergedConns), len(want))
	}
	same := true
	for i := range divergedConns {
		if divergedConns[i].Alpha != want[i].Alpha || divergedConns[i].Beta != want[i].Beta {
			same = false
		}
	}
	if same {
		t.Fatal("setup error: applying `late` live out of order should have diverged from rebuild — scenario no longer exercises the bug")
	}
}

// TestBatchSupersedesKnownDetectsSameSessionKindRegardlessOfID pins ADR-0041
// 決定2's premise directly: a re-perceived generation shares its old one's ts
// exactly (an experience's ts is its session's last event, unchanged across
// extractor_ver bumps), so only the id differs — and by nothing but a fresh
// random suffix. Detection must not depend on which way that coin falls.
func TestBatchSupersedesKnownDetectsSameSessionKindRegardlessOfID(t *testing.T) {
	known := []*Experience{
		{ID: "aaaa", SessionID: "sess-1", TS: 5000, Kind: KindExecution},
	}
	lowerID := []*Experience{
		{ID: "0000", SessionID: "sess-1", TS: 5000, Kind: KindExecution},
	}
	higherID := []*Experience{
		{ID: "zzzz", SessionID: "sess-1", TS: 5000, Kind: KindExecution},
	}
	unrelated := []*Experience{
		{ID: "mmmm", SessionID: "sess-2", TS: 5000, Kind: KindExecution},
	}

	if !batchSupersedesKnown(lowerID, known) {
		t.Error("a re-perceived id lower than known's must still be detected as a superseding generation")
	}
	if !batchSupersedesKnown(higherID, known) {
		t.Error("a re-perceived id higher than known's must still be detected as a superseding generation")
	}
	if batchSupersedesKnown(unrelated, known) {
		t.Error("a different session must not be flagged as a re-perceived generation")
	}

	// The gap batchSupersedesKnown exists to close: batchOutOfOrder alone
	// sees the higher-id case as perfectly in order (same ts, a larger id),
	// so it cannot tell a re-perception from ordinary new evidence — that
	// coin-flip case is exactly why ADR-0041 needs a second condition.
	if batchOutOfOrder(higherID, known) {
		t.Fatal("setup error: this case must look in-order by (ts, id) alone, or it no longer demonstrates the gap")
	}
}

// TestPerceiveBatchRebuildsOnReperceivedGenerationRegardlessOfIDDirection is
// TestBatchSupersedesKnownDetectsSameSessionKindRegardlessOfID's end-to-end
// counterpart: PerceiveBatch itself, not just the predicate, must rebuild a
// re-perceived generation whichever way its fresh id happens to sort against
// the generation it replaces.
func TestPerceiveBatchRebuildsOnReperceivedGenerationRegardlessOfIDDirection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		newID string
	}{
		{"lower id", "0000-lower"},
		{"higher id", "zzzz-higher"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeRepo()
			en := &Engine{Repo: r}

			gen1 := execExp("gen1-fixed-id", 5000, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
			r.exps = append(r.exps, gen1)
			if err := en.Apply(gen1); err != nil {
				t.Fatal(err)
			}
			if err := en.ReconcileMerges(gen1.TS); err != nil {
				t.Fatal(err)
			}
			beforeCurrent, err := r.CurrentExperiences()
			if err != nil {
				t.Fatal(err)
			}

			// Same session+kind, same ts (the session's last event, replayed
			// unchanged by re-perception) — only extractor output and id differ.
			gen2 := execExp(tc.newID, gen1.TS, "claude", map[string]string{"cap": "impl", "lang": "go"}, Outcome{Adopted: "as-is"})
			r.exps = append(r.exps, gen2)

			rebuilt, err := en.PerceiveBatch([]*Experience{gen2}, beforeCurrent, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !rebuilt {
				t.Errorf("re-perceiving the same session+kind at a %s must trigger a rebuild", tc.name)
			}
		})
	}
}

// TestPerceiveBatchLogsBeforeRebuildStarts pins ADR-0041's 前提と残す露出:
// Rebuild is not one transaction, so the one honest log line must run before
// Rebuild is attempted, not after it returns — a crash partway through must
// not leave the operator without even that line.
func TestPerceiveBatchLogsBeforeRebuildStarts(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}

	known := execExp("known", 2000, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	r.exps = append(r.exps, known)
	if err := en.Apply(known); err != nil {
		t.Fatal(err)
	}
	beforeCurrent, err := r.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}

	late := execExp("late", 1000, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	r.exps = append(r.exps, late)

	called := false
	var clearCallsAtCallback int
	onRebuild := func() {
		called = true
		clearCallsAtCallback = r.clearCalls
	}
	if _, err := en.PerceiveBatch([]*Experience{late}, beforeCurrent, onRebuild); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("onRebuild must run whenever PerceiveBatch decides to rebuild")
	}
	if clearCallsAtCallback != 0 {
		t.Errorf("onRebuild must run before Rebuild's ClearProjections, but %d call(s) had already happened", clearCallsAtCallback)
	}
}

// TestPerceiveBatchReportsRebuiltOnlyWhenRebuildSucceeds guards against
// conflating "a rebuild was attempted" with "the projection is now
// canonical": Rebuild is not one transaction (ADR-0041 前提と残す露出), so a
// caller deciding whether the live projection can be trusted needs to know
// it actually finished, not merely that PerceiveBatch tried.
func TestPerceiveBatchReportsRebuiltOnlyWhenRebuildSucceeds(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}

	known := execExp("known", 2000, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	r.exps = append(r.exps, known)
	if err := en.Apply(known); err != nil {
		t.Fatal(err)
	}
	beforeCurrent, err := r.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}

	late := execExp("late", 1000, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	r.exps = append(r.exps, late)

	r.clearErr = fmt.Errorf("disk full")
	rebuilt, err := en.PerceiveBatch([]*Experience{late}, beforeCurrent, nil)
	if err == nil {
		t.Fatal("the injected Rebuild failure must propagate")
	}
	if rebuilt {
		t.Error(`PerceiveBatch must report rebuilt=false when Rebuild itself failed — "attempted" is not "succeeded"`)
	}
}

func snapshotLedger(r *fakeRepo) map[string]LedgerEntry {
	out := map[string]LedgerEntry{}
	for k, e := range r.ledger {
		out[k] = *e
	}
	return out
}

// TestApplyFeedsBothBetTargets (ADR-0014 Decision 1): one execution
// experience with a plan feeds two ledgers — the provider's and the plan's —
// with the same birth, observation, and surprise machinery.
func TestApplyFeedsBothBetTargets(t *testing.T) {
	r := newFakeRepo()
	en := &Engine{Repo: r}
	exp := execExp("e1", 500, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	exp.Plan = "implement>test"
	r.exps = []*Experience{exp}
	if err := en.Apply(exp); err != nil {
		t.Fatal(err)
	}

	prov, _ := r.GetConnection(ConnCapability, "cap=impl", "claude")
	if prov == nil {
		t.Fatal("provider connection missing")
	}
	pl, _ := r.GetConnection(ConnPlan, "cap=impl", "implement>test")
	if pl == nil {
		t.Fatal("plan connection missing")
	}
	almostEqual(t, pl.Alpha, PriorAlpha+1.0, 1e-12, "plan observed the success")
	if led, _ := r.LedgerFor(ConnPlan, "cap=impl", "implement>test"); len(led) != 1 {
		t.Errorf("plan surprise ledger should have one entry, got %d", len(led))
	}

	// A plan-less experience touches only the provider side.
	plain := execExp("e2", 600, "claude", map[string]string{"cap": "impl"}, Outcome{Adopted: "as-is"})
	r.exps = append(r.exps, plain)
	if err := en.Apply(plain); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetConnection(ConnPlan, "cap=impl", "implement>test")
	almostEqual(t, got.Alpha, PriorAlpha+1.0, 1e-12, "plan posterior untouched by plan-less run")
}

// nonStationaryScenario is n experiences at one (cap, lang) scope, the first
// half all adopted and the second half all reverted. lang never varies, so
// it is the one candidate token split() ever has to score for the cap=impl
// connection — and, being present in every experience, it never earns
// enough evidence to clear ThetaSplit. The ledger is therefore never reset
// (ADR-0002's designed "static, but Questioned" outcome), so every
// remaining experience re-summons judge → split on a connection whose
// matching-experience set keeps growing — the shape that made Rebuild
// O(n²) before caching (ADR-0037 Decision 2 fix a/b).
func nonStationaryScenario(n int) []*Experience {
	exps := make([]*Experience, 0, n)
	var ts int64 = 1_700_000_000_000
	half := n / 2
	for i := 0; i < n; i++ {
		ts += 60000
		o := Outcome{Adopted: "as-is"}
		if i >= half {
			o = Outcome{Reverted: true}
		}
		exps = append(exps, execExp(fmt.Sprintf("ns-%d", i), ts,
			"claude", map[string]string{"cap": "impl", "lang": "rust"}, o))
	}
	return exps
}

func benchmarkRebuildNonStationary(b *testing.B, n int) {
	exps := nonStationaryScenario(n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := newFakeRepo()
		r.exps = exps
		en := &Engine{Repo: r}
		if err := en.Rebuild(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRebuildNonStationary100(b *testing.B)  { benchmarkRebuildNonStationary(b, 100) }
func BenchmarkRebuildNonStationary200(b *testing.B)  { benchmarkRebuildNonStationary(b, 200) }
func BenchmarkRebuildNonStationary400(b *testing.B)  { benchmarkRebuildNonStationary(b, 400) }
func BenchmarkRebuildNonStationary800(b *testing.B)  { benchmarkRebuildNonStationary(b, 800) }
func BenchmarkRebuildNonStationary1600(b *testing.B) { benchmarkRebuildNonStationary(b, 1600) }
