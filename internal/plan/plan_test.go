package plan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
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

func TestNameRoundTrip(t *testing.T) {
	steps := []string{"analyze", "implement", "test"}
	if got := Steps(Name(steps)); strings.Join(got, ",") != strings.Join(steps, ",") {
		t.Errorf("round trip: %v", got)
	}
}

func TestInitialMenu(t *testing.T) {
	menu := Initial("implement")
	if len(menu) != 3 {
		t.Fatalf("implement should start with 3 variants, got %d", len(menu))
	}
	for _, steps := range menu {
		if !Legal(steps) {
			t.Errorf("initial variant %v must be legal", steps)
		}
	}
	// Every other capability is its own one-step plan.
	other := Initial("review")
	if len(other) != 1 || Name(other[0]) != "review" {
		t.Errorf("default menu should be the capability itself, got %v", other)
	}
}

func TestLegal(t *testing.T) {
	cases := []struct {
		steps []string
		want  bool
	}{
		{[]string{"implement"}, true},
		{[]string{}, false},
		{[]string{"implement", "implement"}, false}, // consecutive dup
		{[]string{"vibecheck"}, false},              // outside vocabulary
		{[]string{"analyze", "implement", "test", "review"}, true},
		{[]string{"analyze", "implement", "test", "review", "commit", "deploy", "notify"}, false}, // too long
	}
	for _, tc := range cases {
		if got := Legal(tc.steps); got != tc.want {
			t.Errorf("Legal(%v) = %v, want %v", tc.steps, got, tc.want)
		}
	}
}

func TestMutationsAreLegalAndDeterministic(t *testing.T) {
	base := []string{"implement", "test"}
	first := Mutations(base)
	second := Mutations(base)
	if len(first) == 0 {
		t.Fatal("a two-step plan must have mutations")
	}
	for i, m := range first {
		if !Legal(m.Steps) {
			t.Errorf("mutation %v is illegal", m)
		}
		if Name(m.Steps) != Name(second[i].Steps) {
			t.Fatal("mutation enumeration must be deterministic")
		}
	}
	// drop of a one-step plan would be empty — never emitted.
	for _, m := range Mutations([]string{"implement"}) {
		if len(m.Steps) == 0 {
			t.Error("empty plan leaked through legality")
		}
	}
}

func TestResolve(t *testing.T) {
	full, ok := Resolve("implement", "full")
	if !ok || full != "analyze>implement>test>review" {
		t.Errorf("full label: %q ok=%v", full, ok)
	}
	canon, ok := Resolve("implement", "implement>test")
	if !ok || canon != "implement>test" {
		t.Errorf("canonical: %q ok=%v", canon, ok)
	}
	if _, ok := Resolve("implement", "implement>vibecheck"); ok {
		t.Error("outside-vocabulary plan must not resolve")
	}
}

// TestProposeBudgetAndSpace (ADR-0014 Decision 3/5): one proposal per
// window, only into free menu space, recorded as plan.generated truth.
func TestProposeBudgetAndSpace(t *testing.T) {
	s := openStore(t)
	menu, err := Live(s, "implement", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(menu) != 3 {
		t.Fatalf("fresh menu should be the 3 initial variants, got %v", menu)
	}

	proposed, err := Propose(s, "implement", menu, now)
	if err != nil {
		t.Fatal(err)
	}
	if proposed == "" || !Legal(Steps(proposed)) {
		t.Fatalf("expected a legal proposal, got %q", proposed)
	}
	for _, existing := range menu {
		if proposed == existing {
			t.Fatal("proposal must be new")
		}
	}

	// The budget is spent for the window.
	if again, _ := Propose(s, "implement", menu, now+1000); again != "" {
		t.Errorf("second proposal within the window must be silent, got %q", again)
	}

	// The proposal entered the live menu via the event log — rebuild-safe.
	menu2, err := Live(s, "implement", now+1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range menu2 {
		if n == proposed {
			found = true
		}
	}
	if !found {
		t.Errorf("proposed variant should be live: %v", menu2)
	}

	// At K variants the menu is full — no more proposals even with budget.
	if len(menu2) >= K {
		if extra, _ := Propose(s, "implement", menu2, now+ProposalWindowMs+1); extra != "" {
			t.Errorf("full menu must not accept proposals, got %q", extra)
		}
	}
}

// TestRetirementDropsDormantVariants (ADR-0014 Decision 5): a proposed plan
// whose connection went dormant leaves the menu; initial variants stay.
func TestRetirementDropsDormantVariants(t *testing.T) {
	s := openStore(t)
	menu, _ := Live(s, "implement", now)
	proposed, err := Propose(s, "implement", menu, now)
	if err != nil || proposed == "" {
		t.Fatal(err)
	}
	// Its connection exists but has been quiet for two half-lives.
	old := now - 2*core.HalfLifeMs - 1000
	if err := s.UpsertConnection(&core.Connection{
		Kind: core.ConnPlan, ScopeKey: "cap=implement", Target: proposed,
		Alpha: 2, Beta: 1, LastUpdate: old, BornTS: old,
	}); err != nil {
		t.Fatal(err)
	}
	live, err := Live(s, "implement", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range live {
		if n == proposed {
			t.Errorf("dormant proposal should retire from the menu: %v", live)
		}
	}
	if len(live) != 3 {
		t.Errorf("initial variants must survive retirement, got %v", live)
	}
}
