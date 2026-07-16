package reflection

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

const now = int64(1_800_000_000_000)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func applyExec(t *testing.T, s *store.Store, en *core.Engine, id, scopeKey, provider string, ts int64, o core.Outcome) {
	t.Helper()
	ctx := map[string]string{}
	for _, tok := range core.ParseScopeKey(scopeKey) {
		k, v, _ := strings.Cut(tok, "=")
		ctx[k] = v
	}
	e := &core.Experience{
		ID: id, SessionID: "sess-" + id, TS: ts, Kind: core.KindExecution,
		ExtractorVer: 1, ExtractorModel: "none",
		Context: ctx, Provider: provider, Outcome: o, Source: "production",
	}
	if err := s.InsertExperience(e); err != nil {
		t.Fatal(err)
	}
	if err := en.Apply(e); err != nil {
		t.Fatal(err)
	}
}

func typesOf(cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Type
	}
	return out
}

// TestDetectSplitBirth: a child born during the batch becomes a split
// candidate whose Diff names the distinguishing token.
func TestDetectSplitBirth(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	ts := now - 100*60000
	// Varied successes, then rust failures — the split scenario.
	for i := 0; i < 2; i++ {
		for _, l := range []string{"go", "python", "java", "ts"} {
			ts += 60000
			applyExec(t, s, en, fmt.Sprintf("s-%s-%d", l, i), "cap=impl|lang="+l, "claude", ts, core.Outcome{Adopted: "as-is"})
		}
	}
	snap, err := TakeSnapshot(s, ts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		ts += 60000
		applyExec(t, s, en, fmt.Sprintf("f-%d", i), "cap=impl|lang=rust", "claude", ts, core.Outcome{Reverted: true})
	}

	cands, err := Detect(snap, s, ts)
	if err != nil {
		t.Fatal(err)
	}
	var split *Candidate
	for i := range cands {
		if cands[i].Type == voice.InsightSplit {
			split = &cands[i]
		}
	}
	if split == nil {
		t.Fatalf("no split candidate detected: %v", typesOf(cands))
	}
	if got := split.Diff.Key(); got != "lang=rust" {
		t.Errorf("distinguishing scope: got %q, want lang=rust", got)
	}
	if split.Text() == "" {
		t.Error("split candidate must render a line")
	}
}

// TestDetectQuestionedAndRehabilitated via direct projection edits: the
// triggers are state transitions, whatever moved them.
func TestDetectQuestionedTransition(t *testing.T) {
	s := openStore(t)
	c := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=go", Target: "claude",
		Alpha: 8, Beta: 2, LastUpdate: now, BornTS: now,
	}
	if err := s.UpsertConnection(c); err != nil {
		t.Fatal(err)
	}
	snap, err := TakeSnapshot(s, now)
	if err != nil {
		t.Fatal(err)
	}
	// Surprise surfaces after the snapshot.
	if err := s.InsertLedger(&core.LedgerEntry{
		Kind: c.Kind, ScopeKey: c.ScopeKey, Target: c.Target,
		ExperienceID: "x", TS: now, P: 0.9, Y: 0, SExcess: 2.5,
	}); err != nil {
		t.Fatal(err)
	}
	cands, err := Detect(snap, s, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Type != voice.InsightQuestioned {
		t.Fatalf("want one questioned candidate, got %v", typesOf(cands))
	}
	// Second batch with no transition: already questioned — silent.
	snap2, _ := TakeSnapshot(s, now)
	cands, _ = Detect(snap2, s, now)
	if len(cands) != 0 {
		t.Errorf("an unchanged questioned state must not re-trigger, got %v", typesOf(cands))
	}
}

func TestDetectRehabilitation(t *testing.T) {
	s := openStore(t)
	failed := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=go", Target: "codex",
		Alpha: 1, Beta: 3, LastUpdate: now, BornTS: now, // gated
	}
	if err := s.UpsertConnection(failed); err != nil {
		t.Fatal(err)
	}
	snap, err := TakeSnapshot(s, now)
	if err != nil {
		t.Fatal(err)
	}
	// New evidence lifts it back over the bar.
	rehabbed := *failed
	rehabbed.Alpha = 6
	if err := s.UpsertConnection(&rehabbed); err != nil {
		t.Fatal(err)
	}
	cands, err := Detect(snap, s, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Type != voice.InsightRehabilitated {
		t.Fatalf("want one rehabilitation, got %v", typesOf(cands))
	}
}

