# Knowledge Evolution

## Purpose

Tomobitは、単にExperienceを蓄積するだけではない。

Experienceは継続的に分析・抽象化され、
最終的に意思決定へ利用できる知識へ進化する。

この文書は、その進化の道筋を定義する。

```text
Reality
│
│ 実際に起きた出来事
▼
Observation
│
│ 知覚された事実
▼
Experience
│
│ 意味のある体験（不変）
▼
Connection
│
│ 育つ知識（Beta事後分布）
▼
Strategy
│
│ 現在の状況に応じた意思決定（毎回生成）
▼
Execution
```

各段階の変換は、それぞれの器官が担う。

```text
Reality     → Observation   Perception Engine
Observation → Experience    Experience Engine
Experience  → Connection    Connection Engine
Connection  → Strategy      Decision Engine
```

---

# Reality

Realityは実際に起きた出来事である。

まだ知識ではない。

例

- Code Generated
- Test Failed
- Retry
- User Approved

Realityは変更されない。

---

# Observation

ObservationはPerception Engineが知覚した事実である。

正規化され、集約されているが、
まだ意味を持たない。

---

# Experience

Experienceは

> Observationから抽出された意味のある体験

である。

Experienceには以下が含まれる。

- Context
- Intent
- Plan
- Capability
- Provider
- Outcome

Experienceは不変であり、
Knowledge Evolutionの唯一の入力となる。

---

# Connection

Connectionは

> 複数のExperienceから育った知識

である。

実体は時間減衰するBeta事後分布ひとつ。
詳細は [CONNECTION_ENGINE.md](CONNECTION_ENGINE.md)。

かつてこの層は
Pattern / Hypothesis / Rule の三段で考えられていた。

現在は、すべてConnectionに吸収されている。

```text
Pattern     = Connectionの統計そのもの
              （Strength / EvidenceCount は導出ビュー）

Hypothesis  = Confidenceが低いConnection
              （Curiosity Engineが検証候補として拾う）

Rule        = 保存しない
              （Decision Engineがその場で生成する）
```

「Rustの実装ではClaudeを第一候補とする」
という文は、どこにも保存されていない。

保存されているのは

```text
(Rust, implement) → Claude   Beta(α, β)
```

だけであり、
文はStrategyとして毎回生まれ、毎回捨てられる。

（この決定の経緯は [ADR-0001](../decisions/ADR-0001-connection-granularity.md)）

---

# Strategy

Strategyは

> Connectionを現在の状況へ適用した意思決定

である。

Strategyは以下を考慮する。

- Knowledge Network（Connectionの集合）
- Current Context
- Current Load
- Provider Availability
- User Preference
- Cost
- Execution Time

強いConnectionが存在しても、
現在の状況によって異なるStrategyを選択できる。

例

```text
Knowledge:  (Rust) → Claude が強い
現在:       Claude unavailable
Strategy:   Codexを利用
```

StrategyはKnowledgeではなく、
毎回生成されるDecisionである。

---

# Design Principles

## Experience is Immutable

Experienceは変更しない。

この不変性が、Splitの「履歴つき誕生」を支える。

---

## Knowledge is Rebuildable

ConnectionはExperienceから再生成できる。

Knowledgeが壊れても、Experienceがあれば育て直せる。

---

## Strategy is Ephemeral

Strategyは永続化された知識ではない。

現在の状況に応じて毎回生成される。

---

## Exploration before Confidence

Tomobitは未知のProviderも適切な割合で試行する。

仮説（Confidenceの低いConnection）を検証し続けることで、
Knowledgeは継続的に進化する。

---

## Experience is the Asset

Tomobit最大の資産はAIではない。

Claude Codeでも、
Codexでも、
Ollamaでもない。

**Experienceから育ったKnowledgeこそがTomobitの資産である。**
