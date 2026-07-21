# Connection Engine

## Purpose

Connection Engineは、
ExperienceからKnowledge Networkを育てる器官である。

責務は、

> **Connectionを生み、強め、弱め、分化させ、畳み、退かせること**

である。

Knowledge Networkは状態であり、
Connection Engineはそれを変化させる器官である。

判断はしない。

学習実行もしない。

Connectionだけを見る。

---

# Philosophy

Connectionはシナプスである。

ただし、単なる重みではない。

そのConnectionがどれだけ信頼できるか、
その知識は今も有効か、
どの文脈で成立するのか。

「生きた属性」を持つオブジェクトである。

しかし——

**帳簿は一つでなければならない。**

生きた属性を独立に保存すれば、
属性同士は必ず矛盾し、
Connectionは多重帳簿の中で腐る。

だからTomobitのConnectionは、

> **一つの実体から、すべての属性を導出する。**

---

# The Substance

Connectionの実体は、

> **時間減衰するBeta事後分布 Beta(α, β)**

ただ一つである。

```text
Success を観測 → α += 1
Failure を観測 → β += 1
時間経過      → α, β を事前分布へ向けて減衰
```

これだけがConnectionの「変化」である。

---

# Derived Views

あらゆる属性は保存しない。

Beta(α, β) から導出する。

```text
Strength      = α / (α + β)            事後平均
Confidence    = f(α + β)               集中度（分散の逆）
EvidenceCount = α + β − prior          証拠量
Freshness     = 減衰そのもの            時間経過で分散が自然に開く
Novelty       = EvidenceCount の低さ
Stability     = 直近更新群での Strength 変動の小ささ
ConflictScore = 直近証拠と過去証拠の食い違い
```

例

```text
1件の成功のみ
Beta(2, 1)
Strength   0.67
Confidence 低い
```

```text
200件、8割成功
Strength   0.80
Confidence 高い
```

「Strength 0.9 でも Experience 1件なら信用できない」は、
ノブではなく、分布の性質として自動的に成立する。

---

# Decay

知識は腐る。

減衰は α, β を事前分布へ戻す。

```text
古い証拠 → 擬似カウントが薄れる
        → 分散が開く
        → Confidence が下がる
        → Freshness の低下として観測される
```

Strengthを下げるのではない。

**確信を下げる。**

Rust 2024で強かったConnectionは、
Rust 2027では「弱い」のではなく「もう確かめていない」状態になる。

---

# Conflict is Non-Stationarity

Conflictとは、対立ではない。

```text
同じContextで
Claude 0.82
Codex  0.80
```

これはConflictではない。

「どちらも良い」であり、
選ぶのはDecision Engineの仕事である。

本当のConflictは、

```text
同じContextで
昔は Claude が勝ち
最近は Codex が連勝
```

**世界が変わったこと（非定常性）**である。

だから ConflictScore は、
Connectionを消す理由にはならない。

> **そのConnectionの減衰を速めるトリガーになる。**

直近と過去が食い違うほど、
記憶の窓は短くなり、
新しい世界に速く追従する。

ConflictとDecayは、同じ器官の別の顔である。

---

# Granularity

ContextとProviderの間には、
複数の粒度のConnectionが同時に存在する。

```text
粒度 粗
Rust ────────────────→ Claude
        証拠多い / Confidence高い / 広い直感

粒度 細
(Rust, Lifetime) ────→ Codex
        証拠少ない / 専門知
```

Decision Engineは階層バックオフで参照する。

```text
細かいConnectionに十分なConfidenceがある
        → 細かい方を信じる

足りない
        → 粗い方へ戻る
```

フルContextでエッジを張ってはならない。

同じContextは二度と来ない。

証拠は永遠に1件のままになり、
Confidenceは育たず、
グラフは一件一葉の墓標で埋まる。

---

# Born — 粗から生まれ、痛みで分化する

Connectionは全粒度で一斉に生まれない。

最初に生まれるのは粗い粒度だけである。

