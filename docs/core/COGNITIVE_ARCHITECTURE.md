# Cognitive Architecture

## Purpose

この文書は、
Tomobitを構成する認知コンポーネントと、
それぞれの責務を定義する。

各コンポーネントは単一責務を持ち、
他の責務を侵害してはならない。

---

# Cognitive Flow

```text
                    User Goal
                        │
                        ▼
                 Capability Planning
                        │
                        ▼
                 Decision Engine
                        │
                        ▼
                   State Machine
                        │
                        ▼
               Executor Manager
                        │
                        ▼
                   LLM Executors
                        │
                        ▼
──────────────────────────────────────────
                  Reality
──────────────────────────────────────────
                        │
                        ▼
                Perception Engine
                        │
                        ▼
                  Observation
                        │
                        ▼
                Experience Engine
                        │
                        ▼
                  Experience
                        │
                        ▼
              Connection Engine
                        │
                        ▼
              Knowledge Network
                        │
         ┌──────────────┴──────────────┐
         ▼                             ▼
 Decision Engine               Curiosity Engine
                                       │
                                       ▼
                                Curiosity Queue
                                       │
                                       ▼
                             Learning Scheduler
                                       │
                                       ▼
                               Executor Manager
```

---

# Responsibilities

## Capability Planning

目的をCapabilityへ分解する。

責務

- Goal分析
- Capability生成
- Capability順序決定

担当しない

- Provider選択
- 実行

---

## Decision Engine

Capabilityに対して
最適な実行戦略を決定する。

入力

- Capability
- Context
- Knowledge Network

出力

- Execution Plan

担当しない

- 学習
- Experience生成

---

## State Machine

Executionの現在状態を管理する。

担当

- State遷移
- Workflow制御
- Retry制御

担当しない

- 推論
- Learning

---

## Executor Manager

利用可能なExecutorを管理する。

担当

- Executor選択
- Queue管理
- Provider管理

担当しない

- Knowledge
- Curiosity

---

## Executors

Realityを生成する。

例

- Claude Code
- Codex
- Ollama
- Gemini
- Qwen

Executorは
仕事を実行するだけである。

---

# Reality

Realityは

実際に起きた出来事。

まだKnowledgeではない。

例

- Code Generated
- Compile Failed
- Test Passed
- Review Accepted

---

## Perception Engine

RealityをObservationへ変換する。

担当

- Event受信
- Normalization
- Aggregation
- Observation生成

担当しない

- 解釈
- Learning

Perceptionは
Tomobitの五感である。

---

## Experience Engine

Observationへ意味を与える。

担当

- Pattern抽出
- Success分析
- Failure分析
- Experience生成

Experienceは
「経験」であり、
Knowledgeではない。

---

## Connection Engine

Experience同士のConnectionを更新する。

担当

- Strength更新
- Confidence更新
- Context統合
- 新規Connection生成

Knowledgeは保存するものではなく、

Connectionから自然に形成される。

---

## Knowledge Network

Tomobitが持つ理解。

KnowledgeはRuleではない。

Connectionの集合から
継続的に形成される。

---

## Curiosity Engine

Knowledge Gapを発見する。

担当

- Novelty検出
- Low Confidence検出
- Learning Candidate生成
- Curiosity Queue更新

担当しない

- 実行

---

## Learning Scheduler

Learning Candidateを実行するか判断する。

担当

- Executor状態確認
- Budget確認
- Rate Limit確認
- Queue管理
- 実行タイミング決定

担当しない

- Learning内容決定

---

# Two Sources of Reality

Realityには二種類存在する。

## Production Reality

ユーザーのGoalを達成するために発生するReality。

例

- 実装
- 修正
- レビュー
- テスト

---

## Learning Reality

Curiosityによって生成されるReality。

例

- 比較実験
- Benchmark
- 再レビュー
- 再評価
- 新Model検証

Perception Engineは
両者を区別せずObservationを生成する。

Observationには
Reality Sourceのみ記録する。

---

# Design Principles

## Reality Before Knowledge

KnowledgeはRealityからしか生まれない。

---

## Observation Before Experience

ExperienceはObservationからのみ形成される。

---

## Experience Before Knowledge

KnowledgeはExperienceの積み重ねから形成される。

---

## Curiosity Creates Reality

LearningとはKnowledgeを読むことではない。

Curiosityが新しいRealityを生み出し、
新しいExperienceを獲得することである。

---

## Single Responsibility

各コンポーネントは
一つの責務だけを持つ。

認知は
複数の小さな器官が協調することで実現される。

---

# Guiding Principle

Tomobitは一つの巨大なAIではない。

世界を知覚し、

経験を意味付けし、

Connectionを育て、

理解を形成し、

好奇心によって新たなRealityを生み出す。

それぞれの役割を持つ認知コンポーネントが協調することで、
Tomobitという一つの「生きたハーネス」が成り立つ。