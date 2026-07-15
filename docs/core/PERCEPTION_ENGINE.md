# Perception Engine

## Purpose

Perception Engineは、
Tomobitが外界を知覚するためのコンポーネントである。

責務は、

> **現実世界で発生した出来事を、構造化された Observation として認識すること**

である。

意味付けは行わない。

学習もしない。

判断もしない。

Tomobitは、まず世界を正しく知覚する。

---

# Philosophy

Tomobitは最初から何も知らない。

世界で起きた出来事を観測し、
Observationとして記録することで、
初めてExperienceを形成できる。

つまり、

```text
Reality

↓

Perception

↓

Observation

↓

Experience

↓

Knowledge
```

Experienceの品質は、
Observationの品質によって決まる。

Observationの品質は、
Perceptionの品質によって決まる。

Perceptionは、
Tomobitの認知の入口である。

---

# Responsibilities

Perception Engineは以下のみを担当する。

- Eventの受信
- Eventの正規化
- Eventの時系列管理
- Eventの集約
- Observation生成
- Observation保存

以下は担当しない。

- Experience生成
- Knowledge更新
- Strategy生成
- 学習
- 推論
- 評価

---

# Reality

Realityとは、
実際に起きた出来事そのものである。

例

- Claudeがコードを生成した
- コンパイルが失敗した
- テストが成功した
- Humanが修正した
- Pull Requestがマージされた

Realityは保存されない。

PerceptionによってObservationへ変換される。

---

# Event

RealityはEventとしてPerception Engineへ送られる。

Eventは完全に機械的である。

例

```text
ExecutorStarted

ExecutorFinished

CompileStarted

CompileFailed

CompileSucceeded

TestsStarted

TestsPassed

TestsFailed

ReviewAccepted

ReviewRejected

HumanEdited

GitCommitted

PullRequestMerged
```

Eventは意味を持たない。

ただ起きた事実である。

---

# Observation

Observationとは、

**Context付きで整理された事実**

である。

Observationは
解釈を含まない。

例

```json
{
  "executor": "claude",
  "capability": "implement",
  "language": "rust",
  "framework": "axum",
  "elapsed_ms": 18230,
  "compile_retry": 2,
  "review_comment": 1,
  "human_edit_ratio": 0.03,
  "tests_passed": 18,
  "tests_failed": 0,
  "result": "success"
}
```

Observationは、
Experience Engineの入力となる。

---

# Observation Context

Observationには
必ずContextを含める。

Context例

- Capability
- Language
- Framework
- Project
- Task Type
- Executor
- Provider
- Model
- Human Review
- Repository
- Branch
- Environment

Contextを持たないObservationは、
Knowledge形成に利用しない。

---

# Observation Sources

Perception Engineは
複数のSourceからObservationを構築する。

例

```text
Executor

Git

GitHub

Filesystem

Compiler

Test Runner

CI

IDE

Browser

Human Feedback

Scheduler

Plugin
```

新しいSourceは
Pluginとして追加できる。

---

# Observation Lifecycle

```text
Reality

↓

Event

↓

Normalization

↓

Aggregation

↓

Observation

↓

Persistence

↓

Experience Engine
```

Perception Engineは
Observationまでを担当する。

Experience生成は別責務である。

---

# Persistence

Observationは永続化する。

ただし、

Knowledgeそのものではない。

Observationは、

Experienceを再生成するための一次情報である。

Experience Engineが改善された場合でも、

ObservationからExperienceを再構築できる。

Observationは
Replay可能であることを前提とする。

---

# Time

Observationは
必ず時系列情報を保持する。

例

- Timestamp
- Duration
- Sequence
- Parent Observation
- Correlation ID

時間情報は
Experience形成の重要な材料となる。

---

# Design Principles

## Reality First

推測ではなく、
Realityを観測する。

---

## Facts over Interpretation

Observationは
意味付けを行わない。

事実だけを保持する。

---

## Context Matters

Observationは
Contextと共に価値を持つ。

Contextを失ったObservationは
Knowledgeを育てられない。

---

## Replayable

Observationは
Experienceを再生成できる品質を持つ。

Knowledgeよりも
Observationの再現性を優先する。

---

## Extensible Perception

Perceptionは
Executorだけではない。

Tomobitは、

コード、

レビュー、

テスト、

Git、

ユーザー操作、

将来的には画面や音声など、

あらゆるRealityを知覚できる。

---

# Guiding Principle

Tomobitは、
世界を理解する前に、

まず世界を観測する。

Perceptionは、

Tomobitの五感であり、

すべてのExperienceの始まりである。