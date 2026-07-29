> **改版注記（2026-07-25）**: 本ADRの実装は既定ONで配備されたが、
> [ADR-0049](ADR-0049-quota-observation-is-opt-in.md) が**既定をOFF（`quota_observe` の
> 明示的opt-in）へ変更した**。本文中の「所有者許可の下で実測済み」という前提は
> 所有者の機械にしか及ばず、OSS公開後は成立しないため。
> 経路Aの選択・却下した代替経路・実測はすべて有効なまま — 変わったのは既定値だけ。

# ADR-0044: Provider の残量観測 — 公式手段は無いが、自分の鍵で自分の残量を聞く経路は実在する

- Status: **Accepted**（2026-07-24 起草。初稿は「取得不可・推定しない」を結論としたが、
  同日の CodexBar 実装調査で前提が変わり全面改稿。さらに同日、所有者の明示許可の下で
  実エンドポイントを実測しスキーマを確定（下記 Context）。同日、所有者裁定により
  **経路A（Decision 1）を採用**し status への配線を実装。Decision 2/3/4 は却下、
  Decision 5/6 は維持、Decision 7（Curiosity 予算ゲート／記帳）は将来 ADR へ切り出し保留。
  実端点疎通（Consequences 項3）は同日実機で **両 Provider 完了**〈codex は当初 token
  stale の当て推量に阻まれたが、計測が仮定を反証し `codexAuthStaleAfter` を撤去→200〉）
