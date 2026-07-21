# ADR-0022: 対話セッション — 会話は入力の器、タスクは記帳の単位

- Status: **Accepted**
- Date: 2026-07-16
- 関連: [ADR-0006](ADR-0006-executor-integration.md)（`do`・Adapter境界・採用確認）, [ADR-0007](ADR-0007-curiosity-question.md)（doの区切りの質問）, [ADR-0008](ADR-0008-appearance.md)（最初の画面は相棒ビュー）, [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md), [ADR-0015](ADR-0015-reflection.md)（doの区切りの鏡）, [ADR-0018](ADR-0018-experience-sovereignty.md)（humanもProvider）, [ADR-0021](ADR-0021-onboarding.md)（Decision 4 欠けた選択が質問になる — chatの起動時と`/provider`切替時にも同じゲートが立つ）, [SCHEMA.md](../design/SCHEMA.md)（R4）

---

## Context

ADR-0006は `do` を非対話（headless）に留め、こう書き残した——
「対話パススルーはPhase 2以降の論点」。その論点が実使用で回収された。

dogfoodで測った摩擦:

- **長いタスクをシェル引数で書く**。クォート・改行のescapeが要り、
  打ち間違えると行編集が効かない（`do` の引数はシェルが読み終えた後の文字列で、
  tomobitに編集の座席はない）
- **続きが依頼できない**。1回で終わらない仕事は、毎回コンテキストを失って再投入する。
  Providerのスレッドは毎回破棄され、`provider_session_id` は記録されるだけで**使われない**
- 結果、`do` は「短い一発タスク」にしか使われず、実タスクの大半が台帳に載らない

ADR-0006自身が最も恐れたのは**摩擦がdogfoodを殺すこと**だった。
経験が積まれないTomobitは何も学ばない。ここは利便性の問題ではなく、
**Realityを吸い込む器官の口径**の問題である。

---

## Decision 1: セッション = タスク、ターンはその中の呼吸

1つのチャットセッション = 1 ledger session = 1 Experience。
ターンはセッションの中の出来事であり、それ自体はセッションではない。

```text
task.started       {intent: <1ターン目のprompt>, source: "production"}
capability.started {capability}
tomo.decided       {...}                    provider auto時
provider.selected  {provider, model, provider_session_id}
provider.output    ...                       1ターン目
provider.finished  {...}
task.turn          {intent: <2ターン目のprompt>, n: 2}   ← 新type
provider.selected / output / finished        2ターン目（同じスレッド）
...
task.finished      {adopted, reverted}      区切りの採用確認
```

区切り（`/new`・`/exit`・Ctrl-D）で **doの尾部をそのまま**走らせる:
採用確認（ADR-0006 D4）→ best-effort知覚（D5）→ Tomoの質問（ADR-0007）→ 鏡（ADR-0015）。
「doの区切り」が守っていた位置は「タスクの区切り」であり、
チャットではセッション終端がその区切りになる。**器官の位置は動いていない**。

却下した対案: **1ターン = 1セッション**（チャットを `do` の薄い皮にする）。
実装は最小で済むが、台帳が嘘をつく——「実装して」「そこ違う、直して」「OK」の
3ターンが *as-is採用 3件* として記録され、実際には手直しが要ったという事実が消える。
チャットは記帳をUIの都合で歪めるためではなく、**タスクの実像に近づけるため**に入れる。
1セッション1verdictなら、手直しの往復はターンとして可視化されたうえで、
Outcomeは「そのタスクがどう転んだか」1つに収束する。

タスクの区切りは**人間が宣言する**（`/new`）。会話の流れからタスク境界をLLMに
推測させない（ADR-0011: 判断は数式、意味だけがモデルの座席）。

## Decision 2: スレッド継続はAdapterの責務

`executor.Request` に `ResumeID` を足し、Adapterが自分のCLIの語彙へ写す:

```text
claude-code  claude -p --resume <id> "<prompt>" ...
codex        codex exec resume <id> "<prompt>" --json ...
```

「どう起動するか」を知っているのはAdapterだけ、という境界（ADR-0006 D2）を
そのまま伸ばす。起動プロファイルをAdapterの責務に入れた実装追記と同じ理屈で、
**スレッドの継ぎ方もCLIを知っていることの一部**である。

ResumeIDの出所は `provider.selected` の `provider_session_id`。
そのため claude-code Adapterが init行の `session_id` を捨てていたのを拾う
（codexの `thread.started` はすでに載せていた——**2つのAdapterが対称になる**）。
`provider.finished` の側も引き続き拾い、両方の新しい方を採る:
将来のCLIがresumeでidを振り直す（fork）実装に変わっても追随できる。

実測（2026-07-16, claude 2.1.210 / codex 0.144.4）:

- claude-code: `--resume` で文脈が継続することを確認（前ターンの内容を回答）。
  session_idは不変。再生（過去ターンのassistant行の再送）は**起きない** →
  ダイジェストに前ターンが二重に積まれる事故はない
- codex: `exec resume <id> <prompt> --json` の引数形と、`thread.started` が
  同じthread_idを返すことを確認。ターン本体はこの機械の `~/.codex/config.toml` が
  未知モデル（gpt-5.4）を指しており失敗する——写像の問題ではないため仕様準拠で確定する

## Decision 3: 入力はインラインの自前ラインエディタ

