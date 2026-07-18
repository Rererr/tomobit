# ADR-0030: Providerのツール出力を表示専用で受け取る — リッチ内容の可視化

- Status: **Accepted**
- Date: 2026-07-19
- 関連: [ADR-0024](ADR-0024-chat-ux.md)（本文markdown描画・ツールdetailの表示専用チャネル。本ADRはその表示専用チャネルを「ツールの出力そのもの」へ広げる）, [ADR-0006](ADR-0006-executor-integration.md)（Adapter境界・digestは名前のみ）, [SCHEMA.md](../design/SCHEMA.md)（R3: ツール名のみ）

---

## Context

ADR-0024で本文のmarkdown描画（Decision 5）とツール行のdetail（Decision 6）が入り、
「何をしているか」は見えるようになった。だが**Providerがツールで生成した出力そのもの**
——Bashの実描画、テスト結果、diff、色サンプル——はchatに一切届かない。claudecode
Adapterは `user`/tool_result を破棄している（R3「台帳はツール名のみ」の帰結）。

これは**台帳の規律であって表示の規律ではない**。chatの本分（ADR-0024）は「本文を読んで
採用を判断する」こと。Providerの答えがツール出力そのものであるとき——実行結果を見せる、
色を実描画して見せる——それが見えなければ採用判断が**不可能**になる。

実測でこの穴が露見した。ユーザー「淡い青のサンプルを見せて」→ProviderがBashで色を実描画
→tomobitは `· Bash printf…` の一行だけ見せ、肝心の色は消える。Providerは「見えましたか?」
と尋ねたが、tomobitがそれを捨てていた。

digestを絞る規律（R3）は今も正しい。だが表示は別の器で運べる——ADR-0024 Decision 6が
既に前例を作った。ツールのdetailは `detail` という**表示専用キー**で運び、記帳する側が
剥ぐ。台帳は不変のまま、人だけが見る。同じ器で「出力そのもの」も運べる。

**計測（claude 2.1.212, `--output-format stream-json`）**: tool_resultの `content` に
生ANSIが**そのまま生存**する:

```
{"type":"user","message":{"content":[{"type":"tool_result",
  "content":"[48;2;30;44;74m  SAMPLE  [0m","is_error":false}]}}
```

リッチ内容は届いている。捨てているのはtomobit側だけ。推測ではなく実stream-jsonで確認した。

## Decision 1: tool_resultは表示専用チャネルで受け取り、台帳には入れない

claudecode Adapterが `user`/tool_result を `provider.output` イベントへ写す。ただし本文の
`text` キーではなく、表示専用キー `tool_result` に載せる。executorの `viewOnlyKeys` に
`tool_result` を加え、記帳する側（do/chatの両sink）が `StripViewOnly` で剥ぐ。**台帳はR3
不変（ツール名のみ）**。ADR-0024 Decision 6の `detail` と完全に同じ規律の拡張であり、
新しい規律ではない。

- 却下: **台帳に記録する**。知覚のdigest予算（maxEventChars/maxSessionChars）を圧迫し、
  extractor_verのバンプ要因になる。表示の都合で台帳を変えない（R3・ADR-0024と同じ線）
- 却下: **現状維持（破棄）**。Providerの答えがツール出力そのものであるとき、採用判断が
  不可能になる。これは器の口径の問題であり、機能追加ではない（ADR-0024と同じ枠）

## Decision 2: ツール出力はSGRのみ通し、他の制御シーケンスは落とす

tool_resultは端末の生出力＝既にANSIを持つ。**mdliteには通さない**——markdown変換は本文の
ためのもので、生ANSIやコードを含む出力を壊す（`#` がboldに、backtickが食われ、生SGRが
混線する）。

だが生ANSIをそのまま端末へ流すと、カーソル移動・画面クリア・復帰（`\r`）等がchatの
ガター・背景・折り返しといったレイアウト不変条件を破壊しうる。よって**SGR（`\x1b[…m`）
だけを通し、それ以外のエスケープ・制御シーケンスは除去**する。色とスタイルは残り、
レイアウトは守られる。「本文の可読が目的で、組版が目的ではない」（ADR-0024 Decision 5）
と同じ節度——見せたいのは色であって、Providerに端末の全権を渡すことではない。

さらに、**行をまたいで開いたままの背景色はガターに漏れる**。chatは各行頭に2列の
ガター（`indent`）をテキストとして前置するだけでANSI状態を追わない。1本のSGRで背景を
開き複数行出力して最後に閉じる——色サンプルの最も自然な書き方——が来ると、2行目以降の
ガターが1行目の背景で着色される。`render.go` の `newline()` が入力行で守る「改行・ガターは
背景を持たない」不変条件を、ツール出力でも守る必要がある。よって `mdlite.ToolOutput` は
**改行の直前で開いている色を閉じ、次の行の最初の出力で開き直す**（ガターは改行と最初の
出力の間に入るので無地のまま）。単一行の色サンプルでは露見しない盲点で、実PTY描画
（`tool-output.exp`・pyteセル再構成）で捕捉した。

