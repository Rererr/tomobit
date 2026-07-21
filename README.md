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
- [docs/design/SPRITES-WINDOW.md](docs/design/SPRITES-WINDOW.md) — Tomoスプライト正本（32×32・犬種3種・グレースケール6トーン）

### 意思決定の記録
- [ADR-0001](docs/decisions/ADR-0001-connection-granularity.md) — Connectionの誕生モデル（粗→Split採用）
- [ADR-0002](docs/decisions/ADR-0002-surprise-and-split-judgment.md) — Surpriseの定義（超過surprisal）とSplit有意判定（補正付きln BF＋ヒステリシス）
- [ADR-0003](docs/decisions/ADR-0003-outcome-and-preference.md) — Outcome三層信号、能力/好みの二重Connection、Tomoの質問
- [ADR-0004](docs/decisions/ADR-0004-tech-stack.md) — 技術選定（Go / SQLite真実と射影の分離 / Ollama＋Deferred Perception / 段階的デーモン化。知覚バックエンド選択はADR-0029で一般化）
- [ADR-0005](docs/decisions/ADR-0005-perception-model-and-schema-boundary.md) — 知覚の実装（qwen3:8b確定 / schemaは「形」・プロンプトは「意味」）
- [ADR-0006](docs/decisions/ADR-0006-executor-integration.md) — Executor統合（`tomobit do` / claude-code Adapter / ダイジェスト記帳 / Feedback）
- [ADR-0007](docs/decisions/ADR-0007-curiosity-question.md) — Curiosityの最初の器官（Preference GapはView / 質問予算はeventsから導出 / doの区切りでTomoの質問）
- [ADR-0008](docs/decisions/ADR-0008-appearance.md) — Tomoの姿（成長ステージはView / ドット絵＝半ブロック＋ANSI / 依存ゼロ — 端末描画はADR-0025で廃止）
- [ADR-0009](docs/decisions/ADR-0009-voice.md) — Tomoの声（発話＝Viewの写像 / LLM不使用 / 語調は確信度のView）
- [ADR-0010](docs/decisions/ADR-0010-codex-adapter.md) — 2つ目のAdapter（codex / `do --provider` / 写像はエラー経路実採取＋仕様準拠）
- [ADR-0011](docs/decisions/ADR-0011-meaning-by-model-judgment-by-math.md) — Meaning by Model, Judgment by Math（判断は純関数、LLMの座席はextractorのみ）
- [ADR-0012](docs/decisions/ADR-0012-decision-rule-thompson-sampling.md) — 決定則＝Thompson Sampling（探索は好みの側で、ミスは構造になる）
- [ADR-0013](docs/decisions/ADR-0013-prior-inheritance-mean-only.md) — 事前分布の継承（平均だけ継ぎ、確信は継がない）
- [ADR-0014](docs/decisions/ADR-0014-plan-learning-same-ledger.md) — Plan学習（台帳は賭ける対象を選ばない）
- [ADR-0015](docs/decisions/ADR-0015-reflection.md) — Reflection（第一級の器官、実体は射影、核は双方向性）
- [ADR-0016](docs/decisions/ADR-0016-curiosity-priority-voi.md) — Curiosityの優先度＝Value of Information
- [ADR-0017](docs/decisions/ADR-0017-stage-function-calibration.md) — ステージ関数の改版（成長のゲートは量でなく較正度）
- [ADR-0018](docs/decisions/ADR-0018-experience-sovereignty.md) — Experience Sovereignty（経験主権と、humanの台帳）
- [ADR-0019](docs/decisions/ADR-0019-companionship-is-derived.md) — 相棒らしさは導出される（感情・儀式・個性は台帳のView）
- [ADR-0020](docs/decisions/ADR-0020-face-window.md) — Tomoの顔窓（窓は第二のレンダラである）
- [ADR-0021](docs/decisions/ADR-0021-onboarding.md) — 初期導入（配線は経験ではない / config.json / `tomobit setup`。知覚配線の質問はADR-0029で拡張）
- [ADR-0022](docs/decisions/ADR-0022-chat-session.md) — 対話セッション（会話は入力の器・タスクは記帳の単位 / ターンはスレッドを継ぐ / インラインの自前ラインエディタ）
- [ADR-0023](docs/decisions/ADR-0023-task-split.md) — タスク分割（Providerの分割提案はプロトコル / サブタスクは独立タスク / 実行者は親の選択方法を継ぐ — autoなら台帳が分配。opt-in `--split` と逐次のみはADR-0028が改定）
- [ADR-0024](docs/decisions/ADR-0024-chat-ux.md) — チャットUX（履歴永続化・Ctrl-R・Tab補完・markdown-lite描画・ツールdetailは表示専用チャネル — 出力そのものへの拡張はADR-0030）
- [ADR-0025](docs/decisions/ADR-0025-face-autolaunch.md) — 端末アバターの廃止と顔窓の自動起動（姿は窓に一本化 / 端末=声とテキスト / 顔窓は既定で出る・設定で止める）
- [ADR-0026](docs/decisions/ADR-0026-ab-duel.md) — A/B実走（好奇心が問いから比較実験へ / Tomoが「試していい?」と申し出てY/n・2Providerを並走・ユーザー判定をpreference経験化 / 顔窓は「考える」吹き出し⚪︎つなぎで可視化 — orchestrator化しない）
- [ADR-0027](docs/decisions/ADR-0027-face-lifetime.md) — 顔窓の寿命（窓は対話が生きている間だけ居る＝既定エフェメラル / 在席は`~/.tomobit/sessions/<pid>.lock`のflockで測る / 常駐は`face_resident`でオプトイン）
- [ADR-0028](docs/decisions/ADR-0028-auto-split-parallel.md) — 判断ゼロの分割と並走（分割プロトコルは常時ON＝判断は毎回Provider / 独立群はProviderが宣言・既定は逐次 / 並走だけ実行直前にy/Nで人が許す＝コストは実測中央値 / chatは成果を親スレッドにfeedし親Providerが統合 / 子は客観信号のみ・主観Feedbackは区切りの親に1回）
- [ADR-0029](docs/decisions/ADR-0029-perception-backend-choice.md) — 知覚バックエンドの選択（`perceive.MLXLM`をOllamaと並ぶExtractorとして追加 / mlx-lmは構造化出力が無くプロンプト＋Go側検証で「形」を保証 / configは`perceive_backend`＋バックエンド別URL・モデル / `--backend`フラグとextractor生成の1箇所集約 / setupはバックエンド選択→URL・モデル→診断）
- [ADR-0030](docs/decisions/ADR-0030-provider-tool-output.md) — Providerのツール出力を表示専用で受け取る（tool_resultは表示専用キーで運び台帳はR3不変 / mdliteに通さずSGRのみ通す＝色は残しレイアウト破壊を防ぐ / 先頭優先で上限に切る / codexも対称。ターン総量はADR-0031が追補）
- [ADR-0031](docs/decisions/ADR-0031-turn-tool-output-budget.md) — ターンのツール出力表示予算（per-result上限はNで積む洪水を縛れない / turnViewにターン累積予算＝先頭優先のターンスケール拡張 / 予算切れは一度だけ省略行を出し以後沈黙・detail行と本文textは予算外 / 上限値は実stream計測で較正）
- [ADR-0032](docs/decisions/ADR-0032-pipe-chat-first-class.md) — pipe chatのGUI一級市民化（`chat --view ndjson`＝stdout全体をNDJSON viewストリームにするオプトイン・台帳はR3不変 / cookedの行継続＝末尾`\`はrawの`\`+Enterと同じ意味論 / `TOMOBIT_FACE=1`の明示はpipeでも顔窓を出す＝TTYゲートはenv沈黙時の既定へ改版・presenceも同条件）
- [ADR-0033](docs/decisions/ADR-0033-organ-of-forgetting.md) — 忘却の器官（忘却は主権の行使＝人間の動詞・Tomoは提案も実行もしない / `forget`＝物理削除+VACUUM・`amend`＝人間による再知覚（追記） / 人の手が入ったセッションは再知覚しない / 直後に自動rebuildで射影整合）

### Archive
`docs/archive/` — 改訂前の原本。参照のみ、更新しない。

## Status

最小コア（記帳 → 知覚 → Connection更新 → rebuild）に加え、相棒の器官を実装済み:
**姿**（顔窓 `tomobit-face`・成長6ステージ・すべてConnectionからのView。端末はテキスト、姿は窓 — ADR-0025）、
**声**（つぶやき・成長報告・提案 — 全発話が決定的導出）、
**質問**（Preference Gap導出・予算1問/24h — ADR-0007）、
**対話**（チャット形式のセッション・ターンは同じスレッドを継ぐ — ADR-0022）。

Stack: **Go / SQLite / Ollama or MLX LM Server**（完全ローカル・ターミナルUI。依存は端末の物理のみ —
raw modeに `x/term`、表示幅に `uniseg`。知覚バックエンドはADR-0029で選択可）

```
tomobit            # 相棒ビュー（発話・Connection一覧）→ そのまま対話へ
                   # パイプ・リダイレクトなら見せて終わる
