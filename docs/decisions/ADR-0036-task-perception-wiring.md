# ADR-0036: 判断が読むトークン — Task Perception の未配線

- Status: **Accepted**（Decision 1 は先行実装済み。Decision 2 は 2026-07-21 に所有者が採用）
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

## Decision 2: 意味属性の Task Perception — 採用（2026-07-21）

`lang` / `framework` / `topic` はタスク記述からしか取れない。
PERCEPTION_ENGINE.md の設計どおり、判断の直前に extractor を1回通す。

### コストは実測で決めた

所有者は「タスク開始ごとに1回・コールドで数十秒」を許容したが、
**実測するとその見積もり自体が過大だった**（2026-07-21、実機・実バックエンド）:

```text
MLX LM Server (このマシンの既定)  真のコールド 5.25s / ウォーム 0.69〜0.81s
Ollama                            コールド 7.04s      / ウォーム 0.80s
到達不能なバックエンド             0.2ms で connection refused（ハングしない）
```

- mlx-lm はモデルをプロセス寿命の間保持する（アイドルでアンロードしない）ため、
  **真のコールドはサーバ再起動時に一度きり**
- Ollama は keep_alive 既定5分でアンロードするので、5分以上あけて叩くと毎回コールドになる
  — バックエンド間で非対称だが、どちらも「数十秒」ではない
- 出力は前置き・コードフェンスなしの素直な JSON。ADR-0029/0005 が置いた前提が実測でも成立

### Decision 2a: 期限のノブは置かない

上限は Extractor が既に持つ HTTP タイムアウト（120s）のままとし、
**タスク知覚のための期限ノブを新設しない**。

実測された失敗モードは (a) バックエンド不在 → 即座に refused、(b) ウォーム → 1秒未満、
(c) コールド → 5〜7秒 の3つで、**「生きているが返らない」は観測されていない**。
観測されていない失敗のためにノブを1つ増やすのは、較正値の根拠を持たないまま
説明責任だけ増やすことになる（推測ではなく計測）。
実際に張り付く事例が出たときが、その値を較正すべきときである。

### Decision 2b: 知覚は「最初に読む者」が起こす — 遅延して1回

判断が起きる経路は3つあり、いずれも**同じタスクの同じトークン**を読まねばならない:
`autoDecide`（`openTask` / `openSubtask` 経由）・`ChoosePlan`（`resolvePlan auto`）・
`pickDuelGap`（`duelOffer`）。

ところがこの3つは**発火条件が独立**である:

- `autoDecide` は `--provider auto` のときだけ
- `ChoosePlan` は `--plan auto` かつ human でないときだけ
- `duelOffer` は `--provider` を**明示していない**とき（既定実行を含む）に評価され、
  さらに interactive かつ予算内で、初めてトークンを読む。
  しかも `task.started` の記帳より**前**に走る唯一の経路である

素直に「タスク開始時に必ず1回」知覚すると、`--provider claude-code --plan direct` のような
**誰も読まない実行でも**叩くことになる。逆に「auto のときだけ」にすると、
既定実行の duel 経路だけが意味トークンを読めない不整合が残る。

したがって**遅延して1回**にする: タスクごとに1つの知覚結果ホルダを作り、
**最初にトークンを要求した者が知覚を起こし、以降はその結果を配る**。

- 誰も要求しなければ知覚は走らない（`--provider` 明示 + `--plan` 非auto + duel不発の実行はコストゼロ）
- 3経路のどれが最初でも、以後は全員が**同じトークン**を読む
- split サブタスクは親のホルダをそのまま受け取る。1つのタスクの分解であって
  別のタスクではないので、子ごとに再知覚しない（トークン効率の側で明確に劣る）
- ホルダの寿命は1タスク。`chat` では `/new`（`closeTask`）で破棄する

### Decision 2c: 語彙も規律も既存の知覚と同じ

