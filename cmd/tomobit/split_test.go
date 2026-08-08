package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/subtask"
)

// plain turns the bare-string group shape into elements that name no executor
// — every proposal before ADR-0060, and what a test writing a subtask as a
// string still means. Tests about the named executor build their elements
// directly.
func plain(groups [][]string) [][]subtask.Element {
	out := make([][]subtask.Element, len(groups))
	for i, g := range groups {
		out[i] = make([]subtask.Element, len(g))
		for j, s := range g {
			out[i][j] = subtask.Element{Do: s}
		}
	}
	return out
}

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

// sessionPayloadOf reads one session's first event of a type. The split tests
// need it per child, which payloadOf (first event repo-wide) cannot give.
func sessionPayloadOf(t *testing.T, s *store.Store, sid, typ string) map[string]any {
	t.Helper()
	var raw string
	if err := s.DB.QueryRow(`SELECT payload FROM events WHERE session_id = ? AND type = ? ORDER BY id LIMIT 1`,
		sid, typ).Scan(&raw); err != nil {
		t.Fatalf("%s/%s: %v", sid, typ, err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// outputTextOf joins one session's provider.output text. An adapter that
// echoes its own name turns this into "which provider actually ran this
// child" — the only observation that distinguishes a routed child from an
// inherited one without a real CLI.
func outputTextOf(t *testing.T, s *store.Store, sid string) string {
	t.Helper()
	rows, err := s.DB.Query(`SELECT payload FROM events WHERE session_id = ? AND type = 'provider.output' ORDER BY id`, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		if s, ok := p["text"].(string); ok {
			texts = append(texts, s)
		}
	}
	return strings.Join(texts, "\n")
}

// TestNamedExecutorRunsThatChildOnTheDeclaredProvider is ADR-0060 Decisions 2
// and 4 end to end: the declared child runs on the named adapter, its
// neighbour in the same group keeps the parent's, and only the declared one
// carries named_provider on its task.started — the field that keeps a named
// run separable from one the lottery routed.
func TestNamedExecutorRunsThatChildOnTheDeclaredProvider(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "parent-side", &fakeSplitAdapter{name: "parent-side", line: "parent-side"})
	registerFakeProvider(t, "named-side", &fakeSplitAdapter{name: "named-side", line: "named-side"})
	const parentSID = "parent-named"
	openParentTask(t, s, parentSID)

	groups := [][]subtask.Element{{
		{Do: "alpha", Provider: "named-side"},
		{Do: "beta"},
	}}
	var out bytes.Buffer
	if err := runSplit(context.Background(), s, parentSID, groups, "big task",
		namedWiring("parent-side"), bufio.NewReader(strings.NewReader("")), &out,
		&fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	subs := subtaskSessionIDs(t, s, parentSID)
	if len(subs) != 2 {
		t.Fatalf("both members of the group should open, got %d", len(subs))
	}
	// Sessions open in proposal order — runGroupParallel opens all of them
	// before any goroutine starts — so subs[0] is the declared one.
	named, inherited := subs[0], subs[1]
	if got := outputTextOf(t, s, named); !strings.Contains(got, "named-side") {
		t.Errorf("the declared child should run on named-side, its output says %q", got)
	}
	if got := outputTextOf(t, s, inherited); !strings.Contains(got, "parent-side") {
		t.Errorf("the neighbour named nobody and must keep the parent's adapter, its output says %q", got)
	}
	if got := sessionPayloadOf(t, s, named, "task.started")["named_provider"]; got != "named-side" {
		t.Errorf("the declaration should be recorded on the child: got %v", got)
	}
	if _, ok := sessionPayloadOf(t, s, inherited, "task.started")["named_provider"]; ok {
		t.Errorf("an inherited child must record no declaration — the field would claim a routing that never happened")
	}
}

// TestForElementFallsBackPerChild pins every refusal in one place: each one
// keeps the parent's wiring for that child alone, records nothing, and says
// why. A dropped declaration in silence would leave the Provider believing it
// routed work it did not route.
func TestForElementFallsBackPerChild(t *testing.T) {
	registerFakeProvider(t, "parent-side", &fakeSplitAdapter{name: "parent-side", line: "parent-side"})
	registerFakeProvider(t, "named-side", &fakeSplitAdapter{name: "named-side", line: "named-side"})
	saved := cfg
	t.Cleanup(func() { cfg = saved })

	parent := namedWiring("parent-side")
	human := subtaskWiring{human: true, capability: "implement"}

	cases := []struct {
		name    string
		w       subtaskWiring
		e       subtask.Element
		off     bool
		wantWhy string
	}{
		{name: "unknown name", w: parent, e: subtask.Element{Do: "a", Provider: "nope"}, wantWhy: "解決できない"},
		{name: "human is refused", w: parent, e: subtask.Element{Do: "a", Provider: "human"}, wantWhy: "human は指名できない"},
		{name: "human parent keeps its children", w: human, e: subtask.Element{Do: "a", Provider: "named-side"}, wantWhy: "人が担っている"},
		{name: "kill switch", w: parent, e: subtask.Element{Do: "a", Provider: "named-side"}, off: true, wantWhy: "named_executor=false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg.NamedExecutor = nil
			if tc.off {
				no := false
				cfg.NamedExecutor = &no
			}
			got, why := tc.w.forElement(tc.e)
			if got.adapter != tc.w.adapter || got.human != tc.w.human {
				t.Errorf("the child should keep the parent's wiring, got %+v", got)
			}
			if got.named != "" {
				t.Errorf("a declaration that was not honoured must record nothing, got %q", got.named)
			}
			if !strings.Contains(why, tc.wantWhy) {
				t.Errorf("the reason should say %q, got %q", tc.wantWhy, why)
			}
		})
	}
}

// TestForElementInheritsWhenNobodyIsNamed is the default ADR-0060 must not
// move (ADR-0054 Decision 1): no declaration, no change, and nothing said.
func TestForElementInheritsWhenNobodyIsNamed(t *testing.T) {
	registerFakeProvider(t, "parent-side", &fakeSplitAdapter{name: "parent-side", line: "parent-side"})
	parent := namedWiring("parent-side")
	got, why := parent.forElement(subtask.Element{Do: "a"})
	if got.adapter != parent.adapter || got.named != "" || why != "" {
		t.Errorf("a plain subtask should inherit silently, got %+v why=%q", got, why)
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

	if err := runSplit(context.Background(), s, parentSID, plain(groups), "big task",
		namedWiring("marker"), in, &out, extractor); err != nil {
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

// (b) The kill switch is the only way back to sequential (ADR-0056 Decision 3).
// With parallel_subtasks=false a declared wide group takes the flat order and
// the ADR-0023 fail-stop, so the first (failing) subtask stops the run before
// the second opens — the observable opposite of (a).
//
// 独立宣言は記帳され続ける: 止めていても、判断を開き直すための証拠は貯まる。
func TestRunSplitKillSwitchRunsSequentialFailStop(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "marker", &markerFailAdapter{name: "marker", failMarker: "FAILSUB"})
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	no := false
	cfg.ParallelSubtasks = &no

	const parentSID = "parent-killswitch"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"FAILSUB alpha", "beta"}, {"gamma"}}
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, plain(groups), "big task",
		namedWiring("marker"), bufio.NewReader(strings.NewReader("")), &out, extractor); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	subs := subtaskSessionIDs(t, s, parentSID)
	if len(subs) != 1 {
		t.Fatalf("sequential fail-stop: only the first (failing) subtask opens, got %d sessions", len(subs))
	}
	if n := countEventsOfTypeInSession(t, s, subs[0], "provider.error"); n != 1 {
		t.Errorf("the failed subtask should record provider.error, got %d", n)
	}
	if strings.Contains(out.String(), "並走") {
		t.Errorf("止めているのに並走を告知した: %q", out.String())
	}
}

// (c) A run with nobody at the terminal parallelises exactly like one with a
// person there (ADR-0056 Decision 1). This is the whole point of the
// retraction: the GUI and every pipe were the entries that could never reach
// the old gate, and they are where tomobit actually runs.
func TestRunSplitParallelsWithNobodyAtTheTerminal(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "marker", &markerFailAdapter{name: "marker", failMarker: "FAILSUB"})
	const parentSID = "parent-noninteractive"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"FAILSUB alpha", "beta"}, {"gamma"}}
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	// nothing on the reader — nobody to ask, and no way to answer if asked.
	if err := runSplit(context.Background(), s, parentSID, plain(groups), "big task",
		namedWiring("marker"), bufio.NewReader(strings.NewReader("")), &out, extractor); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	// 群は走り切る (ADR-0028 Decision 4): 隣が失敗しても群の中は最後まで走り、
	// 止まるのは次の群である。逐次なら1件しか開かない。
	if n := len(subtaskSessionIDs(t, s, parentSID)); n != 2 {
		t.Fatalf("宣言された2本が並走して走り切るはず, got %d sessions", n)
	}
	if !strings.Contains(out.String(), "並走") {
		t.Errorf("並走することは言う: %q", out.String())
	}
	if strings.Contains(out.String(), "[y/N]") {
		t.Errorf("許可は訊かない (ADR-0056): %q", out.String())
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

	if err := runSplit(context.Background(), s, parentSID, plain(groups), "big task",
		namedWiring("fake-split"), in, &out, extractor); err != nil {
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

	if _, ok := parallelCostEstimate(s, plain(groups)); ok {
		t.Error("no cost sample must yield no estimate — a fabricated number is worse than none")
	}

	seedProviderCosts(t, s, 0.10, 0.30, 0.20) // median 0.20
	usd, ok := parallelCostEstimate(s, plain(groups))
	if !ok {
		t.Fatal("with samples an estimate should be available")
	}
	if want := 0.20 * 2; usd != want {
		t.Errorf("estimate = %v, want median(0.20) × width(2) = %v", usd, want)
	}
}

// (f) task.split records what the Provider declared and what the run said it
// would cost — and nothing about an offer or an answer, because there is
// neither (ADR-0056 Decision 4, SCHEMA.md R4).
//
// 人へ言った数字を記帳する規律は、文言が問いから事実に変わっても同じである。
func TestRunSplitRecordsDeclarationAndEstimateButNoGate(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "done"})
	const parentSID = "parent-payload"
	openParentTask(t, s, parentSID)
	seedProviderCosts(t, s, 0.10, 0.30, 0.20) // median 0.20

	groups := [][]string{{"x", "y"}} // width 2 → est 0.40
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, plain(groups), "big task",
		namedWiring("fake-split"), bufio.NewReader(strings.NewReader("")), &out, extractor); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	split := payloadOf(t, s, "task.split")
	for _, gone := range []string{"parallel_offered", "parallel_accepted"} {
		if _, ok := split[gone]; ok {
			t.Errorf("%s は記帳しない — 提示も回答も無い (ADR-0056 D4): %v", gone, split)
		}
	}
	est, ok := split["est_cost_usd"].(float64)
	if !ok || est != 0.40 {
		t.Errorf("est_cost_usd = %v, want median(0.20) × width(2) = 0.40", split["est_cost_usd"])
	}
	// 告知した数字と記帳した数字は同じでなければならない。
	if !strings.Contains(out.String(), "$0.40") {
		t.Errorf("記帳した概算を人にも言うこと: %q", out.String())
	}
}

