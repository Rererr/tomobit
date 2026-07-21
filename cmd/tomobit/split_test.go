package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
)

// markerFailAdapter is a test-only adapter whose exit code depends on the
// prompt: a subtask whose text contains failMarker exits non-zero, any other
// exits clean. One registered provider can thus produce a mix of failing and
// succeeding subtasks deterministically — the only way to pin "the whole group
// runs even though one member fails" without a nondeterministic auto lottery.
type markerFailAdapter struct {
	name       string
	failMarker string
}

func (a *markerFailAdapter) Name() string { return a.name }

func (a *markerFailAdapter) Command(req executor.Request) (string, []string, []string) {
	exit := 0
	if a.failMarker != "" && strings.Contains(req.Prompt, a.failMarker) {
		exit = 1
	}
	return "sh", []string{"-c", fmt.Sprintf("echo ran; exit %d", exit)}, nil
}

func (a *markerFailAdapter) Translate(line []byte) ([]executor.Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	return []executor.Event{{
		Type:    executor.EventProviderOutput,
		Payload: map[string]any{"text": string(line)},
	}}, nil
}

func openParentTask(t *testing.T, s *store.Store, parentSID string) {
	t.Helper()
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}
}

func seedProviderCosts(t *testing.T, s *store.Store, costs ...float64) {
	t.Helper()
	for i, c := range costs {
		if err := s.AppendEvent(fmt.Sprintf("cost-%d", i), "provider.finished", int64(500+i),
			map[string]any{"cost_usd": c}); err != nil {
			t.Fatal(err)
		}
	}
}

// (a) An accepted gate runs a wide group's members in parallel to completion —
// a failing member does not abandon its neighbor (走り切り) — and a group-level
// failure stops the next group from starting (群間失敗停止, ADR-0028 Decision 4).
func TestRunSplitAcceptedRunsWholeGroupThenStopsAtGroupBoundary(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "marker", &markerFailAdapter{name: "marker", failMarker: "FAILSUB"})
	const parentSID = "parent-accept"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"FAILSUB alpha", "beta"}, {"gamma"}}
	in := bufio.NewReader(strings.NewReader("y\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, groups, "big task", "marker",
		"implement", "", "", 0, in, &out, true, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	subs := subtaskSessionIDs(t, s, parentSID)
	if len(subs) != 2 {
		t.Fatalf("group走り切り + 群間停止: both wide members open, gamma never does — want 2 sessions, got %d", len(subs))
	}
	failures := 0
	for _, sid := range subs {
		if n := countEventsOfTypeInSession(t, s, sid, "provider.output"); n < 1 {
			t.Errorf("member %s produced no output — the group did not run it", sid)
		}
		if n := countEventsOfTypeInSession(t, s, sid, "task.finished"); n != 1 {
			t.Errorf("member %s: task.finished = %d, want 1", sid, n)
		}
		failures += countEventsOfTypeInSession(t, s, sid, "provider.error")
	}
	if failures != 1 {
		t.Errorf("exactly the FAILSUB member records provider.error, got %d", failures)
	}
}

// (b) A declined gate falls back to the flat sequential order with the ADR-0023
// fail-stop: the first (failing) subtask stops the run before the second opens,
// so a wide group is not run to completion here — the observable opposite of (a)
// (ADR-0028 Decision 3: n でも作業は失われない, but it is sequential).
func TestRunSplitDeclinedRunsSequentialFailStop(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "marker", &markerFailAdapter{name: "marker", failMarker: "FAILSUB"})
	const parentSID = "parent-decline"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"FAILSUB alpha", "beta"}, {"gamma"}}
	in := bufio.NewReader(strings.NewReader("n\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, groups, "big task", "marker",
		"implement", "", "", 0, in, &out, true, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	subs := subtaskSessionIDs(t, s, parentSID)
	if len(subs) != 1 {
		t.Fatalf("sequential fail-stop: only the first (failing) subtask opens, got %d sessions", len(subs))
	}
	if n := countEventsOfTypeInSession(t, s, subs[0], "provider.error"); n != 1 {
		t.Errorf("the failed subtask should record provider.error, got %d", n)
	}
	split := payloadOf(t, s, "task.split")
	if split["parallel_offered"] != true || split["parallel_accepted"] != false {
		t.Errorf("a declined wide-group gate: offered=true, accepted=false — got %v", split)
	}
}

// (c) A non-interactive run never sees the gate, so it stays sequential even
// though a "y" is waiting on the reader — the pipe/CI determinism ADR-0028
// Consequences requires. Same observable as (b): one session, fail-stop.
func TestRunSplitNonInteractiveNeverParallels(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "marker", &markerFailAdapter{name: "marker", failMarker: "FAILSUB"})
	const parentSID = "parent-noninteractive"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"FAILSUB alpha", "beta"}, {"gamma"}}
	in := bufio.NewReader(strings.NewReader("y\n")) // must be ignored: no terminal
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, groups, "big task", "marker",
		"implement", "", "", 0, in, &out, false, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	if n := len(subtaskSessionIDs(t, s, parentSID)); n != 1 {
		t.Fatalf("non-interactive must stay sequential (fail-stop after the first), got %d sessions", n)
	}
	if strings.Contains(out.String(), "並走") {
		t.Errorf("no gate line may be shown non-interactively, got %q", out.String())
	}
	split := payloadOf(t, s, "task.split")
	if split["parallel_offered"] != false || split["parallel_accepted"] != false {
		t.Errorf("non-interactive: the gate is neither offered nor accepted — got %v", split)
	}
}

