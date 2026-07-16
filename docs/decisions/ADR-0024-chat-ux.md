# ADR-0024: チャットUX — 入力の記憶・出力の可読・端末市民権

- Status: **Accepted**
- Date: 2026-07-16
- 関連: [ADR-0022](ADR-0022-chat-session.md)（対話セッション・ラインエディタ。本ADRはその実装時ノブ「履歴のプロセス跨ぎ永続化」「スピナーの体裁」を回収する）, [ADR-0006](ADR-0006-executor-integration.md)（Adapter境界）, [ADR-0008](ADR-0008-appearance.md)（端末の流儀に乗る）, [SCHEMA.md](../design/SCHEMA.md)（R3: ツール名のみ）

---

## Context

ADR-0022の動機は「摩擦がdogfoodを殺す」だった。chatは入ったが、Claude Code / Codex
と並べて使うと摩擦の残りが見える:

- **入力の記憶がない**。履歴はプロセス内のみ（ADR-0022が明示的に残したノブ）。
  Ctrl-Rも補完もなく、Ctrl-Uで消した長文は戻せない（Escを無配線にした理由と同じ
  非可逆消去が、Ctrl-Uには残っている）
- **出力が生のmarkdown**。`**` や ``` がそのまま見え、採用判断のために読むべき
  本文（turnViewの存在理由）が読みにくい
- **ツール行が名前だけ**。`· Edit` は「生きている」しか伝えない。何をどこに
  しているかは、Claude Code/Codexとの体感差の主因の一つ
- **Ctrl-Zで固まる**。raw modeのままサスペンドできず、端末の市民として振る舞えない

いずれも器の口径の問題であり、機能追加ではない。チャットの本分（タスクを渡し、
本文を読んで採用を判断する）の摩擦だけを返済する。

## Decision 1: 履歴はdbの隣のファイルへ永続化する

`<db>.history`（既定 `~/.tomobit/tomobit.db.history`）。1エントリ1行、
`\` と改行をエスケープ。起動時に読み、送信ごとに追記。上限は既存の200件。
読み書きの失敗は**stderrへ1度警告して会話を続ける**——履歴は台帳ではなく、
会話を止める資格がない。

- 却下: **DBに入れる**。履歴はUI状態であって経験ではない。台帳のスキーマを
  表示の都合で触らない（R3と同じ規律）
- 却下: **固定パス `~/.tomobit/history`**。`--db` を替えた実験・テストが
  実履歴を汚す。db隣接ならテスト分離がタダで付く

## Decision 2: Ctrl-R 逆方向インクリメンタル検索

readline準拠の最小形: `(reverse-i-search)'query': match` を入力行の位置に描き、
Ctrl-R=さらに古い一致へ、Backspace=クエリを削る、Esc/Ctrl-G=中止して元の
ドラフトに戻る、Enter=一致をそのまま送信、移動・編集キー=一致をバッファへ
取り込んで編集続行。

- 却下: **fish型のghost autosuggest**。常時の先読み描画は折り返し・全角幅の
  計算を常設化する。履歴200件に対して複雑さが利得を上回る

## Decision 3: kill バッファ（1スロット）と Ctrl-Y

Ctrl-U/K/W が消したテキストを保持し、Ctrl-Y で戻す。連続する Ctrl-W は
readline同様に前へ蓄積する。

- 却下: **emacsのkill ring全体**（M-yローテーション）。「消した長文が戻る」
  という安全網は1スロットで満たされ、それ以上は華美

## Decision 4: `/` コマンドのTab補完

エディタは汎用の Completer 関数を受け取り（エディタはchatのコマンド表を
知らない——lineeditの汎用性はADR-0022 D3の境界のまま）、chatがコマンド名と
`/provider`・`/size` の引数語彙を渡す。一意なら確定+空白、複数なら共通prefix
まで補完して候補を下に列挙する。

- 却下: **ファイルパス補完**。チャットの入力はタスクの意図であって、パスの
  組み立てはProviderの仕事

## Decision 5: Provider出力のmarkdown-lite描画（表示のみ）

turnViewが **bold**・`inline code`・見出し・箇条書き・コードフェンスをANSIに
写す。TTYかつNO_COLOR無しのときだけ（dimと同じゲート）。**台帳に入るtextは
無加工のまま**——描画は読む人のため、記録は知覚のため。

- 却下: **markdownレンダラ依存（glamour等）**。端末の物理以外は自前という
  ADR-0022 D3の線を越えない。行ベースの自前変換で足りる
- 却下: **リンク・テーブルの描画**。v1では対象外（本文の可読が目的で、
  組版が目的ではない）

## Decision 6: ツール行の detail は表示専用チャネルで運ぶ

Adapterは tool_use の input から人が読める短い要約（file_path / commandの
先頭 / pattern等、60runeで切る。パスは末尾保持——ファイル名が「どこに」の
答えであり、深い絶対パスの共通prefixは何も答えない。実ターンの計測で
ファイル名が切れて確定）を `detail` としてpayloadに載せる。executorが
**view-onlyキーの語彙とStrip関数**を持ち、記帳する側（do/chatの両sink）が
AppendEvent前に剥ぐ。台帳は今日と同じくツール名のみ（R3不変）。

- 却下: **detailを記帳する**。知覚のdigest予算（maxEventChars/maxSessionChars）
  を圧迫し、extractor_verのバンプ要因になる。表示の都合で台帳を変えない
- 却下: **現状維持（名前のみ）**。「何をしているか」が見えない摩擦は
  dogfoodの密度を直接下げる

## Decision 7: Ctrl-Z はサスペンドとして通す

raw中の 0x1a は cooked復帰 → 自プロセスへSIGTSTP → SIGCONTで再raw+全再描画。

- 見送り: **SIGWINCHの即時再描画**。幅は毎再描画で読み直しており、次の
  キーストロークで直る。読み待ちループのselect化はコストが利得に見合わない
  （実装時ノブとして残す）

---

## Consequences

- dbの隣に履歴ファイルという新しい状態が増える（台帳ではないのでスキーマ管理外）
- Adapterのイベントに表示専用キーが入る。**sinkがStripを忘れると台帳が変わる**
  ——両sinkの記帳がdetailを含まないことをテストでpinする
- lineedit・描画の依存は増えない（x/term・uniseg のまま）
- 実PTYスイートに Ctrl-R / Tab補完 / Ctrl-Y / Ctrl-Z のケースを足す
  （Ctrl-Zはバイナリ直spawnだとorphaned process groupで停止シグナルが
  カーネルに破棄されるため、対話シェルを挟んだ suspend.exp で検証する）
- NO_COLOR はmarkdown-liteを丸ごと止める（dimと同じゲート）。箇条書きの
  `•` 置換や見出しboldは厳密には「色」ではないが、ゲートを分けるほどの
  複雑さに利得が見合わない。NO_COLOR常用者には生のmarkdown記号が構造情報
  として残る——欠落ではなく退化であることは把握しておく
- スピナーの体裁（ADR-0022ノブ）はツール行のdetailが実質の回答になる:
  「生きている」はスピナー、「何をしているか」はツール行
