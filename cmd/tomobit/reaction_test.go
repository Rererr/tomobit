package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

// reactAt places one reaction straight into the ledger, the way /react would.
func reactAt(t *testing.T, s *store.Store, sid string, n int, word string) {
	t.Helper()
	if err := s.AppendEvent(sid, "user.reaction", 1000, map[string]any{"n": n, "word": word}); err != nil {
		t.Fatal(err)
	}
}

// ADR-0057 Decision 1: 走行中のタスクへ、指定したターンの反応を置ける。
// 台帳に残るのは {n, word} で、n は turn.started / task.turn と同じ採番。
func TestChatReactRecordsTheReactionOnTheOpenTask(t *testing.T) {
	s := openTestStore(t)
	c, _ := newTestChat(t, s, &threadAdapter{}, "")
	if err := c.turn("implement it"); err != nil {
		t.Fatal(err)
	}

	done, err := c.command("/react 3 up")
	if err != nil || done {
		t.Fatalf("/react must answer in place and keep the conversation: done=%v err=%v", done, err)
	}

	p := payloadOf(t, s, "user.reaction")
	if p["word"] != "up" || p["n"] != float64(3) {
		t.Errorf("user.reaction payload = %v, want {n:3, word:up}", p)
	}
	if n := countEventsOfTypeInSession(t, s, c.sid, "user.reaction"); n != 1 {
		t.Errorf("the reaction belongs to the open task's session: got %d in %s", n, c.sid)
	}
}

// ADR-0057 Decision 1: 番号は検証しない。存在しないターンへの反応も受理する —
// 締めが読むのは最後の1件の word だけなので、Outcome を歪めない。
func TestChatReactAcceptsATurnNumberOutOfRange(t *testing.T) {
	s := openTestStore(t)
	c, _ := newTestChat(t, s, &threadAdapter{}, "")
	if err := c.turn("implement it"); err != nil { // 1ターンしか走っていない
		t.Fatal(err)
	}

	if _, err := c.command("/react 99 up"); err != nil {
		t.Fatal(err)
	}

	if p := payloadOf(t, s, "user.reaction"); p["n"] != float64(99) {
		t.Errorf("an out-of-range turn is recorded as given: %v", p)
	}
}

// ADR-0057 Decision 1: 区切りの上には置けない。第2層の書き手Bがその受け皿なので、
// 断るだけでなく行き先を書く。
func TestChatReactOnTheBoundaryRefusesAndNamesTheSecondLayer(t *testing.T) {
	s := openTestStore(t)
	c, out := newTestChat(t, s, &threadAdapter{}, "")

	done, err := c.command("/react 3 up")
	if err != nil || done {
		t.Fatalf("a refusal is answered in place: done=%v err=%v", done, err)
	}

	if n := countEventsOfType(t, s, "user.reaction"); n != 0 {
		t.Errorf("nothing may be recorded with no task open, got %d", n)
	}
	if !strings.Contains(out.String(), "tomobit verdict") {
		t.Errorf("the refusal must name where to go instead: %q", out.String())
	}
}

// 打ち間違いは会話を止めない (command() の姿勢: error は台帳が書けない時だけ)。
// 閉語彙の外・非数値・引数不足のいずれも、台帳には1件も落ちない。
func TestChatReactRefusesMalformedArgumentsWithoutRecording(t *testing.T) {
	for _, line := range []string{
		"/react",           // 引数なし
		"/react 3",         // 語が無い
		"/react up",        // 番号が無い
		"/react three up",  // 番号が数字でない
		"/react 0 up",      // 正の整数でない
		"/react 3 great",   // 閉語彙の外
		"/react 3 up down", // 余計な語
	} {
		t.Run(line, func(t *testing.T) {
			s := openTestStore(t)
			c, out := newTestChat(t, s, &threadAdapter{}, "")
			if err := c.turn("implement it"); err != nil {
				t.Fatal(err)
			}

			done, err := c.command(line)
			if err != nil || done {
				t.Fatalf("a mistyped command keeps the chat: done=%v err=%v", done, err)
			}
			if n := countEventsOfType(t, s, "user.reaction"); n != 0 {
				t.Errorf("%q must record nothing, got %d", line, n)
			}
			if out.String() == "" {
				t.Errorf("%q must be answered on the spot, said nothing", line)
			}
		})
	}
}