// 概算が取れないときは、金額を作らずに幅だけ言う (ADR-0028 Decision 3 の
// 「作り話の金額を出さない」は、問いが事実の告知に変わっても不変)。
func TestParallelNoticeSaysNoFigureWhenThereIsNoSample(t *testing.T) {
	withFigure := parallelNotice(2, 1, 3, 0.40, true)
	if !strings.Contains(withFigure, "$0.40") || !strings.Contains(withFigure, "2本を並走") {
		t.Errorf("概算があるときは幅と金額を言う: %q", withFigure)
	}
	without := parallelNotice(2, 1, 3, 0, false)
	if strings.Contains(without, "$") {
		t.Errorf("標本が無いのに金額を出した: %q", without)
	}
	if !strings.Contains(without, "2本を並走") {
		t.Errorf("金額が無くても、並走することは言う: %q", without)
	}
	for _, s := range []string{withFigure, without} {
		if strings.Contains(s, "?") || strings.Contains(s, "[y/N]") {
			t.Errorf("これは告知であって問いではない: %q", s)
		}
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

	if err := runSplit(context.Background(), s, parentSID, plain(groups), "big task",
		namedWiring("fake-split"), in, &out, extractor); err != nil {
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

// W3b: a wide group whose parent is the human runs off the goroutine path — a
// human has no stream — and still completes (ADR-0028 Decision 4). Since
// ADR-0054 Decision 1 the whole split shares the parent's one relationship, so
// a group is all-human or all-provider; there is no mixed case left to pin.
func TestRunGroupParallelRunsHumanMembersOneAtATime(t *testing.T) {
	s := openTestStore(t)

	const parentSID = "parent-human-group"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"h1", "h2"}} // a wide group, the parent is the human
	// gate "y", then one line per human member (runHuman reads and discards it).
	in := bufio.NewReader(strings.NewReader("y\n\n\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, plain(groups), "big task",
		subtaskWiring{human: true, capability: "implement"}, in, &out, extractor); err != nil {
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

// The view stream carries no per-subtask "decided" any more (ADR-0054
// Decision 1): a split is one task, so the one decision was announced when the
// parent task opened. This test used to pin the opposite — one decided per
// subtask, each with its own sid — and inverting it is the point: a GUI that
// grew a per-child decision badge would be showing a lottery draw, not a
// judgment.
func TestExecuteSplitViewEmitsNoPerSubtaskDecision(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "fake-split", &fakeSplitAdapter{name: "fake-split", line: "done"})
	const parentSID = "parent-view-split"
	openParentTask(t, s, parentSID)

	buf := &bytes.Buffer{}
	stream := newNDJSONStream(buf)
	newView := func(name string, gi, total int) view {
		return &ndjsonView{s: stream, name: name, n: 1, sub: gi + 1, subTotal: total}
	}
	groups := [][]string{{"sub one"}, {"sub two"}} // flat: parallelGate never fires

	if _, _, err := executeSplit(context.Background(), s, parentSID, plain(groups), "big task",
		namedWiring("fake-split"), bufio.NewReader(strings.NewReader("")),
		noteWriter{s: stream}, newView); err != nil {
		t.Fatalf("executeSplit: %v", err)
	}

	subs := subtaskSessionIDs(t, s, parentSID)
	if len(subs) != 2 {
		t.Fatalf("want 2 subtask sessions, got %d", len(subs))
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stream line not JSON: %q: %v", line, err)
		}
		if ev["type"] == "decided" {
			t.Errorf("a subtask announced a decision of its own: %v", ev)
		}
	}
	for _, sid := range subs {
		if n := countEventsOfTypeInSession(t, s, sid, "tomo.decided"); n != 0 {
			t.Errorf("child %s recorded %d tomo.decided, want 0", sid, n)
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

	if err := runSplit(ctx, s, parentSID, plain(groups), "big task",
		namedWiring("pc"), in, &out, extractor); err != nil {
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

// 並列分岐でも子は自分の決定を持たない（ADR-0054 Decision 1）。sequential 側の
// テストは単要素 group だけなので runGroupParallel に入らず、この経路だけが
// 「並列の子が勝手に選び直す」退行を捕まえられる。
func TestRunGroupParallelChildrenDecideNothing(t *testing.T) {
	s := openTestStore(t)
	// Replace the registry rather than adding to it: registerFakeProvider only
	// inserts, so the real claude-code/codex adapters would stay reachable and
	// a stray lookup could launch an actual CLI from a unit test.
	saved := providers
	providers = map[string]executor.Adapter{
		"fake-a": &fakeSplitAdapter{name: "fake-a", line: "did a"},
		"fake-b": &fakeSplitAdapter{name: "fake-b", line: "did b"},
	}
	t.Cleanup(func() { providers = saved })

	const parentSID = "parent-parallel-decide"
	openParentTask(t, s, parentSID)

	groups := [][]string{{"x", "y"}} // 2要素の1グループなので並列経路に入る。
	in := bufio.NewReader(strings.NewReader("y\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}

	if err := runSplit(context.Background(), s, parentSID, plain(groups), "big task",
		namedWiring("fake-a"), in, &out, extractor); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	split := payloadOf(t, s, "task.split")
	if split["groups"] == nil {
		t.Fatalf("this test only says something if the group actually ran in parallel, got %v", split)
	}
	subs := subtaskSessionIDs(t, s, parentSID)
	if len(subs) != 2 {
		t.Fatal("expected two subtask sessions to have run in parallel")
	}
	for _, sid := range subs {
		if n := countEventsOfTypeInSession(t, s, sid, "tomo.decided"); n != 0 {
			t.Errorf("parallel child %s decided for itself: %d tomo.decided", sid, n)
		}
	}
}
