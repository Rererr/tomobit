package main

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/curiosity"
	"github.com/Rererr/tomobit/internal/store"
)

// TestDuelEligible: the offer belongs to the unpinned/auto path only — a bare
// `do` (default provider, not explicit) is eligible, an explicit pin or human
// is not (ADR-0026 Decision 2). The split protocol no longer gates it
// (ADR-0028 made it always-on).
func TestDuelEligible(t *testing.T) {
	cases := []struct {
		name             string
		providerExplicit bool
		providerName     string
		want             bool
	}{
		{"bare do (default, unpinned)", false, "claude-code", true},
		{"explicit --provider auto", true, "auto", true},
		{"explicit pin", true, "codex", false},
		{"human", true, "human", false},
		{"unpinned human default is not human", false, "claude-code", true},
	}
	for _, c := range cases {
		if got := duelEligible(c.providerExplicit, c.providerName); got != c.want {
			t.Errorf("%s: duelEligible(%v,%q) = %v, want %v",
				c.name, c.providerExplicit, c.providerName, got, c.want)
		}
	}
}

// TestPickDuelGap: among VoI-sorted gaps, pick the first whose scope is known
// pre-run (subset of the capability token) and whose pair is two runnable
// providers — skipping finer-scoped gaps and human-paired ones (ADR-0026
// Decision 2).
func TestPickDuelGap(t *testing.T) {
	registerFakeProvider(t, "p-a", &fakeSplitAdapter{name: "p-a"})
	registerFakeProvider(t, "p-b", &fakeSplitAdapter{name: "p-b"})
	tokens := []string{core.CanonToken("cap", "implement")}

	tooFine := curiosity.Gap{ // highest VoI but scope unknowable pre-run
		A: "p-a", B: "p-b", VoI: 9,
		Scope: core.NewScope("cap=implement", "lang=go"),
	}
	humanPair := curiosity.Gap{ // runnable-vs-human is not a duel
		A: "human", B: "p-a", VoI: 5,
		Scope: core.NewScope("cap=implement"),
	}
	runnable := curiosity.Gap{ // the one to pick
		A: "p-a", B: "p-b", VoI: 1,
		Scope: core.NewScope("cap=implement"),
	}

	got, ok := pickDuelGap([]curiosity.Gap{tooFine, humanPair, runnable}, tokens)
	if !ok {
		t.Fatal("a runnable capability-scoped gap should be pickable")
	}
	if got.A != "p-a" || got.B != "p-b" || got.VoI != 1 {
		t.Errorf("picked %+v, want the runnable gap", got)
	}

	if _, ok := pickDuelGap([]curiosity.Gap{tooFine, humanPair}, tokens); ok {
		t.Error("no runnable pre-run gap should mean no duel")
	}
}

