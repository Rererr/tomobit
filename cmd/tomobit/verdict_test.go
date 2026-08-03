package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

// openSessionFor builds the ledger shape a real closed task leaves behind, so
// the two writers below are exercised against sessions verdictAllowed will
// actually accept.
func openSessionFor(t *testing.T, s *store.Store, sid string, extra ...func()) {
	t.Helper()
	if err := s.AppendEvent(sid, "task.started", 1000,
		map[string]any{"intent": "some task", "source": "production"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range extra {
		f()
	}
	if err := s.AppendEvent(sid, "task.finished", 2000,
		map[string]any{"adopted": "as-is", "reverted": false}); err != nil {
		t.Fatal(err)
	}
}

func boolp(b bool) *bool { return &b }

func verdictEvents(t *testing.T, s *store.Store, sid string) []map[string]any {
	t.Helper()
	evs, err := s.EventsBySession(sid)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range evs {
		if e.Type == "user.verdict" {
			out = append(out, e.Payload)
		}
	}
	return out
}

// ---- 書き手A: 境界の矛盾フォローアップ (ADR-0055 Decision 1) ----

// The question exists for exactly one contradiction — a red suite and a
// 文句なし on the same task — because that is the only place in
// OutcomeWeight's derivation where an observed signal outranks an answered one.
func TestContradictionAsksOnlyWhenARedTestMeetsNoComplaints(t *testing.T) {
	yes := map[string]any{"adopted": "as-is", "reverted": false}
	cases := []struct {
		name     string
		passed   *bool
		feedback map[string]any
		want     bool
	}{
		{"赤 × 文句なし → 訊く", boolp(false), yes, true},
		{"緑 × 文句なし → 訊かない", boolp(true), yes, false},
		{"観測なし × 文句なし → 訊かない", nil, yes, false},
		{"赤 × まあまあ → 訊かない（所有者決定）", boolp(false),
			map[string]any{"adopted": "with-edits", "reverted": false}, false},
		{"赤 × だめだった → 訊かない（同じ向き）", boolp(false),
			map[string]any{"adopted": "", "reverted": true}, false},
		{"赤 × まだ言えない → 訊かない", boolp(false), map[string]any{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			sid := "sess-" + tc.name
			openSessionFor(t, s, sid, func() {
				if tc.passed == nil {
					return
				}
				if err := s.AppendEvent(sid, "test.result", 1500,
					map[string]any{"passed": *tc.passed, "command": "go test ./..."}); err != nil {
					t.Fatal(err)
				}
			})

			var out bytes.Buffer
			in := bufio.NewReader(strings.NewReader("y\n"))
			askVerdictOnContradiction(s, sid, in, &out, true, tc.feedback)

			asked := strings.Contains(out.String(), "文句なし?")
			if asked != tc.want {
				t.Errorf("訊いたか = %v, want %v (出力: %q)", asked, tc.want, out.String())
			}
			if got := len(verdictEvents(t, s, sid)); (got > 0) != tc.want {
				t.Errorf("user.verdict %d件, 訊くはず=%v", got, tc.want)
			}
		})
	}
}

// 同じセッションが観測を複数持つとき、矛盾を決めるのは最後の観測だけである
// (ADR-0055 Decision 1)。赤を見て直して緑になった日は矛盾していないので問いは
// 立たず、緑のあとに壊した日は立つ。過去の赤が残り続ける読み方だと、直した人
// ほど毎回「文句なし?」と訊かれる。
func TestContradictionReadsOnlyTheNewestObservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		passed []bool
		want   bool
	}{
		{"赤→緑（直した）", []bool{false, true}, false},
		{"緑→赤（壊した）", []bool{true, false}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			sid := "sess-" + tc.name
			openSessionFor(t, s, sid, func() {
				for i, passed := range tc.passed {
					if err := s.AppendEvent(sid, "test.result", 1500+int64(i),
						map[string]any{"passed": passed, "command": "go test ./..."}); err != nil {
						t.Fatal(err)
					}
				}
			})

			var out bytes.Buffer
			askVerdictOnContradiction(s, sid, bufio.NewReader(strings.NewReader("y\n")),
				&out, true, map[string]any{"adopted": "as-is", "reverted": false})

			if asked := strings.Contains(out.String(), "文句なし?"); asked != tc.want {
				t.Errorf("訊いたか = %v, want %v (出力: %q)", asked, tc.want, out.String())
			}
			if got := len(verdictEvents(t, s, sid)); (got > 0) != tc.want {
				t.Errorf("user.verdict %d件, 訊くはず=%v", got, tc.want)
			}
		})
	}
}

