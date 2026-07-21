package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/lineedit"
	"github.com/Rererr/tomobit/internal/store"
)

// splitChatAdapter drives the whole chat fold-back deterministically off one
// registered provider (ADR-0028 Decision 5). It classifies each launch by its
// prompt — the opening turn carries the split protocol (contains tomobit_split),
// a subtask carries subtask.Prompt's "[tomobit subtask" frame, the fold-back
// carries feedPrompt's "これらの結果を統合" line — and answers each phase in
// character, recording every launch's ResumeID so a test can see the feed turn
// resume the parent thread. threadID "th-parent" is emitted on the SEL line of
// the provider-stream phases (opening / feed), the only ones that run through
// the chat's own thread; subtasks run in their own sessions with no resume.
type splitChatAdapter struct {
	mu       sync.Mutex
	launches []launch
	proposal string // provider.output on the opening (protocol) turn
	feedOut  string // provider.output on the fold-back turn
	failSub  string // a subtask whose prompt contains this exits non-zero
}

type launch struct{ resume, prompt string }

func (a *splitChatAdapter) Name() string { return "fake" }

func (a *splitChatAdapter) Command(req executor.Request) (string, []string, []string) {
	a.mu.Lock()
	a.launches = append(a.launches, launch{req.ResumeID, req.Prompt})
	a.mu.Unlock()
	switch {
	case strings.Contains(req.Prompt, "これらの結果を統合"): // the fold-back feed turn
		return "printf", []string{"SEL\n" + a.feedOut + "\n"}, nil
	case strings.Contains(req.Prompt, "[tomobit subtask"): // one subtask
		exit := 0
		if a.failSub != "" && strings.Contains(req.Prompt, a.failSub) {
			exit = 1
		}
		script := "echo subtask ran"
		// A subtask instruction may itself carry the threadAdapter-style
		// TOOLDETAIL marker (chat_test.go) — a test's way of asking this
		// subtask to also emit a tool call, so the view contract's "tool line"
		// half (ADR-0032) can be pinned on a subtask, not just an ordinary turn.
		if strings.Contains(req.Prompt, "TOOLDETAIL") {
			script += "; echo TOOLDETAIL"
		}
		return "sh", []string{"-c", fmt.Sprintf("%s; exit %d", script, exit)}, nil
	case strings.Contains(req.Prompt, "tomobit_split"): // the opening turn (protocol attached)
		return "printf", []string{"SEL\n" + a.proposal + "\n"}, nil
	default: // an ordinary continuation turn
		return "printf", []string{"SEL\n" + req.Prompt + "\n"}, nil
	}
}

func (a *splitChatAdapter) Translate(line []byte) ([]executor.Event, error) {
	switch s := strings.TrimSpace(string(line)); s {
	case "":
		return nil, nil
	case "SEL":
		return []executor.Event{{Type: executor.EventProviderSelected, Payload: map[string]any{
			"provider": "fake", "provider_session_id": "th-parent",
		}}}, nil
	case "TOOLDETAIL": // mirrors threadAdapter's own token (chat_test.go)
		return []executor.Event{{Type: executor.EventProviderOutput,
			Payload: map[string]any{"tool": "Edit", executor.PayloadDetail: "cmd/x.go"}}}, nil
	default:
		return []executor.Event{{Type: executor.EventProviderOutput,
			Payload: map[string]any{"text": s}}}, nil
	}
}

func newSplitChat(t *testing.T, s *store.Store, a *splitChatAdapter, in string) (*chat, *bytes.Buffer) {
	t.Helper()
	providers["fake"] = a
	t.Cleanup(func() { delete(providers, "fake") })
	out := &bytes.Buffer{}
	return &chat{
		s: s, out: out, providerName: "fake", capability: "implement",
		extractor: &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}},
		in:        bufio.NewReader(strings.NewReader(in)),
	}, out
}

