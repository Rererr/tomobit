# ADR-0033: The Organ of Forgetting — 忘却の器官

- Status: **Accepted**
- Date: 2026-07-19
- 関連: [ADR-0034](ADR-0034-forgetting-reach.md)（忘却の到達範囲 — Decision 2/5 を改定）,
  [ADR-0055](ADR-0055-verdict-is-a-veto.md)（verdict — 動詞の分業相手。amend との対比表の正本）,
  [ADR-0018](ADR-0018-experience-sovereignty.md)（経験主権 — 所有者だけが動かせる）,
  [SCHEMA.md](../design/SCHEMA.md)（D3 追記専用トリガー / D4 版数共存 / D10 rebuild）,
  [ADR-0004](ADR-0004-tech-stack.md)（真実と射影の分離）,
  [ADR-0005](ADR-0005-perception-model-and-schema-boundary.md)（知覚の版数）,
  [VISION](../core/VISION.md)（Experience is the Asset）

<!-- 改版:begin — tools/sync-adr-superseded.sh が生成する。手で編集しない -->
> **改版済み** — この決定の一部は後のADRが置き換えた。範囲は各Decisionの改版注記が持つ。
>
> - Decision 2 → [ADR-0034](ADR-0034-forgetting-reach.md)
> - Decision 5 → [ADR-0034](ADR-0034-forgetting-reach.md)
<!-- 改版:end -->

---

## Context

GUI（tomobit-gui ADR-0001 Decision 3）のメモリViewは読み取り専用で止まっている。
経験の編集・削除は「本体側の忘却の器官のADR待ち」— 本ADRがそれである。

experiences と events はDBトリガーで追記専用になっている（SCHEMA.md D3/D4）。
これは資産の保護だが、間違った知覚・記録したくなかった内容・機微情報を
**所有者すら**消せないなら、それは保護ではなく幽閉である
（ADR-0018: 「完全禁止は主権ではなく幽閉」）。

削除・編集には二つの固有の整合問題があり、個別に作ると危険なので
一つの器官として設計する:

1. **射影整合** — connections / surprise_ledger は経験の射影。真実を削れば
   射影も作り直さなければ、消したはずの経験が台帳に残り続ける
2. **再知覚による復活** — 経験だけ消しても生eventsが残る限り、
   extractor_ver 改版後の perceive が PendingSessions でそのセッションを
   拾い直し、忘れたはずの記憶を再抽出する。人が訂正した経験も、
   機械の再知覚が黙って上書きする

---

## Decision 1: 忘却は主権の行使である — VISIONとの両立

「Experience is the Asset」の資産という言葉には**処分権が含まれる**。
ADR-0018の「Tomobitを育てた経験は、誰にも持っていかれない」は
「所有者が手放せない」を意味しない。持っていかれない・けれど手放せる、が資産である。

役割の線引きを固定する:

- 追記専用トリガーが守るのは **Tomo・バグ・手癖からの**不変性であり、この意味は変えない
- 忘却の器官の呼び口は**CLIのみ**（＝所有者と、その代理たるGUI）。
  Tomoの自発経路（decide / reflection / curiosity）はこの器官に配線しない。
  **Tomoは忘却を提案も実行もしない** — 忘却は人間の動詞である
- decay は重みの忘却であり真実は残る（数学の器官）。本器官は真実の外科手術であり
  所有者専用（主権の器官）。二つは別物で、後者だけが人の手を要る

---

## Decision 2: 二つの動詞 — forget（忘却）と amend（訂正）

> **改版**: 本 Decision の `--id` は「指名した行の物理削除」として書かれたが、
> 版数共存（D4）と噛み合っていなかった。指名の単位・到達範囲は
> [ADR-0034](ADR-0034-forgetting-reach.md) が改定する — `--id` は現行世代の行のみを受理し、
> 同じ (session, kind) の下位世代も併せて削除する。下の「巻き添えは作らない」は
> `--session` と子セッションの関係についての規律であり、そちらは不変。
> 世代方向の巻き添えは ADR-0034 Decision 3 が明示的な例外として置き、数えて告知する。

