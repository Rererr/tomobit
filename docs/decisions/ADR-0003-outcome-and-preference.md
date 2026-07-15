# ADR-0003: Outcomeの質と、Tomoの質問 — 能力と好みの二重Connection

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0001](ADR-0001-connection-granularity.md), [ADR-0002](ADR-0002-surprise-and-split-judgment.md), [CONNECTION_ENGINE.md](../core/CONNECTION_ENGINE.md), [CURIOSITY_ENGINE.md](../core/CURIOSITY_ENGINE.md)

---

## Context

問い: **「人間のgood/bad判定だけで、使用者の個性は出るか?」**

分析の結論:

- 個性は既に2経路で入る
  1. 判定基準そのものが使用者（厳しい採点者でも全Providerが同じ採点者なので自己整合）
  2. グラフの形が経験分布の形（Splitは使用者が痛みを経験した場所でしか起きない）
- しかし**同点勝負の好み**はbinaryでは原理的に書けない
  （両方Successなら、どれだけ経験を積んでも同じ強さに収束する）
- さらに毎回の明示判定は**判定疲れ**で続かない

---

## Decision 1: Outcomeは三層

```text
第1層  Objective（自動収穫・毎回）
       テスト通過 / コンパイル / そのまま採用 / 手直し量 / 後日Revert
       → 能力のConnectionへ。判定疲れゼロ

第2層  Explicit Verdict（任意）
       👍/👎 — 強い上書きとして能力のConnectionへ

第3層  Preference（Tomoの質問への回答）
       「どっちが好みだった?」 → 好みのConnectionへ
```

ラベリング義務をpull型の会話に反転させ、
頻度制御の責任を人間からTomobit側（Learning Scheduler）へ移す。

---

## Decision 2: 能力と好みの二重Connection

「負けた」と「できなかった」は別の事実である。
選好を能力に記帳すると、有能な次点が失敗者として沈む
（Aが不在の日にBを不当に避ける誤り）。

```text
能力のConnection
  (Rust) → Claude           Beta(α, β)
  第1層・第2層が記帳される

好みのConnection
  (Rust) : Claude vs Codex  Beta(α, β)
  α = Claudeが選ばれた回数 / β = Codexが選ばれた回数
```

- Decision Engineは**能力で足切りし、好みで順位を付ける**（二段構え）
- One Ledger原則との整合: 原則は「一つの事実に一つの帳簿」。
  能力と好みは直交する別の事実であり、各々がBeta一つを持つのは多重帳簿ではない
- 好みのConnectionも実体は減衰Betaひとつ。**既存機構がすべてそのまま適用される**
  - Decay: 好みも古びる
  - Confidence: 1回の回答で好みを断定しない
  - Surprise → Split: 「CLIツールの時だけCodexの簡潔さを選ぶ」→ 趣味にもシナプスが生える

---

## Decision 3: Tomoの質問はLearning Realityの一種

新しい器官は作らない。既存パイプラインに乗せる。

```text
Curiosity Engine     Preference Gap検出
                     「能力が互角、かつ好みを知らない、かつ文脈が頻繁」
      ▼
Curiosity Queue      質問候補として積む
      ▼
Learning Scheduler   質問予算・タイミングを判断
      ▼
Human Executor       Tomoが聞く「どっちが好みだった?」
      ▼
回答 = Learning Reality → Perception → Experience → 好みのConnection
```

- **いつ聞くか＝情報利得**: BFが中立帯（互角）×文脈の頻度（決定に効く）。
  本当に迷っている時しか聞かないので、質問は自然と「賢い質問」になる
- **ストレス設計**: 質問予算（初期値: 1日1問）、タスク完了の区切りでのみ、
  スキップ自由・ペナルティなし
- ADR-0002の判定機構（BF中立帯）がトリガーとして再利用される

---

## Consequences

- 個性の穴（同点勝負の好み）が塞がり、Outcomeの質のOpen Questionが閉じる
- 判定疲れが構造的に解消（第1層が毎回、第2層は気が向いた時、第3層は聞かれた時だけ）
- Curiosity Signalに2種が加わる: Questioned（Surprise台帳の浮上、ADR-0002）と
  Preference Gap（本ADR）
- 実装時に調整するノブ:

```text
第1層の暗黙シグナルの重み   例: そのまま採用=1.0 / 軽微な手直し=0.7 / Revert=強い失敗
質問予算                     初期値 1日1問
好みの事前分布               Beta(1,1) から開始
```

- 残るOpen Questions: 減衰半減期、事前分布の継承、backoffブレンド