tomobit chat [--provider claude-code|codex|human|auto] [--cap <capability>] ["<prompt>"]
                   # 対話セッション。1つの会話 = 1つのタスク = 1つの経験。
                   # /new か /exit で区切ると Feedback→知覚→Tomoの質問→鏡 が走る
tomobit do [--provider claude-code|codex] [--cap <capability>] "<prompt>"
                   # 非対話の一発（スクリプト向け）。区切りの器官はchatと同じ。
                   # 分割プロトコルは常時ON: Providerが「難しすぎる/独立に分けられる」と
                   # 提案したらサブタスクとして実行（autoなら実行者は台帳が選ぶ）。
                   # 独立群を宣言されたら実行直前に y/N で並走可否を聞く（既定N=全逐次、
                   # 概算コストを実測中央値から提示。並走は[n:provider]表示。非TTYは
                   # 常に逐次 — ADR-0023/0028。config split_protocol=false で止める）
tomobit record  --session <id> --type <event.type> [--json '{...}']
tomobit perceive   # 未知覚セッションをOllamaかMLX LM Server（既定はconfig/OS解決 — ADR-0029）で経験化しConnectionへ反映
tomobit rebuild    # 射影を破棄しexperiencesから再構築（決定的 — 姿も再現される）
tomobit forget  --id <exp-id> [--id ...] | --session <id>  [--yes]
                   # 経験/セッションの物理削除（自動rebuild+VACUUM。非TTYは--yes必須。
                   # 忘却は人間の動詞 — ADR-0033）