```text
初めてのExperience
Rust / Claude / Success

Born:
Rust → Claude
```

細かいConnectionは、

> **粗いConnectionが予測を外し続けた時にだけ生まれる。**

これをSplitと呼ぶ。

```text
Rust → Claude は成功するはずだった

しかし
Lifetime が絡む時だけ失敗が続く

予測誤差が Lifetime と相関する

Split:
(Rust, Lifetime) → Claude を Born
```

新しいシナプスは、
食い違いの痛みからしか生まれない。

---

# Surprise

Splitの燃料は予測誤差である。

ただし、外れた回数ではない。

> **Surprise ＝ 驚き − 迷い（超過surprisal）**

```text
s_excess = −log P(y | 予測) − H(予測)
```

較正されたConnectionでは期待値ゼロ。

成功が続けば沈み、
失敗が続けば浮上する。

迷っているConnectionは、
外れても驚かない。

確信しているConnectionが外れた時だけ、
深く驚く。

観測のたび、台帳に記す。

```text
{ connection, experience_id, Context属性一式, y, p̂, s_excess }
```

台帳はExperienceログから再計算可能な
**導出インデックス**である。

One Ledgerは無傷である。

台帳が正に浮上したら（+2 nats）、
そのConnectionはQuestionedとなり、
Curiosity Queueに載る。

```text
Curiosity Engine   台帳の浮上に「気付く」
Connection Engine  審判を「実行する」
```

Curiosityは気付き、Connectionは変わる。

---

# The Judgment

割るかどうかは、対数ベイズファクターが決める。

```text
H0  このConnectionは一枚岩
H1  属性aの有無で世界が違う

ln BF = ln P(D|H1) − ln P(D|H0)
        （Beta-Binomial閉形式 / 減衰済み実効カウント）

属性をm個調べたら割り引く

判定値 = ln BF − ln m
```

閾値はヒステリシスを持つ。

```text
判定値 ≥ +3   Split（履歴つき誕生）
判定値 ≤  0   Merge（親へ畳む）
中間帯        現状維持
```

**形成には強い刺激が要るが、維持は安い。**

シナプス可塑性の非対称性と同型である。

誤って割っても、
Born with HistoryとMergeが安く畳み直す。

だから審判は、やや前のめりでよい。

詳細は [ADR-0002](../decisions/ADR-0002-surprise-and-split-judgment.md)。

---

# Born with History

Experienceは不変であり、
Knowledgeは再生成可能である。

だからSplitで生まれるConnectionは、

> **証拠1件の赤ん坊として生まれる必要がない。**

過去のExperienceを再走査（Replay）し、
生まれた瞬間に過去の証拠をすべて注入する。

```text
Split発火

過去のExperienceをReplay

(Rust, Lifetime) → Claude   α=1, β=3
(Rust, Lifetime) → Codex    α=2, β=1

履歴を持って生まれる
```

Replayは過去を**元の日付のまま**数え直す。
注入した日に若返らせてはならない。

事前は親の事後平均だけを、固定質量にスケールして継ぐ。

平均だけ継ぎ、確信は継がない。
質量は証拠が運ぶ。

判断は最細一致のConnectionを一つだけ読む。
粗の知識は誕生時に事前として流れ済みであり、
クエリ時のブレンドはしない。

詳細は [ADR-0013](../decisions/ADR-0013-prior-inheritance-mean-only.md)。

気付くのが遅れても、
気付いた瞬間に過去全体が再解釈される。

---

# Merge

Splitの逆向きも、同じ判定である。

```text
子Connectionが
親Connectionと食い違わなくなった

→ 子を親へ畳む
→ 子は Retired
```

入場審査に落ちたものは退場する。

判定器は一つ。

> **細は、粗と統計的に食い違う時だけ存在を許される。**

---

# Capability and Preference

Connectionには二種類ある。