- 却下: **生のまま流す**。ツール出力の1本の `\x1b[2J` がchat画面を消し、`\r` がガターを
  崩す。Providerのツールが吐く任意のエスケープにレイアウトを明け渡すことになる
- 却下: **全ANSIを剥ぐ**。色が消え、そもそもの目的（リッチ内容の可視化）を果たさない

## Decision 3: 出力は先頭優先で上限に切る

ツール出力は巨大化しうる（executorが実測した5MB stdoutの前例）。chatを溺れさせない。
先頭から見せ、超えたら省略マーカーと `\x1b[0m`（切断でSGRが宙に浮かないため）を付す。
上限は**可視rune数と行数の両方**を持ち、先に達した方で切る——可視rune上限だけでは、
1行が短く行数が多い出力（diff・テスト結果・スタックトレース＝本ADRが名指しした典型例）で
数千行が素通りしうる（実測: 1文字×6000行は可視rune上限4000では4000行目まで通る）。
高さを縛るのは行数上限の仕事。どちらも色サンプルのような短い出力を絶対に切らない値に
置く（実装ノブ・実出力で調整）。

- 却下: **全量表示**。1本のログdumpがそのターンの本文を画面外へ押し流す
- 却下: **LLM要約**。判断を挿入し、runを1回消費する（ADR-0028 Decision 5がfold-backの
  要約を却下したのと同じ理由——決定論的な切り詰めで足りる）
- 先頭優先の理由: fold-backは結論が着地する末尾を採る（ADR-0028）が、**表示**では出力の
  頭から読むのが自然（スクロールした端末と同じ）。用途が違えば向きも違う

## Decision 4: codexも対称にする（ただし実stream計測後）

二つのAdapterが表示チャネルで非対称だと、Providerを替えた瞬間に見え方が変わる不公平に
なる（detailで対称にしたのと同じ）。よってcodexも `command_execution` の出力を同じ
`tool_result` 表示専用キーへ写す——**表示側（mdlite.ToolOutput・turnView）は完全に共通**で、
Adapterが同じキーに載せさえすればそのまま色付きで見える。

ただし本環境ではcodexが走らない（`gpt-5.4`はChatGPTアカウント非対応、ADR-0010の検証保留と
同じ状態）ため、codexの `command_execution` が出力をどのフィールドで運ぶかを**実stream-jsonで
計測できていない**。推測でフィールド名を書けば「推測ではなく計測」の軸に反する。よってcodex
写像は**計測が取れ次第の対称化**として残す。器（`tool_result`キー・表示層）は共通に用意済みで、
codexのTranslateがそのキーへ載せる1点を足すだけになる。

---

## Consequences

- `viewOnlyKeys` が `detail` と `tool_result` の2つになる。**sinkがStripを忘れると台帳が
  変わる**——両sinkの記帳がtool_resultを含まないことをテストでpinする（ADR-0024と同じ番人）
- **tool_resultイベントはStrip後に空になる**（`detail`と違い、剥いだ後に残る有意キーが無い）。
  空payloadの `provider.output {}` をそのまま記帳すると、内容は漏れずとも**イベント行の形で
  digest予算を消費**し、Decision 1の却下理由（予算圧迫）を弱く再導入する。よって記帳側
  （全sink共通の `recordEvent`）は**Strip後に空になったイベントの `AppendEvent` 自体をスキップ**
  する。`tool_use` は剥いでも `{"tool":name}` が残るのでこの非対称は起きない
- SGRサニタイザという新しい純関数が増える（`internal/mdlite` 隣、または executor）。
  「SGRだけ残す」の境界（`\x1b[…m` のみ許可、OSC・CSI非m・裸の制御を除去、`\r`除去）を
  テストでpinする。ANSI注入に対する表示の防壁でもある
- NO_COLOR/パイプではtool_resultも止まる（mdlite・detailと同じ表示ゲート）。台帳には元々
  入らないので、記録側は何も変わらない
- ツール出力が見えることでchatの縦密度が上がる。上限（Decision 3）と、既存のツール行が
  detailで「何を」を既に言っていること（ADR-0024）が溺れを防ぐ
- 表示専用チャネルが「要約(detail)」から「出力そのもの(tool_result)」へ広がった。以後
  Adapterが表示のために載せてよいものの範囲が一段広い——ただし台帳へ漏らさない規律
  （StripViewOnly）は不変

---

## 追記（2026-07-19）: ADR-0031（ターン表示予算）による追補

本文Consequencesの「上限（Decision 3）と既存のツール行がdetailで『何を』を既に
言っていることが溺れを防ぐ」は、受理当日の実運用で反証された。Decision 3の上限は
**1結果あたり**で、ツールをN回呼ぶエージェント的なターンでは総量がN×40行まで積む。
[ADR-0031](ADR-0031-turn-tool-output-budget.md) がターン累積の表示予算を導入し、
per-result上限も実測で引き締めた。Decision 3自体（先頭優先・決定論的切り詰め・
LLM要約の却下）は不変のまま、その射程がターンスケールへ延びた形。
