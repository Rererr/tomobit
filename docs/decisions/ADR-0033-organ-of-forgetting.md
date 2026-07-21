# ADR-0033: The Organ of Forgetting — 忘却の器官

- Status: **Accepted**
- Date: 2026-07-19
- 関連: [ADR-0018](ADR-0018-experience-sovereignty.md)（経験主権 — 所有者だけが動かせる）,
  [SCHEMA.md](../design/SCHEMA.md)（D3 追記専用トリガー / D4 版数共存 / D10 rebuild）,
  [ADR-0004](ADR-0004-tech-stack.md)（真実と射影の分離）,
  [ADR-0005](ADR-0005-perception-model-and-schema-boundary.md)（知覚の版数）,
  [VISION](../core/VISION.md)（Experience is the Asset）

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

- トリガーは**単一トランザクション内**で一時 DROP → DELETE → 再作成する。
  SCHEMA.md D3 が予定していた「意図的な保守作業ではその時だけDROP」の実装。
  DDLもトランザクショナルなので、途中で死んでもトリガーごとロールバックされる
- COMMIT 後に `PRAGMA wal_checkpoint(TRUNCATE)` + `VACUUM` を実行し、
  WALと空きページに残る痕跡ごと消す。**「消した」と言って消えていないのは
  主権の嘘になる**（機微情報の忘却がこの器官の想定ユースケースに含まれる）
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
- **curiosity_queue**: 状態であり射影ではない（rebuild で消えない）。忘れた
  セッション由来のシグナルが残りうるが、payload は集約値のみで経験内容を
  含まない。v1 では忘却の対象外 — 個別に消したければ既存の dismiss がある
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
