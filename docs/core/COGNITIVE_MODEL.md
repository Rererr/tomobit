# Cognitive Model

## Philosophy

Tomobitは知識ベースではない。

Tomobitは、

> **経験によって Connection を育てる認知システム**

である。

この思想は、人間の脳におけるシナプスから着想を得ている。

ただし、Tomobitは脳を模倣することを目的としない。

重要なのは、

> **経験を保存することではなく、経験同士の結び付きが変化し続けること**

である。

Tomobitが成長するとは、

新しいRuleを覚えることではない。

Connectionが強まり、
弱まり、
再構成されることで、

より自然な意思決定ができるようになることである。

---

# Synaptic Thinking

TomobitではExperienceを知識へ変換しない。

ExperienceはKnowledge Networkへ刺激を与える。

```text
Experience

↓

Connection Update

↓

Knowledge Network

↓

Strategy Generation
```

Experienceは消えない。

Connectionだけが変化し続ける。

このConnectionの変化が、
Tomobitにおける「学習」である。

---

# Why Connection

Ruleは固定された知識である。

Connectionは関係性である。

例えば

```text
Rust

↓

Borrow Checker

↓

Lifetime

↓

Claude Code

↓

Success
```

Tomobitは

「RustならClaude」

というRuleを保存するのではなく、

これらのNode同士のConnectionを保持する。

Connectionは以下の情報を持つ。

- Strength
- Confidence
- Frequency
- Recency
- Evidence

Experienceが増えるほど、
Connectionは継続的に変化する。

---

# Knowledge Network

KnowledgeはRuleの集合ではない。

KnowledgeはConnection Networkとして保持される。

```text
                 Borrow Checker
                ╱               ╲
               ╱                 ╲
           Rust ───────── Lifetime
             │                     │
             │                     │
             ▼                     ▼
       Claude Code            Codex
             │                     │
             └────────┬────────────┘
                      ▼
                  Successful Fix
```

Nodeよりも重要なのは、
Node同士のConnectionである。

KnowledgeはConnection全体の状態として存在する。

---

# Strategy Generation

Strategyは保存しない。

毎回生成する。

```text
Current Context

        +

Knowledge Network

        +

Current Goal

        +

Current State

        ↓

Small Local LLM

        ↓

Strategy
```

StrategyはKnowledgeではない。

Knowledgeから生成される
**現在最適な判断**である。

---

# Role of the Local LLM

軽量ローカルLLMは
コードを書くためだけに存在するのではない。

Tomobitでは

> **Knowledge Networkを読み解き、ConnectionからStrategyを生成する認知エンジン**

として機能する。

またアイドル状態では

- Experienceの再解釈
- Connection更新
- 類似Experienceの発見
- 知識の再構成

を継続的に実施できる。

Tomobitは停止中も成長し続ける。

---

# Convergence

Knowledgeは最初から整理されない。

初期状態では

- Connectionは疎である
- 矛盾も存在する
- Weightも安定しない

Experienceを重ねることで

- 強いConnectionは強くなる
- 弱いConnectionは自然に薄れる
- 新しいConnectionが形成される

Knowledgeは設計者が整理するものではない。

> **Experienceによって自然に収束していくもの**である。

---

# Design Principles

## Experience is Permanent

Experienceは事実であり、
Knowledgeの源泉である。

削除ではなく蓄積を基本とする。

---

## Connection is the Knowledge

Tomobitが保持するのはRuleではない。

Connectionである。

Ruleは必要に応じてConnectionから導出できる。

---

## Strategy is Ephemeral

Strategyは保存しない。

Knowledge Networkから、
その瞬間ごとに生成される。

---

## Continuous Evolution

Knowledgeに完成形はない。

ConnectionはExperienceによって
生涯変化し続ける。

---

## Living Harness

TomobitはAIを管理するツールではない。

経験を通じてConnectionを育て、

そのConnectionから判断を生み出し続ける

**Living Harness**

である。

---

# Guiding Principle

実装がどれほど変化しても、
この思想だけは変えてはならない。

Tomobitの価値は、

AIでも、

モデルでも、

アルゴリズムでもない。

**Connectionが育ち続けること。**

それがTomobitの「成長」である。