// ADR-0057 Decision 3: 記帳の確認として view に流れる。消費者は押した通りに
// 描かず、本体が記帳したものを描く。
func TestChatReactEmitsTheReactionToTheViewStream(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "")
	if err := c.turn("implement it"); err != nil {
		t.Fatal(err)
	}

	if _, err := c.command("/react 1 up"); err != nil {
		t.Fatal(err)
	}

	ev := firstOfType(viewEvents(t, buf), "reaction")
	if ev == nil || ev["n"] != float64(1) || ev["word"] != "up" {
		t.Errorf("reaction event = %v, want {n:1, word:up}", ev)
	}
}

// ADR-0057 Decision 1 が「GUI は turn.started の n を持っている」と書けるのは、
// view が見せる n と台帳の task.turn の n が同じ採番だからである。/react はその
// 番号をそのまま受けるので、3つが食い違った日、反応は黙って別のターンへ乗る。
func TestReactionTakesTheSameTurnNumberTheViewAndLedgerShow(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "")
	for _, p := range []string{"implement it", "now fix it"} {
		if err := c.turn(p); err != nil {
			t.Fatal(err)
		}
	}

	var shown []any
	for _, ev := range viewEvents(t, buf) {
		if ev["type"] == "turn.started" {
			shown = append(shown, ev["n"])
		}
	}
	if len(shown) != 2 || shown[0] != float64(1) || shown[1] != float64(2) {
		t.Fatalf("turn.started の n = %v, want [1 2]", shown)
	}
	if p := payloadOf(t, s, "task.turn"); p["n"] != shown[1] {
		t.Errorf("task.turn の n = %v, view が見せた %v と同じ採番でなければならない", p["n"], shown[1])
	}

	if _, err := c.command("/react 2 up"); err != nil {
		t.Fatal(err)
	}

	if p := payloadOf(t, s, "user.reaction"); p["n"] != shown[1] {
		t.Errorf("反応の n = %v, 人が画面で見た番号は %v", p["n"], shown[1])
	}
}

// 断られた反応は view にも残らない (ADR-0057 Decision 3)。
func TestChatReactRefusedEmitsNothingToTheViewStream(t *testing.T) {
	s := openTestStore(t)
	c, buf := newViewChat(t, s, &threadAdapter{}, "")
	if err := c.turn("implement it"); err != nil {
		t.Fatal(err)
	}

	if _, err := c.command("/react 1 great"); err != nil {
		t.Fatal(err)
	}

	if ev := firstOfType(viewEvents(t, buf), "reaction"); ev != nil {
		t.Errorf("a refused reaction must not reach the view: %v", ev)
	}
}

// ADR-0057 Decision 3: 語彙は init が配る。ラベルは区切りの問いの選択肢の文言
// そのままで、clear は配らない（あれは語ではなく取り消し）。
func TestInitCarriesTheReactionVocabularyAndNotClear(t *testing.T) {
	init := initEvent()
	words, ok := init["reactions"].([]map[string]any)
	if !ok || len(words) != 3 {
		t.Fatalf("init must carry the three reaction words: %v", init["reactions"])
	}
	want := []string{"up", "meh", "down"}
	for i, w := range want {
		if words[i]["word"] != w {
			t.Errorf("reactions[%d] = %v, want word %q", i, words[i], w)
		}
	}
	for _, r := range words {
		if r["word"] == reactionClear {
			t.Errorf("clear is a withdrawal, not a word to offer: %v", words)
		}
		label, _ := r["label"].(string)
		if label == "" || !strings.Contains(feedbackChoices(), label) {
			// 語彙のドリフト防止: init のラベルと締めの問いの文言は同じ表から出る。
			t.Errorf("reaction label %q must be the closing question's own wording (%q)", label, feedbackChoices())
		}
	}
}

