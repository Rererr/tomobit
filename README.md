# tomobit

> **Tomobit is not built to use AI. Tomobit is built to grow with it.**

複数のコーディングAIの前に立ち、経験（Experience）からConnectionを育てる **Living Harness**。

## Getting Started

前提:

- Go 1.26+
- Provider CLI — [claude](https://claude.com/claude-code)（claude-code）/ codex の少なくとも一方が PATH にあること
- 知覚バックエンド（任意・推奨）— [Ollama](https://ollama.com) または mlx-lm。
  無くてもタスク実行はでき、知覚（Connectionを育てる素材の抽出）は保留と表示され、
  後から `tomobit perceive` で追いつける。選択の背景は [ADR-0029](docs/decisions/ADR-0029-perception-backend-choice.md)

```sh
git clone https://github.com/Rererr/tomobit.git && cd tomobit
go install ./cmd/tomobit ./cmd/tomobit-face   # tomobit-face は顔窓（無くても動く。警告1行で続行）
tomobit setup    # 対話式で配線を決める(claude profile / 知覚バックエンド / 顔窓) → ~/.tomobit/config.json
tomobit          # 相棒ビュー → そのまま対話へ。単発なら `tomobit do "..."`
```

`do` / `chat` の `--provider` の既定は **auto** — Tomoが台帳の経験から実行者を選ぶ
（[ADR-0043](docs/decisions/ADR-0043-auto-by-default.md)）。autoの候補になるのは実際に
起動できるProviderだけなので、codex未導入でもそのまま動く。`--provider claude-code` の
明示指定は今までどおり効く。

台帳は `~/.tomobit/tomobit.db` に住む（[経験主権 — ADR-0018](docs/decisions/ADR-0018-experience-sovereignty.md)）。
コマンド一覧は `tomobit help`。初期導入の設計は [ADR-0021](docs/decisions/ADR-0021-onboarding.md)。

デスクトップGUIは別リポジトリ [tomobit-gui](https://github.com/Rererr/tomobit-gui)。

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
- [ADR-0002](docs/decisions/ADR-0002-surprise-and-split-judgment.md) — Surpriseの定義（超過surprisal）とSplit有意判定（補正付きln BF＋ヒステリシス。Merge側の安全弁「誤Splitは減衰で静かに畳まれる」は減衰だけでは働かないと実測で判明 — ADR-0037）
- [ADR-0003](docs/decisions/ADR-0003-outcome-and-preference.md) — Outcome三層信号、能力/好みの二重Connection、Tomoの質問
- [ADR-0004](docs/decisions/ADR-0004-tech-stack.md) — 技術選定（Go / SQLite真実と射影の分離 / Ollama＋Deferred Perception / 段階的デーモン化。知覚バックエンド選択はADR-0029で一般化）
- [ADR-0005](docs/decisions/ADR-0005-perception-model-and-schema-boundary.md) — 知覚の実装（qwen3:8b確定 / schemaは「形」・プロンプトは「意味」）
- [ADR-0006](docs/decisions/ADR-0006-executor-integration.md) — Executor統合（`tomobit do` / claude-code Adapter / ダイジェスト記帳 / Feedback）
- [ADR-0007](docs/decisions/ADR-0007-curiosity-question.md) — Curiosityの最初の器官（Preference GapはView / 質問予算はeventsから導出 / doの区切りでTomoの質問。発火条件（対人ゲート）はADR-0035が改定）
- [ADR-0008](docs/decisions/ADR-0008-appearance.md) — Tomoの姿（成長ステージはView / ドット絵＝半ブロック＋ANSI / 依存ゼロ — 端末描画はADR-0025で廃止）
- [ADR-0009](docs/decisions/ADR-0009-voice.md) — Tomoの声（発話＝Viewの写像 / LLM不使用 / 語調は確信度のView）
- [ADR-0010](docs/decisions/ADR-0010-codex-adapter.md) — 2つ目のAdapter（codex / `do --provider` / 写像はエラー経路実採取＋仕様準拠）
- [ADR-0011](docs/decisions/ADR-0011-meaning-by-model-judgment-by-math.md) — Meaning by Model, Judgment by Math（判断は純関数、LLMの座席はextractorのみ）
- [ADR-0012](docs/decisions/ADR-0012-decision-rule-thompson-sampling.md) — 決定則＝Thompson Sampling（探索は好みの側で、ミスは構造になる。Decision 3の名誉回復は継承事前下で未解決 — ADR-0037実測／ADR-0038 Proposed）
- [ADR-0013](docs/decisions/ADR-0013-prior-inheritance-mean-only.md) — 事前分布の継承（平均だけ継ぎ、確信は継がない）
- [ADR-0014](docs/decisions/ADR-0014-plan-learning-same-ledger.md) — Plan学習（台帳は賭ける対象を選ばない）
- [ADR-0015](docs/decisions/ADR-0015-reflection.md) — Reflection（第一級の器官、実体は射影、核は双方向性。発火条件（対人ゲート）はADR-0035が改定）
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
- [ADR-0033](docs/decisions/ADR-0033-organ-of-forgetting.md) — 忘却の器官（忘却は主権の行使＝人間の動詞・Tomoは提案も実行もしない / `forget`＝物理削除+VACUUM・`amend`＝人間による再知覚（追記） / 人の手が入ったセッションは再知覚しない / 直後に自動rebuildで射影整合。忘却の到達範囲（行単位か世代単位か）はADR-0034が改定）
- [ADR-0034](docs/decisions/ADR-0034-forgetting-reach.md) — 忘却の到達範囲（`forget --id`は指名行だけでなく同一(session,kind)の下位世代も削除し版数の巻き戻りを防ぐ / 現行世代でないidの指名はエラー / 巻き添え行数は`+N superseded rows`で告知 — ADR-0033 Decision 2/5を改定）
- [ADR-0035](docs/decisions/ADR-0035-boundary-organs-reach-the-pipe.md) — 対人ゲートの改版（Tomoの質問・鏡の発火条件を`isTTY(os.Stdin)`から`isTTY(os.Stdin) || --view ndjson`へ拡張しGUI経由でも境界の器官が届くようにする / 対人（`humanPresent`）と端末描画（`c.interactive`）の述語を分離 — ADR-0007/ADR-0015を改定）
- [ADR-0036](docs/decisions/ADR-0036-task-perception-wiring.md) — 判断が読むトークン（判断は`cap=`1トークンしか読まず粒度1のConnectionとSplit子は到達不能だった / Decision 1は決定的に既知の`size`もトークンにする / Decision 2はタスク記述をextractorに通して`lang`/`framework`/`topic`を判断へ配線＝遅延して1タスク1回・誰も読まないなら叩かない・期限ノブは置かない・判断の記録は抽出プロンプトから外す・事前知覚と事後知覚のズレは埋めず監査に残す）
- [ADR-0037](docs/decisions/ADR-0037-merge-reachability.md) — 継承事前の下での名誉回復を試みるも実測で頓挫（μ<0.48の親から生まれた子は減衰しても悲観ゲートを通らず、選ばれないから経験も来ずmerge判定機会も来ない自己強化デッドロック / merge判定の到達性は修復したが、実測でln BFはThetaMerge=0へ「上から」漸近するのみで実質到達不能（約15年）と判明 — 名誉回復は未解決のままADR-0038へ引き継ぐ）
- [ADR-0038](docs/decisions/ADR-0038-gate-under-inherited-priors.md) — 継承事前下の能力ゲート（悲観ゲートの基準線を`q−margin`から`min(q, PriorQuantile(q))−margin`へ一般化し、低いμを継いだ子の恒久ゲート落ちを解消 / 一様事前の根では今日と同一判定・継承事前でも今日より厳しくならない片側緩和 / 定数0.20は一様事前のq分位点を書き下したたまたまの値だった — ADR-0012 Decision 3の未解決点への回答、ADR-0037 Decision 1を改版）
- [ADR-0039](docs/decisions/ADR-0039-status-machine-view.md) — 相棒ビューの機械可読view（`tomobit status --view json` でstage/mood/speakを1オブジェクトで出す / 台帳が無ければ作らず`exists:false` / 顔窓起動・挨拶記帳なし — GUIのstage移植570行を廃し、台帳を書くバイナリ自身が導出する。Proposed）
- [ADR-0040](docs/decisions/ADR-0040-decision-audit-view.md) — 判断の監査行をviewへ流す（`tomo.decided`に記帳済みのCandidates〈分位点・ゲート・勝ち数〉を`chat --view ndjson`の`decided`イベントとしても流し、GUIが「なぜこのProviderか」を開示可能にする / 声は不変・既定は畳む — ADR-0011根拠3の監査可能性を表示経路へ延伸。Proposed）
- [ADR-0041](docs/decisions/ADR-0041-out-of-order-perception.md) — 順序外の知覚は正典に立ち返る（遅延知覚がlive射影を減衰重み1.0で汚し無期限に残る実測 / バッチが既知覚より古ければlive Applyを捨ててrebuild — forgetの自動rebuildと同じ姿勢。Proposed）
- [ADR-0042](docs/decisions/ADR-0042-split-starvation-and-lexical-shadowing.md) — Splitの飢餓と辞書順の遮蔽（均衡混合でexcess surprisalが発火せず、同粒度tie-breakの辞書順でlang=系Connectionが系統的に読まれない — 11連敗Providerが最頻選択される実測 / 対案5件を実測で序列づけ、対案2「選ぶのは一つ、拒否は同粒度の全員」を先行適用。ADR-0013 Decision 2改版。所有者の追認待ち）
- [ADR-0043](docs/decisions/ADR-0043-auto-by-default.md) — 既定をautoへ（実台帳41セッション全てclaude-code・tomo.decided 0件＝判断の器官が既定経路で一度も呼ばれていない実測 / do・chatの`--provider`既定をautoにし、候補は起動できるProviderに限る＝環境の不備をProviderの能力の証拠にしない / 起動できなかった実行は非ゼロ終了・経験にもしない / humanは知っている文脈でのみ候補 / GUIはProviderを明示的に持ち既定auto — ADR-0010 Decision 1・ADR-0018 Decision 2を改版。Accepted・配備済み）
- [ADR-0044](docs/decisions/ADR-0044-provider-quota-observation.md) — Providerの残量観測（公式手段は無いが、自分のOAuthトークンでベンダー自身のusage端点を読む経路が実在する〈着想元CodexBar・MIT・独立実装〉 / 経路A採用を推奨・PTYスクレイプとブラウザcookie読みとcodexbar依存は却下 / 取れないときは「不明」＝推定値を出さない・決定則には混ぜない / curiosity予算への残量ゲートと`quota.observed`記帳を提案。実端点の実証は所有者許可待ち。Proposed）
- [ADR-0045](docs/decisions/ADR-0045-stage-needs-a-real-choice.md) — 鋭さは選択肢が無ければ測れない（候補1つだとWobbleが構造的に常に0で「おとな」へ素通り・2Providerだと90セッションでも未到達の正反対の病理を合成dogfoodで実測 / 鋭さは競争のある島でのみ測る・較正判定に標本数を要求〈ThetaCalMin=8.0実測較正〉・S5「あいぼう」は自分との比較を知っていること — ADR-0017の3ゲートを精密化。Accepted・配備済み）
- [ADR-0046](docs/decisions/ADR-0046-growth-is-legible.md) — 次の段に何が足りないかを開示する（成長は段が変わる瞬間しか見えず、しかも3ゲートが同時に開いて段が踏まれない＝育てている実感が生まれない / `status --view json`に`growth`〈各ゲートの値・閾値・充足〉を足し、測定不能と未達を別の顔で出す / 声は不変・見せ方はレンダラの裁量・既定は畳む — ADR-0040 Decision 2と同じ二層。Proposed・未実装）