- Date: 2026-07-24
- 着想元: [CodexBar](https://github.com/steipete/CodexBar)（MIT, Peter Steinberger）。
  **もらったのは経路の存在と仕組みだけ**で、コードは独立実装（`internal/quota/`）
- 関連: [ADR-0010](ADR-0010-codex-adapter.md)（Decision 2:「観測できないものを推測で埋めない」の先例）,
  [ADR-0006](ADR-0006-executor-integration.md)（Adapterは起動と翻訳だけ）,
  [ADR-0007](ADR-0007-curiosity-question.md)（質問の予算窓）,
  [ADR-0016](ADR-0016-curiosity-priority-voi.md)（価値の物差しはVoI、財布の紐は別責務）,
  [ADR-0026](ADR-0026-ab-duel.md)（A/B実走 — 実トークンを燃やす学習行動）,
  [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md)（決定則は台帳の上に建つ）,
  [ADR-0039](ADR-0039-status-machine-view.md)（機械viewはレンダラへのデータ供給）

<!-- 改版:begin — tools/sync-adr-superseded.sh が生成する。手で編集しない -->
> **改版済み** — この決定の一部は後のADRが置き換えた。範囲は各Decisionの改版注記が持つ。
>
> - 決定全体 → [ADR-0049](ADR-0049-quota-observation-is-opt-in.md)
<!-- 改版:end -->

---

## Context

要望は「Provider ごとの現在の使用量・残量を tomobit で一望したい」。

### 初稿の調査結果 — 公式手段が無いことは今も正しい

2026-07-24、両CLIとも導入済み・ログイン済みの実機で調査した:

| Provider | 非対話・機械可読な残量取得 | 実態 |
|---|---|---|
| claude-code (v2.1.218) | **無し** | 残量は対話TUIの `/usage` のみ。`--help` の全サブコマンドに usage/quota 系は無い |
| codex (v0.145.0) | **無し** | 残量は対話TUIの `/status` のみ。`codex doctor --json` にも usage 項目は無い |

- Anthropic は CLI/hook での usage 露出要求（anthropics/claude-code#38380）を
  **closed as not planned** としている。方針として提供されない
- `claude -p --output-format json` のスキーマは実測で確定した:
  `total_cost_usd` と `usage` は返るが、**`rate_limits` は無い**
- `statusLine` hook の `rate_limits` は対話セッションの副産物で、
  能動的にポーリングできる入口ではない

### 前提を変えた事実 — CodexBar は実際に取れている

CodexBar（MIT・macOSメニューバー常駐）は、**使用者自身の OAuth トークンで
ベンダー自身の usage エンドポイントを読む**ことで両 Provider の残量を表示している。
実装調査（2026-07-24）で判明した経路は4系統ある:

| # | 経路 | 仕組み | 脆弱性 | 侵襲度 |
|---|---|---|---|---|
| A | **OAuthトークン → ベンダーのusage端点** | claude: `claudeAiOauth.accessToken`（macOSでは Keychain、後述の実測3。ファイル型インストールでは `~/.claude/.credentials.json`）で `GET api.anthropic.com/api/oauth/usage`（`anthropic-beta: oauth-2025-04-20`、`user:profile` スコープ）。codex: `~/.codex/auth.json`（`$CODEX_HOME` 優先）の `tokens.access_token` で `GET chatgpt.com/backend-api/wham/usage` | 非公式API。消えたら**エラーになる**（誤読はしない） | 自分の鍵で、鍵の発行元に、自分のデータを聞く |
| B | PTYで対話TUIを駆動し `/usage`・`/status` をスクレイプ | 擬似端末で子プロセスを飼い、画面文字列を正規表現で拾う | 表示文言の変更で壊れ、**壊れ方が「誤読」になりうる** | 自分のCLIだが、内部UIへの寄生 |
| C | ブラウザの cookie DB を読んで Web ダッシュボードを叩く | Safari/Chrome の cookie 格納庫から セッションを取り出す | ブラウザの暗号化・TCCと恒常的に戦う | **他アプリの資格情報格納庫に手を伸ばす** |
| D | `codexbar --format json` へ shell out | 外部CLIの出力をパースする | 他人の出力形式への依存（Bと同型の脆さ） | 間接的にA〜Cを実行させる。macOS専用依存 |

経路Aが返す値は**ベンダー自身が集計した利用率**である（実スキーマは後述の実測1・2）。

ここで初稿の核心的な論拠を再検討する必要が生じた。初稿 Decision 1 は
「tomobit が観測できるのは tomobit 経由の消費だけ → 上限−消費で作った残量は
常に楽観側に外れる」と論じた。これは**自前積算による推定**への反論としては
今も完全に正しい。しかし経路Aの値は tomobit の積算ではない —
**他所（対話セッション・別ツール・別マシン）の消費も合算済みの、
ベンダー側の観測値**である。初稿の表題「残量は観測できない」は、正しくは
「**tomobit 単独では**観測できない。ベンダーに聞けば観測できる（非公式だが）」だった。

### 実測（2026-07-24、所有者の明示許可の下で実施）

所有者の実資格情報で両エンドポイントを叩き、**両方 HTTP 200 で取得できた**。
以下はその実測。値は所有者の実データなので記さない（構造・単位のみ）。
トークン値・email・user_id は本ADRにも台帳にも書かない。

**実測1: claude（`GET api.anthropic.com/api/oauth/usage`、Bearer + `anthropic-beta: oauth-2025-04-20`、Max プラン、scopes に `user:profile` あり）**

- `five_hour` / `seven_day`: `{utilization, resets_at, limit_dollars: null, used_dollars: null, remaining_dollars: null}`
  - **`utilization` は 0–100 のパーセント**（0–1 ではない）
  - **`resets_at` は RFC3339 文字列**（マイクロ秒＋数値オフセット付き。形の例: `…T05:09:59.817927+00:00`）
- `seven_day_opus` / `seven_day_sonnet` / `seven_day_oauth_apps` 等のモデル別・種別キーは
  **null で返りうる**（今回は全て null）。未知キー・null キーを必須扱いしてはならない
- `limits`: 配列。各要素 `{kind, group, percent(整数), severity, resets_at, scope, is_active}`。
  観測された `kind` は `session` / `weekly_all` / `weekly_scoped`、`scope` は
  `{model: {id: null, display_name}, surface: null}` の形を取りうる
- `extra_usage`: `{is_enabled, monthly_limit: null, used_credits: null, utilization: null, …}`
- `spend`: `{used: {amount_minor: int, currency, exponent}, limit: null, percent}` —
  **金額は minor unit + exponent**。浮動小数で受けてはならない
- `member_dashboard_available: bool`

**実測2: codex（`GET chatgpt.com/backend-api/wham/usage`、Bearer は `~/.codex/auth.json` の `tokens.access_token`、`auth_mode: "chatgpt"`、`last_refresh` は RFC3339）**

> **追測（2026-07-24 配線時に反証）: `last_refresh` は鮮度の指標ではない。**
> 初版の取得器は「`last_refresh` が8日超なら失効＝ネットワーク前に fail-fast」
> という当て推量（`codexAuthStaleAfter`）を持っていた。だが配線後の実機で、
> **ログイン済みで正常に使えている codex の auth.json の `last_refresh` が9日前**
> だった（ファイル mtime も同じ）。codex CLI は access_token をメモリ内で更新し、
> auth.json を書き換えない。よって8日カットオフは「使えるトークンを不明にする
> 偽陰性」を生む。撤去し、**トークンが実際に失効しているかは端点（401）に判定
> させる**（`codex.go` の3行下に既にあった「unparseable な last_refresh は
> request に落とす。端点が権威」の原則を全面適用）。推測ではなく計測が方針を
> 覆した一例（回帰テスト `TestCodexFileTokenOldLastRefreshStillYieldsTheToken`）。

- トップレベルに `user_id` / `account_id` / **`email`** / `plan_type` —
  **email は個人情報。Snapshot に持ち込まない・ログに出さない**（取得器は最初から decode しない）
- `rate_limit`: `{allowed, limit_reached, primary_window: {…}|null, secondary_window: {…}|null}`。
  window は `{used_percent(0–100), limit_window_seconds, reset_after_seconds, reset_at}`
  - **`reset_at` は unix epoch 秒（整数）** — claude の RFC3339 文字列と方言が違う。
    取得器の `Window` 型が時刻に正規化して吸収する
- `additional_rate_limits`: 配列 `[{limit_name, metered_feature, rate_limit: {同上}}]`（モデル別枠）
- `credits`: `{has_credits, unlimited, overage_limit_reached, balance(文字列), approx_local_messages, approx_cloud_messages}`
- `rate_limit_reset_credits`: `{available_count, applicable_available_count}`。
  別端点 `…/wham/rate-limit-reset-credits` も 200 で返る

**実測3【設計に影響】: claude の資格情報の在処はプロファイル依存**

`~/.claude/.credentials.json` も `~/.claude-personal/.credentials.json` も
**存在しなかった**。実体は macOS Keychain にあり、サービス名が
`CLAUDE_CONFIG_DIR` に依存する:

- 既定プロファイル: `Claude Code-credentials`
- 非既定プロファイル: **`Claude Code-credentials-` + sha256(configDir) 先頭8桁（16進小文字）**
  - 実証: `/Users/example/.claude-personal` → `Claude Code-credentials-5034c31c`（計算と実在項目が一致）

tomobit は `~/.tomobit/config.json` の `claude_config_dir` を既に持っている
（claude-code アダプタがこの値で起動している）ので、取得器は**設定から
サービス名を決定的に導ける**。

さらに重要な失敗モードを実測した: **既定サービス名（別プロファイル）の
トークンで叩くと、401 ではなく HTTP 429 `rate_limit_error` が返る**。
つまり「資格情報の取り違え」が「レート制限」に化ける。素朴に既定の場所だけ
読む実装は、残量が読めない理由を取り違えて、しかも黙って誤った説明をする —
Decision 5 が禁じる型の事故である（対処は Decision 5 追補）。

**取り込み範囲**: 取得器が Snapshot にするのは utilization を持つ窓だけ
（claude: `five_hour`/`seven_day`＋モデル別週次が非nullなら。codex:
`rate_limit` の2窓）。`limits[]` のスコープ付き窓・`additional_rate_limits`・
`spend`・`credits` は構造を本ADRに記録した上で**取り込まない** —
読む View が現れるまで、依存するスキーマ表面を増やさない。

### 既に出しているもの

tomobit 経由の消費実績（claude-code の `cost_usd` 合計、codex のトークン数）は
`status` の Provider 利用ビューに実装済み（初稿 Decision 2、`provider_usage.go`）。
本ADRはその上に「残量」を足すか、足すならどの経路か、を裁定する。

---

## Decision 1: 経路A（自分の鍵 → ベンダーのusage端点）を採用する【採用済み】

4経路のうちこれだけを採る。論拠:

- **観測であって推定ではない。** 値を数えているのはベンダー自身で、他所の消費も
  含む。初稿が推定を却下した理由（外れる方向が既知で安全側でない）が適用されない
- **鍵は発行元にしか送らない。** claude のトークンは api.anthropic.com へ、
  codex のトークンは chatgpt.com へ。第三者は経路上に存在しない
- **壊れ方が正直。** 非公式APIが消えれば HTTP エラーになり、Decision 5 により
  「不明」に退化するだけ。嘘の数字に化けない（経路Bとの決定的な違い）

正直に書くべきトレードオフ — **これが裁定の中心点**:

- tomobit がトークンそのものをプロセスメモリに読み、自前のHTTPリクエストに載せる。
  現行の tomobit は資格情報を**触らない**（`CLAUDE_CONFIG_DIR` を子プロセスの
  環境に渡すだけで、読むのは CLI 自身）。経路Aはこの一線を一段越える。
  「相棒が飼い主の鍵を預かって、鍵の発行元に残高を聞きに行く」ことを許すか
- `anthropic-beta: oauth-2025-04-20` と `wham/usage` は無保証。ある日消える前提で、
  消えても製品が壊れない形（Decision 5）でのみ持つ

採る場合の範囲も最小に絞る:

- 読む場所は **CLI 自身が資格情報を置いた場所だけ**。codex は
  `$CODEX_HOME/auth.json` または `~/.codex/auth.json`。claude は実測3の通り
  macOS では Keychain が実体（初稿段階の「ファイルのみ・Keychain は実機が
  現れてから」は、所有者の実機にファイルが無いと実測された時点で解除条件に
  到達した）。サービス名は `claude_config_dir` から決定的に導出する
  （`quota.ClaudeKeychainServiceName` — 実行に使うプロファイルと同じ鍵を読む。
  実測3の 429 事故は、既定サービス名を無条件に読む実装が引き起こす）。
  Keychain の実読（`security find-generic-password` への shell out）は
  配線時に実装し、取得器には注入可能なリーダの座席だけを置く。
  ファイル `~/.claude/.credentials.json` はファイル型インストールの
  フォールバックとして残す
- トークンのリフレッシュは実装しない。claude の期限切れ・codex の
  `last_refresh` 8日超は「CLI を一度起動してください」というエラーで返す。
  リフレッシュフローを持った瞬間、tomobit は資格情報の**管理者**になる —
  預かるのは読み取りまで

## Decision 2: 経路B（PTYスクレイプ）は却下する

初稿の却下理由がそのまま生きている: Provider の内部UIへの依存で、
Executor 抽象（ADR-0006「起動と翻訳だけ」）を監視のために破る。

経路Aとの比較で決定的なのは**壊れ方**である。Aは端点が消えればエラーになるが、
Bは表示文言が変わったとき「エラー」ではなく「誤読した数字」を返しうる。
静かに嘘をつく器官は持たない（ADR-0005 の「沈黙する誤り」の型）。

## Decision 3: 経路C（ブラウザcookie読み）は却下する

自分の鍵ではなく、**預かってもいない鍵**（ブラウザに保存された資格情報）に
手を伸ばす。使用者が tomobit に渡したのは CLI の配線であって、ブラウザの
中身ではない。相棒が飼い主のブラウザを漁る形はコンセプトの毀損であり、
便利さで正当化しない。技術的にも cookie 暗号化・TCC との恒常的な戦いになる。

## Decision 4: 経路D（codexbar CLI へ shell out）は却下する

- macOS 専用・63 Provider 対応の常駐ツールを、2 Provider・非常駐の tomobit の
  依存に足すのはコンセプト純度に反する
- 出力形式は他人の保守物 — 経路Bと同型の脆さを場所を変えて抱えるだけ
- MIT なので**発想と仕組みはもらった**（本ADR冒頭に明記）。実装は tomobit の
  流儀（注入可能・オフラインテスト・fail-honest）で独立に持つ

## Decision 5: 取れないときは「不明」— 推定値・0%・古い値を黙って出さない【初稿の核の継承】

初稿 Decision 1 の核はそのまま維持する:

- fetch の失敗は理由付きエラーで返し、表示側は「不明（理由）」を出す。
  0% やそれらしいフォールバック値を発明しない
- **自前積算による残量推定の却下も維持する。** 経路Aが死んでいる間、
  台帳の消費実績から残量を「補完」することはしない — 楽観側に外れる論拠は
  一字も変わっていない
- 器官が黙ってそれらしい数字を出すより、境界を見せる方が相棒として誠実である

**追補（実測3を受けて）: 「不明」の理由も正直でなければならない。**
プロファイルを取り違えたトークンは 401 ではなく **429 に化ける**と実測された。
「レート制限です」とだけ言うエラーは、この場合**もっともらしい嘘**になる。
よって:

- 取得器のエラーは**どの資格情報を読んだか**（Keychain サービス名 or ファイル
  パス）を必ず含める（`ClaudeFetcher.CredentialOrigin` / `CodexFetcher` 同）。
  トークン値そのものは決してエラーに載せない
- 429 のエラー文言は「レート制限 **または** 別プロファイルのトークン」と
  両方の可能性を言う。サーバ側からは区別できないものを、器官が勝手に
  一方に断定しない

### 改訂（2026-07-25・枠の名前は1つの語彙に揃える）

`Window.Label` は当初「ベンダー自身の語彙のまま（`five_hour` / `7d`）。
見たことのないキーを改名するのは、間違いうるものを一つ増やす」としていた。
実機で並べたら、**同じ画面の同じ意味の枠が `five_hour` と `7d` の2つの綴りで
出た** — GUI のサイドバー（GUI ADR-0006）でも人間向け status の表でも同じ。

そこで span の語彙1つに揃える。**却下した理由は、実は改名の禁止ではなく
「読んでいないものを言い換える」ことの禁止**だった:

- codex は既に `limit_window_seconds` から span を出している（`codexWindowLabel`）。
  claude 側の綴られたキーを同じ形へ言い換える（`spanLabelFromKey`:
  `five_hour` → `5h`、`seven_day` → `7d`、`seven_day_opus` → `7d opus`）
- **読めなかったキーはベンダーの語のまま返す**。数詞・単位の表に無い語、
  区切りの無い語は一切触らない — 旧規則が守ろうとした「見たことのないキーへの
  推測」はここでも行わない
- 表は小さく保つ（`one`〜`twelve` と分/時/日/週/月）。これは実際に読んだ span の
  忠実な言い換えであって、スキーマについての推測ではない

## Decision 6: 残量を決定則（ADR-0012）の入力にはしない【初稿 Decision 3 の維持・理由を差し替え】

初稿は「入力が観測できないから」混ぜなかった。観測できるようになっても混ぜない。
理由が変わる:

- 決定則は**台帳＝経験**の上に建っていることが値打ちである。残量は経験ではなく
  外部状態のスナップショットで、減衰も証拠も持たない異質な入力になる
- 「claude の残量が逼迫したら codex に倒す」は、選択の理由を好み・能力の証拠から
  **財布**にすり替える。倒した先で積まれた経験は「codex が選ばれた文脈」として
  記帳され、残量という一時的な事情が恒久的な証拠に化ける（credit assignment の汚染）

残量が判断に効いてよい場所は Provider の選択ではなく、**学習行動の抑制**である
（Decision 7a）。それでも決定則に混ぜるべきだと考えるなら、本ADRの改版ではなく
別ADRを起こして裁定に回す。

## Decision 7: 台帳との接続【将来 ADR へ保留 — CodexBar に対する tomobit の優位】

CodexBar は表示して捨てる。tomobit は台帳を持つ。その差を使う接続を提案する
（本ADRでは**実装しない**。採否は所有者裁定）:

### 7a. Curiosity 予算への残量ゲート【推奨】

VISION は Curiosity を *"a privilege made possible by available time and
resources"* と定義し、「十分な余裕があるときだけ、Tomobit は静かに好奇心を
満たす」と書いている。現行の予算は**時間窓だけ**
（`curiosity.HasBudget` = 24時間に1問、ADR-0007 Decision 3）で、
*resources* 側は未実装だった — 取れなかったからである。

提案: 質問（ADR-0007）と duel（ADR-0026）の申し出の前に、残量スナップショットを
見て逼迫時（例: いずれかの窓が閾値超）は申し出自体を控える。

- Decision 6 と両立する: Provider の**選択**を歪めるのではなく、実トークンを
  燃やす**学習行動**を「余裕があるときだけ」に絞る。VISION が予定していた設計
- **不明は「乏しい」と解釈しない**（Decision 5 の帰結）: 取れないときは現行どおり
  時間窓のみで動く。残量が取れないことを理由に好奇心を殺さない

### 7b. 残量スナップショットの記帳（`quota.observed`）【任意】

観測した残量を events に記帳すれば、台帳の `cost_usd`・トークン実績と
突き合わせて「このタスク種は週次枠の何%を食うか」「今のペースであと何日か」を
後日**導出**できる（保存するのは観測、導出はView — いつもの形）。
7a の閾値の較正材料にもなる。

ただし R4 の type カタログ（SCHEMA.md、14種で確定）への追加になるため
SCHEMA 追記が要る。7a より重いので、7a を先に、7b は必要が実証されてから。

---

## 却下した対案（Decision 2〜4 で個別却下したものを除く）

- **`statusLine` hook を仕込んで `rate_limits` を拾う** → tomobit は claude を
  非対話（`-p` 系）で起動するため statusLine は走らない。走らせるために
  対話セッションを別途維持するのは ADR-0006 Decision 2 の境界を破る
- **使用者にプラン上限を設定させ、消費を積算して残量を推定する** → 他所の消費が
  見えない以上、必ず楽観側に外れる。設定の摩擦を足して間違った数字を返す
  （Decision 5 で恒久却下）
- **何も足さない（消費実績の表示だけで止める）** → 初稿はこれを結論としたが、
  自分の鍵で自分のデータを読む経路が実在すると分かった今、「持てるのに
  持たない」も一つの選択として明示的に裁定されるべきである。所有者の不満
  （各Providerの usage/status を見に行く手間）は消費実績では解けていない

## Consequences

- **実証は完了**（2026-07-24、所有者の明示許可の下）: 両端点 HTTP 200、
  スキーマ・単位・時刻方言・プロファイル依存・429 の失敗モードを pin し、
  `internal/quota/` のフィクスチャとパーサは実測スキーマに追随済み
  （フィクスチャの値は自作ダミー。実データは持ち込んでいない）
- 採用時（Decision 1 承認後）の作業と、2026-07-24 配線時点の状態:
  1. **【実装済み】** Keychain リーダ（`internal/quota/keychain.go`:
     `ReadClaudeKeychain` = `security find-generic-password -s <service> -w` の
     shell out。darwin 限定・5s タイムアウト・stderr のみをエラーに載せ stdout の
     パスワードは決して載せない）。`cmd/tomobit/quota.go` が `claude_config_dir`
     （env > config、`wireClaude` と同じ解決）からサービス名を導出し、ファイルが
     在ればファイル source、無ければ darwin で Keychain source を選ぶ
  2. **【実装済み】** 配線: `status --view json` に `quota` フィールド
     （`statusPayload.Quota`、exists:true のときだけ付与）、人間向け `status` に
     「残量（各Providerの申告値・tomobitの保証ではない）」ブロック、GUI へは
     ADR-0039 の経路（tomobit-gui `stage.go` の `TomoStatus.Quota` デコード＋
     MemoryPane の残量セクション、見出しで保証ではない旨を明示）。chat のターン間
     `/status` は nil collector でオフライン（毎ターンのネットワーク待ちを避ける）
  3. **【両 Provider 完了】** Go 取得器そのものの実端点疎通。2026-07-24
     所有者確認の下で `tomobit status --view json` を実機実行した結果:
     - **claude-code: 完了。** Keychain（`Claude Code-credentials-5034c31c`）を
       実読みし `api.anthropic.com/api/oauth/usage` が 200、取得器コード経由で
       `five_hour`/`seven_day` の window を実際にパースできた（値は所有者の実
       データなので本 ADR には記さない）。プロファイル依存 Keychain の導出が
       実機で効くことを確認 — フェイク上の通過ではない実疎通
     - **codex: 完了（当初の想定を計測が反証）。** 初回は `last_refresh` 9 日前を
       理由に `codexAuthStaleAfter` が fail-fast し不明に退化。だが実機計測で
       「ログイン済みでも auth.json は書き換わらない＝last_refresh は鮮度指標では
       ない」と判明（実測2の追測）。当て推量のカットオフを撤去し端点を権威に
       すると `chatgpt.com/backend-api/wham/usage` が 200 を返し、`7d` window を
       実際にパースできた（null だった 5h の `primary_window` は 0% でなく正しく
       消えることも実機で確認）
  4. **【設計で担保】** 資格情報の読みは fetch の瞬間だけ・読み捨て（TokenSource は
     呼び出しごとに読む）。トークンを DB・ログ・エラーメッセージのどこにも書かない
     （エラーに載るのは Keychain サービス名 / ファイルパスまで — Decision 5 追補。
     テスト `TestCollectQuotaErrorCarriesOriginNotToken` が 429 経路で非漏洩を固定）
- 非公式端点が消えた日: エラー → 「不明（理由）」に自然に退化する。
  本ADRの改版は不要（Decision 5 が吸収する）
- Decision 1 が却下された場合: `internal/quota/` は配線されないまま削除し、
  本ADRの結論を初稿（消費実績のみ・残量列は作らない）へ戻す
