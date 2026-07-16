package curiosity

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

const now = int64(1_800_000_000_000)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ctxOf(scopeKey string) map[string]string {
	ctx := map[string]string{}
	for _, tok := range core.ParseScopeKey(scopeKey) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			ctx[k] = v
		}
	}
	return ctx
}

// applyExec inserts and applies one execution experience at ts=now, growing
// the real connections exactly as production would.
func applyExec(t *testing.T, s *store.Store, en *core.Engine, id, scopeKey, provider string, o core.Outcome) {
	t.Helper()
	e := &core.Experience{
		ID: id, SessionID: "s-" + id, TS: now, Kind: core.KindExecution,
		ExtractorVer: 3, ExtractorModel: "none",
		Context: ctxOf(scopeKey), Provider: provider, Outcome: o, Source: "production",
	}
	if err := s.InsertExperience(e); err != nil {
		t.Fatal(err)
	}
	if err := en.Apply(e); err != nil {
		t.Fatal(err)
	}
}

// grow seeds a scope+provider with nSuccess adoptions and nFail reverts.
func grow(t *testing.T, s *store.Store, en *core.Engine, scopeKey, provider string, nSuccess, nFail int) {
	t.Helper()
	for i := 0; i < nSuccess; i++ {
		applyExec(t, s, en, fmt.Sprintf("%s-%s-s%d", scopeKey, provider, i), scopeKey, provider, core.Outcome{Adopted: "as-is"})
	}
	for i := 0; i < nFail; i++ {
		applyExec(t, s, en, fmt.Sprintf("%s-%s-f%d", scopeKey, provider, i), scopeKey, provider, core.Outcome{Reverted: true})
	}
}

func gapsAt(t *testing.T, s *store.Store) []Gap {
	t.Helper()
	gaps, err := Gaps(s, now)
	if err != nil {
		t.Fatal(err)
	}
	return gaps
}

// TestAllGatesOpenYieldsGap: two providers, indistinguishable on capability,
// seen often, never compared on preference — exactly one Gap emerges.
func TestAllGatesOpenYieldsGap(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	grow(t, s, en, "lang=rust", "claude", 4, 1)
	grow(t, s, en, "lang=rust", "codex", 4, 1)

	gaps := gapsAt(t, s)
	if len(gaps) != 1 {
		t.Fatalf("want 1 gap, got %d: %+v", len(gaps), gaps)
	}
	g := gaps[0]
	if g.ScopeKey != "lang=rust" || g.A != "claude" || g.B != "codex" {
		t.Errorf("gap identity: got scope=%q A=%q B=%q", g.ScopeKey, g.A, g.B)
	}
	if g.LnBF >= ThetaEven {
		t.Errorf("providers should read as even, ln BF=%v", g.LnBF)
	}
	if g.Freq < FMin {
		t.Errorf("scope should be frequent, freq=%v", g.Freq)
	}
}

// TestEvenGateClosesOnLargeBayesFactor: when one provider clearly wins, the
// pair is not even — no Gap, even though frequency and evidence pass.
func TestEvenGateClosesOnLargeBayesFactor(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	grow(t, s, en, "lang=rust", "claude", 5, 0)
	grow(t, s, en, "lang=rust", "codex", 0, 5)

	if gaps := gapsAt(t, s); len(gaps) != 0 {
		t.Errorf("a decisive capability difference must not be a preference gap, got %+v", gaps)
	}
}

// TestEvenGateClosesOnThinEvidence: matched success rates read as even, but
// one Connection carries less than n_min evidence — that is a Knowledge Gap,
// not a Preference Gap.
func TestEvenGateClosesOnThinEvidence(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	grow(t, s, en, "lang=rust", "claude", 2, 0) // evidence ~2 < NMin
	grow(t, s, en, "lang=rust", "codex", 5, 0)

	claude, _ := s.GetConnection(core.ConnCapability, "lang=rust", "claude")
	if claude.Evidence(now) >= NMin {
		t.Fatalf("fixture broken: claude evidence %v should be below NMin", claude.Evidence(now))
	}
	if gaps := gapsAt(t, s); len(gaps) != 0 {
		t.Errorf("thin-evidence pair must not yield a gap, got %+v", gaps)
	}
}

// TestFrequentGateClosesOnRareScope isolates the frequent gate: both
// Connections carry ample evidence, yet the scope has almost no runs. This
// pairs hand-built Connections with a sparse experience log — the only way
// the gate binds alone, since a qualifying pair otherwise implies freq >= 2*NMin.
func TestFrequentGateClosesOnRareScope(t *testing.T) {
	s := openStore(t)
	for _, target := range []string{"claude", "codex"} {
		if err := s.UpsertConnection(&core.Connection{
			Kind: core.ConnCapability, ScopeKey: "lang=rust", Target: target,
			Alpha: 5, Beta: 2, LastUpdate: now, BornTS: now, // evidence 5 >= NMin
		}); err != nil {
			t.Fatal(err)
		}
	}
	en := &core.Engine{Repo: s}
	applyExec(t, s, en, "e1", "lang=rust", "claude", core.Outcome{Adopted: "as-is"})
	applyExec(t, s, en, "e2", "lang=rust", "codex", core.Outcome{Adopted: "as-is"})

	freq, _, _ := scopeStats(core.ParseScopeKey("lang=rust"), mustExps(t, s), now, func(e *core.Experience) string { return e.Target() })
	if freq >= FMin {
		t.Fatalf("fixture broken: freq %v should be below FMin", freq)
	}
	if gaps := gapsAt(t, s); len(gaps) != 0 {
		t.Errorf("a rarely-seen scope must not yield a gap, got %+v", gaps)
	}
}

