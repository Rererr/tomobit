# Execution Model

## Purpose

Tomobitは、特定のAIを実行するシステムではない。

Tomobitは、

> **「何を達成したいか」を宣言し、「誰が実現するか」を経験から学習するシステム**

である。

AIは交換可能であり、経験こそがTomobit最大の資産となる。

この文書は「実行」側のレイヤー構造を定義する。
学習側（Reality以降の認知フロー）は
[COGNITIVE_ARCHITECTURE.md](COGNITIVE_ARCHITECTURE.md) が定義する。

---

# レイヤー構造

```text
User Request
      │
      ▼
Intent
      │
      ▼
Plan
      │
      ▼
Capability
      │
      ▼
Decision Engine ──── Knowledge Network を参照
      │
      ▼
Provider
      │
      ▼
Executor
      │
      ▼
Runtime
```

各レイヤーは単一責務を持ち、下位レイヤーの実装詳細を意識しない。

---

# Intent

Intentは

> **ユーザーが最終的に達成したい目的**

を表す。

例

- Fix Bug
- Implement Feature
- Improve Performance
- Review Code
- Analyze Repository

IntentはAIやWorkflowに依存しない。

---

# Plan

Planは

> **Intentを達成するための実行計画**

である。

例

```text
Fix Bug
  ↓
Analyze
  ↓
Implement
  ↓
Test
  ↓
Review
```

Planは固定ではない。

Planの改善もExperienceから学習される。

PlanはConnectionのもう一つの賭け先である。

```text
(context) ──台帳──▶ Plan       どの手順が効くか
(context) ──台帳──▶ Provider   どの相棒が効くか
```

選択・減衰・Split・事前継承は、
Providerと同じ機構が全て適用される。

新しいPlan変種はCuriosityが提案する。
変異は純関数、採否は数式、誕生は継承。

LLMはPlanを生成しない。

詳細は [ADR-0014](../decisions/ADR-0014-plan-learning-same-ledger.md)。

Tomobitにおける「成長」とは、Planの改善でもある。

---

# Capability

Capabilityは

> **Planを構成する最小の能力単位**

である。

Capabilityは「誰が実行するか」を持たない。

例

- analyze
- implement
- review
- refactor
- summarize
- test
- benchmark
- commit
- deploy
- notify

WorkflowはCapabilityのみを定義する。

```yaml
plan:
  - capability: analyze
  - capability: implement
  - capability: test
  - capability: review
```

---

# Decision Engine

Capabilityと「誰がやるか」を結ぶのはDecision Engineである。

Knowledge Network（Connectionの集合）を参照し、
現在の状況を考慮してStrategyを生成する。

判断材料

- Knowledge Network
- Current Context
- Cost
- Execution Time
- User Preference
- Provider Availability
- Current Load

Decision Engineは学習しない。

Connectionを読むだけである。

（Strategyの性質は [KNOWLEDGE_EVOLUTION.md](KNOWLEDGE_EVOLUTION.md)）

---

# Provider

Providerは

> **Capabilityを提供する実行手段**

である。

例

```text
implement
  ↓
Claude Code
Codex
Gemini
```

Providerは実行可能な候補を返す。

---

# Executor

ExecutorはProviderを利用してCapabilityを実行する。

例

- LLM Executor
- Shell Executor
- Git Executor
- Docker Executor
- MCP Executor
- Human Executor

ExecutorはRuntimeの違いを吸収する。

Executorの選択・Queue管理はExecutor Managerが担う。

---

# Runtime

Runtimeは実際の実行環境である。

例

- Claude Code CLI
- Codex CLI
- Ollama
- Docker
- Shell
- Git
- Browser
- Remote API

TomobitはRuntimeを直接意識しない。

---

# 実行と学習の接続

実行の終わりは、学習の始まりである。

```text
Executor が Reality を生成
      │
      ▼
Perception Engine → Observation
      │
      ▼
Experience Engine → Experience
      │
      ▼
Connection Engine → Knowledge Network
      │
      ▼
次の Decision Engine が、より賢く選ぶ
```

この循環こそがTomobitの「成長」である。

AIは成長しない。

**Tomobitが経験から成長する。**
