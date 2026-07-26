# ADR-0041: 順序外の知覚は正典に立ち返る — 乖離は静かに残さない

- Status: **Accepted**（2026-07-24 起草・goal 指示の自律セッションが先行適用、同日所有者が配備裁定で追認）
- Date: 2026-07-24
- 関連: [ADR-0005](ADR-0005-perception-model-and-schema-boundary.md)（知覚の実装）,
  [ADR-0029](ADR-0029-perception-backend-choice.md)（バックエンド停止時は pending に積まれる — 本件の露出条件）,
  [ADR-0033](ADR-0033-organ-of-forgetting.md)（forget が自動 rebuild する前例）

---

## Context

live の射影更新（Apply）は `(ts, id)` 順で適用される前提を持ち、engine.go は
こう自認している:

> If a live Apply ever runs out of (ts, id) order the projection can
> diverge regardless; `tomobit rebuild` restores the canonical state.

この前提は正規運用では守られるが、**遅延知覚**で破れる。知覚バックエンドが
落ちていた日の経験は pending に積まれ（ADR-0029 の設計どおり）、復旧後の
`tomobit perceive` で消化される — このときバッチ内はソートされるが、
**既に知覚済みの経験より古い ts** がバッチに混ざることは検知されない。

合成 dogfood での実測（2026-07-24）: 30日前に終了したセッションを後から
知覚すると、過去の成功が減衰重み 1.0（本来 2^(−26/90)=0.82）で加算され、
live 射影 α=7.6604 vs rebuild 7.4789 の乖離が生じた。status 表示にも出る
（0.58/10.9 vs 0.57/10.8）。rebuild は forget / amend / 手動でしか走らない
ため、**古い証拠ほど過大評価された射影が無期限に残る**。

## Decision

### perceive は、live Apply が正典と一致しない形のバッチを検知したら rebuild する

live Apply が正典（rebuild）と一致するのは、**バッチ全体が既知覚の厳密な
後続で、かつ世代交代を含まない**場合だけである。判定は二条件:

- **順序外**: バッチの最小 `(ts, id)` が、既に知覚済みの経験の最大 `(ts, id)`
  より小さい（遅延知覚 — ADR-0029 の pending 消化）
- **世代交代**: バッチに、既知覚と同一 `(session_id, kind)` の経験が含まれる
  （extractor_ver 改版後の再知覚）。再知覚の新世代は旧世代と**同じ ts** を
  持ち（経験の ts はセッション末尾イベント由来）、id はランダム接尾辞のみで
  順序に意味が無いため、(ts,id) 判定はコイントスに帰着する。しかも live
  Apply は旧世代が射影へ与えた寄与を撤回できず、証拠の二重計上になる —
  `amend` が常に無条件 rebuild する（ADR-0033）のと同じ理由で、rebuild 必須

どちらにも該当しない通常運用: 今日と1ビットも変わらない — live Apply のまま
- 順序外: バッチの記帳（experiences への追記）後、live Apply の代わりに
  `rebuild` を走らせる。forget が既にやっていること（ADR-0033: 削除後の
  自動 rebuild + vacuum。ただし本件は vacuum 不要）と同じ姿勢で、
  1行の正直なログを添える（例: `out-of-order batch — rebuilding projections`）
- 乖離の「検知して警告だけ」は採らない。正典（rebuild の結果）は定義済みで
  安価に到達できるのに、ユーザーへ「rebuild してね」と宿題を渡すのは
  器官の怠慢である

## 却下した対案

- **常に rebuild する（検知しない）** → 個人規模の台帳では実測サブ秒であり
  成立するが、live Apply と rebuild の等価性（既存テストが守る不変条件）を
  日常経路から失い、乖離バグの検出器を一つ捨てることになる
- **順序外の経験を再ソートして遡及 Apply** → 減衰は「適用時点までの経過」を
  畳み込むため、途中挿入は以後の全行の再計算になる。それは rebuild の
  再実装である
- **何もしない（現状明文化）** → 「バックエンドが落ちた日の経験は、以後
  永続的に過大評価される」を仕様として認めることになり、Experience is the
  Asset と正面衝突する

## 前提と残す露出（明文化）

- duel / curiosity / reflection の単一経験 Apply（TS=生成時点の壁時計）は
  本判定を通らない。これは**壁時計が単調である**前提に立つ — NTP 後退や
  時刻変更で破れうるが、クランプ（`max(now, 既知覚最大ts)`）は経験の ts を
  歪めるため採らない。前提はコードコメントに明記する
- rebuild は単一トランザクションではない（従来から。forget / 手動 rebuild と
  同じ露出）。自動発火の場面で途中死しても黙らないよう、1行ログは rebuild の
  **開始前**に出す。未完了 rebuild の検出機構は本ADRのスコープ外に残す

## Consequences

- `cmdPerceive`（および `do`/chat 尾部の知覚適用が同じ経路を通るなら同様）に
  順序判定と rebuild への切替。判定は既知覚経験の max(ts, id) 取得のみで安価
- **duel は毎回 rebuild になる**（実装後の dogfood 実測）: verdict の
  preference 経験（ts=now）が先に Apply され、子セッションの知覚バッチ
  （ts=子の実行時刻 < verdict）が必ず順序外になる。射影は正しく、個人規模の
  台帳ではサブ秒だが、台帳が育つと duel ごとの全再生コストが線形に伸びる。
  解消するなら duel 側の記帳順（子の知覚→verdict）の見直しであって、
  本判定を緩める方向ではない — 将来の較正課題として残す
- テスト: (1) 順序内バッチで rebuild が走らない（live Apply のまま）
  (2) 順序外バッチ後の射影が rebuild 結果と一致する
  (3) 順序外検知の1行ログが出る — を固定
- README の ADR 索引に追加
