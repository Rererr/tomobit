# Cognitive Model

## Philosophy

Tomobitは知識ベースではない。

Tomobitは、

> **経験によって Connection を育てる認知システム**

である。

この思想は、人間の脳におけるシナプスから着想を得ている。

ただし、Tomobitは脳を模倣することを目的としない。

重要なのは、

> **経験を保存することではなく、経験同士の結び付きが変化し続けること**

である。

Tomobitが成長するとは、

新しいRuleを覚えることではない。

Connectionが強まり、
弱まり、
再構成されることで、

より自然な意思決定ができるようになることである。

---

# Synaptic Thinking

TomobitではExperienceを知識へ変換しない。

ExperienceはKnowledge Networkへ刺激を与える。

```text
Experience

↓

Connection Update

↓

Knowledge Network

↓

Strategy Generation
```

Experienceは消えない。

Connectionだけが変化し続ける。

このConnectionの変化が、
Tomobitにおける「学習」である。

---

# Why Connection

Ruleは固定された知識である。

Connectionは関係性である。

例えば

```text
Go

↓

Goroutine

↓

Data Race

↓

Claude Code

↓

Success
```

Tomobitは

「GoならClaude」

というRuleを保存するのではなく、

このContextとProviderのConnectionを保持する。

Connectionは以下の情報を持つ。

- Strength
- Confidence
- Frequency
- Recency
- Evidence

Experienceが増えるほど、
Connectionは継続的に変化する。

---

# Knowledge Network

KnowledgeはRuleの集合ではない。

KnowledgeはConnection Networkとして保持される。

ノードは語彙のトークンとProvider、
辺は生きたBeta台帳を持つConnectionである。

```text
(go) ──────────────────────── Claude   ████████░░
(go) ──────────────────────── Codex    ███░░░░░░░

              │
              │ Split — 意味のある差の発見
              ▼

(go, topic=concurrency) ───── Claude   ██░░░░░░░░
(go, topic=concurrency) ───── Codex    ███████░░░
```

粗い島ではClaudeが勝ち、
並行処理の島ではCodexが逆転する。

この逆転を発見し、
島を切り出すのがSplitである。

トークン同士を直接結ぶ辺は存在しない。

GoとGoroutineは、
同じExperienceの中で出会う。

その出会いに意味があるとき、
Splitが新しい島として切り出す。

KnowledgeはConnection全体の状態として存在する。

---

# Episodic Memory

類似Experienceの想起は、
器官としてはまだ存在しない。

しかしexperiencesという真実が
追記専用で残り続ける限り、

「似た経験を思い出す」は、
いつでも後から導出できる射影である。

権利は保全されている。

約束はまだしない。

---

# Strategy Generation

Strategyは保存しない。

毎回生成する。

生成には二つの座席があり、
座る者が異なる。

```text
タスク記述

        ↓

LLM（意味付け）

        ↓

Context属性トークン

        +

Knowledge Network

        ↓

純関数（判断）

        ↓

Strategy（Provider決定）
```

LLMが担うのは意味付けまでである。

タスク記述を、
Experienceと同じ語彙のContext属性トークンへ写像する。

そこから先は数式である。

同じConnectionと同じContextからは、
同じ判断が導かれる。

StrategyはKnowledgeではない。

Knowledgeから導出される
**現在最適な判断**である。

---

# Role of the Local LLM

軽量ローカルLLMは
判断するために存在するのではない。

Tomobitでは

> **Realityとタスク記述を、共有語彙へ写像する知覚器官（extractor）**

として機能する。

LLMの座席はPerception / Experience側にのみある。

判断の側に、
交換不能なAIは存在しない。

アイドル状態のLLMの仕事も、
判断を練ることではない。

> **過去をより良く知覚し直すこと**である。

- extractorの改版（extractor_ver +1）
- 蓄積されたRealityの再知覚
- Experienceの再生成

Tomobitは停止中も成長し続ける。

ただし磨かれるのは判断のアルゴリズムではなく、
判断が読むExperienceの品質である。

---

# Convergence

Knowledgeは最初から整理されない。

初期状態では

- Connectionは疎である
- 矛盾も存在する
- Weightも安定しない

Experienceを重ねることで

- 強いConnectionは強くなる
- 弱いConnectionは自然に薄れる
- 新しいConnectionが形成される

Knowledgeは設計者が整理するものではない。

> **Experienceによって自然に収束していくもの**である。

---

# Design Principles

## Experience is Permanent

Experienceは事実であり、
Knowledgeの源泉である。

削除ではなく蓄積を基本とする。

---

## Connection is the Knowledge

Tomobitが保持するのはRuleではない。

Connectionである。

Ruleは必要に応じてConnectionから導出できる。

---

## Strategy is Ephemeral

Strategyは保存しない。

Knowledge Networkから、
その瞬間ごとに生成される。

---

## Meaning by Model, Judgment by Math

意味付けはLLM、判断は数式。

LLMは世界とタスクを語彙へ写像する。

Providerを決めるのは、
Connectionの統計に対する純関数である。

判断の側に交換不能なAIが存在しないから、

成長は検証でき、
判断は監査でき、
AIは交換できる。

---

## Continuous Evolution

Knowledgeに完成形はない。

ConnectionはExperienceによって
生涯変化し続ける。

---

## Living Harness

TomobitはAIを管理するツールではない。

経験を通じてConnectionを育て、

そのConnectionから判断を生み出し続ける

**Living Harness**

である。

---

# Guiding Principle

実装がどれほど変化しても、
この思想だけは変えてはならない。

Tomobitの価値は、

AIでも、

モデルでも、

アルゴリズムでもない。

**Connectionが育ち続けること。**

それがTomobitの「成長」である。