// ADR-0057 Decision 2: 反応が置かれていれば締めでは訊かない。最後の1件の word が
// そのまま task.finished の payload になり、対応は問いの 1/2/3 と同じ表から引く。
func TestClosingTakesThePlacedReactionWithoutAsking(t *testing.T) {
	for _, tc := range []struct {
		word     string
		adopted  string
		reverted bool
		label    string
	}{
		{"up", "as-is", false, "文句なし"},
		{"meh", "with-edits", false, "まあまあ（手を焼いた）"},
		{"down", "", true, "だめだった"},
	} {
		t.Run(tc.word, func(t *testing.T) {
			s := openTestStore(t)
			const sid = "reacted"
			reactAt(t, s, sid, 1, tc.word)

			// 人の次の1行は残っていなければならない — 訊いていないのだから
			// 読んでもいないはずである。
			in := bufio.NewReader(strings.NewReader("1\n"))
			var out bytes.Buffer
			got := feedbackPayload(s, sid, in, &out)

			if got["adopted"] != tc.adopted || got["reverted"] != tc.reverted {
				t.Errorf("%s: payload = %v, want adopted=%q reverted=%v", tc.word, got, tc.adopted, tc.reverted)
			}
			if strings.Contains(out.String(), "今回、どうだった?") {
				t.Errorf("%s: the closing question must not be asked: %q", tc.word, out.String())
			}
			if !strings.Contains(out.String(), tc.label) {
				t.Errorf("%s: 黙って記録しない — 何として記録したか言う: %q", tc.word, out.String())
			}
			if rest, _ := in.ReadString('\n'); rest != "1\n" {
				t.Errorf("%s: the human's next line was consumed by a question that should not have run", tc.word)
			}
		})
	}
}

// ADR-0057 Decision 1: 上書きは最後が勝つ。台帳には両方残る（気が変わったこと
// 自体が Reality）が、締めが読むのは最後の1件だけ。
func TestClosingReadsOnlyTheLastReaction(t *testing.T) {
	s := openTestStore(t)
	const sid = "changed-mind"
	reactAt(t, s, sid, 3, "up")
	reactAt(t, s, sid, 7, "down")

	got := feedbackPayload(s, sid, bufio.NewReader(strings.NewReader("")), io.Discard)

	if got["reverted"] != true {
		t.Errorf("the last reaction is the answer: %v", got)
	}
}

// ADR-0057 Decision 2: clear は「答えない」へ戻す操作なので、締めは従来どおり訊く。
func TestClosingAsksAgainAfterAReactionIsCleared(t *testing.T) {
	s := openTestStore(t)
	const sid = "withdrawn"
	reactAt(t, s, sid, 1, "up")
	reactAt(t, s, sid, 1, reactionClear)

	var out bytes.Buffer
	got := feedbackPayload(s, sid, bufio.NewReader(strings.NewReader("3\n")), &out)

	if !strings.Contains(out.String(), "今回、どうだった?") {
		t.Errorf("a cleared reaction leaves the question standing: %q", out.String())
	}
	if got["reverted"] != true {
		t.Errorf("the answered 3 is what lands: %v", got)
	}
}

// ADR-0057 Consequences: `do` の締めは1バイトも変わらない — 会話が無いのだから
// 反応も1件も無く、問いはこれまでどおり立つ。
func TestClosingWithoutAnyReactionAsksAsBefore(t *testing.T) {
	s := openTestStore(t)
	var out bytes.Buffer
	got := feedbackPayload(s, "do-session", bufio.NewReader(strings.NewReader("1\n")), &out)

	if !strings.Contains(out.String(), "今回、どうだった? [1=文句なし / 2=まあまあ（手を焼いた） / 3=だめだった / Enter=まだ言えない]") {
		t.Errorf("the closing question must be unchanged, word for word: %q", out.String())
	}
	if got["adopted"] != "as-is" {
		t.Errorf("payload = %v, want the answered 1", got)
	}
}

// 区切りの問いの答えは "1"/"2"/"3" との完全一致だけを受ける。表を1枚にした
// 副作用で語彙が広がっていないことの固定 — "+1" や "01" は数としては 1 だが、
// 打ち間違いを「文句なし」として台帳に載せないのが feedbackPayload の既定である。
func TestClosingAnswerIsAnExactChoiceNotANumber(t *testing.T) {
	// 前後の空白は入れない: 行末の改行ごと TrimSpace するのは旧実装からの仕様で、
	// 空白は打ち間違いではなく端末の都合である（\r\n もそこで落ちる）。
	for _, typo := range []string{"+1", "01", "1.0", "4", "１"} {
		s := openTestStore(t)
		var out bytes.Buffer
		got := feedbackPayload(s, "do-session", bufio.NewReader(strings.NewReader(typo+"\n")), &out)
		if len(got) != 0 {
			t.Errorf("%q は無信号でなければならない: %v", typo, got)
		}
	}
	// 完全一致は通る（上の厳しさが、答えられなくなるところまで行っていない）。
	s := openTestStore(t)
	got := feedbackPayload(s, "do-session", bufio.NewReader(strings.NewReader("2\n")), io.Discard)
	if got["adopted"] != "with-edits" {
		t.Errorf("payload = %v, want the answered 2", got)
	}
}

