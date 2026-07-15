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

Signalは検出器である。

「これは調べる価値があるかもしれない」と
推薦するまでが仕事であり、順位は付けない。

順位は単一の物差し
（[Value of Information](#value-of-information)）が付ける。

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

## Questioned

Surprise台帳が正に浮上した（+2 nats）。

確信しているConnectionが、
現実に殴られ続けている。

審判（Split判定）を提案する。

（[ADR-0002](../decisions/ADR-0002-surprise-and-split-judgment.md)）

---

## Preference Gap

能力が互角で、好みを知らない。

```text
(Rust) : Claude vs Codex

能力     互角（BFが中立帯）
文脈     頻繁に来る（決定に効く）
好み     証拠なし
```

**Tomoの質問**を生成する。

「最近RustのレビューでClaudeとCodex両方使ってるけど、
どっちが好みだった?」

質問はLearning Realityの一種であり、
Human Executorが実行する。

本当に迷っている時しか聞かないので、
質問は自然と賢い質問になる。

質問予算（初期値: 1日1問）は
Learning Schedulerが守る。

スキップは自由。ペナルティはない。

（[ADR-0003](../decisions/ADR-0003-outcome-and-preference.md)、
実装形は [ADR-0007](../decisions/ADR-0007-curiosity-question.md)）

---

## Plan Proposal

あるIntentのPlanメニューに空きがあり、
既存Planの台帳は収束している —
今のメニューから学べることが減っている。

既存Planへの構造的変異
（drop / insert / swap、いずれも純関数）で
新しいPlan変種を提案する。

LLMはPlanを生成しない。

変異は純関数、採否は数式、誕生は継承。

新参Planの初陣は n(stakes) が自然と
軽いタスクに割り当てる。

（[ADR-0014](../decisions/ADR-0014-plan-learning-same-ledger.md)）

---

# Value of Information

Signalの推薦に順位を付けるのは、
単一の純関数である。

```text
VoI = 文脈の到来頻度 × 判断の揺らぎ

到来頻度   その文脈が仕事でどれくらい来るか
           （eventsから数えるだけ）

揺らぎ     判断のくじ（Thompson Sampling）を
           M本引いてみて、勝者が割れる率。
           毎回同じなら0 — もう迷っていない
```

新しい部品はない。
判断に使うサンプラーを、そのまま計測に使い回す。

> **不確実性は好奇心の理由にならない。**
> **判断が変わることだけが理由になる。**

年に一度しか来ない島の謎は、
どれほど深くてもVoI ≈ 0である。

コストでは割らない。

価値の物差しはVoI、財布の紐はScheduler。
Learning CandidateのEstimated Costは
予約フィールドである（v1では常に空）。

（[ADR-0016](../decisions/ADR-0016-curiosity-priority-voi.md)）

---

# Curiosity Queue

Curiosity Engineは
Learning TaskをQueueへ登録する。

> ただし、connectionsとexperiencesから**再導出できるシグナル**
> （Preference Gap / Questioned）はQueueに保存しない。
> 聞く・裁く直前にViewとして導出する（[ADR-0007](../decisions/ADR-0007-curiosity-question.md)）。
> Queueが持つのは再導出できない外部観測のみ。

例

```text
VoI 0.34   Compare Codex with Claude on Go
           毎日来る × 迷っている

VoI 0.08   Evaluate Claude 5 on Rust
           時々来る × やや迷い

VoI 0.01   Re-evaluate Python Refactoring
           よく来る × ほぼ確定
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

ただし豊かさは、使われる場所で測る。

誰も訪れない島の地図を精密にすることは、
豊かさではなく退蔵である。

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