```text
tomobit forget --id <exp-id> [--id ...]   経験単位の忘却（行を物理削除）
tomobit forget --session <session-id>     セッション単位の完全忘却
                                          （events + experiences を物理削除。
                                           生ログの機微内容まで消す唯一の手段）
tomobit amend  --id <exp-id>              経験の訂正（削除ではなく追記 — Decision 3）
               [--context '<json>'] [--outcome '<json>'] [--provider <name>]
```

- 両動詞とも、真実の変更後に**同一コマンド内で自動 rebuild** する（D10）。
  射影整合をユーザーの手順（forget してから rebuild を忘れずに…）にしない
- forget は不可逆なので確認ゲートを置く: TTYでは y/N、非TTYでは `--yes` 必須
  （GUIは `--yes` を付けて呼ぶ）
- `forget --session` は削除前に子セッション（task.started の parent 参照）を列挙して
  通知するが、消すのは指名されたセッションだけ — 巻き添えは作らない。
  ツリーごと消したければ列挙された id で再実行する（GUIはツリー表示で束ねられる）。
  通知は stderr に出す — stdout は Decision 6 の1行サマリ契約を守る

---

## Decision 3: amend は削除ではなく「人間による再知覚」

D4の版数共存機構をそのまま使う。人間を抽出器とした再知覚として**追記**する:

- 対象経験と同じ (session_id, kind) の現行世代**全行**を extractor_ver+1 で
  再主張（copy-forward）し、対象行だけ内容を差し替える。全行を運ぶのは
  experiences_current が (session, kind) ごとの最大verを選ぶため —
  対象行だけ ver を上げると兄弟行がビューから消える
- 差し替えた行の extractor_model は `human`。**運ばれただけの兄弟行は元の
  extractor_model を保持する**（出自は嘘をつかない）。ts は元の出来事の時刻を保持
- 編集できるのは **context / outcome / provider**。plan は機械属性
  （ハーネスだけが知る採用Plan — ADR-0014）なので人間の再知覚の対象外
- 対象は experiences_current の行のみ。過去世代の訂正は「現在の知覚」を
  変えないので意味を持たない（エラーにする）
- バリデーション:
  - context: JSONオブジェクト（値は文字列）。key は既存スキーマの閉集合
    （cap / lang / framework / topic / size / model — SCHEMA.md R2）。
    **人間も key を増やせない** — 増やすのは schema 改版（コード変更）である。
    value は CanonValue で正規化
  - outcome: `core.Outcome` に厳密 unmarshal（未知フィールド拒否）
  - provider: Adapter登録名 + `human` に限定（R3: 自由入力を許さない）

amend はトリガーに触れない。真実は追記のみで表現され、旧知覚も履歴として残る。
内容ごと消したいときは forget を使う — 動詞の役割が重ならない。

---

## Decision 4: 忘却の恒常性 — 人の手が入ったセッションは再知覚しない

**人間の知覚は最終知覚である。**

forget（経験単位）と amend は、その事実自体を events に記帳する:

```text
user.forgot   payload = {ids: [<exp-id>, ...]}     忘れた対象のidのみ。内容は載せない
user.amended  payload = {id: <exp-id>, ver: <new>} 差し替え元idと新しい世代
```

PendingSessions（Deferred Perception のキュー導出）は、このマーカーを持つ
セッションを**恒久的に除外**する。これがないと、extractor_ver 改版のたびに
機械が忘れた記憶を復活させ、人の訂正を「より良い解釈」で上書きする —
それこそが主権侵害である。

- 帰結: 該当セッションは以後の抽出器改善の恩恵を受けない。
  これは代償ではなく定義 — 所有者が確定させた記憶は機械が触らない
