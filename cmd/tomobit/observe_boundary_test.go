package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/store"
)

// wireFirstLayer points config at a throwaway place with the given command and
// returns that place, restoring the process-wide cfg afterwards.
func wireFirstLayer(t *testing.T, command string) string {
	t.Helper()
	dir := t.TempDir()
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg = config.Config{TestCommands: map[string]string{dir: command}}
	return dir
}

func testResultPayload(t *testing.T, s *store.Store, sid string) map[string]any {
	t.Helper()
	events, err := s.EventsBySession(sid)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, e := range events {
		if e.Type == "test.result" {
			return e.Payload
		}
	}
	return nil
}

// The first layer finally has a writer (ADR-0052): the command wired for this
// place runs at the boundary and its exit code lands as test.result. Before
// this, the type existed in SCHEMA and perception read it, but nothing in the
// tree ever wrote one — the ledger's first layer was structurally empty.
func TestObserveFirstLayerRecordsAPassingSuite(t *testing.T) {
	s := openTestStore(t)
	dir := wireFirstLayer(t, "exit 0")
	const sid = "obs-pass"

	var out bytes.Buffer
	observeFirstLayer(s, sid, &out, dir)

	p := testResultPayload(t, s, sid)
	if p == nil {
		t.Fatalf("a wired place must record test.result")
	}
	if passed, _ := p["passed"].(bool); !passed {
		t.Errorf("exit 0 is a pass: %v", p)
	}
	// The fact is shown to the human before they grade (Decision 5), without a
	// recommendation attached.
	if !strings.Contains(out.String(), "通った") {
		t.Errorf("the boundary should state the result: %q", out.String())
	}
}

func TestObserveFirstLayerRecordsAFailingSuite(t *testing.T) {
	s := openTestStore(t)
	dir := wireFirstLayer(t, "exit 1")
	const sid = "obs-fail"

	var out bytes.Buffer
	observeFirstLayer(s, sid, &out, dir)

	p := testResultPayload(t, s, sid)
	if p == nil {
		t.Fatalf("a red suite is still an observation")
	}
	if passed, _ := p["passed"].(bool); passed {
		t.Errorf("exit 1 is not a pass: %v", p)
	}
}

// Silence is the opt-in boundary (ADR-0049 applied to ADR-0052 Decision 2): a
// machine that never wired a place must run nothing and record nothing.
func TestObserveFirstLayerIsSilentWhenUnwired(t *testing.T) {
	s := openTestStore(t)
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg = config.Config{}
	const sid = "obs-unwired"

	var out bytes.Buffer
	observeFirstLayer(s, sid, &out, t.TempDir())

	if p := testResultPayload(t, s, sid); p != nil {
		t.Errorf("an unwired machine records nothing, got %v", p)
	}
	if out.Len() != 0 {
		t.Errorf("an unwired machine says nothing, got %q", out.String())
	}
}

// A runner that cannot start observed nothing about the deliverable (Decision
// 4). Recording passed:false here would file a broken test setup as the
// Provider's failure — OutcomeWeight puts a red suite above the human's own
// grade, so the lie would outrank them.
func TestObserveFirstLayerRecordsNothingWhenItCouldNotObserve(t *testing.T) {
	s := openTestStore(t)
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	gone := filepath.Join(t.TempDir(), "not-created")
	cfg = config.Config{TestCommands: map[string]string{gone: "exit 0"}}
	const sid = "obs-broken"

	var out bytes.Buffer
	observeFirstLayer(s, sid, &out, gone)

	if p := testResultPayload(t, s, sid); p != nil {
		t.Errorf("an unobservable boundary records nothing, got %v", p)
	}
}

