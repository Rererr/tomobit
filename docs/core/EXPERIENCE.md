# EXPERIENCE.md

# Experience Model

## 目的

Tomobitにおける「経験」を定義する。

Experienceは単なるログではない。

> **将来の意思決定に利用できる知識へ変換するための素材**

である。

TomobitはExperienceを蓄積し、
Patternを抽出し、
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
Pattern
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
| Pattern | Experienceの統計・傾向 |
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

Experienceは
後続のPattern抽出に利用される。

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

ContextはPattern抽出の重要な要素となる。

---

# Outcome

Experienceは必ず結果を持つ。

例

- Success
- Failed
- Cancelled
- Partial Success

加えて、

- Execution Time
- Cost
- Retry Count
- Human Intervention
- Test Result

なども保持する。

---

# Pattern

Patternは

> **Experienceから抽出された傾向**

である。

例

```text
Rust

Fix Bug

Claude Code

Success Rate

96%
```

または

```text
React

Review

Codex

Average Time

18 sec
```

PatternはExperienceを直接変更しない。

Patternは継続的に再生成できる。

---

# Strategy

Strategyは

Patternを利用して
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

Pattern

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

Pattern・Strategyは
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