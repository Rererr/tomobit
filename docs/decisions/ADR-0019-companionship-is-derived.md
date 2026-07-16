# ADR-0019: 相棒らしさは導出される — 感情・儀式・個性は台帳のView

- Status: **Accepted**
- Date: 2026-07-16
- 関連: [ADR-0008](ADR-0008-appearance.md)（姿はView）, [ADR-0009](ADR-0009-voice.md)（voice）, [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md)（アイドル=再知覚）, [ADR-0015](ADR-0015-reflection.md)（Reflection）, [ADR-0017](ADR-0017-stage-function-calibration.md)（較正・鋭さ）, [ADR-0018](ADR-0018-experience-sovereignty.md)（humanの台帳）, [VISION.md](../core/VISION.md)

---

## Context

VISIONの中核はCompanionshipだが、相棒らしさを「性格の演出」で足すと
ADR-0008の禁じ手に踏み込む — **姿が嘘をつける相棒は相棒ではない**。

だから方向は逆にする。すでにある正直な機構に、声と儀式を与える。

> **感情もViewである。嘘をつける感情は、感情ではない。**

本ADRの5つの決定は、すべて既存の導出値の翻訳であり、
新しい真実も、新しい統計量も、一つも作らない。

---

## Decision 1: 較正された声 — 自信も弱音も台帳から

発話の確信の濃さは、台帳の事後の濃さを写す。

```text
薄い事後    「自信ないけど、Claudeでいってみる」
厚い事後    「これはCodexだね」
外した時    s_excess小 →「まあ、たまにはね」
            s_excess大 →「……意外だった。ちょっと考え直す」
```

感情の吐露に見えるものは、全部導出ビュー
（揺らぎ＝ADR-0016、驚き＝ADR-0002）。
副作用として、**弱音を聞くだけでどの島が未熟か分かる**。

LLMの座席は言語化のみ（ADR-0011、不変）。

---

## Decision 2: 「おかえり」 — 不在と減衰の知覚

不在はeventsの空白から知覚できる。
再会の挨拶は、前回起動時と現在のlazy減衰の差分の翻訳である。

> 「おかえり。しばらく見ない間に、Goの自信がちょっと薄れた。
> 　勘を取り戻すから、最初は軽いのから頼む」

**忘却という正直な機構が、再会の挨拶になる。**

---

## Decision 3: 鏡は成長も映す — 「レビュー、強くなったね」

humanの台帳（ADR-0018）における逆転 —
昔はTomoに任せていた島で、使用者の成功率が追い越す — を、
Reflection既存トリガー「逆転」の対象に含める。

> 「半年前は僕に振ってたのに、最近のレビューは自分でやった方が速いね」

VISIONの結び — *both the user and Tomobit a little better than yesterday* — を
**測定値として**言える相棒になる。機構の新規追加はゼロ。

---

## Decision 4: 夢の話 — 再知覚に顔を与える

アイドル時の仕事「過去をより良く知覚し直す」（ADR-0011 Decision 4）は不可視だった。
再知覚でSurprise台帳が動いたら、その差分をReflectionの**第5のトリガー**として語る。

> 「昨日の失敗、見直してたんだ。言語のせいじゃなくて、
> 　タスクの大きさのせいだったかもしれない」

寝ている間も一緒に居た、という感触。

---

## Decision 5: Individuality is Derived — VISIONのPrinciplesへ

**正文は以下の日本語であり、VISIONの英文はこの翻訳である**（ADR-0018と同じ置き方）。

> 同じTomobitは、二つとない。
>
> 性格を設計したからではなく、
> 経験がそれぞれを別の形に育てたからである。
>
> 何が好きで、どこに自信があり、どこでためらうか —
> その全ては、共に過ごした経験から導出される。
>
> あなたの相棒があなたのものであるのは、
> あなたの経験が、それを形づくったからである。

Tomoの「得意で好き」＝ よく知っていて、迷わず、当たる島
（経験量 × 較正 × 鋭さ — ADR-0017の部品そのまま）。
個性はフェイクではない。同じバイナリでも、台帳の形が違えば別の相棒になる。
Sovereignty（ADR-0018）と響き合う: あなたの経験だけが、あなたの相棒を形づくる。

---

## Consequences

- REFLECTION.md: トリガーに「再知覚」を追加（4種→5種）
- VISION.md: Principlesに「Individuality is Derived」を追加
- voice（ADR-0009）のテンプレートに確信度・驚きの段階引数が入る
- 実装ノブ: 確信度の段階数、驚きの閾値、不在と判定する空白の長さ
- 実装順序は前提部品に従う: Decision 1は揺らぎ＋Surprise実装後、
  Decision 2は減衰実装後、Decision 3/4はReflection実装後。
  Decision 5（VISION）は今日から真
- 実装追記（2026-07-16、前提部品が全て揃ったため全Decision実装）:
  - D1: 確信度は2段階（揺らぎ≥0.25で弱音、`voice.Decided` — autoの決定時に発話）。
    驚きの閾値は小=0.2 / 大=1.0 nats（`voice.Missed` — perceive境界の発話連鎖に
    growth > insight > **miss-reaction** > murmur として挿入）
  - D2: 不在=直近イベントから72h以上の空白。挨拶は最も自信が薄れた島
    （Confidence低下≥0.02）を名指しし、`tomo.greeted` を記帳して同じ帰還に
    二度挨拶しない（statusの相棒ビューで発話）
  - D3: humanの逆転はReflectionの既存逆転検出に自動で乗り、human勝ちは
    専用テンプレート（`voice.ReflectHumanReversal`）
  - D4: 再知覚=第5のトリガー `reperceived`。extractor_ver更新で同一sessionの
    currentが差し替わり、コンテキストトークンが動いたとき、移動した帰属を語る