// 沈黙は同意ではない (ADR-0049): an unanswered prompt leaves the red standing.
// It stands as a y=0 the human declined to override, which is a different fact
// from one they were never asked about — but the recorded outcome is the same.
func TestContradictionDefaultsToLeavingTheRedStanding(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", ""} {
		s := openTestStore(t)
		const sid = "sess-default-no"
		openSessionFor(t, s, sid, func() {
			if err := s.AppendEvent(sid, "test.result", 1500,
				map[string]any{"passed": false}); err != nil {
				t.Fatal(err)
			}
		})

		var out bytes.Buffer
		askVerdictOnContradiction(s, sid, bufio.NewReader(strings.NewReader(answer)),
			&out, true, map[string]any{"adopted": "as-is"})

		if n := len(verdictEvents(t, s, sid)); n != 0 {
			t.Errorf("answer %q: 上書きしていないのに %d件記帳された", answer, n)
		}
	}
}

// 人が居ない入口（パイプ・CI）では訊かない — 境界の他の器官と同じ規律
// (ADR-0035 Decision 2)。
func TestContradictionStaysSilentWithNobodyThere(t *testing.T) {
	s := openTestStore(t)
	const sid = "sess-nobody"
	openSessionFor(t, s, sid, func() {
		if err := s.AppendEvent(sid, "test.result", 1500, map[string]any{"passed": false}); err != nil {
			t.Fatal(err)
		}
	})

	var out bytes.Buffer
	askVerdictOnContradiction(s, sid, bufio.NewReader(strings.NewReader("y\n")),
		&out, false, map[string]any{"adopted": "as-is"})

	if out.Len() != 0 {
		t.Errorf("誰も居ないのに訊いた: %q", out.String())
	}
}

// 観測は消さない (Decision 1): the red result stays in the ledger and the two
// facts coexist in the experience — the derivation, not the ledger, is where
// the stronger one wins.
func TestContradictionKeepsTheObservationAndLetsTheVerdictWin(t *testing.T) {
	s := openTestStore(t)
	const sid = "sess-coexist"
	openSessionFor(t, s, sid, func() {
		if err := s.AppendEvent(sid, "test.result", 1500,
			map[string]any{"passed": false, "command": "go test ./..."}); err != nil {
			t.Fatal(err)
		}
	})

	askVerdictOnContradiction(s, sid, bufio.NewReader(strings.NewReader("y\n")),
		&bytes.Buffer{}, true, map[string]any{"adopted": "as-is"})

	if p := testResultPayload(t, s, sid); p == nil || p["passed"] != false {
		t.Errorf("赤の観測が消えている: %v", p)
	}
	if v := verdictEvents(t, s, sid); len(v) != 1 || v[0]["verdict"] != "up" {
		t.Fatalf("判定が記帳されていない: %v", v)
	}

	// The derivation is what resolves them: a red suite alone is y=0, and the
	// verdict lifts it to 1 because it is read first.
	red := false
	e := &core.Experience{Kind: core.KindExecution, Outcome: core.Outcome{
		TestsPassed: &red, Adopted: "as-is", Verdict: "up",
	}}
	if y, ok := core.OutcomeWeight(e); !ok || y != 1 {
		t.Errorf("判定が赤に勝っていない: y=%v ok=%v", y, ok)
	}
}

// ---- 書き手B: tomobit verdict (ADR-0055 Decision 2) ----

