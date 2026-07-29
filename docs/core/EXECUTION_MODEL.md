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
Executor ──── Adapter が CLI ごとの差を訳す
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

IntentはAIに依存しない。

Intentは型ではなく、人が書いた依頼そのものである。
知覚（extractor）がそこから `cap=` / `lang=` / `framework=` / `topic=` という
判断のためのトークンを取り出す（[ADR-0036](../decisions/ADR-0036-task-perception-wiring.md)）。

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

Capabilityは独立した型ではなく、Contextの正規化トークン `cap=implement` として
台帳に住む（[SCHEMA.md](../design/SCHEMA.md) D6）。判断が読むのはこのトークンである。

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

判断は純関数であり、LLMを呼ばない（[ADR-0011](../decisions/ADR-0011-meaning-by-model-judgment-by-math.md)）。

**既定は `auto`** — 誰にやらせるかは、人が選ばなければ台帳が選ぶ
（[ADR-0043](../decisions/ADR-0043-auto-by-default.md)）。候補になるのは
**実際に起動できるProvider**に限る。環境の不備をProviderの能力の証拠にしないためで、
起動できなかった実行は経験にもしない。`human` は、人がやると知っている文脈でのみ候補になる。

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
human
```

Providerは実行可能な候補を返す。

`human` も同じ台帳に乗る一人前の候補である
（[ADR-0018](../decisions/ADR-0018-experience-sovereignty.md)）。

---

# Executor

Executorは**1つ**である。Provider CLI を子プロセスとして起動し、その stream を
正典イベント（`provider.selected` / `provider.output` / `provider.finished` /
`provider.error`）へ変換する（[ADR-0006](../decisions/ADR-0006-executor-integration.md)）。

CLIごとの差を吸収するのは Executor ではなく **Adapter** である。
Adapterは「どう起動するか」と「streamをどう訳すか」だけを知る。
子プロセスの寿命（起動・timeout・SIGINT転送）、`ts` の付与、終了コードと実時間の観測は
共通のExecutorが持つ。DB・Connection・`seq` には触れない。

実装されているAdapterは `claude-code` と `codex`
（[ADR-0010](../decisions/ADR-0010-codex-adapter.md)）。

Executorが運ぶ依頼（`executor.Request`）は次を持つ:

| 項目 | 意味 | 出典 |
|---|---|---|
| `Prompt` | 依頼本文 | — |
| `ResumeID` | Providerのスレッドを継ぐ（空なら新規） | [ADR-0022](../decisions/ADR-0022-chat-session.md) D2 |
| `PermissionMode` | tomobit中立の3語 `auto` / `strict` / `open` | [ADR-0053](../decisions/ADR-0053-permission-is-asked.md) D1 |
| `AllowedTools` | 人がこのセッションで許した道具の名前 | ADR-0053 D3 |
| `Timeout` | 0 は無制限 | — |
| `WorkDir` | Tomoが働く場所（子のcwd）。Executorが落とす | [ADR-0047](../decisions/ADR-0047-workspace-is-wiring.md) D2 |
| `AddDirs` | 働く場所の外で読んでよい場所。Adapterが訳す | ADR-0047 D3 |

`WorkDir` は OS の概念なので誰も訳さない。`AddDirs` と `PermissionMode` は
**CLIごとに語彙が違う**ので Adapter が訳す。claude の permission と codex の sandbox は
同型ではない、と明示してある。

**許可要求はターンを終わらせる。** `--input-format stream-json` でも制御は返ってこない
（2026-07-27 実測）。だから「その場で続行」ではなく `permission.required` を報告して終わり、
人が許可を与えたうえで**再実行**する（費用がもう一度かかることを問いに書く）。
許可の寿命はセッションで、ディスクには書かない。

---

# Runtime

Runtimeは実際の実行環境である。

- Claude Code CLI
- Codex CLI

TomobitはRuntimeを直接意識しない。Adapterだけが意識する。

知覚のためのローカルLLM（Ollama / mlx-lm）はここには居ない。
あれは実行層ではなく Perception 側のバックエンドである
（[ADR-0029](../decisions/ADR-0029-perception-backend-choice.md)）。

---

# 配線は経験ではない

働く場所・読み取り先・許可・テストコマンドは、**タスクの境界でだけ替わる配線**であって、
Providerの成績ではない。だから台帳に書かない
（[ADR-0047](../decisions/ADR-0047-workspace-is-wiring.md) / [ADR-0053](../decisions/ADR-0053-permission-is-asked.md) D4）。

- 対話中は `/cd` と `/add-dir` で替える
- 許可で止まったターンを `provider.error` にはしない。人が渡さなかった判断が
  Providerの成績になってはいけない
- テストの走らせ方も配線（config の「働く場所 → コマンド」）。書かなければ1バイトも動かない
  （[ADR-0052](../decisions/ADR-0052-first-layer-is-observed.md) D2）

---

# タスクは分かれる

実行層の上に、タスクを分ける層が乗っている。

- **分割プロトコルは常時ON。** 分けるかどうかを判断するのは毎回Providerであって、
  tomobitは判断しない（[ADR-0023](../decisions/ADR-0023-task-split.md) / [ADR-0028](../decisions/ADR-0028-auto-split-parallel.md)）
- **子は親タスクの内訳である。** 決定はタスクにつき1回で、子は親が選んだ相手で走る。
  子は経験にしない（イベントは残す）。片側だけの証拠を作らないため
  （[ADR-0054](../decisions/ADR-0054-a-child-is-the-breakdown.md)）
- **独立宣言は信じる。** Providerが「この群は独立だ」と宣言したら、訊かずに並走する。
  止める手は `parallel_subtasks`（[ADR-0056](../decisions/ADR-0056-independence-is-trusted.md)）
- **作業場は宣言で分ける。** tomobitは `git` という語を持たず、「このプロジェクトが使う
  隔離手段で分けよ」とだけ渡す。宣言はさせるが検証はしない。隔離の単位はセッション木で、
  子は親を継ぐ（[ADR-0050](../decisions/ADR-0050-workspace-isolation-protocol.md)）
- **duel の子だけが例外。** A/B実走の2本は、それぞれが独立した発注であり各自の作業場を持つ
  （[ADR-0026](../decisions/ADR-0026-ab-duel.md)）
- **分け方も評価の対象になる。** 采配は「分け方＝Provider」と「割り当て＝決定エンジン」に割れる
  （[ADR-0051](../decisions/ADR-0051-orchestration-is-judged.md)）

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

Realityは Provider の stream だけではない。tomobit自身がテストを走らせて
第1層（`test.result`）を観測し（[ADR-0052](../decisions/ADR-0052-first-layer-is-observed.md)）、
機械の赤と人の「文句なし」が食い違ったときだけ、第2層（`user.verdict`）が拒否権として1問入る
（[ADR-0055](../decisions/ADR-0055-verdict-is-a-veto.md)）。

この循環こそがTomobitの「成長」である。

AIは成長しない。

**Tomobitが経験から成長する。**
