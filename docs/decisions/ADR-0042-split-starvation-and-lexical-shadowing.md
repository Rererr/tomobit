# ADR-0042: Split の飢餓と辞書順の遮蔽 — 決定が既知の危険を読めない

- Status: **Proposed（問題提起 — 対策は所有者の裁定待ち。実装していない）**
- Date: 2026-07-24
- 関連: [ADR-0002](ADR-0002-surprise-and-split-judgment.md)（Split トリガ = excess surprisal）,
  [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md)（悲観ゲート）,
  [ADR-0013](ADR-0013-prior-inheritance-mean-only.md)（Decision 2: 判断は最細一致のみを読む）,
  [ADR-0016](ADR-0016-curiosity-priority-voi.md)（VoI — 対案3の器官）,
  [ADR-0037](ADR-0037-merge-reachability.md)（同族: 選ばれない→証拠が来ない→直らない）

---

## Context（合成 dogfood 2026-07-24 の実測。再現一式は tools/dogfood）

claude-code に go 成功11件 + **rust 失敗11連続**（うち7件は直近1週間）を積んだ
台帳で、rust タスクの decide を 200 seed 実行した:

- 選択分布: **claude-code 81 / codex 65 / human 54 — 11連敗中の Provider が最頻**
- 監査行: claude の読まれた Connection は `scope="cap=implement"`（quantile
  0.313, passed）。`lang=rust|claude-code`（strength **0.08**, evidence 10.6）は
  台帳に**存在するのに一度も読まれない**

機構は二段になっている:

1. **Split の飢餓**: go 成功と rust 失敗の均衡混合は親の p を最大エントロピー
  近傍に保ち、excess surprisal（誤較正の検出器, ADR-0002）が発火しない。
  実測 LedgerSum(cap=implement/claude)=1.07 < ThetaTrigger 2.0。一方、
  反実仮想の lnBF(lang=rust で分割)=**5.95 ≥ 発火線** — 召喚さえされれば
  即分割の証拠量が眠っている。しかも定常状態では逆進する: 親の mean が
  0.40 の今、次の rust 失敗の excess は **−0.16（負）** — **証拠が増えるほど
  トリガ統計量が減る**
2. **辞書順の遮蔽**: `{cap=implement, lang=rust}` の下で同粒度（1トークン）の
  一致が複数あるとき、tie-break は ScopeKey 辞書順（decide.go finestMatch —
  「監査が一意の行を指すため」）。`cap=` < `lang=` なので **lang= 系の
  Connection は cap= 系が存在する限り系統的に読まれない**。乱択ですらなく、
  常に同じ側が遮蔽される

帰結: 「知覚は記録し、網は保持し、決定だけが盲目」— ADR-0037 が扱った
自己強化デッドロックの同族で、今回は**悲観ゲートが守るべき本番タスク**で
守れていない。

## なぜ即修正しないか

どの対策も判断数学そのものに触れ、既存台帳で選ばれる Provider が変わる。
ADR-0038 のときこの種の変更は所有者の明示的な了承のもとで入った。
また対策次第で ADR-0002（トリガの較正）/ ADR-0013 Decision 2（最細一致）/
ADR-0016（VoI）のどれを改版するかが分かれる — 設計の分岐点である。

## 対案（トレードオフ列挙 — 裁定待ち）

1. **tie-break を「証拠の重い側」へ変える**（decide.go の1箇所）
   同粒度なら evidence（LedgerSum）最大の Connection を読む。
   **→ 実測で棄却**（下記「対案の実測」）: 本ADR初稿の根拠
   「lang=rust evidence 10.6 > cap 側の読み値」は evidence と quantile の
   比較違いで、evidence 同士では cap=implement 22.10 > lang=rust 10.54。
   「より多く見た側」は依然 cap であり、遮蔽は1ビットも動かない
2. **同粒度一致はゲートだけ全数読む**
   選択（TS）は最細一致のまま、悲観ゲートは同粒度一致全ての min(quantile) で
   引く。「危険を知っているのに黙る」は消える。ADR-0013 Decision 2 の
   「読むのは一つ」を「選ぶのは一つ、拒否は全員ができる」へ改版する必要。
   **境界の精密化（実装レビューで確定）**: min の影響はゲート判定のみに
   閉じる — フォールバックの least-bad 順位・通過者タイブレークは最細一致の
   quantile のままで、今日と1ビットも変えない。監査行は、通過時は
   「選択が読んだ最細一致」を、ゲート落ち時は「拒否した兄弟」
   （ScopeKey/quantile/passed=false）を指す。GatePass（名誉回復, ADR-0015）は
   connection 単位のまま変更しない — 名誉回復は「そのConnectionの回復」で
   あって「タスク文脈での集合ゲート通過」ではない