```text
能力のConnection
  (Rust) → Claude           Beta(α, β)
  テスト結果・👍👎・暗黙シグナルが記帳される

好みのConnection
  (Rust) : Claude vs Codex  Beta(α, β)
  Tomoの質問への回答が記帳される
```

「負けた」と「できなかった」は別の事実である。

選好を能力に記帳すると、
有能な次点が失敗者として沈む。

だから帳簿を分ける。

One Ledgerは「一つの事実に一つの帳簿」であり、
能力と好みは直交する別の事実である。

Decision Engineは、

> **能力で足切りし、好みで順位を付ける。**

好みのConnectionも実体は減衰Betaひとつ。

Decay / Confidence / Surprise / Split のすべてが
同じように働く。

**趣味にもシナプスが生える。**

詳細は [ADR-0003](../decisions/ADR-0003-outcome-and-preference.md)。

---

# Lifecycle is a View

Connectionのライフサイクルは状態として保存しない。

導出する。

```text
Born         Split直後 / 初回観測直後
Grow         EvidenceCount 増加中
Stable       Confidence高 / Stability高
Questioned   元Stable かつ Freshness低下 or Surprise発生
Dormant      長期間、対象Contextが観測されない
Revived      Dormant後に証拠が再流入
Retired      Mergeで畳まれた / 減衰しきった（※Mergeは反証の証拠で起きる。
             減衰だけではMerge帯に届かない — ADR-0037「実測による訂正」）
```

Curiosity Engineはフラグを立てない。

**Questionedに該当するConnectionをクエリする。**

生きている感触は保ちながら、
二重帳簿は作らない。

---

# Operations

Connection Engineの操作は、これだけである。

```text
Create      Split（履歴つき誕生）
Strengthen  α += 1
Weaken      β += 1
Decay       事前分布への減衰（Conflictで加速）
Merge       子を親へ畳む
Retire      減衰しきったものを退かせる
```

Ruleは絶対に作らない。

作るのは

```text
(Rust, Lifetime) → Codex
Beta(α, β)
```

だけである。

RuleはDecision Engineがその場で生成する。

---

# Design Principles

## One Ledger

実体はBeta(α, β)一つ。

すべての属性は導出する。

保存された属性は、いずれ嘘をつく。

---

## Admission by Evidence

Connectionの存在自体が主張である。

グラフにあるエッジはすべて、

「粗い知識では説明できなかった」

という統計的な発見である。

---

## Born with History

新しいConnectionは、
過去を継いで生まれる。

ExperienceのReplayがこれを保証する。

---

## Same Test Both Ways

Splitの判定とMergeの判定は同じものである。

入口の審査と出口の審査に、
別の基準を持ってはならない。

---

## Curiosity Notices, Connection Acts

気付くのはCuriosity Engine。

変えるのはConnection Engine。

---

# Open Questions

次に詰めるべき未解決点。

```text
1. 減衰の半減期
   基本値と、Conflictによる加速率

2. 二値属性のSplitタイブレーク
   属性が2値しかないとき ln BF は with/without 対称で厳密に同値になり、
   辞書順の勝者（成功側かもしれない）だけが子としてBornする。
   発見自体は粒度1のConnectionが保持するため実害は交互作用の子に限られるが、
   「敗者側の子も対でBornすべきか」は誕生の思想（ADR-0001）と摩擦があり、
   dogfoodで実害を観測してから決める
```

解決済み

```text
Surpriseの定義      → 超過surprisal（本文 Surprise節 / ADR-0002）
Splitの有意判定     → 補正付き ln BF とヒステリシス（本文 The Judgment節 / ADR-0002）
Outcomeの質         → 三層信号＋能力/好みの二重Connection＋Tomoの質問（ADR-0003）
```

---

# Guiding Principle

Connectionは機械的に量産されない。

食い違いという痛みから生まれ、

過去を継いで生まれ、

証拠によって存在を許され、

世界が変われば速く忘れ、

説明が要らなくなれば静かに畳まれる。

グラフに残るすべてのエッジが、

**Tomobitが世界から学び取った発見である。**
