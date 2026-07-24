# ADR-0018: Experience Sovereignty — 経験主権と、humanの台帳

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [VISION.md](../core/VISION.md), [ADR-0004](ADR-0004-tech-stack.md)（単一SQLiteファイル）, [ADR-0006](ADR-0006-executor-integration.md)（Human Executor）, [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md), [ADR-0015](ADR-0015-reflection.md)

---

## Context

VISIONの第一原則「Experience is the Asset」は、主権が無ければ執行できない。
経験が誰かのクラウドの金庫にあるなら、それは「あなたの資産」ではなく
「預けさせられた資産」である。

現在の設計は既に主権的である — 単一SQLiteファイル、ローカルOllama、
クラウド依存ゼロ。本ADRはこれを**偶然から約束に格上げする**。

また、Human Executorは[ADR-0006](ADR-0006-executor-integration.md)から存在するが、
humanを**台帳に乗せてよいか**（能力Connectionの可否）は未決だった。
二つの問いは独立に見えて、前者が後者の前提条件である。

---

## Decision 1: 経験主権をVISIONのPrinciplesに追加する

**正文は以下の日本語であり、VISIONの英文はこの翻訳である。**

> 経験は、使用者のものである。
>
> 経験は使用者のマシンに住み、
> 使用者が持ち運べる形で取り出せ、
> 既定では誰の手も経由しない。
>
> Tomobitは取り替えられてもよい。
> しかし、Tomobitを育てた経験は、誰にも持っていかれない。

言葉選びの意図:

- **「住み」** — 「保存される」ではない。経験は生き物、のメタファーの継続
- **「持ち運べる形で取り出せ」** — ロックインの禁止。エクスポート機能の後付けではなく、
  単一SQLiteファイルそのものが持ち運べる形式である現状（ADR-0004）を権利として固定する
- **「既定では」** — 完全禁止ではない。主権とは「動かさない」ことではなく
  「**所有者だけが動かせる**」こと。完全禁止は主権ではなく幽閉。
  将来のopt-in同期の扉だけ残す
- **結びの対句** — 第一原則「Everything else is replaceable」と韻を踏む。
  器は交換可、中身は不可侵

---

## Decision 2: humanは台帳に乗る — 対称性

> **改版（[ADR-0043](ADR-0043-auto-by-default.md) Decision 4・2026-07-24）**:
> `--provider auto` の候補に human が入るのは、そのタスクの文脈に human の
> capability connection が既にあるときだけになった（空白＝無知なら候補外）。
> 変わるのは**候補集合だけ**である — 台帳・減衰・悲観ゲート・名誉回復の対称性は
> 不変で、「これはあなたがやった方が早い」のルーティングも証拠がある文脈では
> 今までどおり働く。`--provider human` の明示宣言も不変。

humanはProviderの一人である。同じ台帳、同じ減衰、同じ悲観ゲート、
同じ名誉回復。特別ルールは作らない（Rule禁止）。

- Decision Engineは「これはあなたがやった方が早い」と正直にルーティングできる
- 対称性は寛容の対称性でもある — **あなたの昔の失敗も、減衰で薄れていく**

---

## Decision 3: 透明性 — 隠れた採点は監視、見える台帳は鏡

Tomoがhumanについて知っていることは、いつでも全部見える。
`tomobit status` にhumanの台帳も同列に並ぶ。

Sovereigntyが前提であることに意味がある:
あなたについての台帳が、あなたのマシンにだけ存在し、
あなたの所有物である — だからこれは監視ではなく鏡になる。

---

## Decision 4: Reflectionはhumanの台帳についても語ってよい（本人確定）

「最近のレビュー、僕に任せた方が早いみたいだよ」

この種の語りを、越えてはいけない線ではなく
**Companionshipの核心**と位置付ける。

---

## Consequences

- VISION Principlesに英文を追加（本ADRの日本語が正文）
- Provider語彙に `human`（Human Executorは既存 — ADR-0006。R3の道具名と同列）
- 将来の同期・共有機能は必ずopt-in。「既定オフ」は原則からの要求であり実装の趣味ではない
- 思想・設計改稿シリーズの論点キュー（5→6→2→3→1→8→9→10）が全て閉じた
- 実装追記（2026-07-16）: `tomobit do --provider human`（自分でやると宣言）と
  `--provider auto` の候補に human が常時参加。humanが選ばれた場合は
  provider.selected {provider: human} を記帳し、Tomoが正直にルーティングを告げ、
  作業完了後は通常の採用質問→知覚で同じ台帳に乗る（Decision 2/3）。
  Reflectionの逆転トリガーはhumanも対象で、human勝ちの逆転は専用の語り
  （ADR-0019 Decision 3）になる（Decision 4）
