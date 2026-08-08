# tomobit

> **Tomobit is not built to use AI. Tomobit is built to grow with it.**

[![DOI](https://zenodo.org/badge/DOI/10.5281/zenodo.21553016.svg)](https://doi.org/10.5281/zenodo.21553016)
[![CI](https://github.com/Rererr/tomobit/actions/workflows/ci.yml/badge.svg)](https://github.com/Rererr/tomobit/actions/workflows/ci.yml)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](LICENSE)

複数のコーディングAIの前に立ち、経験（Experience）からConnectionを育てる **Living Harness**。

*English: [README.en.md](README.en.md)（正本はこの日本語版）*

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
- [docs/core/REFLECTION.md](docs/core/REFLECTION.md) — 気付きを人へ映す双方向のミラー（語る器官）

### 実行アーキテクチャ
- [docs/core/EXECUTION_MODEL.md](docs/core/EXECUTION_MODEL.md) — Intent → Plan → Capability → Provider → Executor → Runtime
- 全体ライフサイクルの初期構想（Workflow / WorkflowStep）は実装されず、
  [docs/archive/STATE_MACHINE.md](docs/archive/STATE_MACHINE.md) へ退避した

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
- [ADR-0012](docs/decisions/ADR-0012-decision-rule-thompson-sampling.md) — 決定則＝Thompson Sampling（探索は好みの側で、ミスは構造になる。Decision 3の名誉回復は継承事前下では効かず、ADR-0037の実測を経てADR-0038が解決）
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
- [ADR-0039](docs/decisions/ADR-0039-status-machine-view.md) — 相棒ビューの機械可読view（`tomobit status --view json` でstage/mood/speakを1オブジェクトで出す / 台帳が無ければ作らず`exists:false` / 顔窓起動・挨拶記帳なし — GUIのstage移植570行を廃し、台帳を書くバイナリ自身が導出する。Accepted・配備済み）
- [ADR-0040](docs/decisions/ADR-0040-decision-audit-view.md) — 判断の監査行をviewへ流す（`tomo.decided`に記帳済みのCandidates〈分位点・ゲート・勝ち数〉を`chat --view ndjson`の`decided`イベントとしても流し、GUIが「なぜこのProviderか」を開示可能にする / 声は不変・既定は畳む — ADR-0011根拠3の監査可能性を表示経路へ延伸。Accepted・配備済み）
- [ADR-0041](docs/decisions/ADR-0041-out-of-order-perception.md) — 順序外の知覚は正典に立ち返る（遅延知覚がlive射影を減衰重み1.0で汚し無期限に残る実測 / バッチが既知覚より古ければlive Applyを捨ててrebuild — forgetの自動rebuildと同じ姿勢。Accepted・配備済み）
- [ADR-0042](docs/decisions/ADR-0042-split-starvation-and-lexical-shadowing.md) — Splitの飢餓と辞書順の遮蔽（均衡混合でexcess surprisalが発火せず、同粒度tie-breakの辞書順でlang=系Connectionが系統的に読まれない — 11連敗Providerが最頻選択される実測 / 対案5件を実測で序列づけ、対案2「選ぶのは一つ、拒否は同粒度の全員」を先行適用。ADR-0013 Decision 2改版。Accepted（対案2）・配備済み。対案3=Split召喚のVoI配線は別ADRの論点として残る）
- [ADR-0043](docs/decisions/ADR-0043-auto-by-default.md) — 既定をautoへ（実台帳41セッション全てclaude-code・tomo.decided 0件＝判断の器官が既定経路で一度も呼ばれていない実測 / do・chatの`--provider`既定をautoにし、候補は起動できるProviderに限る＝環境の不備をProviderの能力の証拠にしない / 起動できなかった実行は非ゼロ終了・経験にもしない / humanは知っている文脈でのみ候補 / GUIはProviderを明示的に持ち既定auto — ADR-0010 Decision 1・ADR-0018 Decision 2を改版。Accepted・配備済み）
- [ADR-0044](docs/decisions/ADR-0044-provider-quota-observation.md) — Providerの残量観測（公式手段は無いが、自分のOAuthトークンでベンダー自身のusage端点を読む経路が実在する〈着想元CodexBar・MIT・独立実装〉— 所有者許可の下で実測済み・両端点200 / claudeの資格情報はプロファイル依存Keychain（サービス名=sha256(configDir)先頭8桁）・取り違えは401でなく429に化ける実測→エラーに資格情報の出所を必ず載せる / 経路A採用を推奨・PTYスクレイプとブラウザcookie読みとcodexbar依存は却下 / 取れないときは「不明」＝推定値を出さない・決定則には混ぜない / curiosity予算への残量ゲートと`quota.observed`記帳は将来ADRへ保留。Accepted・経路A採用し`status --view json`／人間view／GUIへ配線済み・実端点疎通のみ所有者確認待ち）
- [ADR-0045](docs/decisions/ADR-0045-stage-needs-a-real-choice.md) — 鋭さは選択肢が無ければ測れない（候補1つだとWobbleが構造的に常に0で「おとな」へ素通り・2Providerだと90セッションでも未到達の正反対の病理を合成dogfoodで実測 / 鋭さは競争のある島でのみ測る・較正判定に標本数を要求〈ThetaCalMin=8.0実測較正〉・S5「あいぼう」は自分との比較を知っていること — ADR-0017の3ゲートを精密化。Accepted・配備済み）
- [ADR-0046](docs/decisions/ADR-0046-growth-is-legible.md) — 次の段に何が足りないかを開示する（成長は段が変わる瞬間しか見えず、しかも3ゲートが同時に開いて段が踏まれない＝育てている実感が生まれない / `status --view json`に`growth`〈各ゲートの値・閾値・充足〉を足し、測定不能と未達を別の顔で出す / 声は不変・見せ方はレンダラの裁量・既定は畳む — ADR-0040 Decision 2と同じ二層。Accepted・実装済み）
- [ADR-0047](docs/decisions/ADR-0047-workspace-is-wiring.md) — 働く場所は配線である（作業ディレクトリと読み取り先が claude 固有の env 経由でしか渡らず、codex を選ぶと無言で効かなくなっていた / `executor.Request` に `WorkDir`・`AddDirs` を持たせ cwd は Executor が落とし `--add-dir` は各 Adapter が翻訳・chat に `/cd` と `/add-dir` を追加〈他の配線と同じくタスク境界でだけ替わる〉 / 台帳には書かない — 配線は経験ではない。Accepted・実装済み）
- [ADR-0048](docs/decisions/ADR-0048-sprite-machine-view.md) — 姿の機械可読view（第三のレンダラが姿を欲しがったが、資産を書き写せば正本が割れ `internal/` は import できない — ADR-0039と同じ構造の問題 / `tomobit-face --view json` が格子・パレット・気分記号の座・アニメのノブを1オブジェクトで配り、窓も台帳も顔ロックも触らずに返る / 口は主CLIでなく顔窓側＝資産を持つ者が配る・Ebiten依存を端末利用者へ伝播させない / 犬種は導出せず引数のまま — ADR-0020 Decision 5。Accepted・実装済み）

- [ADR-0049](docs/decisions/ADR-0049-quota-observation-is-opt-in.md) — 沈黙は同意ではない（ADR-0044の「所有者許可の下で実測済み」は所有者の機械にしか及ばず、公開すると全員の機械で無言で走る / `quota_observe` を追加し**他のポインタboolと非対称にnil=OFF**＝降格の事故より無言の実行の事故が重い / 無効時は行を出さず「不明」を使わない＝訊いていないのにその語を出すとviewで唯一正直な言葉が嘘になる / setupの質問文にコストを書く＝理解されていないopt-inは同意ではない / ADR-0044は撤回せず既定値だけを変える・既存の機械も一度OFFに落ちる＝自分の機械にも同じ規則を適用する。Accepted・実装済み）

- [ADR-0050](docs/decisions/ADR-0050-workspace-isolation-protocol.md) — 作業場は宣言で分ける（duel の2本・複数の起動・GUIの四分割が同じ作業ツリーを共有し、汚染した実行が `provider.error`→y=0 で**Providerのせいでない泥**を台帳に入れる / 分割・隔離と同じプロトコル型で「このプロジェクトが使うバージョン管理の隔離手段で作業場を分けよ」とだけ渡す＝`git` という語を持たない・`kind` は自由文字列 / **宣言はさせるが検証はしない**＝台帳に載るのは「Providerがそう宣言した」であって「隔離された」ではない・`isolated:false` は失敗でなく正常な返答 / 隔離の単位はセッション木＝サブタスクは親を継ぎ〈群間依存を壊さない〉duelの子だけ各自持つ・群内並走の共有は塞がない / 置き場は tomobit が渡し後始末は人がする＝マージも掃除もVCSの語彙。Accepted・実装済み〈Phase 1〜3〉。実 claude で一発追従を実測）
- [ADR-0051](docs/decisions/ADR-0051-orchestration-is-judged.md) — 分け方は分け方として評価する（分割は常時ONなのに、分け方を返した親は `do` では無傷・`chat` では会話全体と混ざったまま＝**采配が学習の外にある** / 采配は「分け方＝Provider」と「割り当て＝決定エンジン」に割れ、後者は既にループが閉じている / 能力とは別の事実なので capability に混ぜない〈ADR-0003 D2の理屈〉が、標本の壁〈ADR-0045〉があるので**信号だけ貯めてConnectionは作らない** / 良かった日は Feedback に相乗り・芳しくなかった日だけ1問増える＝摩擦が結果に比例 / ADR-0028が却下した「クレジット割当」とは向きが逆で、按分しないので配分比も相関も生まれない。Accepted・実装済み。起草時の見立ては実台帳に否定され理由を差し替えた）
- [ADR-0052](docs/decisions/ADR-0052-first-layer-is-observed.md) — 第1層を生やす（ADR-0003の三層のうち第1層と第2層は**書き手がコードに存在せず**、実台帳の `test.result` は0件だった / ADR-0006 D3 の先送りの理由〈子プロセス内のテスト実行を外から決定的に識別できない〉は正しいまま、**tomobit自身が走らせれば識別が要らない**ので無効化する / Providerには宣言させない＝宣言は Beta を動かす資格がない〈自分の成績表を書かせない〉・観測手段がある以上「観測できるものは観測する」 / テストの走らせ方は配線〈ADR-0047〉なので config に「働く場所→コマンド」・書かなければ1バイトも動かない / 起動失敗もタイムアウトも記帳しない＝成果物についての判定ではない / 分割の子には走らせない＝群間逐次では途中の赤が正常な中間状態。Accepted・実装済み。主観Feedbackゼロで Beta が動くところまで実測）

- [ADR-0053](docs/decisions/ADR-0053-permission-is-asked.md) — 許可は人が与える（GUIから起動したProviderがファイルを書く仕事をできなかった＝`--permission-mode` が誰からも渡っていない / **気持ちよく使うこととパーミッションは別**なので `bypassPermissions` を既定にはしない〈GUI ADR-0007 が「実行経路は便利さのために既定で開けてよい口ではない」と切った判断を無言で覆すことになる〉 / 既定は auto、ただし**語彙は Adapter が訳す**＝tomobitは中立3語(auto/strict/open)だけを持ち claude の permission と codex の sandbox は**同型ではない**と明記 / **許可要求はターンを終わらせる**（実測: `--input-format stream-json` でも control が来ない）ので「その場で続行」ではなく「許可 → 再実行」・費用がもう一度かかることを問いに書く / 粒度は道具・寿命はセッション・ディスクに書かない〈覚えた許可は次に人が見ていない時にも効く〉／**分割の子はこの寿命の内側**〈2026-08-08 に実装漏れを修正。`PermissionMode` は継ぎながら `AllowedTools` だけ落ちており、子は許可済みの道具を訊き直すか、無人なら人が既に許した作業を拒否で落としていた〉 / 許可は配線であって経験ではないので台帳に書かず、権限で止まったターンを `provider.error` にもしない〈人が渡さなかった判断がProviderの成績になる〉。Accepted・実装済み。ADR-0050の隔離と噛み合って初めてGUIから実作業が通った）
- [ADR-0054](docs/decisions/ADR-0054-a-child-is-the-breakdown.md) — 子は親タスクの内訳である（ADR-0028 D5 が子に与えた客観信号は実台帳で**一度も鳴っていなかった**＝13セッション・全体の27%がConnectionを1ミリも動かしていない / 論点を詰めたら**コードの事実が設計より先に答えを決めた**: ①ADR-0013の親子は Connection の scope の親子でタスクの親子ではない＝子専用の棚も親子を結ぶ線も無い ②`auto` は子ごとに `autoDecide` を呼ぶが**親の知覚トークン**で引いており、記帳は**子の意味**の棚へ落ちる＝**引いた腕と更新する腕が違う** ③分割の子の Request に `WorkDir` が無く、**別のリポジトリで作業していた** / 所有者の判断は「親の意味で統一する」＝子は独立した発注ではない〈人事として、採用したメンバーがオフショアした先のチームまでは評価しない — 引いているのは木の深さではなく**契約の深さ**の線で、`auto` でも tomo は子を見て選んでいないので契約が発生していない〉 / D1 決定はタスクにつき1回・子は親の相手で走る〈`tomo.decided` も human抽選も子から消える〉 / D2 子は経験にしない・イベントは残す〈副産物として**片側だけの証拠**も外れる: 失敗だけが y=0 で乗る形では、成功率95%のProviderが未計測のProviderより下に並ぶ〉 / D3 子は親の作業場所で働き、隔離が宣言されていればその中で働く / 却下: 子ごとに意味を取り直す〈知覚コスト4倍・子は親の文脈を持たない・tomobitがオーケストレーションを始める〉、一様1/Kで主観を按分〈人は子を1つも見ていない〉。Accepted・実装済み。ADR-0026 の duel だけが唯一の例外）
- [ADR-0055](docs/decisions/ADR-0055-verdict-is-a-veto.md) — 判定は拒否権である（ADR-0003 の三層のうち第2層 `user.verdict` だけが初期カタログに載ったまま**6年間書き手を持たなかった** / `OutcomeWeight` の導出を縦に読むと、**機械が人より上に立つ場所は `TestsPassed=false` ただ1つ**〈`provider.error` は既に as-is に負け、3=だめだった は赤と同じ向き〉＝**第2層が要る瞬間は「赤テスト × 1=文句なし」の1点に特定できる** / だから第1層が空だった間は上書きすべき対象が無く、**ADR-0052 が第1層に書き手を生やした日に初めて必要になった** / D1 境界で矛盾した時だけ1問〈ADR-0003 の pull 型反転・ADR-0051 と同じ「摩擦は結果に比例」。2=まあまあ は含めない — 所有者決定。既定N＝沈黙は同意ではない。**観測は消さず**赤と判定が経験の中で共存し導出で強い方が勝つ〉 / D2 `tomobit verdict <sid> up\|down\|clear`＝「まだ言えない」〈実台帳で訊かれた26回中8回〉の受け皿。**イベントが真実で、copy-forward が運ぶのは永続性ではなく即時性**〈以後の再知覚が読み直すので判定は不滅〉。amend と違い execution 1行だけ動かす〈`experiences_current` は kind 単位で、execution はセッションに1行〉 / D3 出自は書き換えず凍結もしない＝**判定だけを差し替えたので、モデルの主張である context には1バイトも触れていない**〈拒否権の行使に税金をかけない〉 / 却下: chat 内 `/verdict`〈成果物未完成の時点の判定は構造的に時期尚早〉、効果を改版待ちにする案〈`test.result` と同じ死に方をする〉。Accepted・実装済み。書き手Aは `test_commands` が配線されるまで休眠する＝第1層への拒否権なので連動休眠が正しい）

- [ADR-0056](docs/decisions/ADR-0056-independence-is-trusted.md) — 独立宣言は信じる（ADR-0028 の残った宿題。並走ゲートが `interactive`〈stdin と stdout の両方が TTY〉を読んでおり、`--view ndjson` が stdout を非TTYに強制する以上 **GUI では構造的に永久に false** だった＝$613 使って並走の提示が0回 / 所有者の観察が問いの形を変えた: **人は大規模タスクを窓に分けず丸ごと投げる**ので「窓の軸」と「分割の軸」は別で、1タスクを速くできる経路は分割の並走だけ / 残った問いは1つ＝**Providerの「この2つは独立だ」を信じるか** / 信じる側へ倒した — ①丸投げの相手を分け方でだけ疑う一貫性の無さ ②ゲートは**答えられない人に訊いていた**〈人はサブタスク文を読んでいない＝同意ではなく手間の増えたコイントス。独立性は意味の問題でモデルの席〉 ③ADR-0054 が子の経験を消したので**壊れても台帳は汚れず**、壊れた成果物は第1層の赤か「3=だめだった」として**親の経験に乗る**＝信じた結果が信じた本人の成績で返る / D2 費用は訊かずに始める前に1行言う〈GUI ADR-0009 の「判断せず事実として言う」〉 / D3 kill switch `parallel_subtasks`＝信じることと止められることは別 / D4 `parallel_offered`/`parallel_accepted` は記帳をやめる〈提示も回答も無いのに false を書くのは「断られた」と読める嘘〉 / 却下: ゲートをGUIへ通す〈通しても人は答えられない〉、常時逐次〈`parallel_subtasks=false` がそのままこの立場〉、並走の単位を窓にする〈所有者の観察が直接否定〉。Accepted・実装済み。実 Provider が非TTY入口で3本を47ms以内に起動するところまで実測。**残った宿題: 並走の子は view フレームを持たずラベル付き note で届く**）

- [ADR-0057](docs/decisions/ADR-0057-reaction-is-the-answer-placed-early.md) — 反応は、締めの答えを先に置く口である（GUIでは窓の×を押してから評価に答えるまで閉じない＝**帰ろうとしている人を、評価のために引き止めている** / 所有者の不満は2つに割れる〈①タイミング ②何往復もした会話を1回の三択へ潰している〉が、**足りないのは粒度ではなく「置ける瞬間が1つしか無い」こと** / ADR-0055 が却下した「開いているタスクへの判定」は**生きている**＝第2層は機械の観測への拒否権なので、観測が立つ境界より前には立てられない。本ADRが会話へ散らすのは判定ではなく **`task.finished` の adopted/reverted そのもの**で、これは上書きの相手が存在せず**最初から人しか持っていない**一次情報 / D1 `/react <turn> up|meh|down|clear`＝走行中のタスクへ追記〈語は verdict と揃える・**ターン番号は必須**〈省略すると走行中に押した反応がどのターンに乗るかを競合が決める〉・区切りの上では断って `tomobit verdict <sid>` へ送る・n は検証しない〉 / D2 反応が置かれていれば**締めでは訊かない**〈読むのは最後の1件の word だけ・訳は問いの 1/2/3 と同じ1枚の表・clear は「答えない」へ戻すので訊く・**黙って記録しない**〉 / **ADR-0052 D5 の保証（赤は採点する人が持っているべき事実）は構造的に破りかける**〈反応は赤を見る前に置かれうる〉が、赤×up の矛盾の問い（ADR-0055 D1）は payload の形が同じなので**配線1バイトも変えずにそのまま立つ**＝**締めが軽くなるのは、軽くしてよい日だけ** / D3 view に `reaction` を足し、語彙は `init` が配る〈消費者に語を持たせない・clear は配らない〈取り消しは語ではない〉・受け取らなかった消費者は口を出さない＝劣化は沈黙〉 / 却下: 反応を `task.finished` へ直接書く〈まだ喋っているセッションが知覚の列に並ぶ〉、絵文字の自由集合〈翻訳先の無い記号を貯めても Tomo は何も学べない〉、反応があっても確認だけ出す〈押させる儀式が1つ減るだけで、待たせている事実は変わらない〉。Accepted・実装済み）

- [ADR-0058](docs/decisions/ADR-0058-borrow-only-the-idea.md) — 借りるのは発想だけ（外部実装〈langchain・MIT〉を5件まとめて読んだが、問われたのは「どれが良いか」ではなく**どこまで持ち帰ってよいか**だった / 危険はライセンスの非互換ではなく**無表示のコピーと翻案**で、Python→Go の書き直しは免罪符にならない / D1 持ち帰るのは発想と責務の分け方だけ＝コード片・識別子・既定値・テストケースの選択と配列・データは持ち込まない〈帰属表記が要るのは表現を持ち込んだときだけ・予防的に先に置かない〉 / D2 読む工程と書く工程を分ける＝実装の入力は ADR に書いた日本語仕様だけ〈法的要件ではなく、後から説明できる状態を記憶でなく手続きで持つため〉 / D3 `docs/` は CC BY-SA なので転載しない・着想元は ADR-0044 と同じ1行で示す / D4 採否を5件＋7件の表で確定 / 却下: `THIRD_PARTY_NOTICES.md` の予防的設置、参照元を読まない完全な clean room。Accepted・規律のみでコード変更は無い。**残った宿題: D4 の #2 Adapterの共通契約試験 / #3 Adapter差の宣言 / #4 実行単位の測定値の正規化 は、着手時にそれぞれ別ADRを起こす**）

- [ADR-0059](docs/decisions/ADR-0059-admission-is-counted-by-the-machine.md) — 弁は、食い合う側に置く（`parallelWidthCap` は `runGroupParallel` ごとに作られる**群ローカルの弁**で、GUI 4窓 × 3本＝**1つの GUI で 12本**の Provider CLI が立ちうる。2つ目の GUI・端末は数にも入らない / 食い合う相手はアカウントの窓なのに、**上限だけがプロセスローカルに置かれている** / D1 財布を2つに分ける＝**席**〈1作業系列に1つ・拒まない〉と**プール**〈並走ぶんだけ・取れなければ待つ〉＝人が頼んだ1本は決して止めず、tomobit が自分で増やした分だけを絞る〈起草中の単一財布案は所有者の指摘で崩れた〉・キーはアカウント〈`claude_config_dir` が違えば別〉 / D2 待ちは失敗でも経験でもない＝Provider の実行時間に混ぜない・台帳に載せない / D3 数は言う、判断はしない＝残量観測〈ADR-0044/0049〉は入場制御に**繋がない** / D4 プールは 2・kill switch `provider_admission`〈nil = ON〉、「実測してから決める」は非決定的な量なので採らない / 却下: 残量連動の動的プール、並走の単位を窓にする、プロセス内セマフォ。**Rejected（2026-08-08 所有者裁定）— 穴は数えたが被害は観測されておらず、根拠は掛け算だけだった。手動では同等以上〈端末6つ・claude-codeからcodex・Ghosttyの複数窓〉が日常的に立っており、tomobit経由だけを絞るほうに理由が要る。ADR-0056 で「信じる」に倒したものを別の口から減らすことにもなる。唯一の擁護材料〈瞬間密度〉は `quota_observe` の受動観測で窓を焼かずに測れた＝測れるものを測れないことにしていた。本文は起草時のまま残す — 逼迫が実測された日の出発点にする**）

- [ADR-0060](docs/decisions/ADR-0060-the-executor-can-be-named.md) — 采配は Provider のもの（所有者の「codex でも並列で実行して欲しい」に対し、claude-code が **Bash で codex CLI を起動して**満たした実測 — 台帳には `claude-code` の成果としてだけ残る / 責めるべき相手はいない＝分割プロトコルに「誰にやらせるか」を書く場所が無く、**ADR-0054 は席の名前を決めてそこを空席のまま置いた** / D1 判断は運ばない、しかし指示は運ぶ〈tomobit が自然文を解釈して切り替えるのは ADR-0011 違反・Provider が宣言し tomobit が写すのは既存の作法〉 / D2 群の要素を文字列 または `{"do":…,"provider":…}` に拡張〈文字列は今日どおり親を継承＝既定は不変・未知の Provider 名はその子だけ親継承に落として警告・`human` の指名は受理しない〉 / D3 プロトコル文に2行〈名指しされたら宣言で表せ／自分で別の CLI を起動するな〉 / D4 指名は記帳する＝選択バイアスがあるので抽選の標本と後から分離できる形で残す / 却下: 道具箱からの剥奪〈`curl` も MCP も塞げない — 塞いだつもりになるほうが危ない〉、duel をユーザーから呼べるようにする〈判定の形が噛み合わない〉。ADR-0054 D1 改版。Accepted・**実装済み**〈記帳先は `task.started` の `named_provider`＝アダプタが出す `provider.selected` を書き換えないため・宣言された名だけを残し親継承に落ちた子はキーを持たない・親が human のときも指名を受理しない〈ADRの穴を実装時に埋めた〉・プロトコル文の例は文字列のままで対の形は規範行の中で示す〉。**実測（2026-08-08・計 $0.50）: 実 claude-code が道具箱でなく対の形で宣言し、`[1:claude-code]`と`[2:codex]`が並走。指名された子だけが `named_provider` を持ち `provider.selected` も codex になった＝台帳が真実を語るようになった。codex の子は `cost_usd` を持たない〈ADR-0028 の既知の非対称〉**。**残った宿題: GUI の画面目視と、子ごとに相手が違う場合を固定する GUI 側テスト**〈Consequences の「チップに出る」は記述が古い — GUI ADR-0014 が常時チップを廃止済みで、現在はメタ1行〉）

## 貢献 / セキュリティ

- [CONTRIBUTING.md](CONTRIBUTING.md) — **CLAは採りません**（DCO / `git commit -s`）。
  設計に関わる変更はADRのドラフトから
- [SECURITY.md](SECURITY.md) — 脆弱性の非公開報告と、このソフトウェアが触るもの

## License

**コードと文書で分けています。**

| 対象 | ライセンス |
|---|---|
| `docs/` 以下の文書、`README.md`、`VISION.ja.md` | [CC BY-SA 4.0](LICENSE-docs) |
| 上記以外のすべて（Goコード・テスト・`tools/`・スプライト資産） | [AGPL-3.0-only](LICENSE) |

© 2026 Rererr

文書を分けているのは、このプロジェクトで最も時間がかかっているのが
コードではなく**設計の記録**だからです。理由は [docs/LICENSE.md](docs/LICENSE.md) に書いています。

ライセンスは再実装を止めません（著作権が守るのは表現であってアイデアではない）。
**この設計を読んで、別の言語で、別の名前で、もっと良いものを作るのは歓迎します。**
そのときこの記録を参照してもらえれば、目的は果たされています。

## Citation

設計を参照・引用する場合は [CITATION.cff](CITATION.cff) を使ってください
（GitHubの "Cite this repository" から各形式で取得できます）。

DOI: [**10.5281/zenodo.21553016**](https://doi.org/10.5281/zenodo.21553016)
— 版を問わず常に最新版へ解決します。特定の版を指したいときは、その版のDOIを
（v0.1.0 = [10.5281/zenodo.21553017](https://doi.org/10.5281/zenodo.21553017)）。