// newSplitViewChat is newViewChat's (chat_view_test.go) own plumbing wired
// onto splitChatAdapter instead: a --view ndjson chat that can also drive a
// split, so the NDJSON contract (ADR-0032) and the split machinery (ADR-0028)
// can be pinned together.
func newSplitViewChat(t *testing.T, s *store.Store, a *splitChatAdapter, stdin string) (*chat, *bytes.Buffer) {
	t.Helper()
	providers["fake"] = a
	t.Cleanup(func() { delete(providers, "fake") })

	buf := &bytes.Buffer{}
	stream := newNDJSONStream(buf)

	dev, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dev.Close() })
	ed := lineedit.New(dev, dev)
	ed.SetReader(flushReader{r: strings.NewReader(stdin), flush: stream.flushAwait})

	c := &chat{
		s: s, ed: ed, in: ed.Reader(), out: noteWriter{s: stream}, stream: stream,
		providerName: "fake", capability: "implement",
		extractor: &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}},
	}
	stream.emit(map[string]any{"type": "init", "v": viewVersion})
	return c, buf
}

// feedLaunch returns the single fold-back launch (its prompt carries the harness
// line), failing if it did not fire exactly once — the depth-1 guarantee is that
// the feed happens once per split, never recursively.
func feedLaunch(t *testing.T, a *splitChatAdapter) launch {
	t.Helper()
	var found []launch
	for _, l := range a.launches {
		if strings.Contains(l.prompt, "これらの結果を統合") {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the fold-back feed must run exactly once, got %d: %v", len(found), a.launches)
	}
	return found[0]
}

// (a) The split protocol rides only the task's opening turn: the first turn's
// prompt carries the instruction, a continuation turn's does not (ADR-0028
// Decision 1). A non-proposing opening keeps the chat on its normal path so the
// second turn is a genuine continuation.
func TestChatSplitProtocolOnOpeningTurnOnly(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{proposal: "ふつうの返答（提案なし）"}
	c, _ := newSplitChat(t, s, a, "")

	if err := c.turn("build the thing"); err != nil {
		t.Fatal(err)
	}
	if err := c.turn("now tweak it"); err != nil {
		t.Fatal(err)
	}

	if len(a.launches) != 2 {
		t.Fatalf("two turns, two launches, got %v", a.launches)
	}
	if !strings.Contains(a.launches[0].prompt, "tomobit_split") {
		t.Errorf("the opening turn must carry the split protocol: %q", a.launches[0].prompt)
	}
	if strings.Contains(a.launches[1].prompt, "tomobit_split") {
		t.Errorf("a continuation turn must not carry the protocol: %q", a.launches[1].prompt)
	}
	if a.launches[1].resume != "th-parent" {
		t.Errorf("the continuation turn must resume the thread: got %q", a.launches[1].resume)
	}
	if n := countEventsOfType(t, s, "task.split"); n != 0 {
		t.Errorf("a non-proposing opening records no split, got %d", n)
	}
}

// (b) A proposing opening runs the subtasks and then feeds a single fold-back
// turn that resumes the parent thread (ADR-0028 Decision 5). The feed is not the
// user's ask, so it records no task.turn; a real follow-up turn afterward
// resumes the same thread the feed carried the integration into.
func TestChatSplitFeedsParentThreadOnceWithoutRecordingTurn(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{
		proposal: `{"tomobit_split": ["alpha task", "beta task"]}`,
		feedOut:  "統合レポート: alphaとbetaを完了",
	}
	c, out := newSplitChat(t, s, a, "")

	if err := c.turn("big build"); err != nil {
		t.Fatal(err)
	}

	if n := len(subtaskSessionIDs(t, s, c.sid)); n != 2 {
		t.Fatalf("both subtasks should open a session, got %d", n)
	}
	feed := feedLaunch(t, a)
	if feed.resume != "th-parent" {
		t.Errorf("the feed must resume the parent thread, got %q", feed.resume)
	}
	if n := countEventsOfType(t, s, "task.turn"); n != 0 {
		t.Errorf("the feed turn is not the user's ask — no task.turn, got %d", n)
	}
	if !strings.Contains(out.String(), "統合レポート") {
		t.Errorf("the integration report must reach the terminal: %q", out.String())
	}
	if c.threadID != "th-parent" {
		t.Errorf("the thread the next turn resumes must be the parent's, got %q", c.threadID)
	}

	// The next user turn continues over the integrated result: it resumes the
	// parent thread and is recorded as a normal continuation.
	if err := c.turn("ship it"); err != nil {
		t.Fatal(err)
	}
	if a.launches[len(a.launches)-1].resume != "th-parent" {
		t.Errorf("the next user turn must resume the parent thread, got %v", a.launches)
	}
	if n := countEventsOfType(t, s, "task.turn"); n != 1 {
		t.Errorf("only the genuine follow-up records task.turn, got %d", n)
	}
}

// (c) The fold-back feed turn's output is never read for a further proposal:
// even when it contains a tomobit_split marker, no second split runs (ADR-0028
// Decision 5 — depth stays 1, structurally, since the feed runs opening=false).
func TestChatSplitFeedOutputIsNotReadAsAProposal(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{
		proposal: `{"tomobit_split": ["alpha task", "beta task"]}`,
		// The feed's own output quotes a marker — a model citing the protocol
		// mid-report. It must not open a new split.
		feedOut: `統合完了。{"tomobit_split": ["x1", "x2", "x3"]}`,
	}
	c, _ := newSplitChat(t, s, a, "")

	if err := c.turn("big build"); err != nil {
		t.Fatal(err)
	}

	if n := countEventsOfType(t, s, "task.split"); n != 1 {
		t.Errorf("exactly one split must run — the feed output is not a proposal, got %d", n)
	}
	if n := len(subtaskSessionIDs(t, s, c.sid)); n != 2 {
		t.Errorf("no subtasks beyond the original two, got %d", n)
	}
	feedLaunch(t, a) // the feed fired exactly once — no recursion
}

// (d) A fail-stop leaves later subtasks unstarted; the fold-back names them as
// 未着手 rather than omitting them, and marks the failed one honestly (ADR-0028
// Decision 5: 親スレッドが実態を知らないままにしない).
func TestChatSplitFeedListsFailedAndUnstartedSubtasks(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{
		proposal: `{"tomobit_split": ["FAILSUB alpha", "beta task"]}`,
		failSub:  "FAILSUB",
		feedOut:  "統合レポート",
	}
	c, _ := newSplitChat(t, s, a, "")

	if err := c.turn("big build"); err != nil {
		t.Fatal(err)
	}

	// Only the first (failing) subtask ever opened — the second is 未着手.
	if n := len(subtaskSessionIDs(t, s, c.sid)); n != 1 {
		t.Fatalf("the fail-stop must leave the second subtask unstarted, got %d sessions", n)
	}
	feed := feedLaunch(t, a)
	for _, want := range []string{"FAILSUB alpha", "（失敗）", "beta task", "未着手"} {
		if !strings.Contains(feed.prompt, want) {
			t.Errorf("the feed prompt must contain %q, got:\n%s", want, feed.prompt)
		}
	}
}

// (W2) An interactive chat can accept the parallelism gate and fold a wide
// group back in flat order (ADR-0028 Decision 3/5). Group 0 runs its two members
// in parallel to completion (走り切り); one fails, which fail-stops group 1 (lone
// C) before it starts — the feed lists all three honestly: the succeeded member,
// the failed one, and the 未着手 lone.
func TestChatSplitInteractiveParallelAcceptFoldsBackInFlatOrder(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{
		proposal: `{"tomobit_split": [["para A", "FAILSUB para B"], "lone C"]}`,
		failSub:  "FAILSUB",
		feedOut:  "統合レポート",
	}
	c, out := newSplitChat(t, s, a, "y\n") // the gate answer
	c.interactive = true                   // drive the accept path without a real terminal

	if err := c.turn("big parallel build"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "並走") {
		t.Errorf("an interactive wide proposal must show the gate: %q", out.String())
	}
	// Both parallel members opened and ran to completion; lone C never started.
	if n := len(subtaskSessionIDs(t, s, c.sid)); n != 2 {
		t.Fatalf("both wide members open (走り切り), lone C stays 未着手 — want 2, got %d", n)
	}
	split := payloadOf(t, s, "task.split")
	if split["parallel_offered"] != true || split["parallel_accepted"] != true {
		t.Errorf("an accepted gate records offered=accepted=true, got %v", split)
	}
	feed := feedLaunch(t, a)
	if feed.resume != "th-parent" {
		t.Errorf("the feed must resume the parent thread, got %q", feed.resume)
	}
	// Flat order: para A (ok) before para B (failed) before lone C (未着手).
	iA := strings.Index(feed.prompt, "para A")
	iB := strings.Index(feed.prompt, "FAILSUB para B")
	iC := strings.Index(feed.prompt, "lone C")
	if iA < 0 || iB < 0 || iC < 0 || !(iA < iB && iB < iC) {
		t.Errorf("the feed must list the subtasks in flat order (A<B<C): %s", feed.prompt)
	}
	for _, want := range []string{"（失敗）", "未着手"} {
		if !strings.Contains(feed.prompt, want) {
			t.Errorf("the feed must contain %q honestly, got:\n%s", want, feed.prompt)
		}
	}
}

// (e) An opening turn with no marker in its output is an ordinary chat turn: the
// session stays open, nothing is split, and the next turn continues it.
func TestChatSplitOpeningWithoutProposalIsOrdinaryTurn(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{proposal: "ふつうに実装した"}
	c, out := newSplitChat(t, s, a, "")

	if err := c.turn("just do it"); err != nil {
		t.Fatal(err)
	}

	if c.sid == "" {
		t.Error("an ordinary opening turn keeps the task open")
	}
	if n := countEventsOfType(t, s, "task.split"); n != 0 {
		t.Errorf("no proposal means no split, got %d", n)
	}
	if !strings.Contains(out.String(), "ふつうに実装した") {
		t.Errorf("the provider's answer must reach the terminal: %q", out.String())
	}
	if len(a.launches) != 1 {
		t.Errorf("no fold-back feed on a non-proposing turn, got %v", a.launches)
	}
}

// (f) Even with a split, the subjective Feedback is asked once, at the boundary,
// about the parent — never per subtask (ADR-0028 Decision 5). The one question
// grades the whole thing: 分割の采配 + 統合 + 会話.
func TestChatSplitBoundaryAsksSubjectiveFeedbackOnceToParent(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{
		proposal: `{"tomobit_split": ["alpha task", "beta task"]}`,
		feedOut:  "統合レポート",
	}
	c, out := newSplitChat(t, s, a, "1\n") // the one Feedback answer

	if err := c.turn("big build"); err != nil {
		t.Fatal(err)
	}
	parentSID := c.sid
	if err := c.closeTask(); err != nil {
		t.Fatal(err)
	}

	if n := strings.Count(out.String(), "今回、どうだった?"); n != 1 {
		t.Errorf("the subjective Feedback must be asked exactly once, got %d", n)
	}
	// The Feedback lands on the parent session, and the subtasks carry none.
	parentEvs, err := s.EventsBySession(parentSID)
	if err != nil {
		t.Fatal(err)
	}
	var parentFinished map[string]any
	for _, e := range parentEvs {
		if e.Type == "task.finished" {
			parentFinished = e.Payload
		}
	}
	if parentFinished["adopted"] != "as-is" {
		t.Errorf("the parent's task.finished should carry the Feedback: got %v", parentFinished)
	}
	for _, sid := range subtaskSessionIDs(t, s, parentSID) {
		evs, err := s.EventsBySession(sid)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if e.Type == "task.finished" && len(e.Payload) != 0 {
				t.Errorf("subtask %s must carry no subjective Feedback, got %v", sid, e.Payload)
			}
		}
	}
}

