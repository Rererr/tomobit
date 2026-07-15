# ADR-0004: 技術選定 — Go / SQLite / Ollama

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0001](ADR-0001-connection-granularity.md)〜[ADR-0003](ADR-0003-outcome-and-preference.md), [EXECUTION_MODEL.md](../core/EXECUTION_MODEL.md)

---

## Context

Tomobitの技術的正体:

> CLIプロセス群（AI Executor）を子プロセスとして操り、
> 追記専用ログに事実を積み、
> そこから統計的射影（Connection）を導出する、ローカルの器官群

必要な能力: プロセス管理＋ストリームI/O、追記ログ、軽い統計計算（lgamma程度）、長寿命。
GPU・分散・重い数値計算は不要。dogfood規模では性能は論点にならない。

---

## Decision 1: 言語 = Go

検討: Rust / TypeScript / Go

- TypeScriptの実利はAgent SDKだが、設計はExecutor＝CLI子プロセスであり、
  SDKロックインしない方が「AIは交換可能」に忠実 → 決定打にならず
- Rustの強み（ゼロコスト抽象・メモリ制御）はTomobitではほぼ出番がない。
  代償（スキーマ激変期の反復速度・asyncの複雑さ）だけが残る
- プロセス操作・CLI・デーモンは**Goのホームグラウンド**（docker / gh / k8s の系譜）
- 決定打: **個人開発の最希少資源はモチベーション。使用者がGoを好き**

失うもの: 直和型（ADT）でのドメインモデリング。
Event型やLifecycleはinterface＋タグ付きstructで表現する。

付随する選定:

```text
SQLiteドライバ   modernc.org/sqlite（CGO不要 → クロスコンパイル可能な単一バイナリ）
並行処理         goroutine＋channel（ストリーム配管）
統計             math.Lgamma で ln BF は素で書ける。外部依存なし
```

---

## Decision 2: 永続化 = SQLite、真実と射影を分離

```text
真実（追記専用・マイグレーション地獄が起きない）
  events           生ログ（Reality）。バージョン付きJSON封筒 {v, type, payload}
  experiences      不変の資産

射影（DELETEして tomobit rebuild で再生できる）
  connections      能力・好み両方。(α, β, last_updated) を保持
  surprise_ledger  導出インデックス

永続（思想上の要請）
  curiosity_queue  「気になったことを忘れない」
```

- 「Knowledge is Rebuildable」をスキーマ構造で体現する
- 実装初期のスキーマ激変は射影側に閉じる
- **グラフDBは使わない**: エッジはハイパーエッジ（属性集合→Provider）で
  プロパティグラフに載らず、クエリは属性集合によるルックアップ、規模は数千。
  ドメイン語彙（Graph）とインフラ選定を混ぜない

### Decay = lazy

タイマーでα,βを書き換えない（可変状態＋バックグラウンド書込み＝腐敗の温床）。
`(α, β, last_updated)` を持ち、**読む瞬間に純関数で減衰を適用**する。
書くのは観測時だけ。

---

## Decision 3: 知覚のLLM = ローカルOllama

SessionからExperienceを作る意味的な仕事
（Context属性の抽出、第1層Outcomeの解釈）はLLMを要する。

- 抽出は `extract-experience` という一つのCapabilityとして扱い、
  Ollama ExecutorをExecutor経路で呼ぶ（LLM呼び出しはExecutor経由に統一）
  → 抽出の品質もExperienceとして記帳され、**Tomobitが自分の知覚をdogfoodする**
- モデルは8Bクラスのinstruct（例: qwen系/llama系）から開始。構造化出力はJSON schema指定
- 決定的にパースできるもの（exit code / テスト結果）はLLMに聞かない

### Deferred Perception（この選定の要）

eventsが追記専用の真実であるため、**知覚は遅延できる**。

```text
Ollamaが落ちている / 遅い / 壊れたJSONを吐いた
→ データは失われない。抽出は積んでおき、後でReplayする
```

ローカルLLM最大の弱点（可用性）を、アーキテクチャが構造的に無効化する。
副次的に、Experienceの内容（コードの文脈）がローカルから出ない。

---

## Decision 4: ランタイム形状 = 段階論

```text
Phase 1: 単一CLIバイナリ（常駐なし）
  tomobit do "..." → 実行 → 記帳 → タスク完了の区切りでTomoの質問 → 終了
  Learning Schedulerの「区切りでのみ」「1日1問」はプロセス内で完結

Phase 2: デーモン化（バックグラウンドLearningが欲しくなってから）
  器官の再配置であって再設計ではない
```

Executor統合はCLI子プロセス＋stream-jsonパース、Provider毎のAdapter方式。
SDKロックインなし。

---

## Consequences

- スタック全体で外部サービス依存ゼロ（AI Provider CLIを除く）。完全ローカル
- Rustで得られたはずのADTモデリングは放棄。型の厳密さより反復速度と継続性を取った
- 実装時ノブ: Ollamaのモデル選定、抽出プロンプト、rebuildの実行タイミング
- 次: 最小コアのスキーマ設計（events / experiencesテーブル初版）→ 実装着手
