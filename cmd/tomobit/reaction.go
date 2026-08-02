package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Rererr/tomobit/internal/store"
)

// closingGrade は締めの答えの語彙1つ分 (ADR-0057)。人がそれを置く口は2つ —
// 区切りの問いの 1/2/3 と、会話の中の `/react` — あるが、台帳に落ちる形は1つで、
// その写像はこの表だけが持つ。
//
// Why not 語彙用と番号用で表を分けるか: ADR-0057 Decision 2 は「訳の対応は
// feedbackPayload の 1/2/3 と同じ表である」と決めた。2枚に割った瞬間、
// 片方にだけ語が足された日に、口ごとに意味の違う台帳が生まれる。
type closingGrade struct {
	word string // `/react` と view の語 (ADR-0055 の up|down に揃えてある)
	// label は区切りの問いの選択肢の文言であり、init が消費者へ配るラベルでもある。
	// 同じ文字列が2箇所に literal で散らないための1本化。
	label    string
	adopted  string
	reverted bool
}

// closingGrades の並びは、そのまま区切りの問いの 1/2/3 の並びである
// (gradeByChoice が添字で引く)。`meh` が第2層 user.verdict の語彙に無いのは
// 正しい非対称で、拒否権には中間が要らない (ADR-0057 Decision 1)。
var closingGrades = []closingGrade{
	{word: "up", label: "文句なし", adopted: "as-is", reverted: false},
	{word: "meh", label: "まあまあ（手を焼いた）", adopted: "with-edits", reverted: false},
	{word: "down", label: "だめだった", adopted: "", reverted: true},
}

// reactionClear は置いたものの取り消し。closingGrades には入れない —
// 「答えない」へ戻す操作であって、答えの1つではないからで、init が配る語彙から
// 外れるのも同じ理由 (ADR-0057 Decision 3)。
const reactionClear = "clear"

// payload は task.finished に落ちる形。キーが adopted/reverted のままなのは
// SCHEMA も rebuild も動かさないため (ADR-0028 で移ったのは呼称だけ)。
func (g closingGrade) payload() map[string]any {
	return map[string]any{"adopted": g.adopted, "reverted": g.reverted}
}

func gradeByWord(word string) (closingGrade, bool) {
	for _, g := range closingGrades {
		if g.word == word {
			return g, true
		}
	}
	return closingGrade{}, false
}

// gradeByChoice は区切りの問いの答え ("1"/"2"/"3") を同じ表から引く。
//
// Why not strconv.Atoi で数として読むか: 旧実装は "1"/"2"/"3" との**完全一致**
// だった。Atoi は "+1" も "01" も 1 として受理するので、数として読み直した瞬間、
// 打ち間違いが「文句なし」として台帳に載る。feedbackPayload が既定を無信号に
// してある理由（うっかりの1打鍵が台帳を賞賛で膨らませてはならない）が、
// 番号側でだけ緩む — 表を1枚にしたことの副作用で語彙を広げない。
func gradeByChoice(answer string) (closingGrade, bool) {
	for i, g := range closingGrades {
		if answer == strconv.Itoa(i+1) {
			return g, true
		}
	}
	return closingGrade{}, false
}

// feedbackChoices は区切りの問いの選択肢を表から組む。
func feedbackChoices() string {
	parts := make([]string, len(closingGrades))
	for i, g := range closingGrades {
		parts[i] = fmt.Sprintf("%d=%s", i+1, g.label)
	}
	return strings.Join(parts, " / ")
}

// reactionVocabulary は init が消費者へ配る語彙 (ADR-0057 Decision 3)。
// 見た目は消費者の自由、語彙は本体のもの — 消費者に語を持たせないので、
// 本体が語を足した日に古い口が黙って出続けることが構造的に起きない。
func reactionVocabulary() []map[string]any {
	out := make([]map[string]any, len(closingGrades))
	for i, g := range closingGrades {
		out[i] = map[string]any{"word": g.word, "label": g.label}
	}
	return out
}

