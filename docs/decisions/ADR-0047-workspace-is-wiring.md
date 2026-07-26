# ADR-0047: 働く場所は配線である — Provider語彙から引き上げる

- Status: **Accepted**（2026-07-25 起草・実装。GUI（tomobit-gui ADR-0004）が「作業ディレクトリと読み取り先」を
  claude 固有の `TOMOBIT_CLAUDE_ARGS_APPEND` 経由で注入していたのを、所有者の指摘
  「ClaudeCodeだけに依存している点・次回チャットからじゃないと動かない点」を受けて本体へ引き上げた）
- Date: 2026-07-25
- 関連: [ADR-0006](ADR-0006-executor-adapter.md)（Adapterは1つのCLIを知るだけ・共通の器はExecutor）,
  [ADR-0021](ADR-0021-wiring-is-not-experience.md)（配線は経験ではない）,
  [ADR-0022](ADR-0022-chat-session.md)（1セッション=1タスク・区切りは人間が宣言する）,
  [ADR-0043](ADR-0043-auto-by-default.md)（Provider選択も同じ「タスク境界で替える配線」）,
  tomobit-gui [ADR-0004](https://github.com/Rererr/tomobit-gui/blob/main/docs/decisions/ADR-0004-workspace-scope.md)（GUIの作業バー）

---

## Context

端末で `tomobit chat` を打つ人は、打った場所がそのまま Tomo の作業場所になる。
Provider CLI は tomobit プロセスの cwd を継承するからで、「このリポジトリで話したい」は
`cd` で表明できていた。作業場所は今まで**暗黙の入力**で、tomobit はそれを知らずに済んでいた。

GUI（第三のレンダラ）にはその `cd` が無い。tomobit-gui ADR-0004 はこれを

- 作業ディレクトリ → chat 子プロセスの `cmd.Dir`
- 読み取り先 → `TOMOBIT_CLAUDE_ARGS_APPEND` に `--add-dir` を積む

で埋めたが、後者は **claude-code でしか効かない**。`TOMOBIT_CLAUDE_ARGS_APPEND` は
claude アダプタの ExtraArgs へ流れる口であり、codex を選んだ人には無言で無効になる。
GUI 側でこれを直そうとすると codex 固有の語彙を GUI が持つことになり、
「Adapter だけが CLI を知る」（ADR-0006）を GUI が破る。

もう一つ、env と cwd は**プロセス起動時に固定される**。GUI で場所を変えても、
走っている chat には届かない — 次にプロセスが立ち上がるまで効かない。
これは `/provider`・`/cap`・`/size` が持っている「タスクを区切れば次から効く」より
一段重い制約で、Provider 選択と同じ配線でありながら扱いだけが不揃いだった。

計測（2026-07-25、claude 2.x / codex 0.144.x 系）: **両 CLI とも `--add-dir <DIR>` を持つ**。
claude は「ツールアクセスを許す追加ディレクトリ」、codex は「主ワークスペースと並んで
書ける追加ディレクトリ」。細部の権限は違うが、「作業場所の外をこの場所も含めて扱わせる」
という**同じ意図の口**が両方に存在する。翻訳先はある。

---

## Decision 1: 作業場所は `executor.Request` の一級市民

`Request` に `WorkDir`（働く場所）と `AddDirs`（その外で扱わせる場所）を持たせる。
Provider の語彙ではなく、**Tomo の仕事の条件**として渡す。

呼び出し側（chat / do / split / duel）は「どこで働くか」だけを言い、
どのフラグに化けるかは知らない。ADR-0006 の分担そのままである。

## Decision 2: `WorkDir` は Executor が cwd に落とす — 翻訳しない

`cmd.Dir = req.WorkDir` は Executor が行う。cwd は OS の概念で、CLI ごとの語彙ではない。
codex の `-C/--cd` のような同義フラグがあっても使わない: 全 Provider に同じ意味で効く
経路が1つあるなら、Adapter に翻訳の余地を残すほうが不揃いを生む。

未設定（空）は従来どおり tomobit プロセスの cwd を継承する。**端末の挙動は1バイトも変わらない**。

## Decision 3: `AddDirs` は Adapter が自分の語彙へ翻訳する

- claudecode: `--add-dir <path>`（ディレクトリ1つにつき1回。可変長引数の後続フラグ境界に依存しない）
- codex: `--add-dir <path>`（同上。`exec resume` にも同じフラグを積む）
- human: 何もしない（人間は自分がどこにいるか知っている）

将来この口を持たない Provider が来たら、その Adapter は**黙って無視する**。
偽のフラグを発明して起動を失敗させるより、できないことをできないままにする方が正直で、
tomobit がその劣化を語る場所は Adapter ではなく人へ向けた表示側にある。

## Decision 4: chat では `/cd` と `/add-dir` — 既存の配線コマンドと同じ規律

`/provider`・`/cap`・`/size` と同じ `setWiring` に乗せる。したがって

- **タスクの途中では替えられない**。「/new で区切ってから」と答える
- 引数なしは現在値の表示

1セッション = 1タスク = 1経験（ADR-0022）である以上、途中で働く場所が変わるのは
「別の仕事を始めた」に等しい。それを区切りなしに許すと、1つの経験が2つの場所の話になる。
Provider を途中で替えさせないのと同じ理由で、ここも境界を要求する。

`/add-dir` の語彙は3つだけ持つ:

- `/add-dir` — 一覧
- `/add-dir <path>` — 足す（重複は黙って畳む）
- `/add-dir clear` — 全部外す

`clear` という名前のディレクトリと衝突しうるが、この口は絶対パスで使うものとして受ける
（相対パスの `clear` を足したい人は `./clear` と書ける）。外す操作を `/drop-dir` として
別コマンドに割るより、配線コマンドを1つ増やさない方を採った。

## Decision 5: 台帳には書かない

働く場所は配線であって経験ではない（ADR-0021）。`WorkDir`/`AddDirs` は台帳に記録しない。
「どこでやったか」が経験の一部になるべきかは、intent と outcome が既に語っている範囲を
超える話で、記帳の語彙（SCHEMA.md R3）を増やす判断は本ADRの外にある。

## Decision 6: 起動時は argv で受ける — 会話面をコマンドの応答で汚さない

`chat --cd <dir> --add-dir <dir>...`（`--add-dir` は繰り返し可）。`/cd`・`/add-dir` と
同じ値を、最初のタスクが開く前から持たせる口である。

GUI のような入口は、起動のたびに配線コマンドを打ち込む必要がなくなる。実機で
確認した実害はそこだった: 起動直後に `/add-dir clear` と `/add-dir <path>` を送ると、
本体の応答（`add-dir: なし` → `add-dir: /path`）が**毎回チャットの1行目に並ぶ**。
人が話し始める前に、配線の独り言が会話面を占める。

検証は両方の口で同じ関数（`checkWorkingPlace`）が行う: 起動時に断られるパスは
会話中も同じ理由・同じ言葉で断られる。二重の検証者を作らない。

---

## Consequences

- GUI（tomobit-gui ADR-0004）は claude 固有の env 注入をやめ、Provider に依らず効く経路に乗る。
  読み取り先の「claude-code のときだけ効く」という不揃いは消える
- 走行中の chat でも、タスクを区切ってさえいれば次のターンから新しい場所で働く。
  プロセスの再起動は要らなくなる（GUI は `/cd` を送るだけでよい）
- 端末の人も会話の途中で作業場所を宣言できるようになった。`cd` してから起動し直す必要はない
- `do`/`split`/`duel` は Request にフィールドが増えただけで挙動は変わらない（ゼロ値＝継承）。
  それらに口を出すかは、必要になってから別途決める

### Open Question

- `--add-dir` の権限の細部は Provider 間で揃っていない（claude はツールアクセス、
  codex は書き込み可能ワークスペース）。「読ませたいだけ」と「書かせてよい」を
  tomobit の語彙として分けるかは、実運用で困ってから決める

---

## 追記（2026-07-27）: 働く場所が、分割の子に届いていなかった

[ADR-0054](ADR-0054-a-child-is-the-breakdown.md) Decision 3 の修正。
本ADRは `WorkDir` / `AddDirs` を `executor.Request` の一級市民にしたが、
**分割の子の Request にはどちらも積まれていなかった**（duel の子には
`AddDirs` が渡っていたので、漏れていたのは分割の子だけ）。

`cmd.Dir` が空だと tomobit 自身の cwd を継ぐ。GUI は常に `--cd` を渡すので、
実運用ではこうなっていた:

```text
親  ~/repos/myapp        で働く
子  tomobit-gui の cwd   で働く   ← 別のリポジトリ
```

**失敗しないのが最悪の性質**である。Provider は立ち上がり、そこにあるものを読み、
それらしい答えを返す。

これは配線の話であって経験の話ではない、という本ADRの線は動かない。動いたのは
**配線がどこまで届くか**で、ADR-0054 の原則（子は親タスクの内訳）から
「子は親と同じ場所で働く」が出る。親が隔離を宣言していれば、子の cwd は
その宣言された場所になる（[ADR-0050](ADR-0050-workspace-isolation-protocol.md)
Decision 3 の「隔離の単位はセッションの木」が、ここで初めて実装として成立した）。