// When the Provider moved its work into an isolated workspace (ADR-0050), the
// first layer must follow it. Testing the original checkout would report a
// serene green about work nobody did there — the exact interaction both ADRs
// flagged as the one thing implementation must not miss.
func TestObserveFirstLayerFollowsAnIsolatedWorkspace(t *testing.T) {
	s := openTestStore(t)
	wired := wireFirstLayer(t, "test -f marker")
	const sid = "obs-isolated"

	// The wired place has no marker; the isolated workspace does. A green
	// result therefore proves the command ran in the worktree, not the repo.
	isolated := t.TempDir()
	if err := os.WriteFile(filepath.Join(isolated, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(sid, "task.workspace", time.Now().UnixMilli(), map[string]any{
		"isolated": true, "kind": "git worktree", "path": isolated,
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	observeFirstLayer(s, sid, &out, wired)

	p := testResultPayload(t, s, sid)
	if p == nil {
		t.Fatalf("the first layer must still be observed")
	}
	if passed, _ := p["passed"].(bool); !passed {
		t.Errorf("the command must have run in the isolated workspace: %v", p)
	}
}

// A declared path that isn't there must not cost the signal. The ledger keeps
// the declaration unchanged (ADR-0050 refuses to verify it); only the place
// the observer runs in degrades, loudly.
func TestObserveFirstLayerFallsBackWhenTheDeclaredPathIsGone(t *testing.T) {
	s := openTestStore(t)
	wired := wireFirstLayer(t, "exit 0")
	const sid = "obs-ghost"

	if err := s.AppendEvent(sid, "task.workspace", time.Now().UnixMilli(), map[string]any{
		"isolated": true, "path": filepath.Join(t.TempDir(), "never-made"),
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	observeFirstLayer(s, sid, &out, wired)

	if p := testResultPayload(t, s, sid); p == nil {
		t.Errorf("a missing worktree must not cost the first-layer signal")
	}
}

// A declined isolation means the work stayed where it was — the observer must
// not go looking elsewhere.
func TestObserveFirstLayerIgnoresADeclinedIsolation(t *testing.T) {
	s := openTestStore(t)
	wired := wireFirstLayer(t, "exit 0")
	const sid = "obs-declined"

	if err := s.AppendEvent(sid, "task.workspace", time.Now().UnixMilli(), map[string]any{
		"isolated": false, "reason": "VCSが無い",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	observeFirstLayer(s, sid, &out, wired)

	if p := testResultPayload(t, s, sid); p == nil {
		t.Errorf("an unisolated run is observed at the wired place")
	}
}

// The parent of a split reaches the boundary with judged=false (ADR-0023
// Decision 5), so its Adopted stays "" — which is exactly the branch where
// OutcomeWeight lets a passing suite score y=0.9. This is the first objective
// signal a `do` split parent has ever been able to receive.
func TestSplitParentReachesTheFirstLayerThroughFinishTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // 境界の知覚は機械共通の perceive.lock を取る — 実HOMEを掴ませない
	s := openTestStore(t)
	dir := wireFirstLayer(t, "exit 0")
	const sid = "split-parent"
	openParentTask(t, s, sid)

	in := bufio.NewReader(strings.NewReader("\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := finishTask(s, sid, in, &out, false, false, extractor, dir); err != nil {
		t.Fatalf("finishTask: %v", err)
	}

	if p := testResultPayload(t, s, sid); p == nil {
		t.Errorf("the split parent's boundary must observe the first layer")
	}
}

// Split children never reach finishTask — split.go records provider.error and
// an empty task.finished directly (ADR-0028 Decision 5). That is not an
// accident this test tolerates, it is the mechanism ADR-0052 Decision 3 relies
// on: groups run sequentially and a later group may depend on an earlier one's
// work, so a red suite between them is a normal intermediate state. Attributing
// it to the child that happened to finish first would make the ledger lie.
func TestSplitChildrenAreNotObserved(t *testing.T) {
	s := openTestStore(t)
	dir := wireFirstLayer(t, "exit 1") // would be a damning signal if it landed
	registerFakeProvider(t, "oc", &fakeSplitAdapter{name: "oc", line: "ran"})
	const parentSID = "obs-split-parent"
	openParentTask(t, s, parentSID)

	in := bufio.NewReader(strings.NewReader("y\n\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, [][]string{{"a"}, {"b"}},
		"big task", namedWiring("oc"), in, &out, false, extractor); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	children := subtaskSessionIDs(t, s, parentSID)
	if len(children) == 0 {
		t.Fatalf("the split must have opened children")
	}
	for _, child := range children {
		if n := countEventsOfTypeInSession(t, s, child, "test.result"); n != 0 {
			t.Errorf("child %s must not carry a test.result, got %d", child, n)
		}
	}
	_ = dir
}
