# ADR-0002: Surpriseの定義とSplitの有意判定

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0001](ADR-0001-connection-granularity.md), [CONNECTION_ENGINE.md](../core/CONNECTION_ENGINE.md)

<!-- 改版:begin — tools/sync-adr-superseded.sh が生成する。手で編集しない -->
> **改版済み** — この決定の一部は後のADRが置き換えた。範囲は各Decisionの改版注記が持つ。
>
> - Decision 2 → [ADR-0037](ADR-0037-merge-reachability.md)
<!-- 改版:end -->

---

## Context

ADR-0001で「粗→Split」を採用した。残された未解決点のうち、

1. Surpriseの定義（予測誤差をどう測るか）
2. Splitの有意判定（「食い違い」の閾値）

を確定する。

---

## Decision 1: Surprise ＝ 超過surprisal

### 検討した候補

| 候補 | 評価 |
|---|---|
| 単純な外れ回数 | 確信度を捨てる。p̂=0.55の外れとp̂=0.95の外れを同じ1票にする。Beta事後分布（One Ledger）の情報を捨てて多数決に戻る格落ち。**却下** |
| 分布距離（KL, Bayesian surprise） | 「信念がどれだけ動いたか」を測る。証拠の厚いConnectionは矛盾されても動かない＝沈黙する。Splitが欲しい瞬間に逆向き。なおKLが大きい状況＝証拠が薄い＝既存のNoveltyと同じもの。**新器官として不要** |
| 対数損失（surprisal） | 確信度を正しく重み付け。ただし較正されたモデルでも期待値が非ゼロ（エントロピー分）のため、補正が必要。**補正付きで採用** |

### 定義

```text
s_excess = −log P(y | Beta(α,β)の予測) − H(予測)
```

**驚きから迷いを差し引いた超過分**だけをSurpriseと呼ぶ。

性質:

- 較正されたConnectionでは期待値ゼロ（成功で沈み、失敗で浮上する自己均衡）
- 迷っているConnection（エントロピー大）は外れても驚かない → p̂≈0.5の誤爆が構造的に消える
- 冷たいConnection（証拠薄）は事前寄り＝エントロピー大 → 生まれたてがノイズでSplit候補にならない
- 減衰が擬似カウントを有界に保つ → p̂は1に張り付けない → **Surpriseも有界**（決して無限に確信しないから、決して無限に驚かない）

### Surprise台帳

```text
{ connection, experience_id, Context属性一式, y, p̂, s_excess }
```

台帳は「Experienceログ＋その時点のConnection状態」から決定的に再計算できる
**導出インデックス**であり、One Ledger原則を破らない。
古いSurpriseはConnectionと同じ半減期で薄れる。

多粒度の帰属: 経験にマッチした各Connectionが、自分の予測に対して各自記帳する
（Decision Engineが実際にどれを使ったかとは独立）。

---

## Decision 2: 審判 ＝ 対数ベイズファクター、二段構え

### 二段構え

```text
第一段: トリガー（安い、毎経験）
  台帳の累積が正に浮上（+2 nats）
  → Questioned → Curiosity Queueへ
  （Curiosityが「気付く」）

第二段: 審判（重い、呼ばれた時だけ）
  属性ごとに ln BF を計算
  （Connection Engineが「実行する」）
```

### 統計量

```text
H0: このConnectionは一枚岩（成功率はひとつの p）
H1: 属性aの有無で世界が違う（p_a と p_¬a は別物）

P(D | H0) = B(α₀+k, β₀+n−k) / B(α₀, β₀)      Beta-Binomial閉形式
P(D | H1) = 「aあり」「aなし」で別々に計算して積

ln BF = ln P(D|H1) − ln P(D|H0)
```

- 必要なのは属性別の成功/失敗カウントのみ。減衰済み実効カウント（小数）でよい
- ln BF ≈ 層別によるSurprise台帳の減少量。**台帳がそのまま検定統計量になる**
- BFはオッカムの剃刀を内蔵（H1のパラメータ増は周辺尤度で自動ペナルティ）
  → 最低証拠数ガードの外付けがほぼ不要

### 多重比較の補正

属性をm個調べたら、事前オッズに織り込む:

```text
判定値 = ln BF − ln m
```

### 閾値とヒステリシス

```text
Split:   判定値 ≥ +3（BF 20相当） → 子をBorn（履歴つき誕生）
Merge:   判定値 ≤  0（BF 1相当）  → 子を親へ畳む
中間帯 (0, 3):                      現状維持
```

Split点とMerge点を分離する（シュミットトリガー）ことで、
境界上のConnectionのバタつきを防ぐ。

**形成には強い刺激が要るが、維持は安い** —— シナプス可塑性の非対称性と同型。

Same Test Both Ways原則は「同じ統計量」で維持し、
入口と出口の敷居の高さだけ変える。

誤Splitのコストは低い: Born with Historyで子は履歴を継いで生まれ、
まぐれなら証拠が薄れてMerge帯に落ち、静かに畳まれる。
だから閾値はやや前のめり（+3）でよい。

> **改版（2026-07-21、実測）**: 「証拠が薄れてMerge帯に落ちる」は成立しない。
> ln BFは減衰につれ`θ_merge = 0`へ**上から** d² のオーダーで漸近するだけで、
> 有限時間では跨がない（実測: 3半減期で0.223、10半減期で2.0e-5、
> 厳密に0.0になるのは約60半減期＝約15年）。θ_merge=0は「自然な中立点」だが、
> **減衰の側からは到達できない点**である。Mergeが実際に発火するのは
> **新しい反証の証拠でln BFが負に振れたとき**だけで、その経路は生きている。
> 誤Splitの安全弁は「時間が畳む」ではなく「反証が畳む」— 反証が来ない
> （＝そのscopeで選ばれなくなった）子は畳まれない。
> 詳細と帰結は[ADR-0037](ADR-0037-merge-reachability.md)の「実測による訂正」。

### ノブ一覧（人間が決める数字はこれだけ）

```text
θ_trigger   +2 nats     安全弁。低すぎても審判が正しく棄却する
θ_split     +3          実質唯一のチューニング対象
θ_merge      0          自然な中立点。証拠が五分なら簡潔な方
```

### 検算例

真実「RustはClaude、ただしLifetimeだけはダメ」。
Axum成功3件が既にあり、Lifetime失敗が積まれる（事前Beta(1,1)、補正前）:

```text
Lifetime失敗 2件   ln BF 1.61   静観
             3件         2.17   静観（Questionedにはなる）
             4件         2.64   静観
             5件         3.04   ★ Split発火
```

Replayにより子 (Rust, Lifetime)→Claude は β=6 を背負って生まれ、
生まれた瞬間から「使うな」と言える。

---

## Consequences

- Open Questions 1・2が閉じ、Connection Engineは実装可能な解像度に到達
- 残る未解決点: 減衰半減期、事前分布の継承、backoffブレンド、Outcomeの質
- 次戦はOutcomeの質（報酬信号の設計）。使用者の個性の主要な入口であるため優先