// lastReaction は締めが読む答え: そのセッションに置かれた user.reaction の
// **最後の1件だけ** (ADR-0057 Decision 2)。
//
// Why not n を読んで束ねるか: 締めの答えはタスク1つに1つでよい
// (ADR-0022 Decision 1)。n が台帳に残るのは「会話のどこで気が変わったか」が
// Reality だからで、Outcome の粒度を増やすためではない。
//
// Why not 最後が clear のときそれを答えとして返すか: 取り消しは「答えない」で
// あって「答えた」ではないので、締めは従来どおり訊く側へ落ちる。未知の語も
// 同じ扱い — 台帳は追記専用で、いつか知らない語が載っている可能性は残る。
func lastReaction(s *store.Store, sid string) (closingGrade, bool, error) {
	evs, err := s.EventsBySession(sid)
	if err != nil {
		return closingGrade{}, false, fmt.Errorf("reaction: %s のイベントを読めない: %w", sid, err)
	}
	grade, placed := closingGrade{}, false
	for _, e := range evs { // EventsBySession は seq 順 = 置かれた順
		if e.Type != "user.reaction" {
			continue
		}
		word, _ := e.Payload["word"].(string)
		grade, placed = gradeByWord(word) // clear も未知語も placed=false へ戻す
	}
	return grade, placed, nil
}

// react は締めの答えを会話の中へ先に置く口 (ADR-0057 Decision 1)。
//
//	/react 3 up      3ターン目に「文句なし」を置く
//	/react 5 down    5ターン目に「だめだった」を置く（最後が勝つ）
//	/react 5 clear   置いたものを取り消す（無反応へ戻る）
//
// error を返すのは台帳が書けない時だけで、人が打ち間違えただけならその場で
// 答えて会話は続く — command() の他のコマンドと同じ姿勢である。
func (c *chat) react(arg string) error {
	// Why not 引数の形を先に見るか: 区切りの上では、正しく打てていても置けない。
	// 打ち方を教えてから断るのは二度手間で、要るのは行き先だけである。
	if c.sid == "" {
		c.sayln("走っているタスクが無い — 区切ったタスクには tomobit verdict <sid> を使う")
		return nil
	}
	fields := strings.Fields(arg)
	if len(fields) != 2 {
		c.sayln("/react <turn> up|meh|down|clear — 例: /react 3 up")
		return nil
	}
	// Why not ターン番号を省略して「直近のターン」に倒すか: 走行中に押した反応が
	// どのターンに乗るかが競合で決まる。GUI は turn.started の n を持っているし、
	// 端末で打つ人は画面を見て番号を言える (ADR-0057 Decision 1)。
	n, err := strconv.Atoi(fields[0])
	if err != nil || n < 1 {
		c.sayln(fmt.Sprintf("%s — ターン番号は正の整数で言う（例: /react 3 up）", fields[0]))
		return nil
	}
	// 範囲の検証はしない: 存在しないターン番号は台帳に残るだけで、締めが読むのは
	// 最後の1件の word だけなので Outcome を歪めない (ADR-0057 Decision 1)。
	word := fields[1]
	grade, known := gradeByWord(word)
	if !known && word != reactionClear {
		c.sayln(fmt.Sprintf("%s — 知らない反応。up|meh|down|clear", word))
		return nil
	}
	if err := c.s.AppendEvent(c.sid, "user.reaction", time.Now().UnixMilli(),
		map[string]any{"n": n, "word": word}); err != nil {
		return fmt.Errorf("reaction: 記帳に失敗: %w", err)
	}
	// 記帳できてから流す: 消費者は押した通りに描かず、本体が記帳したものを描く
	// (ADR-0057 Decision 3) ので、断られた反応は画面にも残らない。
	if c.stream != nil {
		c.stream.emit(map[string]any{"type": "reaction", "n": n, "word": word})
	}
	if word == reactionClear {
		c.sayln(dim(fmt.Sprintf("%dターン目の反応を取り消した", n)))
		return nil
	}
	c.sayln(dim(fmt.Sprintf("%dターン目に「%s」を置いた — 締めでは訊かない", n, grade.label)))
	return nil
}