`perceive.SemanticKeys` と `Store.KnownValues`（上限付き選抜）をそのまま使う。
**判断のために新しい語彙を増やさない** — 増やすのは schema 改版である。
値は `core.CanonValue` を通してから scope 一致に使う。

`cap` と `size` は Decision 1 で決定的に確定しているので、
extractor がそれらを返しても**決定的な値が勝つ**。機械が既に知っていることを
モデルの推測で上書きしない（ADR-0011: 判断は数学、モデルの座席は意味だけ）。

### Decision 2d: 判断の記録は知覚に見せない

タスク知覚の結果は `tomo.decided` / `plan.selected` の payload に監査として載せる
（封筒方式・DDL変更なし。ADR-0012 Decision 5 の「同じ台帳＋同じ seed → 同じ判断」は
「同じトークン」を前提にしているので、**どのトークンで判断したかは残さねばならない**）。

ところが `perceive` の抽出プロンプト（`eventsSection`）は、セッションの**全イベントの
payload を type で区別せず**流し込んでいる。判断の記録をそのまま載せると、
事後の知覚が**自分の事前推測を読んでしまう** — 機械が自分の推測を追認する経路になる。

したがって: **抽出プロンプトは「起きたこと」だけを見る。**
`tomo.decided` / `plan.selected` はハーネス自身の内心であって Reality ではないので
（PERCEPTION_ENGINE.md: Reality → Observation）、抽出プロンプトから外す。
台帳からは消さない — 監査は残り、見せる相手が変わるだけである。
副次的にプロンプトも短くなる。

### Decision 2e: 事前知覚と事後知覚は一致しない — 一致させない

判断は**タスク記述から推測した**トークン T_pre で Connection を読み、
経験は**実際に起きたセッションから抽出した**トークン T_post で記帳される。
両者は一致するとは限らない（credit assignment のズレ）。

**一致させない。** T_post を T_pre に合わせれば、機械の事前推測が
実際に観測された現実を上書きすることになり、ADR-0011 の座席割り当て
（モデルは意味、判断は数学、そして**知覚は起きたことに従う**）が壊れる。
逆に T_pre を T_post に合わせることは時間順序上できない。

代わりに**ズレを観測可能にする**: T_pre を監査に残すことで、
「賭けた scope と記帳された scope が食い違った」は後から数えられる事実になる。
両者は同じ extractor・同じ語彙・同じ schema を通るので系統的には寄るはずで、
寄らないなら**それは extractor の較正の問題**として別途扱える。
ズレを黙って埋めるより、測れる形で残すほうがこの製品の流儀に合う。

---

## Consequences

- Decision 1 は先行実装済み。size 粒度の Connection が判断に届く
- Decision 2 により `lang` / `framework` / `topic` の Connection と、
  それらを軸に生まれた Split 子が判断へ届く。**育ちが行動に届く**
- 配線した瞬間に、既存の台帳を持つ環境では**選ばれる Provider が変わりうる**。
  これは意図された挙動であり、その前提として
  [ADR-0038](ADR-0038-gate-under-inherited-priors.md)（継承事前下の能力ゲート）が
  先に効いている必要がある — 低い μ を継いだ子が読まれた瞬間に恒久ゲート落ちするのを防ぐ
- `perceive.Extractor` にタスク記述からの抽出口が増える。
  疑似 event を組み立てて既存経路に流し込む道は採らない — 起きていないことを
  出来事の形にするのは台帳の意味を濁す（実装の都合で真実の形を借りない）
- `face.isSharp` の S4 ゲート（ADR-0017）が測る島スコープ単位の wobble は、
  判断のスコープが実際に島の粒度を持つようになって初めて意味を持つ。
  本Decisionでその前提が満たされる
- `finestMatch` の粒度1同士のタイブレークは辞書順で、現行語彙では偶然 `cap` が常に勝つ
  （`c` < `f` < `l` < `s` < `t`）。**設計ではなく語彙の並びに依存している**ので、
  この挙動をテストでピン留めする — 語彙が増えた日に黙って変わらないように