// TestNoEvidenceGateClosesOncePreferenceKnown: once a preference Connection
// carries e_max evidence, the question is answered — the gate closes.
func TestNoEvidenceGateClosesOncePreferenceKnown(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	grow(t, s, en, "lang=rust", "claude", 4, 1)
	grow(t, s, en, "lang=rust", "codex", 4, 1)
	if gaps := gapsAt(t, s); len(gaps) != 1 {
		t.Fatalf("baseline should have one open gap, got %d", len(gaps))
	}

	if err := s.UpsertConnection(&core.Connection{
		Kind: core.ConnPreference, ScopeKey: "lang=rust", Target: "claude~codex",
		Alpha: 2, Beta: 1, LastUpdate: now, BornTS: now, // evidence 1 >= EMax
	}); err != nil {
		t.Fatal(err)
	}
	if gaps := gapsAt(t, s); len(gaps) != 0 {
		t.Errorf("a known preference must close the gap, got %+v", gaps)
	}
}

func TestGapsAreDeterministic(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	grow(t, s, en, "lang=rust", "claude", 4, 1)
	grow(t, s, en, "lang=rust", "codex", 4, 1)
	grow(t, s, en, "lang=go", "claude", 3, 0)
	grow(t, s, en, "lang=go", "codex", 3, 0)

	first, second := gapsAt(t, s), gapsAt(t, s)
	if fmt.Sprintf("%+v", first) != fmt.Sprintf("%+v", second) {
		t.Errorf("derivation is not deterministic:\n %+v\n %+v", first, second)
	}
}

// TestGapsOrderByPriority: Freq descending, then scope_key ascending on ties.
func TestGapsOrderByPriority(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	grow(t, s, en, "lang=rust", "claude", 5, 0) // freq 10
	grow(t, s, en, "lang=rust", "codex", 5, 0)
	grow(t, s, en, "lang=go", "claude", 3, 0) // freq 6
	grow(t, s, en, "lang=go", "codex", 3, 0)
	grow(t, s, en, "topic=api", "claude", 3, 0) // freq 6, ties lang=go
	grow(t, s, en, "topic=api", "codex", 3, 0)

	gaps := gapsAt(t, s)
	got := make([]string, len(gaps))
	for i, g := range gaps {
		got[i] = g.ScopeKey
	}
	want := []string{"lang=rust", "lang=go", "topic=api"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("priority order: got %v, want %v", got, want)
	}
}