// (d) A flat proposal (no wide group) is never offered the gate, even
// interactively with a "y" ready: there is no declared parallelism to permit,
// so it runs sequentially and records no parallel offer (ADR-0028 Decision 2/3).
func TestRunSplitFlatProposalSkipsGate(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "done"})
	const parentSID = "parent-flat-gate"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"one"}, {"two"}}
	in := bufio.NewReader(strings.NewReader("y\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, groups, "big task", "fake-split",
		"implement", "", "", 0, in, &out, true, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	if strings.Contains(out.String(), "並走") {
		t.Errorf("a flat proposal must not show the gate, got %q", out.String())
	}
	if n := len(subtaskSessionIDs(t, s, parentSID)); n != 2 {
		t.Errorf("both flat subtasks should run sequentially, got %d sessions", n)
	}
	split := payloadOf(t, s, "task.split")
	if _, ok := split["parallel_offered"]; ok {
		t.Errorf("a flat proposal omits the gate fields (SCHEMA R4: groups以降を省略), got parallel_offered=%v", split["parallel_offered"])
	}
	if _, ok := split["groups"]; ok {
		t.Errorf("a flat proposal omits groups, got %v", split["groups"])
	}
}

// (e) The cost estimate is the median of the recent real provider costs times
// the parallel width; with no cost sample it reports "no estimate" rather than
// fabricating a number (ADR-0028 Decision 3).
func TestParallelCostEstimate(t *testing.T) {
	s := openTestStore(t)
	groups := [][]string{{"x", "y"}} // width 2

	if _, ok := parallelCostEstimate(s, groups); ok {
		t.Error("no cost sample must yield no estimate — a fabricated number is worse than none")
	}

	seedProviderCosts(t, s, 0.10, 0.30, 0.20) // median 0.20
	usd, ok := parallelCostEstimate(s, groups)
	if !ok {
		t.Fatal("with samples an estimate should be available")
	}
	if want := 0.20 * 2; usd != want {
		t.Errorf("estimate = %v, want median(0.20) × width(2) = %v", usd, want)
	}
}

// (f) The gate's presentation and answer are recorded on task.split for the
// Decision 1 metric: parallel_offered / parallel_accepted / est_cost_usd
// (ADR-0028 Decision 3, SCHEMA.md R4).
func TestRunSplitRecordsGatePayload(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "done"})
	const parentSID = "parent-payload"
	openParentTask(t, s, parentSID)
	seedProviderCosts(t, s, 0.10, 0.30, 0.20) // median 0.20

	groups := [][]string{{"x", "y"}} // width 2 → est 0.40
	in := bufio.NewReader(strings.NewReader("y\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, groups, "big task", "fake-split",
		"implement", "", "", 0, in, &out, true, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	split := payloadOf(t, s, "task.split")
	if split["parallel_offered"] != true || split["parallel_accepted"] != true {
		t.Errorf("an accepted gate records offered=accepted=true, got %v", split)
	}
	est, ok := split["est_cost_usd"].(float64)
	if !ok || est != 0.40 {
		t.Errorf("est_cost_usd = %v, want median(0.20) × width(2) = 0.40", split["est_cost_usd"])
	}
}

// W3a: a group wider than parallelWidthCap still runs every member to
// completion — the semaphore throttles concurrency, it does not drop members
// (ADR-0028 Decision 4 / 実装時ノブ). Width 4 against cap 3 forces the throttle.
func TestRunSplitParallelGroupWiderThanCapCompletesEveryMember(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "ran"})
	const parentSID = "parent-wide"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"s1", "s2", "s3", "s4"}} // width 4 > parallelWidthCap (3)
	in := bufio.NewReader(strings.NewReader("y\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, groups, "big task", "fake-split",
		"implement", "", "", 0, in, &out, true, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	subs := subtaskSessionIDs(t, s, parentSID)
	if len(subs) != 4 {
		t.Fatalf("every member of a wide group opens a session, got %d", len(subs))
	}
	for _, sid := range subs {
		if n := countEventsOfTypeInSession(t, s, sid, "provider.output"); n < 1 {
			t.Errorf("member %s produced no output — the cap dropped it instead of throttling", sid)
		}
		if n := countEventsOfTypeInSession(t, s, sid, "task.finished"); n != 1 {
			t.Errorf("member %s: task.finished = %d, want 1 (all must complete)", sid, n)
		}
	}
}

// W3b: when --provider auto routes a wide group's members to the human executor,
// they run off the goroutine path — a human has no stream — and still complete
// (ADR-0028 Decision 4). An empty providers map makes "human" the only candidate,
// so the routing is deterministic; the all-human group exercises exactly the
// human branch of runGroupParallel (the mixed provider+human case cannot be
// pinned deterministically, since auto's per-member lottery is nondeterministic).
func TestRunGroupParallelRunsAutoRoutedHumanMembers(t *testing.T) {
	s := openTestStore(t)
	saved := providers
	providers = map[string]executor.Adapter{} // no adapters → auto can only route to human
	t.Cleanup(func() { providers = saved })

	const parentSID = "parent-human-group"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"h1", "h2"}} // a wide group, both auto-routed to human
	// gate "y", then one line per human member (runHuman reads and discards it).
	in := bufio.NewReader(strings.NewReader("y\n\n\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, groups, "big task", "auto",
		"implement", "", "", 0, in, &out, true, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	subs := subtaskSessionIDs(t, s, parentSID)
	if len(subs) != 2 {
		t.Fatalf("both human members open sessions, got %d", len(subs))
	}
	for _, sid := range subs {
		if n := countEventsOfTypeInSession(t, s, sid, "provider.selected"); n != 1 {
			t.Errorf("human member %s should record provider.selected (human), got %d", sid, n)
		}
		if n := countEventsOfTypeInSession(t, s, sid, "task.finished"); n != 1 {
			t.Errorf("human member %s: task.finished = %d, want 1", sid, n)
		}
		if n := countEventsOfTypeInSession(t, s, sid, "provider.error"); n != 0 {
			t.Errorf("a human member has no failure signal, got provider.error = %d", n)
		}
	}
}

// TestMedianEvenCountAveragesTheTwoMiddle covers the even branch (the sample can
// hold any count) and pins that median leaves the caller's slice order intact —
// the recent-cost sample is newest-first and must stay that way.
func TestMedianEvenCountAveragesTheTwoMiddle(t *testing.T) {
	if got := median([]float64{0.10, 0.20, 0.30, 0.40}); got != 0.25 {
		t.Errorf("median of 4 = %v, want mean of the two middle (0.20,0.30) = 0.25", got)
	}
	src := []float64{0.40, 0.10, 0.30, 0.20}
	if got := median(src); got != 0.25 {
		t.Errorf("unsorted median = %v, want 0.25", got)
	}
	if src[0] != 0.40 {
		t.Errorf("median must not reorder the caller's slice, got %v", src)
	}
}

// (g) SIGINT while a group runs records task.cancelled on every opened child and
// the parent, and skips the closing finishTask (ADR-0028 Decision 4, duel's
// shape). A pre-cancelled context is a deterministic stand-in for the signal.
func TestRunSplitParallelCancellationRecordsAllChildrenAndParent(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "pc", &fakeSplitAdapter{name: "pc", line: "ran"})
	const parentSID = "parent-cancel"
	openParentTask(t, s, parentSID)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the signal has already arrived by the time the group runs

	groups := [][]string{{"p1", "p2"}}
	in := bufio.NewReader(strings.NewReader("y\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(ctx, s, parentSID, groups, "big task", "pc",
		"implement", "", "", 0, in, &out, true, extractor, nil); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	children := subtaskSessionIDs(t, s, parentSID)
	if len(children) != 2 {
		t.Fatalf("both members open their sessions before the run, got %d", len(children))
	}
	for _, sid := range children {
		if n := countEventsOfTypeInSession(t, s, sid, "task.cancelled"); n != 1 {
			t.Errorf("child %s: task.cancelled = %d, want 1", sid, n)
		}
		if n := countEventsOfTypeInSession(t, s, sid, "task.finished"); n != 0 {
			t.Errorf("child %s: a cancelled child records no task.finished, got %d", sid, n)
		}
	}
	if n := countEventsOfTypeInSession(t, s, parentSID, "task.cancelled"); n != 1 {
		t.Errorf("parent task.cancelled = %d, want 1", n)
	}
	if n := countEventsOfTypeInSession(t, s, parentSID, "task.finished"); n != 0 {
		t.Errorf("a cancelled split records no parent task.finished, got %d", n)
	}
}