tomobit amend   --id <exp-id> [--context '<json>'] [--outcome '<json>'] [--provider <name>]
                   # 経験の訂正＝人間による再知覚（追記・自動rebuild — ADR-0033）
tomobit status     # 相棒ビュー（見て終わる）
tomobit-face       # Tomoのマスコット窓（表示専用・DBは読み取りのみ — ADR-0020）
```

姿は窓が担う: `chat` / `do` / `status` を端末（TTY）で使うと顔窓が自動で出る。
止めるなら `config.json` の `"face_auto_launch": false` か `TOMOBIT_FACE=0`（ADR-0025）。
窓の寿命は対話に接地する（ADR-0027）: 既定では `chat`/`do` が生きている間だけ居て、
最後の対話が終わると数秒後に自閉する（`status` 単体は約3秒の一瞥）。手で閉じるまで残す
従来挙動が欲しければ `config.json` の `"face_resident": true` か `TOMOBIT_FACE_RESIDENT=1`。

チャットの中では `/new`（区切って次のタスク）・`/provider`・`/cap`・`/size`・`/status`・`/help`・`/exit`。
入力は ↑↓履歴・Ctrl-A/E/W/U・複数行貼り付け（そのまま1つの依頼になる）・Shift+Enter か `\`+Enter で改行。
実行中の Ctrl-C はそのターンの中断で、タスクは続く。

DBは `~/.tomobit/tomobit.db`（`--db` / `$TOMOBIT_DB` で変更可）。
実装時ノブ: 減衰半減期・事前分布の継承・backoffブレンド → [CONNECTION_ENGINE.md の Open Questions](docs/core/CONNECTION_ENGINE.md#open-questions)
