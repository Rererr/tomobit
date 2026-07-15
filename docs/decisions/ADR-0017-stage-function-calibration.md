# ADR-0017: ステージ関数の改版 — 成長のゲートは量でなく較正度

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0008](ADR-0008-appearance.md)（改版元 — Decision 2のみ置換）, [ADR-0002](ADR-0002-surprise-and-split-judgment.md)（Surprise）, [ADR-0016](ADR-0016-curiosity-priority-voi.md)（揺らぎ）, [ADR-0003](ADR-0003-outcome-and-preference.md)（preference）

---

## Context

[ADR-0008](ADR-0008-appearance.md)のステージ関数は、思想では「質のマイルストーン」と
言いつつ、中身は数のゲートだった — `Evidence ≥ 3`、`stable ≥ 2`、`Split子 ≥ 1`。

これでは**経験は大量にあるが予測を外しまくるTomoでも大人になれる**。
肥満が成長に見える指標である。

成長と呼ぶべきは、世界を正しく見ていること — **較正**である。
「70%成功すると思った場面で、本当に7割成功したか」。

測定装置はすでにある:

- **較正**: Surprise（ADR-0002の超過surprisal）は、較正されていれば
  減衰平均が0近くに自己均衡するよう設計済みの計測器
- **鋭さ**: 較正だけには抜け穴がある — いつも五分五分と言う予報士は
  外れもしない（無知のまま較正できる）。判断の揺らぎ（ADR-0016の
  TSくじの割れ率）が低いこと＝迷っていないこと、で塞ぐ

新しい統計量は導入しない（ADR-0008 Decision 2の制約を維持）。

---

## Decision: S3/S4を「較正＋鋭さ」でゲートする

```text
S0 たまご    connectionsが空                       据え置き
S1 ひよこ    connection ≥ 1                        据え置き
S2 こども    max Evidence ≥ 3                      据え置き
S3 わかもの  較正の立ち上がり —
             減衰Surprise平均 ≤ θ_cal              予測が世界とズレていない
S4 おとな    較正 ＋ 鋭さ —
             S3 かつ 頻繁な島での揺らぎ ≤ θ_sharp   迷わず、しかも当たる
S5 あいぼう  S4 かつ preferenceのEvidence ≥ 1       据え置き
```

- **S0〜S2の量ゲートは意図的に残す** — 卵は較正しようがない
  （データゼロで較正度は定義できない）。幼少期は喰って育つ
- **S5のpreferenceゲートは触らない** — Companionshipの定義そのもの
- 「頻繁な島」の定義は到来頻度（VoIと共有、ADR-0016）

---

## 副産物: 若返りが物語になる

環境が変わってTomoが外し始めると、Surpriseが上がりS4→S3へ後退する。

量ゲートの若返り（単なる減衰）と違い、

> **「世界が変わって、学び直している」が姿に出る。**

ADR-0008の「ステージは単調でない＝正直さ」が、より深い意味を持つ。
ステージ後退はReflection（[ADR-0015](ADR-0015-reflection.md)）の将来トリガー候補でもある
（初期4種には入れない。Questioned・逆転と同族なので語彙が揃ってから）。

---

## Consequences

- ADR-0008はDecision 2のみ本ADRが置換。Decision 1/3/4
  （保存しない・表情・描画）は不変。「ステージはView」の原則も不変 —
  較正も揺らぎも台帳からの導出値であり、保存しない
- 実装ノブ: θ_cal（減衰Surprise平均の上限）、θ_sharp（揺らぎの上限）、
  頻繁な島の下限頻度 — いずれも実装時に実測で決める
- `face.Stage`（internal/face/sprite.go）の改版が必要。ただし前提部品
  （Surprise台帳・TSサンプラー）が未実装のため、実装はそれらの後。
  それまで現行の量ゲートが暫定として動くことを許容する