- 記帳は真実であり追記（忘却もRealityである）。マーカーには id しか載せず、
  忘れた**内容**は運ばない
- `forget --session` はマーカー不要 — events ごと消えるため、
  PendingSessions の導出元から存在自体が消える

---

## Decision 5: forget は物理消去 — VACUUM までやる

> **改版**: 消す行の範囲は [ADR-0034](ADR-0034-forgetting-reach.md) が改定した。
> 物理消去の対象は「指名された行」ではなく、その系譜の下位世代を含む削除結果である
> — 旧世代に残る訂正前の本文まで消えて初めて、物理消去が主権の嘘にならない。

- トリガーは**単一トランザクション内**で一時 DROP → DELETE → 再作成する。
  SCHEMA.md D3 が予定していた「意図的な保守作業ではその時だけDROP」の実装。
  DDLもトランザクショナルなので、途中で死んでもトリガーごとロールバックされる
- COMMIT 後に `VACUUM` → `PRAGMA wal_checkpoint(TRUNCATE)` の順で実行し、
  WALと空きページに残る痕跡ごと消す。**「消した」と言って消えていないのは
  主権の嘘になる**（機微情報の忘却がこの器官の想定ユースケースに含まれる）。
  順序が literal に効く: journal_mode=WAL では VACUUM が書き直す
  コンパクトなページ列は本体 db ファイルではなく WAL フレームへ着地する。
  先に checkpoint すると、その時点の WAL（VACUUM 前の、削除済みだが
  未整地なページを持つ内容）だけを本体へ書き戻して終わり、後から走る
  VACUUM の出力は WAL に残ったまま次の checkpoint を待つ — つまり
  「消した」の対象が本体ファイルへ一度も辿り着かない。VACUUM を先に
  走らせれば、WAL が持つ最新の内容は常に「削除済みかつ整地済み」になり、
  その後の checkpoint(TRUNCATE) が本体ファイルへ書き戻して WAL を空にする
  操作こそが、ディスク上の旧バイト列を実際に上書きする一手になる
- checkpoint は**1回だけ**呼ぶ。リトライは張らない — 実測（modernc/sqlite・WAL・
  `busy_timeout=5000`）で、アイドルの read-only 接続も SELECT を終えた接続も
  TRUNCATE を妨げず（busy=0・1ms 未満）、妨げるのは**開いた読みトランザクションを
  保持する接続だけ**で、その相手には PRAGMA 自身が busy_timeout を丸ごと待ってから
  busy=1 を返す。待つ機構は既に busy_timeout であり、その外側のループは同じ5秒を
  試行回数だけ掛け算する（実測: 5回で25秒）以外に何もしない。顔窓のポーリング
  （ADR-0020、mode=ro）は問い合わせて返る側なので、そもそも妨げない
- busy 報告は PRAGMA の**正常な出力**であってエラー返却ではないため、ここで
  エラーへ変換しなければ沈黙になる。沈黙にはしない: 対象の行は既に論理削除
  済みで、同じ id の forget をやり直しても「unknown experience」で弾かれる
  — 物理消去だけを再試行する経路は無い。正直なエラーだけが
  「物理消去は未完」を伝える唯一の機会である
- 逆向きの嘘もつかない: VACUUM が失敗しても論理削除と rebuild は commit 済み。
  サマリを出した上で「物理消去は未完」を明示してエラー終了する —
  削除の成否と物理消去の成否を出力上分離する
- 指名された id / session が存在しなければエラー（黙って no-op にしない —
  typo が「忘れたつもり」を作るのが最悪の失敗モード）

---

## Decision 6: 周辺整合

- **射影**: 自動 rebuild で全再生（D10）。忘れた経験の ledger 行・そこからしか
  生えていなかった Connection は消え、Split系譜も現存経験だけから再構築される
