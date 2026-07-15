# ADR-0001: Connectionの誕生モデル — 粗から生まれ、食い違いで分化する

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [CONNECTION_ENGINE.md](../core/CONNECTION_ENGINE.md), [COGNITIVE_MODEL.md](../core/COGNITIVE_MODEL.md)

---

## Context

KnowledgeはConnection Graph（Hypergraph）として保持すると決めた。
ExperienceのContextは複数の属性を持つ。

```text
(Rust, Axum, Lifetime, MediumProject, HumanReview) → Claude
```

この属性の部分集合すべてがエッジを張れる場所（lattice）になる。
問題は **Connectionをいつ・どの粒度で Born させるか**。

### 案A: 全粒度一斉

Experienceが来た瞬間、Contextの全部分集合にConnectionを生成し、以後すべて更新する。

### 案B: 粗→Split

最初は粗い粒度のみ生成。細かい粒度は「粗いConnectionでは説明できない食い違い」が検出された時だけSplitで生む。

---

## Comparison

| | A: 全粒度一斉 | B: 粗→Split |
|---|---|---|
| 交互作用の発見 | 必ず捕まえる | 検出器の質に依存 |
| グラフの意味 | エッジの存在が無意味化 | 1本1本が「発見の証」 |
| Confidence機構 | evidence=1の墓標の海で機能不全 | 健全に機能 |
| Curiosity Queue | Low Confidence候補で洪水 | 本当に怪しい候補だけ |
| 経験あたり更新コスト | 2^属性数で爆発 | 既存Connection数に比例 |
| 追加機構 | 不要（力技） | Split検出器＋Replay |
| 失敗モード | ノイズに溺れる | 交互作用の見逃し |

### 決定的な観察

1. **両案の急所は同じ判定器**。Aの「刈り込み」もBの「Split検出」も、実体は「細は粗と統計的に食い違うか?」という同一の検定。同じものを書くなら、入場審査（B）に置く方がグラフが常に清潔。

2. **未実体化のデータは失われない**。Experience is Immutable / Knowledge is Rebuildable の原則により、細粒度の証拠はExperienceログに常に存在する。AとBの差は「どこに持つか」ではなく「いつ実体化するか」だけ。

3. **Aは思想と逆のインセンティブを生む**。Contextを豊かにするほど更新コストが指数的に増える → Contextを削りたくなる。Context Firstの原則と衝突する。

---

## Decision

**案B を採用。ただし裸のBではなく、3点セットで:**

1. **Surprise駆動のSplit検出**
   既存Connectionの予測誤差をContext属性つきで記録。誤差が特定属性と相関したらSplit候補としてCuriosity Queueへ。
   （Curiosityが気付き、Connection Engineが実行する — 既存の役割分担のまま）

2. **履歴つき誕生（Born with History）**
   Split時はExperienceログをReplayし、生まれるConnectionに過去の証拠を全注入。
   「Splitは学習が遅い」という弱点をここで消す。

3. **逆方向のMerge**
   子が親と食い違わなくなったら親へ畳み、子はRetired。
   Split判定とMerge判定は同一の検定（Same Test Both Ways）。

---

## Consequences

### 良い影響

- グラフ上のエッジはすべて「統計的な主張を通過した発見」になる
- Confidence / Curiosity / ライフサイクルの各機構が墓標ノイズなしに機能する
- 経験あたりコストが有界になり、Contextを豊かにしてもシステムが壊れない

### 受け入れるコスト

- Split検出器（Surprise定義・有意判定）を設計・実装する必要がある → 次の壁打ちテーマ
- 3属性以上の深い交互作用は段階的Splitで掘るため発見が遅い
  （dogfood規模では交互作用を統計的に支える経験数自体が貯まらないため実害は小さいと判断）

### 併せて確定した設計（同日の議論）

- **One Ledger**: Connectionの実体は減衰Beta(α,β)一つ。Strength/Confidence/Freshness等の属性はすべて導出ビュー。独立保存は多重帳簿ドリフトを生むため禁止
- **Conflict = 非定常性**: ConflictScoreは削除トリガーではなく減衰加速トリガー
- **Lifecycle is a View**: Born〜Retiredの状態は保存せずクエリで導出

### 既存文書との不整合（改訂済み 2026-07-15）

- `Knowledge_EvolutionModel.md`: Pattern→Hypothesis→**Rule**→Strategy の階層が本決定（Rule禁止）と矛盾していた → [KNOWLEDGE_EVOLUTION.md](../core/KNOWLEDGE_EVOLUTION.md) として改訂。Pattern/Hypothesis/RuleはConnectionに吸収（Pattern=導出ビュー、Hypothesis=低ConfidenceのConnection、Rule=保存せずDecision Engineがその場生成）。原本は `docs/archive/`
- `STATE_MACHINE_0.0.1.txt`: Experience Engineの責務に「重み更新」とあった（Connection Engineの責務と矛盾）→ [EXECUTION_MODEL.md](../core/EXECUTION_MODEL.md) として改訂。実行層スタック（Intent→Plan→Capability→Provider→Executor→Runtime）は保存、学習側の記述はCOGNITIVE_ARCHITECTUREへ委譲。原本は `docs/archive/`