func TestHasBudgetIsRollingTwentyFourHours(t *testing.T) {
	s := openStore(t)
	if ok, err := HasBudget(s, now); err != nil || !ok {
		t.Fatalf("empty history should have budget: ok=%v err=%v", ok, err)
	}
	if err := s.AppendEvent("ask", "tomo.asked", now, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := HasBudget(s, now+3600*1000); ok {
		t.Error("within 24h of a question the budget is spent")
	}
	if ok, _ := HasBudget(s, now+BudgetWindowMs+1); !ok {
		t.Error("past the 24h window the budget refreshes")
	}
}

func TestAskMapsReplies(t *testing.T) {
	gap := Gap{Scope: core.NewScope("lang=rust"), ScopeKey: "lang=rust", A: "claude", B: "codex"}
	tests := []struct {
		in            string
		wantPreferred string
		wantOver      string
		wantAnswered  bool
	}{
		{"1\n", "claude", "codex", true},
		{"2\n", "codex", "claude", true},
		{"\n", "", "", false},
		{"", "", "", false}, // EOF
		{"x\n", "", "", false},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		preferred, over, answered := Ask(bufio.NewReader(strings.NewReader(tt.in)), &out, gap)
		if preferred != tt.wantPreferred || over != tt.wantOver || answered != tt.wantAnswered {
			t.Errorf("Ask(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tt.in, preferred, over, answered, tt.wantPreferred, tt.wantOver, tt.wantAnswered)
		}
		if !strings.Contains(out.String(), "rust") || !strings.Contains(out.String(), "claude") {
			t.Errorf("question should name the scope value and providers, got %q", out.String())
		}
		if !strings.HasPrefix(out.String(), "「") {
			t.Errorf("question text should be quoted, got %q", out.String())
		}
	}
}

// TestAnswerBirthsPreferenceConnectionAndSurvivesRebuild: a reply flows into
// a preference Connection, and the live projection matches the canonical
// rebuilt one (live/rebuild consistency, ADR-0004).
func TestAnswerBirthsPreferenceConnectionAndSurvivesRebuild(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	grow(t, s, en, "lang=rust", "claude", 4, 1)
	grow(t, s, en, "lang=rust", "codex", 4, 1)

	gap := gapsAt(t, s)[0]
	preferred, over, answered := Ask(bufio.NewReader(strings.NewReader("1\n")), &bytes.Buffer{}, gap)
	if err := RecordAndPerceive(s, en, gap, preferred, over, answered, "do-session", 3, now); err != nil {
		t.Fatal(err)
	}

	pref, _ := s.GetConnection(core.ConnPreference, "lang=rust", "claude~codex")
	if pref == nil {
		t.Fatal("an answer should birth a preference connection")
	}
	if pref.Mean(now) <= 0.5 {
		t.Errorf("claude won, mean should favor it: %v", pref.Mean(now))
	}

	live := mustAllConns(t, s)
	if err := en.Rebuild(); err != nil {
		t.Fatal(err)
	}
	rebuilt := mustAllConns(t, s)
	if len(live) != len(rebuilt) {
		t.Fatalf("connection count differs after rebuild: live %d, rebuild %d", len(live), len(rebuilt))
	}
	for i := range live {
		a, b := live[i], rebuilt[i]
		if a.Kind != b.Kind || a.ScopeKey != b.ScopeKey || a.Target != b.Target ||
			a.Alpha != b.Alpha || a.Beta != b.Beta || a.LastUpdate != b.LastUpdate ||
			a.BornTS != b.BornTS || a.ParentKey != b.ParentKey {
			t.Errorf("connection %d diverged:\n live=%+v\n rebuild=%+v", i, a, b)
		}
	}
}

// TestSkipRecordsOnlyTheQuestion: a skip writes tomo.asked, no experience,
// and leaves nothing for deferred perception to reprocess.
func TestSkipRecordsOnlyTheQuestion(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	gap := Gap{Scope: core.NewScope("lang=rust"), ScopeKey: "lang=rust", A: "claude", B: "codex", Freq: 5}

	if err := RecordAndPerceive(s, en, gap, "", "", false, "do-session", 3, now); err != nil {
		t.Fatal(err)
	}

	if n := countEvents(t, s, "tomo.asked"); n != 1 {
		t.Errorf("skip should record exactly one tomo.asked, got %d", n)
	}
	if n := countEvents(t, s, "user.preference"); n != 0 {
		t.Errorf("skip must not record a preference, got %d", n)
	}
	if exps := mustExps(t, s); len(exps) != 0 {
		t.Errorf("skip must produce no experience, got %d", len(exps))
	}
	// The ask session holds no task.finished, so deferred perception never
	// sees it — the skipped session cannot be reprocessed forever.
	if pending, err := s.PendingSessions(3); err != nil || len(pending) != 0 {
		t.Errorf("ask session must not be pending: got %v err=%v", pending, err)
	}
}

func mustAllConns(t *testing.T, s *store.Store) []*core.Connection {
	t.Helper()
	conns, err := s.AllConnections()
	if err != nil {
		t.Fatal(err)
	}
	return conns
}

func mustExps(t *testing.T, s *store.Store) []*core.Experience {
	t.Helper()
	exps, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	return exps
}

func countEvents(t *testing.T, s *store.Store, typ string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM events WHERE type = ?`, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestVoIDeprioritizesPartiallyAnsweredGaps (ADR-0016): equal arrival
// frequency, but the pair that already leans one way (preference evidence
// below EMax, so the gap is still open) wobbles less — and sinks in the
// ranking. 判断が変わることだけが理由になる。
func TestVoIDeprioritizesPartiallyAnsweredGaps(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	grow(t, s, en, "lang=rust", "claude", 3, 0)
	grow(t, s, en, "lang=rust", "codex", 3, 0)
	grow(t, s, en, "lang=go", "claude", 3, 0)
	grow(t, s, en, "lang=go", "codex", 3, 0)

	// lang=go already leans claude a little: 0.4 evidence keeps the gap
	// open (< EMax) but sharpens the lottery below the blank slate's coin.
	if err := s.UpsertConnection(&core.Connection{
		Kind: core.ConnPreference, ScopeKey: "lang=go", Target: "claude~codex",
		Alpha: 1.4, Beta: 1.0, LastUpdate: now, BornTS: now,
	}); err != nil {
		t.Fatal(err)
	}

	gaps := gapsAt(t, s)
	if len(gaps) != 2 {
		t.Fatalf("want 2 open gaps, got %d: %+v", len(gaps), gaps)
	}
	if gaps[0].ScopeKey != "lang=rust" || gaps[1].ScopeKey != "lang=go" {
		t.Errorf("blank pair should outrank the leaning pair: %q then %q",
			gaps[0].ScopeKey, gaps[1].ScopeKey)
	}
	if gaps[0].Wobble <= gaps[1].Wobble {
		t.Errorf("wobble should shrink with evidence: %v vs %v",
			gaps[0].Wobble, gaps[1].Wobble)
	}
	for _, g := range gaps {
		if g.VoI <= 0 || g.VoI != g.Freq*g.Wobble {
			t.Errorf("VoI must be Freq×Wobble > 0: %+v", g)
		}
	}
}
