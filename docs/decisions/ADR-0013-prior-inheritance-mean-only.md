# ADR-0013: 事前分布の継承 — 平均だけ継ぎ、確信は継がない

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0001](ADR-0001-connection-granularity.md), [ADR-0003](ADR-0003-outcome-and-preference.md), [ADR-0004](ADR-0004-tech-stack.md), [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md), [CONNECTION_ENGINE.md](../core/CONNECTION_ENGINE.md)

---

## Context

CONNECTION_ENGINEにOpen Questionが二つ残っていた。

```text
2. 事前分布      Beta(1,1)か、親の事後を子の事前に継承するか
3. backoff       粗と細のConfidenceをどう混ぜるか
```

さらにBorn with History（[ADR-0001](ADR-0001-connection-granularity.md)）との緊張がある。
子はReplayで実証拠を注入されて生まれる。親の事後を**質量ごと**継ぐと、
同じ経験が事前の中とReplayとで二度数えられる。

本ADRは二つのOpen Questionを一つの機構で同時に閉じる。

---

## Decision 1: 子の事前 = 親の事後平均を、固定質量にスケール

```text
子の事前 = Beta(μ·m₀, (1−μ)·m₀)

μ   Split時点の親の事後平均
m₀  固定質量（初期値 2）
```

親が1000戦の猛者でも、子はm₀の赤ん坊としてBornする。
ただし赤ん坊の第一印象は、親の意見である。

**平均だけ継ぎ、確信は継がない。質量は証拠が運ぶ。**

二重計上は高々m₀擬似観測に有界 — 定数であり、実証拠が積もれば消える。

---

## Decision 2: 継承こそがbackoff — 判断は最細一致のみを読む

粗の知識は、誕生の瞬間に事前として一度だけ子へ流れる。
以後、クエリ時のブレンド（hard switch / 重み付き平均）は行わない。

**Decision Engineは、最細一致のConnectionを一つだけ読む。**

- どのConnectionを読んだかが常に一意 → 監査可能性（[ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md)）が保たれる
- 粗のConnectionはSurprise/Split検出のために生き続けるが、判断には読まれない

副産物として、backoffは時間方向にも自然に現れる。
One Ledgerの減衰は「事前分布へ戻す」動きであるため、
子が忘れきると継承時のμ — 親の意見 — へ帰る。

> **忘却の底は白紙ではなく、家系の記憶である。**

細い証拠が薄れたら粗い知識へ退避する。これは別機構ではなく、同じ一つの機構の帰結。

---

## Decision 3: Replayは元の日付で数え直す

Replayとは、

> **過去の出来事を、元のタイムスタンプのまま、通常の減衰計算に食わせ直すこと**

である。注入した日に若返らせてはならない。

半年前の成功は、子のノートでも半年ぶんのインクの薄さで数える。
「今日の出来事」として写せば昔の手柄が若返り、
「直近の失敗は古い成功より重い」（[ADR-0012](ADR-0012-decision-rule-thompson-sampling.md)）を含む
時間ベースの設計が全て狂う。

これは真実/射影の分離とlazy減衰（[ADR-0004](ADR-0004-tech-stack.md)）の帰結であり、新機構ではない。
ただし実装の近道（親のα,βの割合コピー、注入日での記帳）は**沈黙して**これを破る
（ADR-0005の「沈黙する誤り」と同型 — 数字は正常に見える）。よって釘を刺す:

```text
不変条件: Split直後の子の(α,β)は、同じexperiencesからの
          rebuild結果と一致しなければならない
```

SplitのReplayとtomobit rebuildは同一コードパスであること。これが正しさの担保になる。

---

## Decision 4: 好みも同じ継承則 — 趣味にも遺伝はある

好みConnection（[ADR-0003](ADR-0003-outcome-and-preference.md)）のSplit時も、μ継承・固定質量を適用する。

ADR-0003の「好みの事前 = Beta(1,1)」は、
**親を持たずに生まれるConnectionの初期値**として残る。

能力と好みで継承則を分けると、
「好みBetaにもDecay/Confidence/Surprise/Splitが全適用」（ADR-0003）の対称性が壊れる。
趣味にもシナプスが生えるなら、趣味にも遺伝がある。

---

## Merge

逆方向に細工は要らない。
統合後のConnectionはReplayで再構築するだけ（Born with Historyと同じ機構）。
本ADRの継承則はSplit専用で閉じる。

---

## Consequences

- CONNECTION_ENGINEのOpen Question 2・3が閉じる。残るは半減期と二値属性のタイブレーク
- 残る実装ノブ: m₀（初期値2。「新入りの謙虚さ」の量）
- Decision Engineの読み取りが「最細一致を一つ」に確定し、実装が単純になる
- 減衰の帰着先が Beta(1,1) 固定ではなく「Connectionごとの事前」になる —
  One Ledgerの実装は事前(μ, m₀)をConnectionの属性として持つ必要がある
