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

既存Connectionが予測を外すたび、
その時のContext属性を添えて記録する。

```text
予測誤差が特定の属性と相関し始める

→ Split候補
```

役割分担

```text
Curiosity Engine   Split候補に「気付く」
Connection Engine  Splitを「実行する」
```

Curiosityは気付き、Connectionは変わる。

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
Retired      Mergeで畳まれた / 減衰しきった
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
1. Surpriseの定義
   予測誤差をどう測るか（対数損失 / 単純な外れ回数 / 分布距離）

2. Splitの有意判定
   「食い違い」の閾値（尤度比検定 / ベイズファクター / 実効サンプル数の下限）

3. 減衰の半減期
   基本値と、Conflictによる加速率

4. 事前分布
   Beta(1,1)か、親Connectionの事後を子の事前に継承するか

5. バックオフのブレンド
   粗と細のConfidenceをどう混ぜるか（hard switch / 重み付き平均）

6. Outcomeの質
   Success/Failの判定信号（テスト結果＋人間の一押し）
   生きたグラフほど、報酬信号の質がクリティカルになる
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