// ADR-0057 Decision 1: 取り消しもコマンドの口から通る。上の
// TestClosingAsksAgainAfterAReactionIsCleared は台帳へ直に置いていたので、
// `/react ... clear` の受付分岐そのものはここでしか通らない。
func TestChatReactClearGoesThroughTheCommandAndWithdrawsTheAnswer(t *testing.T) {
	s := openTestStore(t)
	c, _ := newTestChat(t, s, &threadAdapter{}, "")
	if err := c.turn("implement it"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.command("/react 1 up"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.command("/react 1 clear"); err != nil {
		t.Fatal(err)
	}
	if n := countEventsOfTypeInSession(t, s, c.sid, "user.reaction"); n != 2 {
		t.Errorf("取り消しも1件の追記として残る（台帳は追記専用）: got %d", n)
	}

	var out bytes.Buffer
	got := feedbackPayload(s, c.sid, bufio.NewReader(strings.NewReader("")), &out)
	if !strings.Contains(out.String(), "今回、どうだった?") {
		t.Errorf("取り消した後の締めは訊く側へ戻る: %q", out.String())
	}
	if len(got) != 0 {
		t.Errorf("Enter は無信号のまま: %v", got)
	}
}

// 消費者規律の裏返し: 台帳は追記専用なので、将来の本体が置いた**知らない語**が
// 最後の1件になる日がありうる。知らない語を勝手に既知の語へ寄せず、無反応と
// 同じ扱い（＝訊く）へ落とす。
func TestClosingAsksWhenTheLastReactionIsAnUnknownWord(t *testing.T) {
	s := openTestStore(t)
	const sid = "from-the-future"
	reactAt(t, s, sid, 1, "up")
	reactAt(t, s, sid, 2, "sparkle")

	var out bytes.Buffer
	got := feedbackPayload(s, sid, bufio.NewReader(strings.NewReader("")), &out)
	if !strings.Contains(out.String(), "今回、どうだった?") {
		t.Errorf("知らない語は答えにしない: %q", out.String())
	}
	if len(got) != 0 {
		t.Errorf("payload = %v, want 無信号", got)
	}
}

// ADR-0057「ADR-0052 Decision 5 の保証をどうするか」: 反応は赤を見る前に置かれ
// うるので、赤テスト × up では矛盾の問い (ADR-0055 Decision 1) がこれまで通り
// 立たなければならない。反応から作った payload も人が打った payload と同じ形で、
// askVerdictOnContradiction の配線は1バイトも変わっていない。
func TestReactionUpStillFacesTheRedContradiction(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // 境界の知覚は機械共通の perceive.lock を取る
	s := openTestStore(t)
	dir := wireFirstLayer(t, "exit 1") // 赤いスイート
	const sid = "reacted-red"
	if err := s.AppendEvent(sid, "task.started", 1000,
		map[string]any{"intent": "go handler を直す", "source": "production"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(sid, "provider.selected", 1000,
		map[string]any{"provider": "fake", "model": "m"}); err != nil {
		t.Fatal(err)
	}
	reactAt(t, s, sid, 2, "up")

	// 締めの問いは出ないので、この "y" は矛盾の問いへの答えである。
	in := bufio.NewReader(strings.NewReader("y\n\n\n\n"))
	var out bytes.Buffer
	extractor := &fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}
	if err := finishTask(s, sid, in, &out, true, true, extractor, dir); err != nil {
		t.Fatal(err)
	}

	if p := payloadOf(t, s, "task.finished"); p["adopted"] != "as-is" {
		t.Fatalf("the reaction must be the closing answer: %v", p)
	}
	if !strings.Contains(out.String(), "テストは赤だったけど") {
		t.Fatalf("赤 × up の矛盾の問いが立っていない: %q", out.String())
	}
	if v := verdictEvents(t, s, sid); len(v) != 1 || v[0]["verdict"] != "up" {
		t.Fatalf("第2層の拒否権が記帳されていない: %v", v)
	}
	cur, err := s.CurrentExperiences()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cur {
		if e.SessionID != sid || e.Kind != core.KindExecution {
			continue
		}
		if y, ok := core.OutcomeWeight(e); !ok || y != 1 {
			t.Errorf("拒否権が効いていない: y=%v ok=%v outcome=%+v", y, ok, e.Outcome)
		}
		return
	}
	t.Fatal("経験が作られていない")
}

// 実バイナリ・実パイプでの通し (ADR-0057 Decision 1 / 2 / 3)。手で組んだ chat では
// 触れない範囲がここに3つある: `init` が実際に語彙を配ること、`/react` が
// cmdChat のコマンド経路を通って記帳と view の両方へ届くこと、そして締めが
// 「窓を閉じる人を引き止めない」— Feedback の問いが1度も現れないこと。
func TestPipedChatReactionAnswersTheClosingWithoutAsking(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs a real binary")
	}
	bin := buildTomobitBinary(t)
	dbPath := filepath.Join(t.TempDir(), "tomobit.db")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "chat", "--db", dbPath,
		"--provider", "human", "--view", "ndjson",
		// 到達しない先を指すのは意図的: 知覚は best-effort (ADR-0006 Decision 5) で、
		// ここで見ているのは締めの器官である。
		"--backend", "ollama", "--url", "http://127.0.0.1:1",
		"first task")
	cmd.Env = []string{"HOME=" + t.TempDir()}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	events := make(chan map[string]any, 64)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("stdout line is not valid JSON: %q: %v", sc.Text(), err)
				continue
			}
			events <- m
		}
	}()

	var seen []map[string]any
	waitFor := func(pred func(map[string]any) bool, label string) map[string]any {
		t.Helper()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					t.Fatalf("stdout closed before %s arrived — stderr:\n%s", label, stderr.String())
				}
				seen = append(seen, ev)
				if pred(ev) {
					return ev
				}
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s — stderr:\n%s", label, stderr.String())
			}
		}
	}
	isType := func(typ string) func(map[string]any) bool {
		return func(ev map[string]any) bool { return ev["type"] == typ }
	}

	init := waitFor(isType("init"), "the stream's init")
	vocab, _ := init["reactions"].([]any)
	if len(vocab) != 3 {
		t.Fatalf("init must hand the consumer the reaction vocabulary: %v", init)
	}
	if first, _ := vocab[0].(map[string]any); first["word"] != "up" || first["label"] != "文句なし" {
		t.Errorf("init reactions[0] = %v, want the body's own word and label", vocab[0])
	}

	waitFor(isType("ready"), "the prompt after the opening turn")
	if _, err := io.WriteString(stdin, "/react 1 up\n"); err != nil {
		t.Fatal(err)
	}
	react := waitFor(isType("reaction"), "the reaction's view event")
	if react["n"] != float64(1) || react["word"] != "up" {
		t.Errorf("reaction event = %v, want {n:1, word:up}", react)
	}

	if _, err := io.WriteString(stdin, "/exit\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(isType("task.finished"), "task.finished")
	for ev := range events {
		seen = append(seen, ev)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("chat exited with error: %v — stderr:\n%s", err, stderr.String())
	}

	for _, ev := range seen {
		if ev["type"] != "note" {
			continue
		}
		if text, _ := ev["text"].(string); strings.Contains(text, "今回、どうだった?") {
			t.Errorf("帰ろうとしている人を引き止めてはならない — 反応が置かれた締めで問いが出た: %v", ev)
		}
	}
	said := false
	for _, ev := range seen {
		if text, _ := ev["text"].(string); strings.Contains(text, "会話中に置いた反応") {
			said = true
		}
	}
	if !said {
		t.Errorf("黙って記録しない — 何として記録したかを1行言うこと: %v", viewTypes(seen))
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if p := payloadOf(t, s, "user.reaction"); p["word"] != "up" || p["n"] != float64(1) {
		t.Errorf("user.reaction: got %v, want {n:1, word:up}", p)
	}
	if p := payloadOf(t, s, "task.finished"); p["adopted"] != "as-is" || p["reverted"] != false {
		t.Errorf("task.finished: got %v, want the reaction's own payload", p)
	}
}
