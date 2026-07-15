---

# Knowledge Evolution Model

## 基本思想

Tomobitは、単にExperienceを蓄積するだけではない。

Experienceは継続的に分析・抽象化され、
最終的に意思決定へ利用できる知識へ進化する。

この知識の進化プロセスを
**Knowledge Evolution Model** と呼ぶ。

```text
Event
│
│ 生ログ
▼
Session
│
│ 1回の実行
▼
Experience
│
│ 意味のある体験
▼
Pattern
│
│ 統計・傾向・相関
▼
Hypothesis
│
│ 仮説
▼
Rule
│
│ 検証済み知識
▼
Strategy
│
│ 現在の状況に応じた意思決定
▼
Execution
```

---

## Event

EventはTomobit内部で発生した最小単位の事実である。

例

- Capability Started
- Provider Selected
- Test Failed
- Retry
- User Approved

Eventは変更されない。

---

## Session

Sessionは一回のRequest全体を表す。

Sessionは複数のEventで構成される。

Sessionは履歴であり、
知識ではない。

---

## Experience

Experienceは

> Sessionから抽出された意味のある体験

である。

Experienceには以下が含まれる。

- Context
- Intent
- Plan
- Capability
- Provider
- Outcome

ExperienceはKnowledge Evolutionの入力となる。

---

## Pattern

Patternは

> 複数のExperienceから得られた傾向

である。

例

```text
Language : Rust
Capability : implement
Provider : Claude Code

Success Rate : 96%
Average Time : 42 sec
Average Cost : $0.03
```

Patternは統計情報であり、
推奨ではない。

---

## Hypothesis

Hypothesisは

> Patternから導かれた仮説

である。

例

```text
Rustの実装は
Claude Codeの成功率が高い可能性がある
```

Hypothesisは未検証である。

Tomobitは必要に応じて別Providerを試行し、
仮説を検証する。

---

## Rule

Ruleは

> 十分なEvidenceによって裏付けられた知識

である。

例

```text
Rustの実装では
Claude Codeを第一候補とする
```

RuleはPatternの一般化であり、
Experienceから再生成可能である。

Ruleは固定ではなく、
Evidenceの変化によって更新される。

---

## Strategy

Strategyは

> Ruleを現在の状況へ適用した意思決定

である。

Strategyは以下を考慮する。

- Rule
- Current Context
- Current Load
- Provider Availability
- User Preference
- Cost
- Execution Time

Ruleが存在しても、
現在の状況によって異なるStrategyを選択できる。

例

Rule

```text
RustではClaude Codeを優先
```

現在

```text
Claude Code unavailable
```

Strategy

```text
Codexを利用
```

StrategyはKnowledgeではなく、
毎回生成されるDecisionである。

---

# Design Principles

## Experience is Immutable

Experienceは変更しない。

---

## Knowledge is Rebuildable

Pattern・Hypothesis・Ruleは
Experienceから再生成できる。

---

## Strategy is Ephemeral

Strategyは永続化された知識ではない。

現在の状況に応じて毎回生成される。

---

## Exploration before Confidence

Tomobitは未知のProviderも適切な割合で試行する。

仮説を検証し続けることで、
Knowledgeは継続的に進化する。

---

## Experience is the Asset

Tomobit最大の資産はAIではない。

Claude Codeでも、
Codexでも、
Ollamaでもない。

**Experienceから育ったKnowledgeこそがTomobitの資産である。**