# ADR-0014: Plan学習 — 台帳は賭ける対象を選ばない

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0003](ADR-0003-outcome-and-preference.md), [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md), [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md), [ADR-0013](ADR-0013-prior-inheritance-mean-only.md), [EXECUTION_MODEL.md](../core/EXECUTION_MODEL.md), [CURIOSITY_ENGINE.md](../core/CURIOSITY_ENGINE.md)

---

## Context

EXECUTION_MODELは「Planの改善もExperienceから学習される」と宣言していたが、
機構がなかった — 設計済みの学習は全てProvider選択に向いており、
Plan構造(どのCapability列が効くか)にはConnectionの置き場すらなかった。

v1でのdescope(宣言を下ろし、eventsが学ぶ権利を保全)を検討したが、
本人の意志は「**使い始める前にもっとリッチにしたい**」。
看板を下ろすのではなく、臓器を作る。

ただし制約が一つ。LLMに自由文でPlanを生成させると、
判断の座席にLLMが座り[ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md)に反する。
**LLMにPlanの生成はさせない**(本人明言)。

---

## Decision 1: Plan = 同じ台帳の、もう一つの賭け先

[ADR-0003](ADR-0003-outcome-and-preference.md)が好みで示した一般化を、Planにも適用する。
Connectionの右側はProviderに限らない — **台帳は、賭ける対象を選ばない**。

```text
これまで:  (context) ──台帳──▶ Provider   どの相棒が効くか
追加:      (context) ──台帳──▶ Plan       どの手順が効くか
```

判断は二段になる。

```text
1. (intent, size, lang, …) → Plan       テンプレートの選択
2. 各ステップで (context, cap) → Provider
```

二段とも同じ機構が全適用される:
決定則([ADR-0012](ADR-0012-decision-rule-thompson-sampling.md): 悲観ゲート＋TS、n(stakes))、減衰、Surprise→Split/Merge、
事前継承([ADR-0013](ADR-0013-prior-inheritance-mean-only.md))、そして好み層(ADR-0003) —
Tomoの質問は「どっちの手順が好みだった?」も聞ける。

「size=largeのときだけreviewありのPlanが勝つ」という発見は、
Providerの島の逆転と同じく、Splitが行う。

---

## Decision 2: Planは閉じたメニュー — LLMは生成しない

Plan = Capability列のテンプレート。Intentごとに宣言された閉集合
(R2の語彙と同じ思想)。初期メニューは手書きの2〜3変種。

```text
fix-bug:
  plan=full     analyze → implement → test → review
  plan=direct   implement → test
  plan=quick    implement
```

LLMによる自由文Plan生成は行わない。

---

## Decision 3: 新Plan変種はCuriosityが提案する — 変異は純関数、採否は数式

提案機構にもLLMを使わない。既存Planへの構造的**変異オペレータ**(純関数)で生成する:

```text
drop     ステップを1つ除く
insert   capability語彙内のcapを1箇所に挿す
swap     隣接ステップを入れ替える
```

- 生成空間はcapability語彙で閉じる(適法性制約: 空でない、連続重複なし)
- どの変異から試すかの優先度はValue of Information(Curiosity優先度の統一の際に詳細化)
- 提案は判断ではない — メニューに入れるのは提案、選ぶのは数式。座席違反にならない

**変異は純関数、採否は数式、誕生は継承。**

---

## Decision 4: 新参Planの誕生と保護

- **誕生**: 変異元(親Plan)からADR-0013の継承 — `Beta(μ·m₀, (1−μ)·m₀)`。
  新Plan構造に一致する過去は存在しないため、Born with HistoryのReplayは適用外。
  m₀の赤ん坊が、親の第一印象だけを持って生まれる
- **保護**: 専用機構は作らない。ADR-0012のn(stakes)が自動で制御する —
  高stakesではgreedy化して新参が選ばれず、低stakesでTSが出番を与える。
  **新参の初陣は、自然と軽いタスクになる**

---

## Decision 5: メニューの新陳代謝

Planも生き物である。勝てないPlanは減衰しRetireへ
(Connectionライフサイクルをそのまま適用)。

- Intentごとの生存上限 K(初期値5)
- Curiosityの提案はメニューに空きがある時のみ

---

## 帰属の混濁(明記しておく弱点)

タスクの成否がPlanの手柄かProviderの手柄かは、台帳上混ざる。

- メニューが小さく(K≤5)減衰が効いている間は実害が小さい
- Plan×Providerの交互作用はSplitが拾える
- 悪化が観測されたら `plan=` をProvider Connectionの文脈属性に昇格して分離する。
  Planは機械的属性(ハーネス自身が知っている)なのでextractor改版は不要、
  R3の `model=` と同型

---

## Consequences

- EXECUTION_MODELの「Planの改善もExperienceから学習される」が真になる
- Plan選択も決定イベントとしてseed記帳(ADR-0012)
- CURIOSITY_ENGINEのSignalにPlan提案を追加
- 実装ノブ: 初期メニューの文面、K、変異優先度(VoI)、提案予算
- 実装追記（2026-07-16、`internal/plan` + engine/decide/curiosity統合）:
  - Planの正準名 = ステップを`>`で連結した文字列（`analyze>implement>test` —
    識別子が手順そのものなのでProvider名と衝突しない）。ラベル full/direct/quick
    は implement の初期メニューの別名
  - connections に kind='plan' を追加。同じ経験が provider と plan の
    **二つの賭け先**に流れる（experiences.plan 機械属性で結線 —
    plan.selected イベントから知覚が決定的に抽出）
  - 選択: `tomobit do --plan auto`（`decide.ChoosePlan` — 同じ悲観ゲート+TS+
    n(stakes)）。実行はステップ列を同一Providerで逐次実行（最初の失敗で停止）。
    ステップの枠付け文は決定的テンプレート（LLMはPlanを生成しない）
  - **二段目の「ステップごとのProvider選択」はv1保留**: ステップ粒度のoutcomeが
    未定義のため、1 doにつきProviderは1人。帰属の混濁の節が予告した通り、
    分離が必要になったら plan= の文脈属性昇格と併せて扱う
  - メニューの生存は plan.generated イベント（真実）から導出 — rebuildで
    消えない。引退 = plan Connectionのdormant（Decision 5のライフサイクル）
  - **Decision 4の誕生継承はv1では白紙誕生に緩和**: 提案時にConnectionを
    直接産むと「rebuildが経験から再現できない状態」が生まれるため
    （Split児の継承はReplay経由で再現可能だが、提案児の親μは経験に無い）。
    白紙Beta(1,1)はゲート基準線上で通過し、TS+n(stakes)による保護
    （新参の初陣は軽いタスク）はそのまま機能する。継承を真実に昇格する場合は
    plan.generated のpayloadに事前を記帳し rebuild が再演する設計になる
  - 変異優先度: 全候補が白紙で揺らぎ同値のため、VoIは分離できない —
    決定的列挙順（drop→swap→insert、位置昇順・語彙順）で最初の新規合法変異。
    提案予算は24hに1つ（質問予算の型）、メニュー空き（K=5未満）時のみ
  - human実行のdoはPlan選択をスキップ（ハーネスが駆動できるステップ境界が無い）
