# ADR-0036: 判断が読むトークン — Task Perception の未配線

- Status: **Proposed**（Decision 1 のみ先行して実装済み。Decision 2 は所有者の判断待ち）
- Date: 2026-07-21
- 関連: [PERCEPTION_ENGINE.md](../core/PERCEPTION_ENGINE.md)（Task Perception — 本ADRが扱う未実装部分）,
  [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md)（Decision 3: タスク記述→extractor→Context属性トークン→Decision Engine）,
  [ADR-0013](ADR-0013-prior-inheritance-mean-only.md)（Decision 2: 判断は最細一致のみを読む）,
  [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md)（Decision 4: n(stakes) は size の純関数）,
  [ADR-0001](ADR-0001-connection-granularity.md)（粗→Split採用）, [ADR-0002](ADR-0002-surprise-and-split-judgment.md)（Split有意判定）,
  [ADR-0004](ADR-0004-tech-stack.md)（Deferred Perception）

---

## Context

**判断は `cap=` 1トークンしか読んでいない。**

`autoDecide` は `tokens := []string{core.CanonToken("cap", capability)}` だけを
`decide.Choose` に渡す（`resolvePlan` の `ChoosePlan` も同じ）。
`capability` は `--cap` フラグ（既定 `implement`）であり、知覚は一切通っていない。

一方で:

- `engine.applyTo` は経験の**全トークン**（lang / framework / topic / size / cap / model）で
  粒度1の Connection を産む
- Split（ADR-0001/0002）は有意判定を通った軸で**2トークン以上の scope を持つ子**を産む
- `decide.finestMatch` は `scope.SubsetOf(tokens)` を要求する

したがって、**scope が `{cap=...}` の部分集合でない Connection は、候補にすらならない**。
`lang=rust` の粒度1 Connection も、Split で生まれた `{cap=implement, lang=rust}` の子も、
本番の判断からは構造的に到達不能である。

これは局所的な取りこぼしではない。VISION の

> **Tomobitの成長とは、保存された知識の量ではなく、Connectionの質が向上することである。**

に対して、**Connection の質が向上しても判断が変わらない**状態が続いている。
Split は台帳の中で正しく育ち、鏡（ADR-0015）はそれを語り、顔窓は成長段階として描くが、
「次にどの Provider に賭けるか」だけは粗い親しか読まない。
育っているのに、育ちが行動に届いていない。

PERCEPTION_ENGINE.md は Task Perception を明示的な図まで添えて置いており、
ADR-0011 Decision 3 も「タスク記述→extractor→Context属性トークン→Decision Engine」を明記している。
**どのADR・どのdocにも descope の記載がない** — 意図的な見送りではなく、未配線である。

さらに、判断時点で**すでに機械が知っている** `--size` すらトークンに入っていない。
`decide.Draws(size)` は同じ size を n(stakes) に使っているのに、
scope 一致の側には渡していない。これは設計判断ではなく実装の穴である。

---

## Decision 1: 決定的に既知の属性は、その場でトークンにする（実装済み）

判断の時点で**LLMを一切通さずに確定している**属性は、そのままトークンに入れる。
v1 では `cap` と `size` の2つ。

- `size` は `--size` フラグ由来で、`Draws(size)` が既に判断の入力として使っている。
  同じ値を scope 一致から外す理由が無い
- 空値はトークンにしない（`core.Experience.Tokens()` が空値をスキップするのと同じ規律）。
  `size=` という台帳に存在しないトークンを混入させない
- マイグレーションは不要。size 粒度の子が無ければ `finestMatch` は従来どおり粗い親を読む

これは新しい決定というより、ADR-0012 Decision 4 と ADR-0013 Decision 2 の間に空いていた穴を塞ぐ修正である。
**適用は判断を行う全箇所**（`autoDecide` / `resolvePlan` の `ChoosePlan` / duel の `pickDuelGap`）— 判断の語彙が呼び出し口で揺れてはならない。

## Decision 2: 意味属性の Task Perception — **所有者の判断待ち**

