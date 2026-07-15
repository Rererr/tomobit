# ADR-0012: 決定則 = Thompson Sampling — 探索は好みの側で、ミスは構造になる

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0001](ADR-0001-connection-granularity.md), [ADR-0002](ADR-0002-surprise-and-split-judgment.md), [ADR-0003](ADR-0003-outcome-and-preference.md), [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md), [CONNECTION_ENGINE.md](../core/CONNECTION_ENGINE.md)

---

## Context

[ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md)で判断は純関数と決めたが、
**どの純関数か**は範囲外として残した。

決定則に求める条件:

- One Ledger（減衰Beta）をそのまま食べる。判断のための追加状態を持たない
- 「Exploration before Confidence」と「Curiosity Never Blocks Production」を両立する
- seedを固定すれば決定的 — リプレイ・監査できる

加えて、使用者視点の前提（本人の言明）:

> 理想は常に完璧な相棒だが、それが不可能なことは意識無意識問わず理解している。
> だから「育てていきたい」「オーダーメイドであれ」と願う。
> つまり前提にあるのは**「一度したミスは繰り返してほしくない」**である。

---

## Decision 1: 決定則 = Thompson Sampling

候補Providerそれぞれについて、該当ConnectionのBeta事後から1本サンプルし、
最大値を引いた候補を選ぶ。

- 追加の状態ゼロ、追加のノブほぼゼロ。事後分布がそのまま決定則の入力
- 探索は不確実性に正比例し、証拠が積もれば自動で縮む
- 減衰が事後を太らせ続けるため、非定常環境で探索が自然死しない。
  Conflict＝減衰加速（ADR-0001）が、そのまま探索再開のトリガーになる

---

## Decision 2: サンプルは好みの側だけ — 能力ゲートは悲観分位点

二重Connection（[ADR-0003](ADR-0003-outcome-and-preference.md)）への統合は非対称にする。

```text
能力ゲート     事後の下側分位点（初期値: 下側20%点）で決定的に足切り
好み順位付け   ゲート通過者に対してThompson Sampling
```

「できないかもしれない」の探索を本番のタスクで張ることは、
Curiosity Never Blocks Production の精神に反する。
能力側の探索は、Curiosityの能動実験とTomoの質問（ADR-0007）へ送る。

---

## Decision 3: 名誉回復は減衰だけで行う

ゲートに落ちたProviderのための専用の復帰機構は作らない。

減衰で事後が太る → 分位点が上がる → 自然に再入場する。

忘却が寛容を生む。機構はすでにある。

---

## Decision 4: 高stakesの温度 = サンプル数 n

n本drawし、平均が最大の候補を選ぶ。

```text
n = 1    純粋なThompson Sampling（探索最大）
n → ∞   事後平均のgreedy（探索ゼロ）
```

ノブは一つ、意味は「何回想像してから決めるか」。
nはContext属性（size等）からの純関数で決める — ここもJudgment by Math。
v1は固定表で十分。

---

## Decision 5: seedはeventsに記帳する

決定ごとにseedを記録する。同じ台帳＋同じseed → 同じ判断。

確率的決定則でも純関数性と監査可能性は失われない
（ADR-0011の伏線の回収）。

---

## 原則の調停

「Exploration before Confidence」と「Curiosity Never Blocks Production」は
衝突していなかった。探索には二つのチャネルがある。

```text
受動探索   Thompson Sampling。本番の判断に内在し、仕事を止めない。
           コストは「最善でないかもしれない候補に1タスク張る」ことだけで、
           その確率は不確実性に比例して自動で縮む
能動探索   Curiosity。アイドル時の実験という特権
```

Exploration before Confidence はTSの性質が無料で実装し、
Curiosity Never Blocks Production は能動探索にだけ効く。

---

## 「一度したミスは繰り返してほしくない」への回答

この前提は、新しい機構ではなく既存の三つの連携で満たす。

1. **減衰＝新近性** — 直近の失敗は古い成功より重い。
   証拠が薄いうちの1敗は悲観分位点を大きく下げ、ゲートが即座に閉まる
2. **👎の強い上書き**（ADR-0003） — 明言されたミスは一度で重く刻まれる
3. **Surprise→Split**（ADR-0002） — 無言のミスが同じContextで繰り返されれば
   台帳が浮上させ、Splitがその文脈を構造として切り出す。
   以後その文脈でだけ外科的に回避され、他の文脈の信頼は守られる

定式化すると:

> **明言されたミスは一度で、無言のミスは数回で、構造に変わる。**

無言の1敗での永久回避は統計的に不可能（ノイズと信号を区別できない）だが、
それを補うのが👎とTomoの質問という人間チャネルである。
「オーダーメイド」とは、グラフが使用者の痛みの場所でだけ細かくなることを指す。

---

## Consequences

- Decision Engineが実装可能な解像度に到達。残る実装ノブ: 分位点q、n(stakes)の固定表
- CONNECTION_ENGINEのOpen Question 3（backoffブレンド）は本ADRでは閉じない —
  粗と細のどのConnectionからサンプルするかの問題として残り、事前分布の継承（OQ2）と
  同時に扱う
- 将来候補（宣言のみ、v1 descope）: `retry` をContext語彙に追加すれば
  「誰の失敗を誰が拾えるか」（救援Connection）が学習可能になる。
  R2のreview同様、追加時はextractor_ver +1