3. **Split 召喚を Curiosity へも配線する**（ADR-0016 の延伸）
   excess surprisal（受動トリガ）に加え、反実仮想 lnBF の大きい分割候補を
   VoI 队列に積み、余裕のあるときに Split 判定を走らせる。トリガの較正
   （ADR-0002）を触らず飢餓だけを解く。器官が一つ仕事を増やす
4. **トリガ統計量の改版**（ADR-0002 の改版）
   excess surprisal をトークン条件付きの誤較正検定に置き換える。最も根治的
   だが最も重い — 較正・テスト・過去挙動の説明の全てをやり直す
5. **何もしない（明文化）** → 「サブスコープの連敗は、親スコープが均衡して
   いる限り判断に反映されない」を仕様と認めることになる。Living Harness の
   看板と正面衝突するため、明文化するなら VISION 側の但し書きが要る

組み合わせも成立する（例: 1+3 は小さく入れて根治は待つ）
**→ 実測で無意味と判明**（下記: 3 が入ると最細一致が一意になり 1/2 は発火余地を失う）。

## 対案の実測（2026-07-24、使い捨てコピー上・200 seed 決定的計測）

同じ合成台帳（go成功11 / rust失敗11連続）で各対案を実装して比較した:

| 対案 | rustタスク (C/X/H) | 11連敗claudeが最頻か | goタスク（退行検知） | uniform不変(ADR-0038) | diff行 |
|---|---|---|---|---|---|
| 基準線（現状） | 81/65/54 | **最頻** | 81/65/54 | ✓ | 0 |
| 1 evidence tie-break | 81/65/54 | **最頻（不変）** | 不変 | ✓ | +12 |
| 2 gate同粒度全読み | **0/90/110** | **消滅** | **不変** | ✓ | +32 |
| 3 split近似（定常状態） | 0/90/110 | 消滅 | **124/22/54（変化）** | ✓ | 〜+68(stub) |
| 1+3 / 2+3 | 3単独と完全一致 | — | — | ✓ | — |

- **対案2が唯一「rust修正・go不変・全不変条件保持」**。claude は
  `lang=rust`(q=0.011) が min を引いてゲート落ちし、監査行に
  `lang=rust / passed=false` が明示される — 「危険を知って黙る」が消える
- 対案3は rust を直すが go でも子 `{cap,lang=go}`(mean 0.693) が最細一致に
  なり claude 優勢化・codex 急落。是非は価値判断（所有者裁定）
- 限界: 対案3は機械一括分娩の定常状態近似で、VoI 队列配線の動的挙動・
  予算は未測定
- **一般性スイープ（同日追試）**: 「単一台帳1点」の限界を潰すため、台帳形状
  5種（基準 / 90日超の古い連敗 / 親も五分の弱い親 / 健全な lang=go 兄弟が
  拮抗 / preference 側の同粒度タイ）で main と対案2ブランチを 200 seed 比較。
  **全形状で連敗 provider の排除は一貫し、健全兄弟の巻き添え・preference の
  ゲート混入・古い危険での過剰ゲートは無し**。go・タイ無し・一様事前は
  全形状で main とビット一致（不変条件保持）。推奨を覆す証拠は出なかった

**起草者の推奨**: 対案2 を先行導入（最小侵襲・不変条件全保持・監査の正直さが
上がる）。対案3（根治）は VoI 配線の設計を別ADRとして裁定する。
実装はブランチ `adr-0042/gate-all-same-granularity` に用意する（main 無変更 —
採用ならマージ、却下ならブランチ削除で済む）。

## Consequences（裁定後）

- 選ばれる Provider が変わりうることの了承（ADR-0038 と同じ手続き）
- 再現・較正の材料は tools/dogfood（本ADRの実測を生成したハーネス）にある
- 採る対案に応じて ADR-0002 / 0013 / 0016 の該当 Decision に改版注記
