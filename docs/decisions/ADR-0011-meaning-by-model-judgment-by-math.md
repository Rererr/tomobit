# ADR-0011: Meaning by Model, Judgment by Math — 判断は純関数、LLMの座席はextractorのみ

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0001](ADR-0001-connection-granularity.md), [ADR-0004](ADR-0004-tech-stack.md), [ADR-0005](ADR-0005-perception-model-and-schema-boundary.md), [COGNITIVE_MODEL.md](../core/COGNITIVE_MODEL.md), [PERCEPTION_ENGINE.md](../core/PERCEPTION_ENGINE.md), [SCHEMA.md](../design/SCHEMA.md)

---

## Context

旧COGNITIVE_MODELは、軽量ローカルLLMを
「Knowledge Networkを読み解き、ConnectionからStrategyを生成する認知エンジン」
と位置付けていた。

つまりLLMの座席が二つあった — **知覚する席**と、**判断する席**。

一方でVISIONの根本原則は「AIは交換可能な部品、資産はExperience」である。
判断の中心に特定のLLMが座るなら、ハーネスの心臓部に交換不能なAIが生まれ、
原則が自壊する。

また、One Ledger（[ADR-0001](ADR-0001-connection-granularity.md)）により
Connectionの実体は減衰Beta(α,β)であり、判断に必要な統計は閉形式で手元にある。
LLMに「読み解かせる」必然性は、実体の側からすでに消えていた。

---

## Decision 1: 原則 — Meaning by Model, Judgment by Math

**意味付けはLLM、判断は数式。**

COGNITIVE_MODELの公式Design Principleとする。
「Small Local LLM = Strategy生成の認知エンジン」は廃止。

LLMの座席はPerception / Experience側（extractor）のみ。
判断側に交換不能なAIは存在しない。

---

## Decision 2: 判断のフロー

```text
タスク記述 ──LLM（意味付け）──▶ Context属性トークン

Context属性トークン ──純関数（判断）──▶ Provider決定
```

Decision Engineは、Connectionの統計（Beta事後）と
Context属性トークンを入力とする**純関数**である。

---

## Decision 3: 実行前知覚はPerception Engineの責務

実行前のContext抽出のために別の器官は立てない。
Perceptionの定義を半歩広げる:

> **Perceptionは過去の出来事と、現在のタスク記述の両方を知覚する。**

同じextractor、同じ語彙（[ADR-0005](ADR-0005-perception-model-and-schema-boundary.md)）、
同じextractor_ver管理（SCHEMA D4）を適用する。

過去の知覚と実行前の知覚が語彙を共有することが、
ExperienceがDecisionに接続できる条件である。
語彙が割れた瞬間に、経験は資産でなくなる。

---

## Decision 4: アイドル時のLLMの仕事

「判断を練る」ことではなく、**「過去をより良く知覚し直す」**こと。

extractor_verの改版と再知覚 — SCHEMA D4の機構そのものであり、
新しい機構は不要。磨かれるのは判断のアルゴリズムではなく、
判断が読むExperienceの品質である。

---

## 根拠

1. **検証可能な成長** — 判断が純関数なら、判断の変化は入力（Connection統計）の
   変化からしか生まれない。Tomoが賢くなったかは台帳で測れる。
   LLMが判断すると、改善がExperienceの蓄積によるものか
   モデルの気まぐれによるものかを切り分けられない。

2. **AI交換可能性** — extractorすら交換可能である
   （extractor_model列＋extractor_ver＋再知覚で全経験を張り替えられる）。
   LLMをどれだけ差し替えても、判断は同じExperienceから同じ数式で導かれる。
   VISIONの「AIは交換可能な部品」が判断の中心でも成立する。

3. **監査可能性** — 「なぜCodexを選んだか」に、
   どのConnectionのどの事後分布から、どの計算で導いたかで答えられる。
   判断はリプレイ可能になる。

---

## 用語の整理

「意味付け」はPerception側の**語彙への写像**を指し、評価・価値判断を含まない。
PERCEPTION_ENGINEの「Facts over Interpretation」とは両立する
（写像はするが、良し悪しは付けない）。

---

## Consequences

- COGNITIVE_MODEL.md「Role of the Local LLM」「Strategy Generation」を改稿、
  Design Principlesに「Meaning by Model, Judgment by Math」を追加
- PERCEPTION_ENGINE.mdにタスク記述の構造化（Task Perception）を追加
- **決定則そのもの（どの純関数か — Thompson Sampling等）は本ADRの範囲外。**別途決定する
- extractorの品質が、TomobitのLLM依存の全てになる。
  ADR-0005「schemaは形、プロンプトは意味」の重要度がさらに上がる
- 決定則が確率的（サンプリングを含む）場合も、seed/drawをeventsに記帳することで
  純関数性と監査可能性を保つ（決定則のADRで詳細化）