- **plan.generated**: セッションと共に消えれば Plan メニューからも消える
  （メニューの生存は events から導出 — 真実が変われば導出も変わる、で一貫）
- **curiosity_queue**: 状態であり射影ではない（rebuild で消えない）。ただし
  v1 時点では書き手が存在しない — Preference Gap は View として導出され
  （ADR-0007 Decision 2）、残り 5 シグナルの書き手（「学習実行」）は ADR-0007
  が別 ADR へ先送りしたまま未実装（Go 側に INSERT/SELECT が無い）。忘れた
  セッション由来の行がここに残るという事態そのものが今は起こり得ないので、
  忘却がこのテーブルに触れるかどうかはまだ問う必要がない — 書き手が実装
  される時に、そのADRの中で決める
- **GUIの口**: 本CLIがそのまま呼び口。メモリViewは mode=ro 読取のまま、
  書き込みは `tomobit forget` / `tomobit amend` のサブプロセス実行で行う
  （終了コード + 1行サマリ。既存コマンドと同じ流儀）

---

## Consequences

- SCHEMA.md の追加済み type カタログに `user.forgot` / `user.amended` を記載し、
  PendingSessions 導出の除外条件を注記する（封筒方式のため DDL 変更はない）
- README / usage に forget / amend を追記
- tomobit-gui BACKLOG「メモリの編集・削除」のブロックが解除される
  （View は読み取り、書きは CLI、という責務分担のまま）
- 「経験は消せる・訂正できる」が、rebuild と同様**いつでも試せるコマンド**として
  体現される（再生成可能性を常時テストするのと同型の、処分権の常時テスト）

---

## 追記（2026-07-27）: verdict は3本目の動詞ではない — 判定だけを差し替える

[ADR-0055](ADR-0055-verdict-is-a-veto.md) が `tomobit verdict` を足した。
Decision 2 の「二つの動詞」に3本目を並べるのではなく、**amend の隣に置いて
役割を割る**形にしてある。対象・機構・出自・凍結・取消の対比表は
[ADR-0055](ADR-0055-verdict-is-a-veto.md) Decision 3 が正本（分業を決めたのは
あちらなので、ここには写さない）。

一言でいえば **「後で言えるようになった」は verdict、「意味の取り方が
間違っていた」は amend**。Decision 2 が forget と amend の間に引いた
「動詞の役割が重ならない」線が、そのまま延びている。

### Decision 3 の copy-forward が全行なのに、verdict が1行で済む理由

Decision 3 が兄弟行を全部運ぶのは `experiences_current` が (session, kind) ごとに
最大版を選ぶためで、対象行だけ版を上げると兄弟がビューから消える。だが
**view の grouping は kind 単位**であり、execution 行はセッションに1つしかない
（`perceiveSession` が1件だけ作る）。preference の兄弟は別 kind なので巻き込まれ
ない。verdict の繰り上げは execution 1行のコピーで閉じる。

### Decision 4 の凍結を verdict に広げてはいけない

D4 が守っているのは「人の訂正を、機械が『より良い解釈』で上書きする」事故である。
verdict は**機械が知覚した意味（context）に1バイトも触れない** —
経験の行のうちモデルの主張は context だけで、outcome は最初から
`parseDeterministic`（決定的コード）の領分だからである。守るべき訂正が存在しない
ので凍結の理由が無く、むしろ凍結**してはいけない**: 👍 を1つ付けた代償に将来の
抽出器改善を全て失うのでは、拒否権の行使に税金がかかる。

同じ理由で `extractor_model` も書き換えない。出自が変わっていない行の出自表示を
変える方が嘘になる（Decision 3 の「出自は嘘をつかない」の適用）。

### verdict が断る相手に amend 済みが入っている

逆向きは塞いである。`user.amended` を持つセッションへの verdict は断り、
`amend --outcome` を案内する — D4 で凍結済みのセッションに、より軽い器官を
重ねない。
