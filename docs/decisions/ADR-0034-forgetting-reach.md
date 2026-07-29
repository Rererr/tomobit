# ADR-0034: 忘却の到達範囲 — 世代の巻き戻りを作らない

- Status: **Accepted**
- Date: 2026-07-21
- 改版: ADR-0033 D2 D5
- 関連: [ADR-0033](ADR-0033-organ-of-forgetting.md)（忘却の器官 — 本ADRはその Decision 2/5 の到達範囲を確定する）,
  [ADR-0018](ADR-0018-experience-sovereignty.md)（経験主権）,
  [SCHEMA.md](../design/SCHEMA.md)（D4 版数共存 / experiences_current の選択規則）,
  [VISION](../core/VISION.md)（Experience is the Asset）

---

## Context

ADR-0033 は忘却を「指名した行の物理削除」として実装した。
指名の単位が**行**であることが、版数共存（D4）と噛み合っていない。

`experiences_current` は (session_id, kind) ごとに **max(extractor_ver) の世代**を現行として選ぶ。
同じ (session, kind) に世代が積まれている状態で現行世代の行だけを消すと、
ビューの選択が**一つ下の世代へ落ちる**。

```text
gen2 (ver 2, human)  A2'  B2      ← 現行。A2' は人が訂正した内容
gen1 (ver 1, qwen3)  A1   B1      ← 旧世代。A1 は訂正前の機械知覚

forget --id A2'  →  gen2 に A が居なくなり、max(ver) は依然 2（B2 が居る）…
                     ではなく、A の系譜としては gen1 の A1 が
                     experiences_current に現れ続ける
```

実測（tomobit-gui `memory_e2e_test.go` の観測ログ、2026-07-19）でも、
人が訂正して忘れた経験の**機械知覚版が現行として戻った**。

これは「忘れられなかった」より悪い。**所有者が明示的に訂正した内容が、
忘却という行為をきっかけに復活する** — 主権の反転である。
ADR-0033 Decision 4 が「人間の知覚は最終知覚である」と置いた恒常性が、
忘却それ自体によって破られている。

同じ形は amend が無くても起きる。extractor_ver を上げて再知覚したセッションの
現行世代を忘れれば、旧 extractor の知覚が現行へ戻る。
**世代が積まれうるすべての経路に同じ穴がある。**

---

## Decision 1: 忘却の単位は行ではなく「(session, kind) の現行世代とその下の全世代」

`forget --id <exp-id>` は、指名された行に加えて、
**同じ (session_id, kind) の extractor_ver がそれより小さい全行**を削除する。

```text
forget --id A2'  →  A2' を削除し、ver < 2 の gen1（A1, B1）も削除する
                     現行世代の兄弟 B2 は残る
```

これで、削除後に (session, kind) の max(ver) が下がることは起こり得ない。
**ビューが過去へ巻き戻る経路が構造的に消える**のであって、
再浮上のケースを個別に潰しているのではない。

複数の `--id` を与えたときは各 id について同じ範囲を取り、その和集合を1トランザクションで消す。

## Decision 2: 忘却できるのは現行世代の行だけ — amend と同じ規律

指名された id が superseded（現行世代でない）なら**エラーにする**。
ADR-0033 Decision 3 が amend に課したのと同じ線を引く。

理由は無意味だからではなく、**嘘になるから**である。
amend の copy-forward は兄弟行を内容ごと次の世代へ運ぶ。
旧世代の行を消しても、その内容は現行世代の複製として残る。
「消した」と報告しながら同じ文字列がDBに残るのは、
ADR-0033 Decision 5 が禁じた**主権の嘘**そのものである。

内容を消したければ現行世代の行を指名する — Decision 1 の連鎖が旧版まで届く。

## Decision 3: 巻き添えは数えて告知する

Decision 1 の連鎖は、**同じ (session, kind) の兄弟系譜の旧世代**も巻き込む。
兄弟の現行行は残るので現在の知覚は変わらないが、
兄弟の**訂正前の内容**は失われる。

ADR-0033 Decision 2 の「巻き添えは作らない」（`forget --session` は子セッションを消さない）に対する
明示的な例外であり、隠さず数えて出す:

```text
forget: 1 experiences (+2 superseded rows) (rebuilt: 14 connections)
```

`--id` が現行世代の行しか受け付けない以上、
ユーザーが見ている行（GUIメモリViewは experiences_current を映す）は必ず消える。
追加で消えた行数は、その行為の代償として画面に出る。

---

## 却下した対案

- **experiences に系譜列（lineage）を足し、系譜単位で消す** →
  amend の copy-forward は系譜を追跡できるが、**機械の再知覚は追跡できない**。
  extractor_ver を上げた再知覚は、行数すら前世代と一致しない
  （kind=preference は抽出された選好の数だけ行が生える）。
  系譜列は amend 経路だけを誠実にし、再知覚経路の巻き戻りを残す。
  **半分だけ誠実な機構は、全部誠実な機構より悪い** — 穴の在り処が
  「どちらの経路で世代が積まれたか」という利用者に見えない条件になるからである。
  真実テーブルへのDDL追加という代償を払って、その状態は買えない

- **旧世代を残したまま experiences_current 側で tombstone を除外する** →
  真実に「消えたことになっている行」が残る。ADR-0033 Decision 5 は
  忘却を論理削除でなく物理消去（VACUUM まで）と決めており、
  機微情報の消去がこの器官の想定ユースケースに入っている。
  ビューだけの忘却は、その用途で嘘になる

- **superseded な id の指名を許し、その行だけ消す** → Decision 2 の通り、
  copy-forward された兄弟に同じ内容が残るため「消した」が嘘になる。
  黙って no-op にするより悪い（ADR-0033 Decision 5: typo が「忘れたつもり」を作るのが最悪の失敗モード）

- **(session, kind) の全世代を消す（現行の兄弟ごと）** → 指名していない
  **現在の知覚**まで消える。所有者が見ていない行を消すのは主権の行使ではなく事故

---

## Consequences

- `forget --id` の削除行数は指名数より多くなりうる。CLI の1行サマリ
  （ADR-0033 Decision 6 の契約）に `+N superseded rows` を追加する。
  GUI はこのサマリをそのまま出せる
- 現行世代でない id を指名するとエラーになる。GUI のメモリViewは
  experiences_current を映しているので、View から辿れる id は常に現行世代である
  — GUI 側の変更は不要（tomobit-gui BACKLOG の該当項目が閉じる）
- ADR-0033 Decision 2/5 に本ADRへの改版参照を追記し、README 索引に ADR-0034 を追加する
- SCHEMA.md の D4（版数共存）に、忘却が世代を跨いで到達することを注記する
- 台帳・知覚・射影の機構は不変。`user.forgot` マーカーの意味も変わらない
  （payload には指名された id のみ — 連鎖で消えた行の id は載せない。
  マーカーは「所有者が何を忘れると言ったか」の記録であって、削除ログではない）
