# ADR-0015: Reflection — 第一級の器官、実体は射影、核は双方向性

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0002](ADR-0002-surprise-and-split-judgment.md), [ADR-0003](ADR-0003-outcome-and-preference.md), [ADR-0007](ADR-0007-curiosity-question.md), [ADR-0009](ADR-0009-voice.md), [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md), [REFLECTION.md](../core/REFLECTION.md)

---

## Context

これまでの設計では、学習の矢印が全てTomoに向かって流れていた。
経験は台帳に溜まり、Tomoは賢くなる — しかしVISIONの「経験を資産に」の
受益者にはもう一人いる。使用者自身である。

Tomoが発見したことを人間に返す器官が存在しなかった。

一方で、語るべき中身はすでに全て生成されている。
Split・逆転・Questioned・名誉回復 — 台帳のイベントは、
そのまま「気付き」の形をしている。

---

## Decision 1: 第一級の器官として立てる。実体は射影

Reflectionに名前と責務を与える（[REFLECTION.md](../core/REFLECTION.md)）。
CuriosityやTomoの質問と混ぜない — 目的が逆向きだからである。

```text
Curiosity     自分が学ぶため
Tomoの質問    自分が知るため
Reflection    相手が育つため
```

ただし実体は新しい真実を作らない**射影**であり、機構は再利用する:
語りはvoice（[ADR-0009](ADR-0009-voice.md)）、予算は質問予算の型
（[ADR-0007](ADR-0007-curiosity-question.md)、初期値1日1つ、スキップ自由）。

---

## Decision 2: 初期トリガーは台帳イベント4種

```text
Splitの誕生   意味のある区別の発見
逆転          事後平均の順位交差（ヒステリシス付き — ADR-0002の
              シュミットトリガーを借用し、僅差の揺れで語らない）
Questioned    Surprise台帳の浮上（既存機構の再利用）
名誉回復      悲観ゲートへの再入場（ADR-0012）
```

いずれも既に計算されている導出イベントであり、
Reflectionのための新しい検出器は作らない。

---

## Decision 3: 反応を記帳する — 双方向性が核（本人確定）

使用者の反応（「意外」「知ってた」「それ違う」）は
Learning Realityとして記帳される（experiences kind='reflection'）。

反応は二重に効く:

1. **Reflectionの選球眼へ** — 気付きの型ごとの台帳が反応から育つ。
   どの候補を語るかは、この台帳への決定則（ADR-0012）で選ぶ。
   台帳は賭ける対象を選ばない — 語りの選択もJudgment by Math
2. **内容の出所へ** — 「それ違う」は該当Connectionへの
   強いフィードバック（ADR-0003 第2層と同じ重み）として流れる

> **育てているつもりが、映されて育てられてもいる。**

---

## Decision 4: LLMの座席は言語化のみ

台帳イベント→日本語の語り、の写像だけがLLMの仕事（意味側の仕事）。
何を語るか・語るかどうかは数式が決める。
[ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md)はReflectionでも破られない。

---

## Consequences

- SCHEMAへイベント追加: `tomo.reflected`（語った事実。予算管理と重複抑止に使う）、
  反応は `tomo.asked` の回答と同型のLearning Reality → experiences kind='reflection'
- 語り自体は保存しない（射影）。真実に残るのは「語った事実」と「反応」のみ
- COGNITIVE_ARCHITECTUREにReflection Engineを追加
- 実装ノブ: 予算初期値（1日1つ）、逆転検出のヒステリシス幅、
  反応の選択肢の文言（voice管轄）
- 残るOpen Question: 反応の入力UI（質問と同じCLI対話形か）
- 実装追記（2026-07-16、`internal/reflection`）:
  - 反応UIは質問と同じCLI対話形で確定（doとperceiveの境界、TTYのみ。
    OQ解消）。逆転ヒステリシス幅=0.1（事後平均差）
  - 語りの言語化はv1では**voiceの決定的テンプレート**。LLMの座席
    （Decision 4）は予約のまま — 鏡がOllamaの起動に依存して黙るのは
    器官として脆いため。LLMによる磨き上げは後続
  - 反応の記帳形: `experiences kind='reflection'`、context=対象Connectionの
    scope、outcomeに `insight`（型）と `reaction` を持つ。insightを
    contextトークンにしない理由: Split判定の候補語彙に混入し、鏡の帳簿で
    能力の世界が分割され得るため。Engine.Applyはreflection経験で
    **Connectionを誕生させない**（既存の一致にのみ流れる）
  - 選球眼の重み: 意外=1・それ違う=1（訂正を引き出したのは鏡の仕事 —
    内容への罰はverdict経由で別に流れる）・知ってた=0
  - 「それ違う」= `verdict: down` を同経験に載せ、通常のApply経路で
    該当Connectionへ（ADR-0003 第2層と同重み・rebuild安全）
  - トリガーは5種（ADR-0019 Decision 4の再知覚を含む）

---

## 追記（2026-07-21）: ADR-0035（境界の器官はviewストリームの向こうの人に届く）による改版

上記実装追記（2026-07-16）の「反応UIは…TTYのみ」は撤回する。GUIが
`tomobit chat --view ndjson` をpipeで飼う構成では、この条件下で鏡が
構造上一度も発火しなかった。[ADR-0035](ADR-0035-boundary-organs-reach-the-pipe.md)が
発火条件を `isTTY(os.Stdin) || --view ndjson`（pipeの向こうに人が居るという
呼び出しごとの宣言）へ改め、GUI経由の対話でも鏡が届くようになった。