// TestDuelBudgetSharedWithQuestion: an offer may not fire within a budget
// window of either a question or a previous offer (ADR-0026 Decision 2).
func TestDuelBudgetSharedWithQuestion(t *testing.T) {
	s := openTestStore(t)
	now := int64(10 * curiosity.BudgetWindowMs)

	if ok, err := duelBudgetOK(s, now); err != nil || !ok {
		t.Fatalf("empty ledger should allow an offer: ok=%v err=%v", ok, err)
	}

	// A question just asked blocks the offer.
	if err := s.AppendEvent("q", "tomo.asked", now-1000, nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := duelBudgetOK(s, now); ok {
		t.Error("a recent tomo.asked should block the offer")
	}
	// Long enough ago, it no longer blocks.
	if ok, _ := duelBudgetOK(s, now+curiosity.BudgetWindowMs); !ok {
		t.Error("a question a full window ago should not block")
	}

	// A recent offer also blocks the next offer.
	later := now + 2*curiosity.BudgetWindowMs
	if err := s.AppendEvent("d", "tomo.duel_offered", later-1000, nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := duelBudgetOK(s, later); ok {
		t.Error("a recent tomo.duel_offered should block the next offer")
	}
}

// TestAskDuelYN: the default is no — only an explicit y/yes opts into doubling
// the cost (ADR-0026 Decision 2).
func TestAskDuelYN(t *testing.T) {
	gap := curiosity.Gap{A: "a", B: "b", Scope: core.NewScope("cap=implement")}
	yes := map[string]bool{"y\n": true, "Y\n": true, "yes\n": true, "  y \n": true,
		"\n": false, "n\n": false, "no\n": false, "nope\n": false, "": false}
	for in, want := range yes {
		got := askDuelYN(bufio.NewReader(strings.NewReader(in)), io.Discard, gap)
		if got != want {
			t.Errorf("askDuelYN(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestRunDuelRunsBothProvidersToCompletion: the duel records one parent with a
// task.duel, two sibling children under it, and runs both providers at once —
// both reach task.finished even when one exits non-zero (ADR-0026 Decision 4:
// unlike a split, one failure does not abandon the other).
func TestRunDuelRunsBothProvidersToCompletion(t *testing.T) {
	registerFakeProvider(t, "prov-a", &fakeSplitAdapter{name: "prov-a", line: "A thinking", exitCode: 0})
	registerFakeProvider(t, "prov-z", &fakeSplitAdapter{name: "prov-z", line: "Z thinking", exitCode: 1})
	s := openTestStore(t)

	gap := curiosity.Gap{A: "prov-a", B: "prov-z", Scope: core.NewScope("cap=implement")}
	noInput := bufio.NewReader(strings.NewReader(""))
	if err := runDuel(context.Background(), s, gap, "do the thing", "implement", "", "", 0, noInput, io.Discard, nil); err != nil {
		t.Fatalf("runDuel: %v", err)
	}

	parents := sessionsWithEvent(t, s, "task.duel")
	if len(parents) != 1 {
		t.Fatalf("want exactly one duel parent, got %d", len(parents))
	}
	children := subtaskSessionIDs(t, s, parents[0])
	if len(children) != 2 {
		t.Fatalf("want two sibling children, got %d", len(children))
	}

	finished, errored := 0, 0
	for _, c := range children {
		if n := countEventsOfTypeInSession(t, s, c, "provider.output"); n < 1 {
			t.Errorf("child %s produced no output — did it run?", c)
		}
		if n := countEventsOfTypeInSession(t, s, c, "task.finished"); n != 1 {
			t.Errorf("child %s: task.finished = %d, want 1", c, n)
		}
		finished += countEventsOfTypeInSession(t, s, c, "task.finished")
		errored += countEventsOfTypeInSession(t, s, c, "provider.error")
	}
	if finished != 2 {
		t.Errorf("both children must finish even if one fails: got %d", finished)
	}
	if errored != 1 {
		t.Errorf("exactly the failing child records provider.error: got %d", errored)
	}
	if n := countEventsOfTypeInSession(t, s, parents[0], "task.finished"); n != 1 {
		t.Errorf("parent task.finished = %d, want 1", n)
	}
}

// TestDuelOfferFiresOnRealOpenGap closes the seam the unit tests stub: a real
// ledger with two indistinguishable, frequent, preference-unknown providers at
// the capability scope produces an open gap (curiosity.Gaps), pickDuelGap
// selects it, and a "y" runs the offer end to end — recording the offer so the
// budget is spent (ADR-0026 Decision 2).
func TestDuelOfferFiresOnRealOpenGap(t *testing.T) {
	registerFakeProvider(t, "prov-a", &fakeSplitAdapter{name: "prov-a"})
	registerFakeProvider(t, "prov-b", &fakeSplitAdapter{name: "prov-b"})
	s := openTestStore(t)
	en := &core.Engine{Repo: s}
	now := int64(1000)
	// Two providers, same capability scope, same 4/1 record → indistinguishable
	// on capability and frequent enough, with no preference yet: an open gap.
	growCapability(t, s, en, "cap=implement", "prov-a", now, 4, 1)
	growCapability(t, s, en, "cap=implement", "prov-b", now, 4, 1)

	gap, accepted := duelOffer(s, "implement",
		bufio.NewReader(strings.NewReader("y\n")), io.Discard, true, now)
	if !accepted {
		t.Fatal("an open capability gap plus y should accept the offer")
	}
	if gap.A != "prov-a" || gap.B != "prov-b" {
		t.Errorf("offer pair = %s/%s, want prov-a/prov-b", gap.A, gap.B)
	}
	if n := countEventsOfType(t, s, "tomo.duel_offered"); n != 1 {
		t.Errorf("the offer must be recorded once (budget spent), got %d", n)
	}

	// Second offer in the same window is now budget-blocked.
	if ok, _ := duelBudgetOK(s, now+1); ok {
		t.Error("the recorded offer should block a second offer in-window")
	}
}

// TestRunDuelRecordsPreferenceFromVerdict: when both sides complete and the
// user picks one, the verdict becomes a preference experience at the gap scope
// and is applied into the preference ledger (ADR-0026 Decision 3) — this is
// where the duel pays off and decide.Choose can start reading it.
func TestRunDuelRecordsPreferenceFromVerdict(t *testing.T) {
	registerFakeProvider(t, "prov-a", &fakeSplitAdapter{name: "prov-a", line: "A thinking", exitCode: 0})
	registerFakeProvider(t, "prov-b", &fakeSplitAdapter{name: "prov-b", line: "B thinking", exitCode: 0})
	s := openTestStore(t)

	gap := curiosity.Gap{A: "prov-a", B: "prov-b", Scope: core.NewScope("cap=implement")}
	verdict := bufio.NewReader(strings.NewReader("1\n")) // prefer A
	if err := runDuel(context.Background(), s, gap, "do the thing", "implement", "", "", 0, verdict, io.Discard, nil); err != nil {
		t.Fatalf("runDuel: %v", err)
	}

	if n := countEventsOfType(t, s, "user.preference"); n != 1 {
		t.Fatalf("a verdict should record exactly one user.preference, got %d", n)
	}
	var preferred, over string
	if err := s.DB.QueryRow(`SELECT json_extract(outcome,'$.preferred'), json_extract(outcome,'$.over')
		FROM experiences WHERE kind = 'preference'`).Scan(&preferred, &over); err != nil {
		t.Fatalf("a preference experience should exist: %v", err)
	}
	if preferred != "prov-a" || over != "prov-b" {
		t.Errorf("preference = %s over %s, want prov-a over prov-b", preferred, over)
	}
	var conns int
	if err := s.DB.QueryRow(`SELECT count(*) FROM connections WHERE kind = 'preference'`).Scan(&conns); err != nil {
		t.Fatal(err)
	}
	if conns < 1 {
		t.Error("the verdict must grow a preference connection the next decision reads")
	}
}

// TestRunDuelSkipsPreferenceWhenOneSideFails: a failed side is judged by its
// own execution experience, not by forfeit — no preference is recorded and no
// verdict is asked (ADR-0026 Decision 3).
func TestRunDuelSkipsPreferenceWhenOneSideFails(t *testing.T) {
	registerFakeProvider(t, "prov-a", &fakeSplitAdapter{name: "prov-a", line: "A", exitCode: 0})
	registerFakeProvider(t, "prov-z", &fakeSplitAdapter{name: "prov-z", line: "Z", exitCode: 1})
	s := openTestStore(t)

	gap := curiosity.Gap{A: "prov-a", B: "prov-z", Scope: core.NewScope("cap=implement")}
	// A "1" here must be ignored: the failure short-circuits before any verdict.
	verdict := bufio.NewReader(strings.NewReader("1\n"))
	if err := runDuel(context.Background(), s, gap, "do the thing", "implement", "", "", 0, verdict, io.Discard, nil); err != nil {
		t.Fatalf("runDuel: %v", err)
	}
	if n := countEventsOfType(t, s, "user.preference"); n != 0 {
		t.Errorf("a duel with a failed side must record no preference, got %d", n)
	}
}

// sessionsWithEvent returns the distinct sessions holding at least one event of
// the given type, in record order.
func sessionsWithEvent(t *testing.T, s *store.Store, typ string) []string {
	t.Helper()
	rows, err := s.DB.Query(
		`SELECT DISTINCT session_id FROM events WHERE type = ? ORDER BY id`, typ)
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