func TestDetectReversalNeedsHysteresis(t *testing.T) {
	s := openStore(t)
	put := func(target string, alpha, beta float64) {
		t.Helper()
		if err := s.UpsertConnection(&core.Connection{
			Kind: core.ConnCapability, ScopeKey: "lang=go", Target: target,
			Alpha: alpha, Beta: beta, LastUpdate: now, BornTS: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	put("claude", 8, 2) // mean 0.8 — leader
	put("codex", 6, 4)  // mean 0.6
	snap, err := TakeSnapshot(s, now)
	if err != nil {
		t.Fatal(err)
	}

	// A photo-finish flip stays silent (hysteresis).
	put("codex", 8.3, 1.9) // mean ≈ 0.813, gap ≈ 0.013 < ReversalBand
	cands, _ := Detect(snap, s, now)
	for _, c := range cands {
		if c.Type == voice.InsightReversal {
			t.Fatalf("within-band flip must not be narrated: %+v", c)
		}
	}

	// A decisive overtake is told, winner and loser named.
	put("codex", 19, 1) // mean 0.95, gap 0.15
	cands, err = Detect(snap, s, now)
	if err != nil {
		t.Fatal(err)
	}
	var rev *Candidate
	for i := range cands {
		if cands[i].Type == voice.InsightReversal {
			rev = &cands[i]
		}
	}
	if rev == nil {
		t.Fatalf("no reversal detected: %v", typesOf(cands))
	}
	if rev.Provider != "codex" || rev.Other != "claude" {
		t.Errorf("winner/loser: got %s over %s", rev.Provider, rev.Other)
	}
}

func TestBudgetIsOneTellingPerDay(t *testing.T) {
	s := openStore(t)
	if ok, _ := HasBudget(s, now); !ok {
		t.Fatal("fresh history should have budget")
	}
	if err := s.AppendEvent("r", "tomo.reflected", now, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := HasBudget(s, now+3600*1000); ok {
		t.Error("budget spent within the window")
	}
	if ok, _ := HasBudget(s, now+BudgetWindowMs); !ok {
		t.Error("budget returns after the window")
	}
}

// TestPickLearnsFromReactions: with 知ってた piled on splits and 意外 on
// reversals, the mirror bets on reversals (ADR-0015: 鏡もまた育つ).
func TestPickLearnsFromReactions(t *testing.T) {
	var exps []*core.Experience
	for i := 0; i < 8; i++ {
		exps = append(exps,
			&core.Experience{ID: fmt.Sprintf("k%d", i), TS: now, Kind: core.KindReflection,
				Outcome: core.Outcome{Insight: voice.InsightSplit, Reaction: ReactionKnown}},
			&core.Experience{ID: fmt.Sprintf("u%d", i), TS: now, Kind: core.KindReflection,
				Outcome: core.Outcome{Insight: voice.InsightReversal, Reaction: ReactionUnexpected}})
	}
	cands := []Candidate{
		{Type: voice.InsightSplit, Scope: core.NewScope("lang=go"), Provider: "claude"},
		{Type: voice.InsightReversal, Scope: core.NewScope("lang=go"), Provider: "codex", Other: "claude"},
	}
	wins := 0
	for seed := int64(0); seed < 100; seed++ {
		if Pick(cands, exps, seed, now).Type == voice.InsightReversal {
			wins++
		}
	}
	if wins < 85 {
		t.Errorf("the mirror should strongly prefer the type that earns 意外: %d/100", wins)
	}
	if Pick(cands, exps, 7, now).Type != Pick(cands, exps, 7, now).Type {
		t.Error("Pick must be deterministic per seed")
	}
}

// TestRecordAndApplyWrongFlowsToTheConnection: 「それ違う」 lands as a
// layer-2 verdict on the subject connection — and never births structure.
func TestRecordAndApplyWrongFlowsToTheConnection(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	subject := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=go", Target: "claude",
		Alpha: 8, Beta: 2, LastUpdate: now, BornTS: now,
	}
	if err := s.UpsertConnection(subject); err != nil {
		t.Fatal(err)
	}
	cand := Candidate{Type: voice.InsightQuestioned, Scope: core.NewScope("lang=go"), Provider: "claude"}
	if err := RecordAndApply(s, en, cand, 42, ReactionWrong, "do-1", 3, now); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetConnection(core.ConnCapability, "lang=go", "claude")
	if got.Beta <= 2 {
		t.Errorf("verdict down should add failure mass: beta %v", got.Beta)
	}
	conns, _ := s.AllConnections()
	if len(conns) != 1 {
		t.Errorf("reflection feedback must not birth connections, got %d", len(conns))
	}

	ts, found, err := s.LastEventTS("tomo.reflected")
	if err != nil || !found || ts != now {
		t.Errorf("tomo.reflected must be recorded: ts=%d found=%v err=%v", ts, found, err)
	}
	exps, _ := s.CurrentExperiences()
	if len(exps) != 1 || exps[0].Kind != core.KindReflection ||
		exps[0].Outcome.Reaction != ReactionWrong || exps[0].Outcome.Insight != voice.InsightQuestioned {
		t.Errorf("reaction experience mismatch: %+v", exps)
	}

	// Rebuild replays the reflection experience identically (projection
	// invariant — the mirror's truths are ordinary truths).
	if err := en.Rebuild(); err != nil {
		t.Fatal(err)
	}
	rebuilt, _ := s.GetConnection(core.ConnCapability, "lang=go", "claude")
	if rebuilt != nil && got != nil && (rebuilt.Alpha != got.Alpha || rebuilt.Beta != got.Beta) {
		// The live path started from a hand-upserted connection with no
		// backing experiences, so a full match isn't expected here — but the
		// replay must at least keep the reflection's failure mass.
		if rebuilt.Beta <= core.PriorBeta {
			t.Errorf("rebuild dropped the reflection verdict: %+v", rebuilt)
		}
	}
}

// TestSkipStillSpendsBudget: a skip records tomo.reflected and nothing else.
func TestSkipStillSpendsBudget(t *testing.T) {
	s := openStore(t)
	en := &core.Engine{Repo: s}
	cand := Candidate{Type: voice.InsightSplit, Scope: core.NewScope("lang=go"), Diff: core.NewScope("lang=go"), Provider: "claude"}
	if err := RecordAndApply(s, en, cand, 1, "", "do-1", 3, now); err != nil {
		t.Fatal(err)
	}
	if ok, _ := HasBudget(s, now+1000); ok {
		t.Error("a skip must still spend the budget")
	}
	if exps, _ := s.CurrentExperiences(); len(exps) != 0 {
		t.Errorf("a skip must record no experience, got %d", len(exps))
	}
}

func TestAskMapsReactions(t *testing.T) {
	var out strings.Builder
	for input, want := range map[string]string{
		"1\n": ReactionUnexpected, "2\n": ReactionKnown, "3\n": ReactionWrong, "\n": "", "x\n": "",
	} {
		if got := Ask(strings.NewReader(input), &out, "line"); got != want {
			t.Errorf("Ask(%q) = %q, want %q", input, got, want)
		}
	}
	if !strings.Contains(out.String(), "line") {
		t.Error("Ask must print the telling")
	}
}

// TestReperceptionCandidates (ADR-0019 Decision 4): a session re-extracted
// at a higher version with a moved attribution becomes the 5th trigger.
func TestReperceptionCandidates(t *testing.T) {
	before := []*core.Experience{{
		ID: "v1", SessionID: "sess", TS: 100, Kind: core.KindExecution,
		ExtractorVer: 1, Context: map[string]string{"cap": "impl", "lang": "rust"},
		Provider: "claude",
	}}
	after := []*core.Experience{{
		ID: "v2", SessionID: "sess", TS: 100, Kind: core.KindExecution,
		ExtractorVer: 2, Context: map[string]string{"cap": "impl", "size": "large"},
		Provider: "claude",
	}}
	cands := ReperceptionCandidates(before, after)
	if len(cands) != 1 || cands[0].Type != voice.InsightReperceived {
		t.Fatalf("want one reperceived candidate, got %+v", cands)
	}
	text := cands[0].Text()
	if !strings.Contains(text, "見直してた") || !strings.Contains(text, "rust") || !strings.Contains(text, "large") {
		t.Errorf("line should name the moved attribution: %q", text)
	}
	// Same reading at a higher version: nothing moved, nothing to tell.
	same := []*core.Experience{{
		ID: "v2b", SessionID: "sess", TS: 100, Kind: core.KindExecution,
		ExtractorVer: 2, Context: map[string]string{"cap": "impl", "lang": "rust"},
		Provider: "claude",
	}}
	if got := ReperceptionCandidates(before, same); len(got) != 0 {
		t.Errorf("unchanged extraction must not trigger, got %+v", got)
	}
}

// TestHumanReversalLine (ADR-0019 Decision 3 / ADR-0018 Decision 4): the
// user overtaking Tomo's provider speaks the growth-mirror line.
func TestHumanReversalLine(t *testing.T) {
	c := Candidate{
		Type: voice.InsightReversal, Scope: core.NewScope("cap=review"),
		Provider: "human", Other: "claude-code",
	}
	text := c.Text()
	if !strings.Contains(text, "自分でやった方が") {
		t.Errorf("human reversal should mirror the user's growth: %q", text)
	}
}
