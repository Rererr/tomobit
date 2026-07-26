# ADR-0053: 許可は人が与える — Providerの権限要求を、問いへ翻訳する

- Status: **Accepted**（2026-07-27 起草・所有者の方針として指示・実装済み。実 LLM で端から端まで実機確認 — 下記「実測（実装後）」）
- Date: 2026-07-27
- 関連: [ADR-0006](ADR-0006-executor-integration.md)（Decision 2: Adapterは起動と翻訳だけ — 権限の語彙もCLIを知っていることの一部）,
  [ADR-0047](ADR-0047-workspace-is-wiring.md)（働く場所は配線であって経験ではない — 許可も同じ側）,
  [ADR-0049](ADR-0049-quota-observation-is-opt-in.md)（沈黙は同意ではない）,
  [ADR-0050](ADR-0050-workspace-isolation-protocol.md)（隔離 — 実課金でこの穴を最初に踏んだ）,
  [ADR-0010](ADR-0010-codex-adapter.md)（Decision 2: 観測できないものを推測で埋めない）,
  GUI [ADR-0007](https://github.com/Rererr/tomobit-gui/blob/main/docs/decisions/ADR-0007-run-command-from-chat.md)（実行ボタン — 「便利さのために既定で開けてよい口ではない」）,
  GUI [ADR-0009](https://github.com/Rererr/tomobit-gui/blob/main/docs/decisions/ADR-0009-four-panes.md)（四分割 — 実課金の並走でこの穴が出た）

---

## Context

2026-07-26 の実課金の並走検証で、GUI から起動した Provider が**ファイルを書く仕事を
できない**ことが分かった。隔離プロトコル（ADR-0050）の指示は正しく届き、モデルは
「git worktree で隔離して作業を行います」と言った。その直後にこうなった:

> ファイル操作と git コマンドの実行に permission が必要です。許可をいただけますか？

`composeChatArgs` は `--permission-mode` を渡しておらず、本体の既定も `""`。
これは隔離が作った穴ではない — 隔離の前から GUI 経由の編集タスクは同じ壁に
当たっていたはずで、隔離プロトコルはそれを**作業の1歩目**へ前倒しにして
見えるようにしただけである。

自明な逃げ道は `bypassPermissions` を既定で渡すことだが、それは
GUI ADR-0007 が「実行経路は便利さのために既定で開けてよい種類の口ではない」と
切った判断を、無言で覆すことになる。

所有者の方針（2026-07-27）:

> このTomoは、頼れる相棒ではあるが「ユーザーがLLMをより気持ちよく使うためのガワ」
> でもある。ただし、**気持ちよく使うこととパーミッションは別**。
> 原則Automodeで各Providerを動かすことを前提に、**Providerから来たPermissionは
> モーダルで許可どりをする**。

---

## 実測（2026-07-27・claude 2.1.220 / haiku・総額 ~$0.19）

設計の前に、権限要求が headless でどう振る舞うかを測った。**結果が設計の形を決めた。**

- `--permission-mode` の選択肢は `acceptEdits` / `auto` / `bypassPermissions` /
  `manual` / `dontAsk` / `plan`。**`auto` は実在する**
- **`auto` だけでは足りない**。`--permission-mode auto` でも `Read` が拒否された:

  ```json
  {"type":"system","subtype":"post_turn_summary","status_category":"blocked",
   "status_detail":"cannot read README.md; permission denied",
   "needs_action":"grant read permission for README.md"}
  ```

  `result.permission_denials[]` に `{tool_name, tool_use_id, tool_input}` が載る

- **許可要求はターンを終わらせる。** `--input-format stream-json`（双方向）でも
  `control_request` の類は一切来ず、`post_turn_summary` の後そのまま `result` で
  終わった。**その場で許可して続行する経路は、この build の CLI には無い**
- **`--allowedTools` は効く**。`--allowedTools "Read" "Edit" "Write"` を足すと
  拒否は0件になり、ファイルが書けた
- **codex は語彙が違う**: `--sandbox` の値は `read-only` / `workspace-write` /
  `danger-full-access`。`auto` を渡したら壊れる

---

## Decision 1: 既定は auto。ただし語彙は Adapter が訳す

`PermissionMode` の既定を `""`（フラグを渡さない）から **`auto`** へ変える。
「原則 Automode」を既定にする、という方針そのもの。

**ただし tomobit は `auto` という文字列を Provider へ素通ししない。** 現状の実装は
`--permission-mode <v>` / `--sandbox <v>` へ**同じ文字列を渡している**が、
実測のとおり2つのCLIは別の語彙を持つ。tomobit 側は中立の3語だけを持ち、
Adapter が自分のCLIの語へ訳す:

```text
tomobit          claude-code               codex
auto        →    --permission-mode auto    --sandbox workspace-write
strict      →    --permission-mode manual  --sandbox read-only
open        →    --permission-mode bypassPermissions
                                           --sandbox danger-full-access
```

これは ADR-0006 Decision 2 の境界そのものである —「Adapterが知ること: CLIの
起動方法（コマンド・フラグ）」。権限モードの語彙も**CLIを知っていることの一部**で、
tomobit が片方のCLIの語を持つのは、その境界を踏み越えている。

**正直に書く**: この対応表は**同型ではない**。claude の permission は「訊く」機構で、
codex の sandbox は「囲う」機構である。`auto` ↔ `workspace-write` は
「普通に働けて、外に出るときだけ止まる」という**意図**の対応であって、
挙動の等価ではない。訳せないものを訳したことにしない。

却下した対案:

- **文字列を素通しし続ける**（現状）: 使う側が「claude なら auto、codex なら
  workspace-write」を覚えることになる。Provider を選ぶのは決定エンジンなので、
  **使う側にはどちらが走るか分からない**
- **`bypassPermissions` を既定に**: GUI ADR-0007 の判断を無言で覆す。所有者の
  方針が明示的に否定した道でもある

---

## Decision 2: 許可要求は「その場で続行」ではなく「許可 → 再実行」

実測のとおり、**許可要求はターンを終わらせる**。双方向モードにも応答経路が無い。
したがってモーダルの意味論はこうなる:

```text
1. ターンが権限で止まる（provider が何を求めたかは permission_denials に載る）
2. tomobit がそれを「問い」として人へ出す
3. 人が許すと、その道具を AllowedTools に足して**同じターンを再実行する**
4. 会話は途切れない — スレッドID は init 行で既に取れている（ADR-0022 Decision 2）
```

**代償を隠さない**: 再実行は**もう一度トークンを燃やす**。止まるところまでの費用は
戻ってこない。これは設計の選択ではなく CLI の制約の写像で、
`control_request` が使えるようになれば「その場で続行」へ差し替えられる
（そのときこの Decision は不要になる）。

再実行するのは**止まったターンだけ**である。人が断ればターンはそのまま終わり、
会話は続く — 断ることが作業の放棄にならないのは、ADR-0028 の並走ゲートが
「n でも作業は失われない」と決めたのと同じ姿勢。

却下した対案:

- **止まったことを黙って受け入れる**（今の姿）: 人はモデルの「許可をいただけますか？」
  という文章を読むが、**答える口がどこにもない**。問いの形をしていて答えられない
  のが、一番悪い
- **拒否された道具を自動で許して再実行**: それは `bypassPermissions` を遠回しに
  実装しているだけである

---

## Decision 3: 許可の粒度は道具、寿命はセッション。ディスクには書かない

モーダルが人に見せるのは**何を求められたか**で、許すのは**その道具**である。

- 粒度は `tool_name`（`Read` / `Edit` / `Bash` …）。求められた入力
  （どのファイル、どのコマンド）は**人に見せるが、許可の単位にはしない** —
  パターンの粒度（`Bash(git *)` のような）をここで発明すると、tomobit が
  claude の許可DSLを持つことになる（Decision 1 と同じ境界）
- 寿命は**このチャットのセッション**まで。次のタスク（`/new`）へは持ち越さない
- **ディスクに書かない**。「毎回許可するのが面倒だから覚えておく」は、
  ADR-0049 が「沈黙は同意ではない」と切った線の内側に落ちる — 覚えた許可は、
  次に人が見ていない時にも効く

却下した対案:

- **gui.json に永続化**: 上記。将来やるなら、それ自体が別の同意を要る決定である
- **入力ごとに許可**（このファイルだけ / このコマンドだけ）: 粒度としては正しいが、
  許可DSLを tomobit が持つことになる。Provider の語彙は Adapter の内側に留める

---

## Decision 4: 許可は配線であって経験ではない — 台帳に書かない

`AllowedTools` は「どう走らせるか」であって「何が起きたか」ではない。
ADR-0047 が働く場所について引いた線がそのまま当たる。

- 台帳に `user.permitted` のようなイベントは作らない
- 知覚も学習も、許可の有無を見ない
- **拒否で止まったターンは `provider.error` にもしない**: 実行は正常に終わっており
  （`is_error` は立たない）、失敗したのは作業であって Provider ではない。
  ここで `y=0` を積んだら、権限を渡さなかった人間の判断が Provider の成績になる

例外の余地を1つ残す: 「権限で止まった率」は運用の健全性の指標になりうる。
ただし**測りたくなってから決める** — 先回りして台帳に型を増やさない。

---

## Decision 5: 問いは境界の器官と同じ経路で出す

新しい表示経路を作らない。権限の問いは `chat --view ndjson` の
`{"type":"permission", "await":true, ...}` として流す（本体 ADR-0032 の契約）。

- **GUI は語彙を持たない**（GUI ADR-0005 Decision 2 と同じ）。何を許すのかも、
  選択肢も、本体が出した行から読む
- 端末（TTY）では、境界の Feedback と同じ1行の問いになる
- 非TTY・スクリプトでは**答えが来ないので拒否**として扱い、ターンはそのまま終わる。
  黙って許すのは、この ADR が防ごうとしているものそのもの

---

## Consequences

- `executor.Request` に `AllowedTools []string` が増える。claude は
  `--allowedTools`、**codex には対応するフラグが無い**（sandbox は道具ごとの
  許可を持たない）ので、codex では許可の問いそのものが立たない。
  **Provider によって体験が違う**ことは隠さない
- `PermissionMode` の既定が `auto` になる。既存の `--permission-mode` フラグは
  中立の3語（`auto` / `strict` / `open`）を受け、CLIの生の語は受けなくなる
- GUI はモーダルを1つ足す（GUI ADR-0007 の作法: **何を許すのかを見せてから**）
- **再実行のコスト**は人に見える形で示す（モーダルに「許可して再実行」と書く）
- 実測で使った claude は 2.1.220。`control_request` が使える build が来たら
  Decision 2 は差し替えになる — その時に備えて、再実行は chat 側の1箇所に閉じる
- **索引 / SCHEMA**: 新しいイベント型は作らないので SCHEMA.md は不変。
  README の索引は Accepted 時に追加する

---

## 実測（実装後・2026-07-27・実 claude / haiku・総額 ~$0.26）

使い捨て台帳と使い捨て git リポジトリで、GUI から実タスクを1つ流した。

```text
1回目  Bash（git status）と Read（README.md）を求めて停止
       → モーダルに道具名と触ろうとした先が並ぶ → 許可 → 同じターンを再実行
2回目  Edit を求めて停止。対象は
       ~/.tomobit/worktrees/0019f9f59824c-f1bdd244/README.md
       ← 隔離が効いている（ADR-0050 の worktree の中）
       → 許可 → 再実行 → 完走
```

結果:

```text
worktree   task/add-granted ブランチに "granted" が着地
元リポ     "# probe" のまま無傷
台帳       task.workspace {"isolated":true,"kind":"git worktree","path":"…"}
           provider.selected × 3（元 + 再実行2回）
           permission.required / provider.error は 0件 ← Decision 4
```

**ADR-0050 の隔離と ADR-0053 の許可が噛み合って、初めて GUI から実作業が通った。**
2026-07-26 の並走検証で「GUI から起動した Provider はファイルを書く仕事ができない」
と書いた状態が、ここで解けている。

観測できたこと:

- **許可は道具ごとに、必要になった順で求められる**。1回で全部は出てこない
  （モデルは Bash → Read → Edit と、使う段になって初めて止まる）。
  Decision 3 の「粒度は道具」は実運用の形と一致した
- **セッション内で許可が積み上がる**ので、2回目の問いは Edit だけになった。
  同じ道具を二度訊かない実装が実際に効いている
- **`maxPermissionRounds`（3）は妥当に見える**。実測では2巡で終わった
- **費用は素直に増える**。1タスクで3回走ったので、素の1回より高い。
  問いに「費用がもう一度かかる」と書いてあることの意味が、実運用でそのまま出る

### 追測（2026-07-27・codex 0.145.0・$0.04）— Decision 1 の「同型ではない」の実物

同じ形の仕事（隔離して1ファイル直す）を codex 側でも1回流した（詳細は
[ADR-0050](ADR-0050-workspace-isolation-protocol.md) の同日の実測）。

**予告どおり、許可の問いは1度も立たなかった。** codex の sandbox は道具ごとの
許可を持たないので、止まって訊く事象がそもそも発生しない。代わりに観測できたのは
**止まらずに迂回する**という別の振る舞いである:

```text
claude   .git を触る前に止まって訊く    → 人が許可 → 同じ手段で続行
codex    .git への書き込みが黙って失敗  → 別の手段（clone）へ自分で切り替え
```

- 共有 Go ビルドキャッシュが書き込み不可だったときも同じで、一時 `GOCACHE` を
  作って進んだ。**sandbox は「訊く」機構ではなく「囲う」機構だ**という
  Decision 1 の但し書きが、そのまま観測になった
- 体験差は Consequences に書いたとおりで、隠さない。ただし実測で判ったのは
  **差は「codex では許可が要らない」ではない**ということ — 制約は同じだけ効いていて、
  人に見えないまま Provider が飲み込んでいる。どちらが良いかはここでは決めない
- **第1層（ADR-0052）はこの摩擦を継がない**。テストコマンドは tomobit 自身が
  sandbox の外で走らせるので、Provider 側の書き込み制約に左右されない

---

## 実装フェーズ（Proposed）

1. **語彙の翻訳**（Decision 1）: 中立3語と Adapter の訳。純関数なのでテストで固定
2. **許可要求の検出と問い**（Decision 2/5）: Adapter が `permission_denials` を
   正準イベントへ訳し、chat が問いを出して答えを読む
3. **再実行**（Decision 2）: 許可された道具を足して同じターンを走らせ直す
4. **GUI のモーダル**: 本体の行を読んでボタンにする（GUI 側は語彙ゼロ）

---

## 実装時ノブ

- 中立語の名前（`auto` / `strict` / `open`）
- モーダルの文言と、入力（ファイル名・コマンド）をどこまで見せるか
- 一度に複数の道具を求められたときの見せ方（1つずつか、まとめてか）
- 再実行の回数上限（同じターンで何度まで許可を求めさせるか）
