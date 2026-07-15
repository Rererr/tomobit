# Curiosity Engine

## Purpose

Curiosity Engineは、
Tomobitの知的好奇心を管理するコンポーネントである。

責務は、

> **「今、何を学ぶ価値があるか」を判断すること**

である。

学習を実行することではない。

実行可能かどうかも判断しない。

---

# Philosophy

Tomobitは、
仕事をしながら学ぶシステムではない。

仕事を最優先し、

余剰リソースが存在する場合のみ、
知識を広げる。

学習は義務ではない。

**好奇心の結果である。**

---

# Responsibility

Curiosity Engineは以下のみを担当する。

- Knowledge Gap の発見
- Novelty の検出
- Confidence の不足検出
- Learning Candidate の生成
- Curiosity Queue の管理
- Learning Priority の更新

以下は担当しない。

- 学習実行
- Token管理
- Executor選択
- Rate Limit管理
- Cost管理

---

# Architecture

```text
                Experience
                     │
                     ▼
            Knowledge Network
                     │
                     ▼
             Curiosity Engine
                     │
                     ▼
             Curiosity Queue
                     │
                     ▼
          Learning Scheduler
                     │
                     ▼
             Executor Manager
                     │
                     ▼
                 Executors
```

Curiosity Engineは
Learning Requestを生成するのみである。

---

# Curiosity Signal

Curiosityは複数のSignalから生成される。

例

## Knowledge Gap

十分なExperienceが存在しない。

例

- Rust + Gemini

Experience : 2

Confidence : Low

---

## Low Confidence

ConnectionのConfidenceが低い。

```text
Rust

↓

Claude

Strength : High

Confidence : Low
```

追加検証を提案する。

---

## New Provider

新しいProviderが追加された。

例

- Claude 5
- GPT-OSS
- Qwen 4
- Gemini 3

既存Knowledgeとの差分を学習する。

---

## Model Update

既存Providerの能力が変化した。

例

Claude 4 → Claude 5

過去Knowledgeの再評価候補となる。

---

## Environment Change

環境が変化した。

例

- Rust Edition
- TypeScript Major Update
- New Framework

Knowledgeの鮮度を保つため、
再評価を提案する。

---

## High Uncertainty

複数Providerで
評価が安定していない。

例

Claude

Success : 91%

Codex

Success : 90%

Gemini

Success : 92%

差が小さいため、
追加Evidenceを要求する。

---

# Curiosity Queue

Curiosity Engineは
Learning TaskをQueueへ登録する。

例

```text
Priority 95

Evaluate Claude 5 on Rust

------------------------

Priority 82

Compare Codex with Claude on Go

------------------------

Priority 61

Re-evaluate Python Refactoring
```

Queueは永続化される。

Tomobitは
「気になったこと」を忘れない。

---

# Learning Candidate

Learning Candidateは

以下を保持する。

- Target Capability
- Target Provider
- Target Context
- Reason
- Expected Value
- Estimated Cost
- Estimated Duration
- Evidence Needed
- Created At

---

# Learning Scheduler

Curiosity Engineは
Learning Schedulerへ依頼するだけである。

Schedulerは

- Executor Availability
- Token Budget
- Rate Limit
- Queue Length
- User Policy
- Cost Budget
- CPU
- RAM

などを確認し、

実行可能な場合のみ
Learning Taskを開始する。

---

# Parallel Learning

Learningは
Productionとは独立して実行される。

```text
Production

      │

      ├───────────────┐
      │               │
      ▼               ▼

Official Result   Learning Result
```

Productionは
Learningの影響を受けない。

Learning Resultのみが
Knowledge更新へ利用される。

---

# Curiosity Lifecycle

```text
Knowledge Update

↓

Curiosity Signal Detection

↓

Candidate Generation

↓

Priority Calculation

↓

Queue

↓

Scheduler Approval

↓

Learning Execution

↓

Experience

↓

Knowledge Update
```

CuriosityはKnowledge Evolutionの一部である。

---

# Design Principles

## Curiosity is Independent

好奇心は
実行能力に依存しない。

---

## Curiosity Never Blocks Production

仕事を止める理由として
Curiosityを利用してはならない。

---

## Knowledge Expansion over Optimization

目的は
最適化ではなく、

Knowledge Networkを豊かにすることである。

---

## Exploration Requires Permission

探索は常に
Schedulerの許可を必要とする。

Curiosityだけでは
Learningは開始されない。

---

## Curiosity Persists

Tomobitは
「試したいこと」を忘れない。

十分なリソースが確保された時点で、
Queueから順に実行する。

---

# Guiding Principle

Tomobitは
常に学ぶわけではない。

Tomobitは、

**知りたいことを覚え続ける。**

そして、

仕事の邪魔をしない範囲で、

少しずつKnowledge Networkを育てていく。

それがTomobitの「好奇心」である。