// The four sessions a judgment cannot mean anything on, and the one that looks
// like them but is not (a duel side — an independent commission, ADR-0026).
func TestVerdictRefusesTheSessionsAJudgmentCannotMean(t *testing.T) {
	t.Run("中断", func(t *testing.T) {
		s := openTestStore(t)
		if err := s.AppendEvent("c", "task.started", 1000, map[string]any{"intent": "x"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("c", "task.cancelled", 2000, nil); err != nil {
			t.Fatal(err)
		}
		mustRefuse(t, s, "c", "成果物が無い")
	})

	t.Run("未終了", func(t *testing.T) {
		s := openTestStore(t)
		if err := s.AppendEvent("r", "task.started", 1000, map[string]any{"intent": "x"}); err != nil {
			t.Fatal(err)
		}
		mustRefuse(t, s, "r", "終わっていない")
	})

	t.Run("amend済み", func(t *testing.T) {
		s := openTestStore(t)
		openSessionFor(t, s, "a")
		if err := s.AppendEvent("a", "user.amended", 3000,
			map[string]any{"id": "e1", "ver": 2}); err != nil {
			t.Fatal(err)
		}
		mustRefuse(t, s, "a", "最終知覚")
	})

	t.Run("分割の子", func(t *testing.T) {
		s := openTestStore(t)
		if err := s.AppendEvent("p", "task.started", 900, map[string]any{"intent": "big"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("p", "task.split", 950, map[string]any{"subtasks": []string{"a"}}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("k", "task.started", 1000,
			map[string]any{"intent": "sub", "parent": "p"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("k", "task.finished", 2000, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		mustRefuse(t, s, "k", "経験を持たない")
	})

	t.Run("duel の側は断らない", func(t *testing.T) {
		s := openTestStore(t)
		if err := s.AppendEvent("dp", "task.started", 900, map[string]any{"intent": "compare"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("dp", "task.duel", 950, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("d1", "task.started", 1000,
			map[string]any{"intent": "compare", "parent": "dp"}); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEvent("d1", "task.finished", 2000, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if err := verdictAllowed(s, "d1"); err != nil {
			t.Errorf("duel の側は独立した発注なので判定できる: %v", err)
		}
	})

	t.Run("知らないセッション", func(t *testing.T) {
		mustRefuse(t, openTestStore(t), "nope", "no such session")
	})
}

func mustRefuse(t *testing.T, s *store.Store, sid, want string) {
	t.Helper()
	err := verdictAllowed(s, sid)
	if err == nil {
		t.Fatalf("%s は断られるはず", sid)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("理由に %q が無い: %v", want, err)
	}
}

// The copy-forward moves exactly one row (Decision 2): a session holds one
// execution experience, and preference siblings sit under another kind, so
// amend's whole-generation carry is not needed here — and must not happen,
// since it would lift rows nobody judged.
func TestCarryVerdictForwardMovesOnlyTheExecutionRow(t *testing.T) {
	s := openTestStore(t)
	const sid = "sess-carry"
	insertExecution(t, s, "e-exec", sid, map[string]string{"lang": "go"}, "fake",
		core.Outcome{Adopted: "as-is"})
	if err := s.InsertExperience(&core.Experience{
		ID: "e-pref", SessionID: sid, TS: 1000, Kind: core.KindPreference,
		ExtractorVer: extractorVer, ExtractorModel: "none",
		Context: map[string]string{"lang": "go"},
		Outcome: core.Outcome{Preferred: "a", Over: "b"}, Source: "learning",
	}); err != nil {
		t.Fatal(err)
	}

	newVer, carried, err := s.CarryVerdictForward(sid, "down", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if !carried || newVer != extractorVer+1 {
		t.Fatalf("carried=%v newVer=%d, want true/%d", carried, newVer, extractorVer+1)
	}

	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	var exec, pref *core.Experience
	for _, e := range cur {
		if e.SessionID != sid {
			continue
		}
		switch e.Kind {
		case core.KindExecution:
			exec = e
		case core.KindPreference:
			pref = e
		}
	}
	if exec == nil || exec.Outcome.Verdict != "down" {
		t.Fatalf("execution 行に判定が乗っていない: %+v", exec)
	}
	if exec.Outcome.Adopted != "as-is" {
		t.Errorf("判定以外まで書き換わった: %+v", exec.Outcome)
	}
	if exec.ExtractorModel == "human" {
		t.Errorf("出自を書き換えてはいけない — 意味には触れていない: %q", exec.ExtractorModel)
	}
	// The preference sibling is under another kind, so its own view row is
	// untouched and still visible at its original version.
	if pref == nil {
		t.Fatal("好みの行が現行ビューから消えた")
	}
	if pref.ExtractorVer != extractorVer {
		t.Errorf("好みの行まで繰り上がった: ver %d", pref.ExtractorVer)
	}
}

// A session whose boundary perception never ran has nothing to carry. That is
// ordinary, not an error: the event is filed and the pending queue reads it.
func TestCarryVerdictForwardIsQuietWhenNothingWasPerceived(t *testing.T) {
	s := openTestStore(t)
	openSessionFor(t, s, "unperceived")

	_, carried, err := s.CarryVerdictForward("unperceived", "up", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if carried {
		t.Error("知覚されていないセッションから何かを繰り上げた")
	}
}

// 判定を変えられること自体が設計 (Decision 2): the ledger keeps both, and the
// last one is what the experience shows. clear walks all the way back to the
// layers below rather than to a third state.
func TestVerdictIsLastWinsAndClearFallsThroughToTheLayersBelow(t *testing.T) {
	green := true
	base := core.Outcome{TestsPassed: &green, Adopted: "with-edits"}

	for _, tc := range []struct {
		verdict string
		wantY   float64
	}{
		{"up", 1},
		{"down", 0},
		{"", 0.7}, // clear → 第3層の with-edits へ落ちる
	} {
		o := base
		o.Verdict = tc.verdict
		y, ok := core.OutcomeWeight(&core.Experience{Kind: core.KindExecution, Outcome: o})
		if !ok || y != tc.wantY {
			t.Errorf("verdict %q: y=%v ok=%v, want %v", tc.verdict, y, ok, tc.wantY)
		}
	}
}

// End to end through the boundary: 第1層が赤を観測し、人が「文句なし」と答え、
// 矛盾の問いが立ち、判定が同じ境界の知覚に間に合う (ADR-0055 Decision 1)。
// 位置が要である — recordSplitVerdict の隣、perceiveBestEffort の前でなければ、
// 判定は最初の知覚に乗らない。
func TestBoundaryVerdictLandsBeforePerception(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // 境界の知覚は機械共通の perceive.lock を取る — 実HOMEを掴ませない
	s := openTestStore(t)
	dir := wireFirstLayer(t, "exit 1") // 赤いスイート
	const sid = "boundary-veto"
	if err := s.AppendEvent(sid, "task.started", 1000,
		map[string]any{"intent": "go handler を直す", "source": "production"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(sid, "capability.started", 1000,
		map[string]any{"capability": "implement"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(sid, "provider.selected", 1000,
		map[string]any{"provider": "fake", "model": "m"}); err != nil {
		t.Fatal(err)
	}

	// Feedback「1=文句なし」→ 矛盾の問い「y」。humanPresent で境界の器官が動く。
	in := bufio.NewReader(strings.NewReader("1\ny\n\n\n\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}
	if err := finishTask(s, sid, in, &out, true, true, extractor, dir); err != nil {
		t.Fatal(err)
	}

	if v := verdictEvents(t, s, sid); len(v) != 1 || v[0]["verdict"] != "up" {
		t.Fatalf("境界で判定が記帳されていない: %v", v)
	}

	// 知覚は同じ境界で走るので、繰り上げ無しで最初の経験に乗っている。
	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	var got *core.Experience
	for _, e := range cur {
		if e.SessionID == sid && e.Kind == core.KindExecution {
			got = e
		}
	}
	if got == nil {
		t.Fatal("経験が作られていない")
	}
	if got.Outcome.Verdict != "up" {
		t.Errorf("判定が最初の知覚に乗っていない: %+v", got.Outcome)
	}
	if got.Outcome.TestsPassed == nil || *got.Outcome.TestsPassed {
		t.Errorf("赤の観測が経験から消えている: %+v", got.Outcome)
	}
	if y, ok := core.OutcomeWeight(got); !ok || y != 1 {
		t.Errorf("赤より強く記録されていない: y=%v ok=%v", y, ok)
	}
}