`lang` / `framework` / `topic` はタスク記述からしか取れない。
PERCEPTION_ENGINE.md の設計どおり実装するなら、判断の直前に extractor を1回通すことになる。
ここは所有者に決めてもらう論点なので、設計案とコストを置いて止める。

### 案（採るならこの形）

- **1タスクにつき1回**。同じタスクから派生する判断（split 子・duel の2本・plan 選択）は
  同じ知覚結果を共有する。ターンごと・判断ごとに叩かない
- **語彙は過去の知覚と同じ**（`perceive.SemanticKeys` と `Store.KnownValues`）。
  判断のために新しい語彙を増やさない — 増やすのは schema 改版である（ADR-0033 Decision 3 と同じ規律）
- **best-effort・短い期限**。期限内に返らない/バックエンドが居ない場合は Decision 1 の
  決定的トークンだけで判断する
- **黙って劣化しない**。劣化したことを `tomo.decided` の audit payload に載せ（`tokens` と
  劣化理由）、運用ログ1行にも出す。ADR-0012 Decision 5 の「同じ台帳＋同じ seed → 同じ判断」は
  「同じトークン」を前提にしているので、**どのトークンで判断したかは監査に必ず残す**
- **台帳は不変**。実行前の知覚は出来事ではない（PERCEPTION_ENGINE.md: 「タスク記述はまだ出来事ではない」）。
  Experience にはせず、`tomo.decided` の payload に監査として載せるだけ — SCHEMA 変更はない

### 決めてほしい点（ここが止めた理由）

1. **第一の責務との衝突**。VISION の Curiosity は「成長は仕事を妨げてはならない／
   Tomobitの第一の責務は今目の前にある仕事を支援すること」と置く。
   ADR-0004 が Deferred Perception を選んだのも同じ理由である。
   タスク開始のたびにローカルLLMを1回叩くのは、モデルが温まっていれば数秒だが、
   **コールドスタートでは数十秒**になる。これを第一の責務への侵害と見るか、
   Provider の実行時間（分単位）に対する誤差と見るかは、所有者の線引きである
2. **期限の値**。上の判断が決まらないと較正できない。推測で決めず、
   実機の実測（コールド/ウォーム両方）で決めるべき値である
3. **決定の変質**。配線した瞬間、これまで読まれていなかった Split 子が判断に効き始める。
   これは意図された挙動だが、**既存の台帳を持つ環境では選ばれる Provider が変わる**。
   先に ADR-0037（継承事前と悲観ゲートの調停）が要る — 継承事前で生まれた子は
   ゲートを恒久的に割りうるため、配線が「全員ゲート落ち→fallback」を常態化させる恐れがある

### 却下した対案（案を採る場合に備えて記録）

- **判断ごとに extractor を叩く** → 同一タスク内で同じ記述を何度も知覚することになる。
  トークン効率の側で明確に劣る
- **`--ctx lang=rust,topic=lifetime` の手動指定を先に入れる** → 実需要が無い。
  PERCEPTION_ENGINE.md が置いた設計は extractor 経路であり、
  手動の口を先に作るのは語彙が二重化するだけ（YAGNI）
- **知覚結果を Experience として記帳する** → 実行前の知覚は出来事ではない。
  記帳すると「起きていないこと」が台帳に入り、rebuild の再現性の意味が濁る

---

## Consequences

- Decision 1 は実装済み。size 粒度の Connection が判断に届くようになる
- Decision 2 が採られない限り、`lang` / `framework` / `topic` の Connection と
  それらを軸に生まれた Split 子は**判断からは到達不能のまま**である。
  その場合は本ADRを descope の記録として Accepted にし、
  PERCEPTION_ENGINE.md と ADR-0011 Decision 3 に「Decision Engine への配線は v1 では行わない」を
  明記する必要がある — **実装しないなら、しないことを正本に書く**（黙って乖離させない）
- `face.isSharp` の S4 ゲート（ADR-0017）は島スコープ単位の wobble を測っているが、
  判断のスコープが `cap`(+`size`) しか無い間、その測定は本番に存在しない籤を測っている。
  Decision 2 の可否と同じ議題で扱うべき
