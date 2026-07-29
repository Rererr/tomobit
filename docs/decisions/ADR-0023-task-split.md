# ADR-0023: タスク分割 — Providerの分割提案と複数Providerへの分配

- Status: **Accepted**
- Date: 2026-07-16
- 関連: [ADR-0006](ADR-0006-executor-integration.md)（Executor統合）,
  [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md)（Meaning by Model, Judgment by Math）,
  [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md)（決定エンジン）,
  [ADR-0014](ADR-0014-plan-learning-same-ledger.md)（Plan学習）,
  [ADR-0018](ADR-0018-experience-sovereignty.md)（humanの台帳）,
  [EXECUTION_MODEL.md](../core/EXECUTION_MODEL.md), [SCHEMA.md](../design/SCHEMA.md)（R4）

<!-- 改版:begin — tools/sync-adr-superseded.sh が生成する。手で編集しない -->
> **改版済み** — この決定の一部は後のADRが置き換えた。範囲は各Decisionの改版注記が持つ。
>
> - Decision 1 → [ADR-0028](ADR-0028-auto-split-parallel.md)
> - Decision 4 → [ADR-0028](ADR-0028-auto-split-parallel.md)
> - Decision 5 → [ADR-0051](ADR-0051-orchestration-is-judged.md)
<!-- 改版:end -->

---

## Context

`do` のタスクは1つのProviderが丸ごと担う。大きすぎる・難しすぎるタスクを
渡されたProviderは、中途半端に実行して壊すか、長大な1実行で品質を落とすか
しかない。タスクの難易度と分割可能性を最初に知るのは、実際にタスクを
受け取ったProvider自身である。

必要なのは3つ:
(1) Providerが「難しすぎる / 分割した方が良い」と**返答できる**決定的なプロトコル、
(2) その提案を受けてサブタスク群を実行する機構、
(3) 各サブタスクの実行者を**台帳が選べる**こと。

---

## Decision 1: 分割提案はProviderの返答プロトコル（opt-in `--split`）

`tomobit do --split "<prompt>"` はプロンプト末尾に決定的なプロトコル文を
追記する:「難しすぎる・分割した方が確実だと判断したら、**作業をせず**、
次の形式のJSONコードブロックだけを出力して分割を提案せよ」。

```json
{"tomobit_split": ["サブタスク1の指示", "サブタスク2の指示"]}
```

- 2〜5個の**自己完結した指示文字列**。順に実行される前提で書かせる
- 検出は純関数 `subtask.Parse`（コードフェンス内・裸のJSONの両方を読む）。
  録画テキストで写像をテストできる — Adapter翻訳と同じ規律
- マーカーあり・形式不正（1個 / 6個以上 / 空文字列 / 型違い）は
  **警告して通常フロー続行**（提案の黙殺も、黙った救済もしない）
- exit≠0 の実行からは提案を読まない（壊れた実行の出力を信用しない）
- 提案と成果物が両方出た場合はマーカーが勝つ（プロトコルは「作業をせず」
  と指示しており、両方出すのはProviderの逸脱。出力自体は端末に見えている）
- **自己エコーは提案ではない**: プロトコル文に埋め込んだ例のJSONは、それ自体が
  形式を満たしてしまう。モデルが指示文を引用しただけの出力（分割不要の説明に
  例を添える等）を提案と誤認すると、プレースホルダを本番タスクとして実行し
  台帳を汚染する。例と同値の候補は「マーカーなし」として扱う

却下した対案:

- **事前アセスメント専用実行**（1回目は判定のみ、2回目で実行）:
  実行が2倍になる。判断材料は同じプロンプトなので1回で足りる
- **常時ON**: プロトコル文が全実行のトークンを食い、「作業より分割へ逃げる」
  誘因を全タスクに入れる。過剰分割の頻度は未計測 → opt-inで実測してから
  既定化を判断する（推測ではなく計測）

---

## Decision 2: 分割はintentの分解であって、Planの生成ではない

ADR-0011/0014の「LLMはPlanを生成しない」との整合を明確にする。
Planは**閉語彙のcapability手順**（台帳の賭け先）であり、分割提案は
**新しいintent（自然文）の誕生**である。intentはもともと人間/LLMの領分
（doのプロンプトそのもの）で、数式が生成できるものではない。
台帳が賭けるのは今回も「誰がやるか」だけで、提案の採否に判断ノブはない
（形式が合法なら受理 — 意味はモデル、判断は数式のまま）。

- 各サブタスクは**独立したタスクセッション**:
  `task.started {intent, source: "production", parent: <親sid>}` から
  通常のタスクと同じ経路を流れる
- `source` は "production" のまま（`experiences.source` の CHECK は
  production|learning の2値。分割由来は `parent` で表現し、知覚schemaは不変）
- 学習: サブタスクごとに別の経験になり、「どのProviderがどの文脈に強いか」
  が細かい粒度で台帳に乗る

---

## Decision 3: サブタスクの実行者は親の選択方法を継ぐ

- 親が `--provider auto` → **サブタスクごとに決定エンジン（ADR-0012）が選ぶ**。
  複数Providerへの分配はここで実現する（humanも候補 — ADR-0018。
  「この確認作業はお前がやれ」と台帳がユーザーに差し戻すこともある）
