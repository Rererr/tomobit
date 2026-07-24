// ADR-0043 (auto by default) の帰結を固定するテスト群:
// Decision 1 = --provider の既定そのもの / Decision 2 = 候補は起動できる
// Provider のみ / Decision 3 = 起動できなかった実行は非ゼロ終了・非記帳 /
// Decision 4 = human は知っている文脈でのみ候補。
package main

import (
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
)

// TestProviderFlagDefaultsToAuto reads the registered flag's own DefValue —
// the default do and chat actually parse — rather than asserting a literal
// the test itself supplied, so silently changing the default back to a fixed
// provider fails here (ADR-0043 Decision 1).
func TestProviderFlagDefaultsToAuto(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	providerFlag(fs)
	f := fs.Lookup("provider")
	if f == nil {
		t.Fatal("providerFlag must register --provider")
	}
	if f.DefValue != "auto" {
		t.Errorf("--provider default = %q, want \"auto\" (ADR-0043 Decision 1)", f.DefValue)
	}
}

// missingExeAdapter is a registered provider whose executable exists nowhere:
// the "registered but not installed" shape ADR-0043 Decision 2/3 draws its
// boundary around.
type missingExeAdapter struct{ name string }

func (a missingExeAdapter) Name() string { return a.name }
func (a missingExeAdapter) Command(executor.Request) (string, []string, []string) {
	return "tomobit-test-no-such-binary", nil, nil
}
func (a missingExeAdapter) Translate([]byte) ([]executor.Event, error) { return nil, nil }

func TestRunnableNamesDropsProvidersWhoseExecutableIsMissing(t *testing.T) {
	registerFakeProvider(t, "prov-here", &fakeSplitAdapter{name: "prov-here"}) // launches via sh
	registerFakeProvider(t, "prov-gone", missingExeAdapter{name: "prov-gone"})
	onPath := func(exe string) bool { return exe == "sh" }
	got := runnableNames([]string{"prov-gone", "prov-here"}, onPath)
	if len(got) != 1 || got[0] != "prov-here" {
		t.Errorf("runnableNames = %v, want [prov-here] — a provider whose executable is missing is no candidate (ADR-0043 Decision 2)", got)
	}
}

// The wiring end of Decision 2: autoDecide's audited candidate set — what the
// lottery could actually have drawn — holds the launchable fake and never the
// unlaunchable one. Real PATH is used on purpose: sh exists everywhere, the
// fake binary nowhere, so the test is deterministic without stubbing.
func TestAutoDecideCandidatesAreOnlyLaunchableProviders(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "prov-here", &fakeSplitAdapter{name: "prov-here"})
	registerFakeProvider(t, "prov-gone", missingExeAdapter{name: "prov-gone"})

	dec, err := autoDecide(s, io.Discard, "s-avail", "implement", "", nil)
	if err != nil {
		t.Fatalf("autoDecide: %v", err)
	}
	var sawHere, sawGone bool
	for _, c := range dec.Candidates {
		switch c.Provider {
		case "prov-here":
			sawHere = true
		case "prov-gone":
			sawGone = true
		}
	}
	if !sawHere {
		t.Error("a launchable provider must be a candidate")
	}
	if sawGone {
		t.Error("a provider whose executable is missing must never enter the lottery (ADR-0043 Decision 2)")
	}
}

// Every registered adapter unlaunchable and human unknown: auto must fail
// with a meaningful error, not panic on an empty tournament or silently pick
// nothing.
func TestAutoDecideErrsWhenNothingCanRun(t *testing.T) {
	s := openTestStore(t)
	saved := providers
	providers = map[string]executor.Adapter{"prov-gone": missingExeAdapter{name: "prov-gone"}}
	t.Cleanup(func() { providers = saved })

	_, err := autoDecide(s, io.Discard, "s-none", "implement", "", nil)
	if err == nil || !strings.Contains(err.Error(), "no runnable provider") {
		t.Errorf("want a 'no runnable provider' error, got %v", err)
	}
}

// TestDoFailsAndRecordsNoExperienceWhenTheProviderNeverStarts (ADR-0043
// Decision 3): the old path recorded provider.error and returned nil — the
// user saw a clean exit, the ledger a provider failure that was really the
// machine's. Now the command errors (main exits non-zero) and the session
// carries neither provider.error nor a task boundary, so PendingSessions can
// never hand it to perception.
func TestDoFailsAndRecordsNoExperienceWhenTheProviderNeverStarts(t *testing.T) {
	t.Setenv("TOMOBIT_FACE", "0")
	db := filepath.Join(t.TempDir(), "t.db")
	registerFakeProvider(t, "prov-gone", missingExeAdapter{name: "prov-gone"})

	err := cmdDo([]string{"--db", db, "--provider", "prov-gone", "run the thing"})
	if err == nil {
		t.Fatal("a run that never started must fail the command")
	}
	if !strings.Contains(err.Error(), "never started") {
		t.Errorf("the error should say the provider never started, got %v", err)
	}

	s, oerr := store.Open(db)
	if oerr != nil {
		t.Fatal(oerr)
	}
	defer s.Close()
	counts := map[string]int{}
	rows, qerr := s.DB.Query(`SELECT type, COUNT(*) FROM events GROUP BY type`)
	if qerr != nil {
		t.Fatal(qerr)
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			t.Fatal(err)
		}
		counts[typ] = n
	}
	if counts["task.started"] != 1 {
		t.Errorf("the ask itself is a fact and stays recorded, got task.started=%d", counts["task.started"])
	}
	for _, typ := range []string{"provider.error", "task.finished", "task.cancelled"} {
		if counts[typ] != 0 {
			t.Errorf("%s = %d, want 0 — a launch that never happened is not evidence and not a boundary", typ, counts[typ])
		}
	}
	if pending, err := s.PendingSessions(1); err != nil || len(pending) != 0 {
		t.Errorf("the session must never enter the perception queue, got %v (err %v)", pending, err)
	}
}

// ADR-0043 Decision 4: human joins the lottery only in a context that already
// holds a human capability connection; blankness — or knowledge about a
// different context — keeps human out.
func TestAutoDecideHumanCandidacyRequiresAKnownContext(t *testing.T) {
	humanIn := func(t *testing.T, s *store.Store) bool {
		t.Helper()
		dec, err := autoDecide(s, io.Discard, "s-human", "implement", "", nil)
		if err != nil {
			t.Fatalf("autoDecide: %v", err)
		}
		for _, c := range dec.Candidates {
			if c.Provider == "human" {
				return true
			}
		}
		return false
	}

	t.Run("blank ledger", func(t *testing.T) {
		s := openTestStore(t)
		registerFakeProvider(t, "prov-here", &fakeSplitAdapter{name: "prov-here"})
		if humanIn(t, s) {
			t.Error("an ignorant lottery must not hand the task back to the user")
		}
	})
	t.Run("human known in another context only", func(t *testing.T) {
		s := openTestStore(t)
		registerFakeProvider(t, "prov-here", &fakeSplitAdapter{name: "prov-here"})
		knowHuman(t, s, "cap=review")
		if humanIn(t, s) {
			t.Error("knowledge about cap=review says nothing about cap=implement")
		}
	})
	t.Run("human known here", func(t *testing.T) {
		s := openTestStore(t)
		registerFakeProvider(t, "prov-here", &fakeSplitAdapter{name: "prov-here"})
		knowHuman(t, s, "cap=implement")
		if !humanIn(t, s) {
			t.Error("a context that knows human must let human compete (ADR-0018 Decision 2 remains for the known case)")
		}
	})
}
