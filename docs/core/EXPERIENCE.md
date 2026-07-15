# EXPERIENCE.md

# Experience Model

## 目的

Tomobitにおける「経験」を定義する。

Experienceは単なるログではない。

> **将来の意思決定に利用できる知識へ変換するための素材**

である。

TomobitはExperienceを蓄積し、
Connectionを育て、
Strategyを改善することで成長する。

---

# 基本思想

TomobitはAIを学習させるシステムではない。

Tomobitが学習する対象は

- Plan
- Capability
- Provider
- Strategy

である。

Experienceはそのための一次情報となる。

---

# Experience Pipeline

```text
Event
    │
    ▼
Session
    │
    ▼
Experience
    │
    ▼
Connection
    │
    ▼
Strategy
```

各レイヤーは責務を持つ。

| Layer | Responsibility |
|--------|----------------|
| Event | 実行中に発生した生ログ |
| Session | 1回の実行全体 |
| Experience | Sessionの要約 |
| Connection | Experienceから育つ知識（Beta事後分布） |
| Strategy | 次回以降の意思決定 |

---

# Event

EventはTomobit内部で発生する最小単位の出来事である。

例

- Task Started
- Plan Generated
- Capability Started
- Provider Selected
- Provider Finished
- Test Passed
- Test Failed
- Retry
- User Approved

Eventは変更不可のログとして保存される。

---

# Session

Sessionは

> **1回のRequest全体**

を表す。

Sessionには複数のEventが含まれる。

```text
Session

├── Event
├── Event
├── Event
└── Event
```

Sessionは履歴であり、
まだ知識ではない。

---

# Experience

Experienceは

> **Sessionを学習可能な単位へ要約したもの**

である。

例

```text
Context

Rust
Axum

↓

Intent

Fix Bug

↓

Plan

Analyze
Implement
Test

↓

Capability

Implement

↓

Provider

Claude Code

↓

Outcome

Success
```

ExperienceはConnection Engineに読まれ、
Connectionを育てる。

---

# Context

Experienceには必ずContextを含める。

Context例

- Language
- Framework
- Repository
- Project Type
- Complexity
- Changed Files
- File Count
- Dependency Size
- Runtime
- Platform

ContextはConnectionの粒度を決める重要な要素となる。

---

# Outcome

Experienceは必ず結果を持つ。

Outcomeは三層の信号から構成される。

```text
第1層  Objective（自動収穫・毎回）
       Test Result / Compile / そのまま採用 / 手直し量 / 後日Revert

第2層  Explicit Verdict（任意）
       👍 / 👎 — 強い上書き

第3層  Preference（Tomoの質問への回答）
       「どっちが好みだった?」 → 好みのConnectionへ
```

第1層が毎回。
第2層は気が向いた時だけ。
第3層は聞かれた時だけ。

**判定疲れを人間に負わせない。**

加えて、

- Execution Time
- Cost
- Retry Count
- Human Intervention

なども保持する。

（[ADR-0003](../decisions/ADR-0003-outcome-and-preference.md)）

---

# Connection

Connectionは

> **複数のExperienceから育った知識**

である。

かつてこの層はPatternと呼ばれていた。

統計（Success Rate / Average Time）は
Connectionの導出ビューに吸収された。

ConnectionはExperienceを直接変更しない。

ConnectionはExperienceから継続的に再生成できる。

詳細は
[CONNECTION_ENGINE.md](CONNECTION_ENGINE.md) /
[KNOWLEDGE_EVOLUTION.md](KNOWLEDGE_EVOLUTION.md)。

---

# Strategy

Strategyは

Knowledge Network（Connectionの集合）を利用して
Tomobitが次回どのように意思決定するかを表す。

例

```text
Intent

Fix Bug

↓

Plan

Analyze

↓

Implement

↓

Test
```

または

```text
Capability

Implement

↓

Preferred Provider

Claude Code
```

StrategyはExperienceによって継続的に改善される。

---

# Learning Loop

Tomobitは以下の循環で成長する。

```text
Request

↓

Intent

↓

Plan

↓

Execution

↓

Event

↓

Session

↓

Experience

↓

Connection

↓

Strategy

↓

Next Request
```

---

# Design Principles

## Immutable Events

Eventは変更しない。

---

## Rebuildable Knowledge

Connection・Strategyは
Experienceから再生成可能である。

---

## AI Agnostic

ExperienceはAI名に依存しない。

Providerは交換可能である。

---

## Context First

ContextなしのExperienceは
十分な知識を持たない。

ContextはExperienceと同等に重要である。

---

## Experience is the Asset

Tomobit最大の資産は

- Claude Code
- Codex
- Ollama

ではない。

> **Experienceである。**