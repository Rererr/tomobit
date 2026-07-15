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

### 意思決定の記録
- [ADR-0001](docs/decisions/ADR-0001-connection-granularity.md) — Connectionの誕生モデル（粗→Split採用）

### Archive
`docs/archive/` — 改訂前の原本。参照のみ、更新しない。

## Status

思想・認知アーキテクチャの設計フェーズ。実装未着手。

次の論点: Split検出器の中身（Surpriseの定義・有意判定の閾値）→ [CONNECTION_ENGINE.md の Open Questions](docs/core/CONNECTION_ENGINE.md#open-questions)