- 親が明示Provider → 全サブタスクも同じProvider
- `--size`（判断の温度 n(stakes)）も親を継ぐ。分割でサブタスクが「小さく」
  なったとハーネスが推測してstakesを下げることはしない — sizeはユーザーが
  この仕事全体に置いた重みであり、サブタスクの失敗が課すコストの財布は親と
  同じ（推測ではなく、ユーザーの明示を使う）

却下した対案: **サブタスクは常にauto**。分配の見せ場は増えるが、ユーザーの
明示選択（`--provider codex`）を黙って別Providerに差し替えることになる。
「shellがたまたま持っているプロファイルを黙って継承する事故こそ防ぎたい」
（ADR-0006追記）と同じ理由で、明示された選択は覆さない。分配が欲しければ
autoを選べばよい。

組み合わせの制約:

- `--split --provider human` はエラー（humanは提案を返すストリームを持たない）。
  autoが親をhumanに回した場合は、提案の機会がないだけの通常実行
- `--split --plan ...` はエラー（どのステップの出力を提案と読むかが曖昧。
  分割とPlanの合成は将来論点）

---

## Decision 4: 逐次・深さ1・失敗で停止

- サブタスクは提案順に**逐次**実行する（後のサブタスクは前の成果に
  依存しうる — planステップと同じ前提。並列化は将来論点）
- サブタスクのプロンプトには**プロトコル文を付けない** → 再帰は深さ1で
  構造的に止まる。難しすぎるサブタスクはそのまま実行され、失敗も正直な
  経験になる
- サブタスク失敗（実行エラー / exit≠0）で**残りは開始しない**（壊れた前提で
  作業しない）。未開始サブタスクはセッションを開かない — 始まらなかった
  タスクを台帳に残さない。提案の全文は task.split イベントに残っているので
  情報は失われない
- 各サブタスクのプロンプトは決定的なハーネス文で親intentを添える
  （stepPromptと同じ流儀。文脈の受け渡しに判断は挟まない）

---

## Decision 5: 記帳と区切り

親セッション:

```text
task.split      {subtasks: [...]}   受理した提案
task.finished   {}                  採用確認なし — 親の実行の成果物は
                                    「分割提案」であり、判定対象の作業物がない
```

サブタスクセッション（通常のタスクと同じ）:

```text
task.started       {intent, source: "production", parent: <親sid>}
capability.started {capability}     親のcapabilityを継ぐ
tomo.decided                        親がautoの時のみ
provider.selected / provider.output / provider.finished / provider.error
task.finished      {adopted, reverted}   サブタスクごとの採用確認
```

区切りの器官（知覚 → Tomoの質問 → 鏡）は**全サブタスク終了後に1回**。
perceiveは全pendingセッションを処理するので、親もサブタスクもまとめて
知覚される。採用確認がサブタスク数だけ増えるのは受け入れる —
サブタスクごとの第1層Outcomeこそ、この機能の学習配当。

---

## Consequences

- 新イベント `task.split`（SCHEMA.md R4 追記）。抽出プロンプト/schemaは
  不変 → extractor_ver のバンプ不要（ADR-0006と同じ理屈）
- 分割の質（過剰分割・分割すべきを分割しない）は今は測らない。
  task.split と各サブタスクのOutcomeが台帳に揃うので、後から導出できる
- chat（ADR-0022）への分割提案は将来論点（ターンの応答に提案が混ざる形は
  未設計。doのopt-inで挙動を実測してから）
- 実装時ノブ: プロトコル文の文言 / サブタスク数の上限（2〜5）/
  マーカーキー名（`tomobit_split`）

---

## 追記（2026-07-26）: ADR-0051 / ADR-0052 — 親にも信号が届くようになる

Decision 5 は親に受け皿を置かなかった。理由はこうだった:

> `task.finished {}` 採用確認なし — **親の実行の成果物は「分割提案」であり、
> 判定対象の作業物がない**

**前半は正しいままである。** 改まるのは後半で、2つの経路から親に信号が届く。

**主観の側**（[ADR-0051](ADR-0051-orchestration-is-judged.md)）: その分割提案自体が
判定対象になりうる。分割が起きた `do` の終わりに、**分け方だけを問う1問**が乗る
（分割が起きなかった `do` は完全に不変）。答えは `user.split_verdict` として記帳され、
`task.finished` の `adopted`/`reverted` には触れない — 能力と分け方は別の事実
（ADR-0003 Decision 2 の理屈）なので、同じ封筒に入れない。

**客観の側**（[ADR-0052](ADR-0052-first-layer-is-observed.md)）: 分割の親は
`judged=false` で `finishTask` に入るため `Adopted` が `""` のままで、これは
`OutcomeWeight` が**テスト通過を y=0.9 として拾う枝**そのものである。全サブタスクが
済んだ後の作業ツリーは親の采配の結果なので、そこで走るテストは親に帰属してよい。

**サブタスクの側は不変**: 子は空の `task.finished`（主観は聞かない）のままで、
ADR-0052 も子には走らせない（群間逐次では途中の赤が正常な中間状態であり、帰属
できないため — 同 Decision 3）。ADR-0028 Decision 5 が保留した「子への主観伝播」は
保留のまま残る。

Decision 1〜4（プロトコル・intentの分解・実行者継承・深さ1）とサブタスク側の記帳の形は
不変。Decision 1 の opt-in（`--split`）と Decision 4 の「逐次のみ」は ADR-0028 が
既に改めたとおり。