raw mode + ANSI。カーソル移動・履歴（↑↓）・Ctrl-A/E/K/W・Backspace/Delete・
折り返し行の再描画・bracketed pasteによる複数行貼り付け・`\`+Enterでの改行。

Ctrl-Uは**入力全消し**（zshの既定 kill-whole-line と同じ）。readlineの
「行頭まで削除」だと、貼り付けた複数行のあとに全部を消す手が無くなる——
実PTY検証で踏んだ。1行を消す手はCtrl-W（単語）で足りる。

- 却下: **cooked mode**（`bufio.Scanner`）。矢印キーがエスケープ列のまま
  混入し、履歴もない。「打ち間違えたら手直しが面倒」という摩擦が消えないなら、
  この変更は目的を果たさない
- 却下: **bubbletea等のフルスクリーンTUI**。alt screenはスクロールバックを奪う。
  Tomoの発話もProviderの出力も「端末に流れて残るログ」であってほしい。
  ADR-0008が半ブロック＋ANSIでアバターを描いたのと同じ思想——
  端末を占有せず、端末の流儀に乗る
- 依存: `golang.org/x/term`（termios / Windows Console の移植性のみ）と
  `github.com/rivo/uniseg`（表示幅——日本語は全角。カーソル位置の計算に要る。
  ebiten経由ですでにモジュールグラフにある）。「依存ゼロのターミナルUI」への
  唯一の例外で、**端末の物理**に対してだけ払う。ANSI・描画・編集ロジックは自前
- 非TTY（パイプ）はraw modeを使わず1行=1ターン。テストとスクリプトの経路が
  同じコードを通る

## Decision 4: 入口 — 無引数のTTYは、相棒ビューのあとそのまま話せる

```text
tomobit          (TTY)  相棒ビュー → そのまま対話に入る
tomobit          (pipe) 従来通り：表を出して終わる
tomobit chat            明示的な入口（TTYなら同じく相棒ビューから始まる）
tomobit chat    (pipe)  1行=1ターン。スクリプトとテストの経路
tomobit status          見て終わる（パイプと同じ振る舞い）
tomobit do "..."        残す：スクリプト・一発・非対話
```

ADR-0008は「最初の画面はマニュアルではなく相棒ビュー」を決めた。
その画面の続きが**プロンプトである**ことは、この決定の延長にある——
相棒に会って、そのまま話しかけられる。会って何もできずシェルに戻るほうが不自然だった。

`chat` が相棒ビューを省かないのは、入口によってTomoに会えたり会えなかったり
するのが変だから——最初の画面は相棒（ADR-0008）で、それは入口の数だけ揺れる
決定ではない。`status` は「見て終わる」を引き受ける。
Claude Code自身の `claude`（対話）と `claude -p`（一発）の分担と同じ形になる。

## Decision 5: `/` コマンドは最小

```text
/new [prompt]    ここまでを区切って次のタスクへ（尾部を走らせる）
/provider <name> 次のタスクのProvider（ターン開始後は不可 → /new が要る）
/cap <name>      同上
/size <s|m|l>    同上（判断の温度 n — `--provider auto` のとき効く）
/status          相棒ビューを挟む
/help /exit
```

`/provider`・`/cap`・`/size` がターンの途中で効かないのは実装の都合ではない:
experiences.provider は1セッション1つで、途中で替えれば
「どのProviderがこの結果を出したか」が壊れる。**台帳の形が拒否している**。

Planは扱わない（`--plan` は `do` に残る）。Planは「1タスク＝複数run」の軸で、
チャットのターンと同じ軸に二重に乗る。どちらが手順を刻むのかを決めていない段階で
両方を動かすと、plan Connectionの賭け先が濁る。v1では対話に載せない。

---

## Consequences

- Realityの口径が広がる。長いタスク・往復の要る仕事——つまり**実タスクの大半**が
  初めて台帳に載る
- 1セッション1Experienceのため、長い会話ほど知覚のダイジェスト上限
  （maxSessionChars=12000）に当たりやすい。当たった分は「omitted」と明示されて
  落ちる（既存の挙動）。`/new` で区切る規律が、そのまま抽出品質の規律になる
- チャット中のCtrl-Cはそのターンの中断であって、タスクの中断ではない。
  スレッドIDはinit行で既に取れているので、次のターンは中断した会話を継ぐ。
  `task.cancelled` は「1ターンも完走しなかったセッション」にだけ付く
  （＝判断材料がない＝シグナルなし。ADR-0003の重み解決に嘘を入れない）
- 実行中に打った文字は端末のcookedバッファに入り、次のターンとして読まれる
  （＝先行入力は捨てられない）。ただしその間のエコーは端末が出すので、
  プロンプトに再描画されるまで二重に見える。v1はこれを受け入れる
- plan学習は当面 `do` からしか育たない。対話にPlanを載せるかは別ADRの論点
- `do` は消さない。非対話の経路はデーモン化（ADR-0004 Phase 2）の足場でもある
- 実装時ノブ: 履歴のプロセス跨ぎ永続化（v1はセッション内のみ）、スピナーの体裁、
  ターン数を `task.finished` に載せるか（手直しの強さの決定的な目安になりうる）

---

## 追記（2026-07-19）: ADR-0032（pipe chat の GUI 一級市民化）による拡張

Decision 3 の「非TTY（パイプ）はraw modeを使わず1行=1ターン」に、raw mode の
`\`+Enter と同じ意味論の行継続が入った: 末尾 `\` の行は `\` を1つ剥いで改行を挿み、
次の行へ続く（[ADR-0032](ADR-0032-pipe-chat-first-class.md) Decision 2）。
「テストとスクリプトの経路が同じコードを通る」性質はそのまま — 加えて GUI も
同じ経路を通る。あわせて `tomobit chat --view ndjson`（オプトイン）で pipe の
stdout を機械可読の view ストリームにできる（同 Decision 1。既定は従来の素テキスト）。