// (g) The kill switch (config split_protocol=false) stops the protocol from
// riding even the opening turn (ADR-0028 Decision 1): the prompt is the raw
// intent, and no split can be proposed.
func TestChatSplitProtocolKillSwitchOff(t *testing.T) {
	s := openTestStore(t)
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	no := false
	cfg = config.Config{SplitProtocol: &no}

	a := &splitChatAdapter{proposal: `{"tomobit_split": ["alpha task", "beta task"]}`}
	c, _ := newSplitChat(t, s, a, "")

	if err := c.turn("big build"); err != nil {
		t.Fatal(err)
	}

	if len(a.launches) != 1 {
		t.Fatalf("no fold-back when the protocol is off, got %v", a.launches)
	}
	if strings.Contains(a.launches[0].prompt, "tomobit_split") {
		t.Errorf("the kill switch must strip the protocol from the opening turn: %q", a.launches[0].prompt)
	}
	if n := countEventsOfType(t, s, "task.split"); n != 0 {
		t.Errorf("no protocol, no split, got %d", n)
	}
}

// (h) ADR-0032 Decision 1 × ADR-0028: under --view ndjson, a subtask's own
// body and tool line reach the stream as the same typed vocabulary the parent
// turn uses — never collapsed into an opaque note the GUI would have to parse
// back apart. Each subtask nests as its own turn.started/turn.finished pair
// repeating the opening turn's n, the same discipline the fold-back feed turn
// already follows.
func TestChatSplitViewEmitsTypedSubtaskEvents(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{
		proposal: `{"tomobit_split": ["alpha TOOLDETAIL task", "beta task"]}`,
		feedOut:  "統合レポート",
	}
	c, buf := newSplitViewChat(t, s, a, "")

	if err := c.turn("big build"); err != nil {
		t.Fatal(err)
	}

	evs := viewEvents(t, buf) // fails on any non-JSON line — a raw echo would break this
	texts := 0
	for _, e := range evs {
		if e["type"] == "text" && e["text"] == "subtask ran" {
			texts++
		}
	}
	if texts != 2 {
		t.Errorf("both subtasks' bodies must arrive as typed text events, got %d: %v", texts, viewTypes(evs))
	}
	if tool := firstOfType(evs, "tool"); tool == nil || tool["name"] != "Edit" || tool["detail"] != "cmd/x.go" {
		t.Errorf("a subtask's tool call must arrive as a typed tool event, not a note: %v", tool)
	}
	if anyNoteContains(evs, "subtask ran") {
		t.Errorf("subtask output must not leak into an untyped note: %v", viewTypes(evs))
	}

	// opening turn + 2 subtasks + the fold-back feed turn = 4 brackets, every
	// one repeating n=1 (ADR-0032 Decision 1: the GUI reads the repeat as
	// nesting, not four unrelated turns).
	for _, ty := range []string{"turn.started", "turn.finished"} {
		count := 0
		for _, e := range evs {
			if e["type"] != ty {
				continue
			}
			count++
			if e["n"] != float64(1) {
				t.Errorf("%s must repeat the opening turn's n=1, got %v", ty, e["n"])
			}
		}
		if count != 4 {
			t.Errorf("%s count = %d, want 4 (opening + 2 subtasks + feed)", ty, count)
		}
	}
}

