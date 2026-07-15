# ADR-0007: Curiosityの最初の器官 — Tomoの質問

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0003](ADR-0003-outcome-and-preference.md)（第3層Preference・質問の設計原則）,
  [ADR-0002](ADR-0002-surprise-and-split-judgment.md)（BF機構の再利用）,
  [ADR-0006](ADR-0006-executor-integration.md)（doの区切り・先送り2件の解消）,
  [CURIOSITY_ENGINE.md](../core/CURIOSITY_ENGINE.md), [SCHEMA.md](../design/SCHEMA.md)（R4: tomo.asked / user.preference）

---

## Context

ADR-0006が二度先送りした「Curiosityの質問」を確定する。

下流は既に全部ある: `user.preference`イベント → 決定的知覚 →
preference Experience → 好みのConnection（実装・テスト済み）。
ないのは上流だけ — **誰が・いつ・何を聞くと決めるか**。

確定事項は4つ: Phase 1のシグナル範囲 / Preference Gapの統計量 /
質問予算の持ち方 / 聞く場所と記帳。

---

## Decision 1: Phase 1のシグナルは Preference Gap のみ

CURIOSITY_ENGINE.mdの7シグナルのうち、Phase 1で動かすのは1つだけ。

理由: シグナルは**消費者（実行器官）がいて初めて意味を持つ**。

- preference_gap の消費者 = Human Executor（ターミナルの1行プロンプト）。
  claude-code以外で**既に存在する唯一のExecutor**が人間である
- knowledge_gap / new_provider / model_update / environment_change /
  high_uncertainty の消費者 = Learning Execution（source='learning'の自動実行）。
  まだ存在しない。消費者のいないシグナルを検出してもentryが腐るだけなので、
  検出ごと学習実行のADRへ送る
- questioned はqueueを経由しない。ADR-0002の「Questioned → Curiosity Queueへ」は、
  実装が審判をApply内にインライン化したことで簡約済み（トリガーが審判をゲートする
  構造は保存。queueホップは審判が重い場合のスケジューリング自由度のためだったが、
  実測で審判は毎Apply内で完了しておりホップ不要）。この簡約を正典とする

---

## Decision 2: Preference GapはViewである — curiosity_queueに書かない

Preference Gapは connections と experiences から**完全に導出できる**。
導出できるものを保存すれば二重帳簿になる（「保存された属性は嘘をつく」）。
Ruleと同じ扱い — 保存せず、聞く直前に毎回導出する。

```text
Gap(scope, A, B) の3ゲート（ADR-0003「互角×頻繁×証拠なし」の統計量化）

互角      ln BF_provider < θ_even
          H0: AとBは同じp / H1: 別々のp。ADR-0002のBeta-Binomial閉形式を
          属性分割でなくProvider分割に適用（実装はtokenBFと同じヘルパ）
          かつ 両能力Connectionの実効証拠 ≥ n_min
          （証拠の薄さによるBF中立を「互角」と誤認しない —
            それはPreference GapでなくKnowledge Gap）

頻繁      scopeにマッチする減衰済みExperience数 ≥ f_min
          （決定に効かない文脈に予算を使わない）

証拠なし  preference Connection (scope, A~B) の実効証拠 < e_max

優先度 = 減衰済み頻度。同点は scope_key 辞書順（決定的）
```

- 多重比較の`ln m`補正はしない: これは検定ではなく質問の**選択**であり、
  誤って聞くコストは予算により1問で有界
- 却下した対案: **検出時にqueueへ積む** → 陳腐化（互角が崩れた後のentry）と
  重複排除の機構が必要になる。導出なら常に新鮮で、dedup問題が存在しない
- curiosity_queueテーブルは空のまま残す: 「忘れてはいけない」対象は
  **再導出できないシグナル**（environment_change等の外部観測）であり、
  それらは学習実行のADRで初めて書き手を得る

---

## Decision 3: 質問予算もeventsから導出 — 新しい状態を作らない

予算チェック = 「直近24hに `tomo.asked` が無いこと」（rolling。暦日でない）。

