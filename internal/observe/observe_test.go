package observe

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLookupMatchesTheDeepestConfiguredPlace(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	inner := filepath.Join(repo, "pkg", "deep")

	commands := map[string]string{
		root: "root-suite",
		repo: "repo-suite",
	}

	// A chat that /cd'd deep into the tree still finds the repository's entry,
	// and the deepest configured place wins over its ancestor (ADR-0052 D2).
	cmd, dir, ok := Lookup(commands, inner)
	if !ok {
		t.Fatalf("a place inside a configured repo must match")
	}
	if cmd != "repo-suite" {
		t.Errorf("deepest match should win: got %q, want %q", cmd, "repo-suite")
	}
	if dir != filepath.Clean(repo) {
		t.Errorf("the command runs at the matched root: got %q, want %q", dir, repo)
	}
}

func TestLookupDoesNotMatchASiblingSharingAPrefix(t *testing.T) {
	root := t.TempDir()
	// "/a/bc" must not be treated as living under "/a/b": the separator check
	// is the whole reason contains() exists rather than a bare HasPrefix.
	configured := filepath.Join(root, "b")
	sibling := filepath.Join(root, "bc")

	if _, _, ok := Lookup(map[string]string{configured: "suite"}, sibling); ok {
		t.Errorf("%q must not match under %q", sibling, configured)
	}
}

func TestLookupTreatsUnwiredAsSilent(t *testing.T) {
	dir := t.TempDir()

	// Three shapes of "nothing is wired here". None of them may run anything:
	// silence is the opt-in boundary this feature stands on (ADR-0049).
	cases := map[string]map[string]string{
		"nil map":       nil,
		"empty map":     {},
		"other place":   {filepath.Join(dir, "elsewhere"): "suite"},
		"blank command": {dir: "   "},
	}
	for name, commands := range cases {
		if _, _, ok := Lookup(commands, dir); ok {
			t.Errorf("%s must not resolve to a command", name)
		}
	}
}

func TestRunReadsTheExitCodeAsTheObservation(t *testing.T) {
	dir := t.TempDir()

	res, ran, err := Run(context.Background(), "exit 0", dir, time.Minute)
	if err != nil || !ran {
		t.Fatalf("a command that runs must be observed: ran=%v err=%v", ran, err)
	}
	if !res.Passed || res.ExitCode != 0 {
		t.Errorf("exit 0 is a pass: %+v", res)
	}

	res, ran, err = Run(context.Background(), "exit 3", dir, time.Minute)
	if err != nil || !ran {
		t.Fatalf("a failing command still ran: ran=%v err=%v", ran, err)
	}
	if res.Passed {
		t.Errorf("exit 3 is not a pass: %+v", res)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit code is kept for audit: got %d, want 3", res.ExitCode)
	}
}

func TestRunRecordsNothingWhenItCouldNotObserve(t *testing.T) {
	// A timeout and a missing working directory are not verdicts on the
	// deliverable. Recording passed:false for either would file a broken test
	// setup as the Provider's failure (ADR-0052 Decision 4).
	dir := t.TempDir()

	_, ran, err := Run(context.Background(), "sleep 5", dir, 50*time.Millisecond)
	if ran {
		t.Errorf("a suite that never finished observed nothing")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the timeout should surface as such: %v", err)
	}

	if _, ran, _ := Run(context.Background(), "true", filepath.Join(dir, "gone"), time.Minute); ran {
		t.Errorf("a command that never started observed nothing")
	}
}

func TestPayloadCarriesTheCommandItJudged(t *testing.T) {
	p := Result{Passed: true, ExitCode: 0, DurationMs: 1200, Command: "go test ./..."}.Payload()

	if passed, ok := p["passed"].(bool); !ok || !passed {
		t.Errorf("perception reads `passed` and nothing else: %v", p["passed"])
	}
	// Without the command, a later reader cannot say what passed:true was true
	// *about* — the audit value is the whole reason it is recorded.
	if got, _ := p["command"].(string); got != "go test ./..." {
		t.Errorf("command must survive into the ledger: %q", got)
	}
	if _, ok := p["duration_ms"]; !ok {
		t.Errorf("duration is part of the audit trail")
	}
}

func TestRunUsesAShellSoPipesAndRedirectionWork(t *testing.T) {
	// GUI ADR-0007's judgment, applied here: argv splitting buys no safety and
	// only removes usable commands. A user's real suite is often a pipeline.
	dir := t.TempDir()
	res, ran, err := Run(context.Background(), "echo hi | grep -q hi", dir, time.Minute)
	if err != nil || !ran {
		t.Fatalf("a pipeline must run: ran=%v err=%v", ran, err)
	}
	if !res.Passed {
		t.Errorf("the pipeline succeeded: %+v", res)
	}
}

func TestLookupExpandsALeadingTilde(t *testing.T) {
	// config is hand-written JSON, so "~/dev/repo" is a shape people type. An
	// unexpanded tilde would silently never match, which reads as "the feature
	// does not work" rather than "the path was wrong".
	home := t.TempDir()
	t.Setenv("HOME", home)
	place := filepath.Join(home, "dev", "repo")

	cmd, _, ok := Lookup(map[string]string{"~/dev/repo": "suite"}, place)
	if !ok || cmd != "suite" {
		t.Errorf("a tilde path must resolve: ok=%v cmd=%q", ok, cmd)
	}
}

func TestRunDefaultsTheTimeoutWhenUnset(t *testing.T) {
	// 0 means "not configured", not "no time at all" — a zero-timeout context
	// would expire before the shell started and record nothing, forever.
	dir := t.TempDir()
	res, ran, err := Run(context.Background(), "true", dir, 0)
	if err != nil || !ran {
		t.Fatalf("an unset timeout must fall back to the default: ran=%v err=%v", ran, err)
	}
	if !res.Passed {
		t.Errorf("`true` passes: %+v", res)
	}
}

func TestLookupIsCaseSensitiveAboutSeparatorsNotContent(t *testing.T) {
	// Guards the shape of contains(): a configured path that already ends in a
	// separator must behave the same as one that does not.
	root := t.TempDir()
	withSep := root + string(filepath.Separator)

	cmd, _, ok := Lookup(map[string]string{withSep: "suite"}, root)
	if !ok || cmd != "suite" {
		t.Errorf("a trailing separator must not break the match: ok=%v cmd=%q", ok, cmd)
	}
	if strings.HasSuffix(filepath.Clean(withSep), string(filepath.Separator)) {
		t.Errorf("absClean should have removed the trailing separator")
	}
}
