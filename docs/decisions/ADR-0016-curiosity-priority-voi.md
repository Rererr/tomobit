# ADR-0016: Curiosityの優先度 = Value of Information

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0003](ADR-0003-outcome-and-preference.md), [ADR-0007](ADR-0007-curiosity-question.md), [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md), [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md), [CURIOSITY_ENGINE.md](../core/CURIOSITY_ENGINE.md)

---

## Context

Curiosity Signalは9種に増えた（Knowledge Gap / Low Confidence / New Provider /
Model Update / Environment Change / High Uncertainty / Questioned /
Preference Gap / Plan Proposal）。

しかし、複数の推薦が並んだとき**誰を先に扱うかの規則が無かった**。
型ごとに優先度ルールを書き始めれば、それは禁止したはずのRuleの群れになる。
Queueの例に書かれていた `Priority 95` は出所のないマジックナンバーだった。

---

## Decision 1: Signalは検出器に格下げする

Signalの仕事は「調べる価値があるかもしれない」という推薦まで。
順位は付けない。

---

## Decision 2: 順位付けは単一の純関数 — VoI

```text
VoI = 文脈の到来頻度 × 判断の揺らぎ

到来頻度   eventsから数える
揺らぎ     判断のくじ（TS）をM本引き、勝者（argmax）が割れる率
```

新しい部品はゼロ — 判断に使うサンプラー（ADR-0012）を計測に使い回す。

> **不確実性は好奇心の理由にならない。判断が変わることだけが理由になる。**

- 素朴な「不確実なものから調べる」は、年1回しか来ない島の謎に予算を燃やす。
  VoIは頻度≈0でそれを自動で沈める
- 新Providerは台帳が空（揺らぎ最大）なので、**頻繁な島から順に**検証される
- Tomoの質問の1日1問も同じ物差しに乗る — Preference Gapの発火条件
  「BF中立帯 × 頻繁な文脈」（ADR-0003）は、実はVoIの特殊例だった。
  本ADRは発明ではなく一般化である

好奇心の配分もJudgment by Math（ADR-0011）に従う。

---

## Decision 3: コスト項はv1では持たない — フィールドだけ予約

VoIをコストで割ることはしない。理由は工数の所在:
割り算のコードは1〜2時間だが、真の工数は
①実験型ごとの見積り関数（型追加ごとに義務化）
②機械コストと人間コストの換算率（正解のない新ノブ）
③見積りのズレが順位を**静かに**歪む（ADR-0005の「沈黙する誤り」の型）の保守にある。

- 責務分離で安全は既に足りる: **価値の物差しはVoI、財布の紐はScheduler**
  （Token Budget / Rate Limit / Cost BudgetはLearning Schedulerの既存責務）
- Learning Candidateの `Estimated Cost` は予約フィールドとして残す（v1は常に空）。
  後から入れる工数は30分 — 「学ぶ権利の保全」のコスト版

---

## Consequences

- Queueの順位に出所ができ、マジックナンバーが消える
- 「Knowledge Expansion over Optimization」に一文追記:
  豊かさは使われる場所で測る（誰も訪れない島の精密な地図は退蔵）
- 実装ノブ: M（draw本数）、到来頻度の窓（減衰カウントで数えるか）
- 残るOpen Question: 頻度の推定に減衰を掛けるか（古い到来は薄く数える方が
  一貫するが、v1は単純カウントでも可）
