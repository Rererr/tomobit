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

会話はSessionの単位ではない。**会話は入力の器で、記帳の単位はタスク**である
（[ADR-0022](../decisions/ADR-0022-chat-session.md)）。1つの対話の中で区切るたびに
Sessionが閉じ、次が始まる。

Sessionは木になる。Providerがタスクを分ければ子Sessionが生まれ、
子は親の作業場と親が選んだ相手を継ぐ
（[ADR-0023](../decisions/ADR-0023-task-split.md) / [ADR-0054](../decisions/ADR-0054-a-child-is-the-breakdown.md)）。

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

# Session と Experience は 1:1 ではない

Sessionが閉じても、Experienceになるとは限らない。

- **分割の子は経験にしない。** 子は親タスクの内訳であって、独立した発注ではない。
  決定はタスクにつき1回で、人は子を1つも見ていない。イベントは残るが、
  Connectionは動かない（[ADR-0054](../decisions/ADR-0054-a-child-is-the-breakdown.md)）
- **duel の子だけが例外。** A/B実走の2本は、人が結果を見て判定するので、
  それぞれが独立した発注として経験になる（[ADR-0026](../decisions/ADR-0026-ab-duel.md)）
- **起動できなかった実行も経験にしない。** 環境の不備をProviderの能力の証拠にしないため
  （[ADR-0043](../decisions/ADR-0043-auto-by-default.md)）
- **配線は経験ではない。** 働く場所・読み取り先・許可は台帳に書かない
  （[ADR-0047](../decisions/ADR-0047-workspace-is-wiring.md) / [ADR-0053](../decisions/ADR-0053-permission-is-asked.md)）

「片側だけの証拠」を作らないための規律である。失敗だけが `y=0` で乗る形にすると、
成功率95%のProviderが、一度も計測されていないProviderより下に並ぶ。

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

三層は当初カタログだけがあり、第1層と第2層は長く書き手を持たなかった。

- **第1層**: tomobit自身がテストを走らせて観測する。Providerに宣言はさせない
  （自分の成績表を書かせない）。走らせ方は config の「働く場所 → コマンド」で、
  書かなければ1バイトも動かない（[ADR-0052](../decisions/ADR-0052-first-layer-is-observed.md)）
- **第2層は「上書き」ではなく拒否権**である。機械が人より上に立つ場所は
  「赤テスト」ただ1つなので、第2層が要るのは**赤 × 「1=文句なし」**が衝突した1点に特定できる。
  そこでだけ1問増え、`tomobit verdict <sid> up|down|clear` が「まだ言えない」を受ける。
  観測は消さず、赤と判定が経験の中で共存する
  （[ADR-0055](../decisions/ADR-0055-verdict-is-a-veto.md)）

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