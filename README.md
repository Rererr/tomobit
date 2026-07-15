# tomobit

> **Tomobit is not built to use AI. Tomobit is built to grow with it.**

複数のコーディングAIの前に立ち、経験（Experience）からConnectionを育てる **Living Harness**。

## Docs

### 思想
- [VISION.ja.md](VISION.ja.md) — なぜTomobitか（日本語版）
- [docs/core/VISION.md](docs/core/VISION.md) — English version

### 認知アーキテクチャ
- [docs/core/COGNITIVE_ARCHITECTURE.md](docs/core/COGNITIVE_ARCHITECTURE.md) — 認知コンポーネントと責務の全体図
- [docs/core/COGNITIVE_MODEL.md](docs/core/COGNITIVE_MODEL.md) — Connection中心の認知モデル（シナプス思想）
- [docs/core/KNOWLEDGE_EVOLUTION.md](docs/core/KNOWLEDGE_EVOLUTION.md) — Reality → Experience → Connection → Strategy の進化の道筋
- [docs/core/PERCEPTION_ENGINE.md](docs/core/PERCEPTION_ENGINE.md) — Reality → Observation（五感）
- [docs/core/EXPERIENCE.md](docs/core/EXPERIENCE.md) — Experienceモデル
- [docs/core/CONNECTION_ENGINE.md](docs/core/CONNECTION_ENGINE.md) — Connectionの実体・Split/Merge・ライフサイクル
- [docs/core/CURIOSITY_ENGINE.md](docs/core/CURIOSITY_ENGINE.md) — 好奇心とLearning候補

### 実行アーキテクチャ
- [docs/core/EXECUTION_MODEL.md](docs/core/EXECUTION_MODEL.md) — Intent → Plan → Capability → Provider → Executor → Runtime
- [docs/core/STATE_MACHINE.md](docs/core/STATE_MACHINE.md) — 全体ライフサイクル

### 実装設計
- [docs/design/SCHEMA.md](docs/design/SCHEMA.md) — スキーマ v1.0（**確定** — D1〜D11・R1〜R4レビュー済み）

### 意思決定の記録
- [ADR-0001](docs/decisions/ADR-0001-connection-granularity.md) — Connectionの誕生モデル（粗→Split採用）
- [ADR-0002](docs/decisions/ADR-0002-surprise-and-split-judgment.md) — Surpriseの定義（超過surprisal）とSplit有意判定（補正付きln BF＋ヒステリシス）
- [ADR-0003](docs/decisions/ADR-0003-outcome-and-preference.md) — Outcome三層信号、能力/好みの二重Connection、Tomoの質問
- [ADR-0004](docs/decisions/ADR-0004-tech-stack.md) — 技術選定（Go / SQLite真実と射影の分離 / Ollama＋Deferred Perception / 段階的デーモン化）
- [ADR-0005](docs/decisions/ADR-0005-perception-model-and-schema-boundary.md) — 知覚の実装（qwen3:8b確定 / schemaは「形」・プロンプトは「意味」）

### Archive
`docs/archive/` — 改訂前の原本。参照のみ、更新しない。

## Status

設計フェーズ完了（ADR-0001〜0005）。実装フェーズへ。

Stack: **Go / SQLite / Ollama**（完全ローカル）

スキーマ確定済み（[SCHEMA.md](docs/design/SCHEMA.md)）。
次の一手: `go mod init` → 最小コア実装（記帳 → 抽出 → Connection更新 → rebuild）。
実装時ノブ: 減衰半減期・事前分布の継承・backoffブレンド → [CONNECTION_ENGINE.md の Open Questions](docs/core/CONNECTION_ENGINE.md#open-questions)