// (i) A Provider that declares an independent group under --view ndjson still
// produces the same typed shape as a flat proposal: the parallel gate never
// fires off a terminal (ADR-0028 Decision 3), so the declaration collapses to
// the same sequential path, and the terminal's [n:provider] labeling
// (ADR-0028 Decision 4, splitSink) — layout, not the view's vocabulary — must
// never reach the stream.
func TestChatSplitViewWideGroupStaysSequentialAndTyped(t *testing.T) {
	s := openTestStore(t)
	a := &splitChatAdapter{
		proposal: `{"tomobit_split": [["alpha task", "beta task"]]}`,
		feedOut:  "統合レポート",
	}
	c, buf := newSplitViewChat(t, s, a, "")

	if err := c.turn("big parallel build"); err != nil {
		t.Fatal(err)
	}

	evs := viewEvents(t, buf) // fails on any non-JSON line
	texts := 0
	for _, e := range evs {
		if e["type"] == "text" && e["text"] == "subtask ran" {
			texts++
		}
		if txt, ok := e["text"].(string); ok && strings.Contains(txt, "[1:fake]") {
			t.Errorf("the terminal's [n:provider] label must never reach the view: %v", e)
		}
	}
	if texts != 2 {
		t.Errorf("both members of the declared group must run sequentially with typed text, got %d", texts)
	}
	if anyNoteContains(evs, "subtask ran") {
		t.Errorf("a wide group must not degrade subtask output to a note: %v", viewTypes(evs))
	}
	split := payloadOf(t, s, "task.split")
	if split["parallel_offered"] != false {
		t.Errorf("a non-interactive view session never offers the gate, got %v", split)
	}
}