- tomo.asked は**スキップされても記帳される＝予算を消費する**。
  予算が守るのは回答数ではなく割込み回数（ストレス設計の本体）
- R4の注記「質問予算の管理と『聞きすぎていないか』の検証に使う」の実装形

---

## Decision 4: 聞く場所 = doの区切り、採用確認の直後。記帳は質問専用session

```text
tomobit do の終了時:
  1. 採用確認（毎回・ADR-0006 Decision 4）
  2. 予算あり かつ Gapが導出されたら、最優先Gapを1問だけ:

     最近 {scope} で {A} と {B} 両方使ってるけど、どっちが好みだった?
     [1={A} / 2={B} / Enter=スキップ]
```

- 質問文はGoのテンプレート（LLM不使用・決定的）
- 直前のdoとscopeの一致は**要求しない**。ストレス設計の本体は「区切りで聞く」
  ことであり話題の一致ではない。質問文は「最近〜」と過去を指すため文脈切替の
  負荷は小さい。一致を要求すると、稀にしか来ないscopeのGapが永遠に聞けなくなる
- 記帳は**質問専用の新session**:
  `tomo.asked {scope, pair, ln_bf, freq, asked_after}` →
  （回答時のみ）`user.preference {preferred, over}`
  - asked_after は直前のdoのsession_id（「聞きすぎ」検証で割込み文脈を追える）
  - 却下した対案: **doのsession末尾に追記** → 知覚がdoのcontextを
    preference Experienceへ写してしまう。質問のscopeはGap由来で、
    doのscopeとは独立
- 質問sessionの知覚は**完全に決定的**: contextはtomo.askedのscopeトークンから
  写す。Ollama不要なのでdoの終了処理内で同期的に知覚してよい。
  抽出プロンプト/schemaは不変 → extractor_verのバンプ不要
- Learning SchedulerはPhase 1では「区切りでの予算チェック」に縮退し、
  Human Executorは「ターミナルの1行プロンプト」に縮退する
  （ADR-0006のPlan縮退と同型）

---

## Consequences

- ADR-0006の先送り2件（tomo.asked / 質問予算）が閉じ、第3層Preferenceの
  パイプラインが上流から下流まで一本つながる
- **発火は2つ目のAdapter（codex）後**: 同一scopeで両Providerの証拠が
  n_minを超えて初めて互角ゲートが開く。実装とテスト（フィクスチャ）は今できる —
  発火はデータが決める
- スキップされた質問session（tomo.askedのみ）は経験ゼロ。PendingSessionsが
  永遠に再処理しないよう、知覚対象の判定から質問sessionを決定的に除外する
  （実装ノブ: 例えばevent typeが tomo.asked / user.preference のみのsessionは
  deferred perceptionの対象外とし、do終了処理内の同期知覚だけが扱う）
- ノブ一覧（人間が決める数字）:

```text
θ_even   +1.0 nat    互角の上限（ln BFがこれ未満なら能力では決められない）
n_min     3.0        互角判定に要る各能力Connectionの実効証拠
f_min     3.0        聞く価値のあるscope頻度の下限
e_max     1.0        「好みを知らない」の上限
質問予算  1問/24h    rolling
```

  実装時の計測メモ（2026-07-15）: 互角ゲートが開く時点で両providerの
  実効証拠の和が 2×n_min = 6 あるため、f_min = 3 の頻繁ゲートは
  単独の閉塞要因にならない（f_min ≤ 2×n_min の間は互角ゲートに含意される）。
  頻繁ゲートを実質化するなら f_min > 2×n_min に上げる

- 残るOpen Questions:
  - 引き分け回答（「どっちも良かった」）の記帳 — Betaに半票(α+0.5, β+0.5)とも
    書けるが意味論が濁る。初版はスキップと同義とする
  - 学習実行（残り5シグナルと非人間Executor、curiosity_queueの書き手）は別ADR
  - pull型 `tomobit ask`（予算外で人間側から質問を引く）の要否
