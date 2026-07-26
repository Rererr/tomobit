package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Rererr/tomobit/internal/store"
)

// openSplitSession stages a session that proposed a split, which is the only
// state recordSplitVerdict acts on.
func openSplitSession(t *testing.T, s *store.Store, sid, provider string) {
	t.Helper()
	now := time.Now().UnixMilli()
	must := func(typ string, payload map[string]any) {
		if err := s.AppendEvent(sid, typ, now, payload); err != nil {
			t.Fatal(err)
		}
	}
	must("task.started", map[string]any{"intent": "big task", "source": "production"})
	must("provider.selected", map[string]any{"provider": provider})
	must("task.split", map[string]any{"subtasks": []string{"a", "b"}})
}

func splitVerdictPayload(t *testing.T, s *store.Store, sid string) map[string]any {
	t.Helper()
	events, err := s.EventsBySession(sid)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, e := range events {
		if e.Type == "user.split_verdict" {
			return e.Payload
		}
	}
	return nil
}

// 文句なし rides along: the human saw the whole session, including how it was
// cut up, and called it good. Reading that as a positive signal about the 分け方
// is not a guess — ADR-0028 Decision 5 already defines this grade as covering
// 「分割という采配 + 統合 + 会話全体」.
func TestSplitVerdictRidesAlongWithAGoodFeedback(t *testing.T) {
	s := openTestStore(t)
	const sid = "sv-good"
	openSplitSession(t, s, sid, "claude-code")

	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader(""))
	recordSplitVerdict(s, sid, in, &out, true, map[string]any{"adopted": "as-is", "reverted": false})

	p := splitVerdictPayload(t, s, sid)
	if p == nil {
		t.Fatalf("a split session must record a verdict")
	}
	if p["verdict"] != "good" || p["source"] != "feedback" {
		t.Errorf("payload: %v", p)
	}
	// The whole point of riding along: no extra question on a good day.
	if out.Len() != 0 {
		t.Errorf("文句なし must cost no second question, got %q", out.String())
	}
	if p["provider"] != "claude-code" {
		t.Errorf("the provider whose 分け方 is judged must be named: %v", p)
	}
}

// The friction lands only on the days it earns. "関係なかった" is a real
// positive signal about the 分け方, not an absence of one.
func TestSplitVerdictAsksOnlyWhenTheSessionWentPoorly(t *testing.T) {
	cases := []struct {
		name     string
		feedback map[string]any
		answer   string
		want     string
	}{
		{"手直しあり・分け方のせい", map[string]any{"adopted": "with-edits"}, "1\n", "bad"},
		{"手直しあり・分け方は無罪", map[string]any{"adopted": "with-edits"}, "2\n", "good"},
		{"だめだった・分け方のせい", map[string]any{"reverted": true}, "1\n", "bad"},
		{"わからない", map[string]any{"adopted": "with-edits"}, "\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			const sid = "sv-poor"
			openSplitSession(t, s, sid, "codex")

			var out bytes.Buffer
			in := bufio.NewReader(strings.NewReader(tc.answer))
			recordSplitVerdict(s, sid, in, &out, true, tc.feedback)

			p := splitVerdictPayload(t, s, sid)
			if p == nil {
				t.Fatalf("a split session must record a verdict")
			}
			if p["verdict"] != tc.want || p["source"] != "question" {
				t.Errorf("got %v, want verdict=%q source=question", p, tc.want)
			}
			if !strings.Contains(out.String(), "分け方は関係あった?") {
				t.Errorf("the follow-up must be asked: %q", out.String())
			}
		})
	}
}

// Enter on the Feedback question means 「まだ言えない」. Pressing for a second
// opinion right after would be asking twice for something they just said they
// could not say.
func TestSplitVerdictDoesNotPressAfterAnUngradedSession(t *testing.T) {
	s := openTestStore(t)
	const sid = "sv-silent"
	openSplitSession(t, s, sid, "claude-code")

	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("1\n")) // would be consumed if asked
	recordSplitVerdict(s, sid, in, &out, true, map[string]any{})

	if out.Len() != 0 {
		t.Errorf("no question after 「まだ言えない」, got %q", out.String())
	}
	// A row is still written: without one, 「聞かなかった」 and 「答えなかった」
	// become indistinguishable later.
	p := splitVerdictPayload(t, s, sid)
	if p == nil {
		t.Fatalf("the absence of a signal is still worth recording")
	}
	if p["verdict"] != "" {
		t.Errorf("no signal must stay empty: %v", p)
	}
}

// `do`'s split parent has no Feedback to ride on (ADR-0023 Decision 5), so it
// asks its own — and that question is about the proposal itself, the one
// artifact the parent really did produce.
func TestSplitVerdictAsksItsOwnQuestionOnADoParent(t *testing.T) {
	s := openTestStore(t)
	const sid = "sv-do"
	openSplitSession(t, s, sid, "claude-code")

	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("1\n"))
	recordSplitVerdict(s, sid, in, &out, false, map[string]any{})

	if !strings.Contains(out.String(), "分けたのは claude-code") {
		t.Errorf("the do question must name the proposer: %q", out.String())
	}
	p := splitVerdictPayload(t, s, sid)
	if p == nil || p["verdict"] != "good" || p["source"] != "question" {
		t.Errorf("payload: %v", p)
	}
}

// A task that never split is untouched — no question, no row. Most tasks.
func TestSplitVerdictIgnoresASessionThatNeverSplit(t *testing.T) {
	s := openTestStore(t)
	const sid = "sv-plain"
	if err := s.AppendEvent(sid, "task.started", time.Now().UnixMilli(),
		map[string]any{"intent": "small task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader("1\n"))
	recordSplitVerdict(s, sid, in, &out, true, map[string]any{"adopted": "with-edits"})

	if out.Len() != 0 {
		t.Errorf("an unsplit task asks nothing, got %q", out.String())
	}
	if p := splitVerdictPayload(t, s, sid); p != nil {
		t.Errorf("an unsplit task records nothing, got %v", p)
	}
}

// The verdict must not touch task.finished: 能力 and 分け方 are different facts
// (ADR-0003 Decision 2's reasoning), and mixing them into one envelope would
// make them inseparable at rebuild.
func TestSplitVerdictStaysOutOfTaskFinished(t *testing.T) {
	s := openTestStore(t)
	const sid = "sv-envelope"
	openSplitSession(t, s, sid, "claude-code")

	in := bufio.NewReader(strings.NewReader("1\n"))
	var out bytes.Buffer
	if err := finishTask(s, sid, in, &out, true, false,
		&fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}, ""); err != nil {
		t.Fatalf("finishTask: %v", err)
	}

	events, err := s.EventsBySession(sid)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type != "task.finished" {
			continue
		}
		for _, k := range []string{"verdict", "source", "split_verdict"} {
			if _, ok := e.Payload[k]; ok {
				t.Errorf("task.finished must not carry %q: %v", k, e.Payload)
			}
		}
	}
	if splitVerdictPayload(t, s, sid) == nil {
		t.Errorf("the verdict must still reach its own event")
	}
}
