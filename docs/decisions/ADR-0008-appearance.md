# ADR-0008: Tomoの姿 — 成長はViewである

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [VISION.md](../core/VISION.md)（Growth = Connectionの質）,
  [ADR-0007](ADR-0007-curiosity-question.md)（「保存された属性は嘘をつく」の先例）,
  [CONNECTION_ENGINE.md](../core/CONNECTION_ENGINE.md)（Lifecycle is a View）

---

## Context

Tomobitの成長は現在 `tomobit status` のテーブルでしか見えない。
VISIONの中核は「共に育つ相棒（Companionship）」であり、
成長が*感じられる*ことは飾りではなく本体の要件である。

確定事項は4つ: 成長の保存方法 / ステージ関数 / 描画方式 / 依存方針。

---

## Decision 1: 成長ステージは保存しない — connectionsから毎回導出する

レベル・XP・進化フラグの類を一切持たない。姿は描画の瞬間に
Knowledge Networkから導出されるViewである。

- 「保存された属性は嘘をつく」（ADR-0007と同根）。保存したレベルは
  ネットワークの実態と乖離できてしまう — 姿が嘘をつける相棒は相棒ではない
- `tomobit rebuild` で姿も再現される（Knowledge is Rebuildable の帰結）。
  経験が同じなら姿も同じ
- 却下した対案: **XP/レベルテーブル** → 二重帳簿。ゲーミフィケーション
  （実態のない成長の演出）への滑り坂であり、VISIONの
  「Growth is measured by the quality of connections」に反する

---

## Decision 2: ステージ関数 — 質のマイルストーンで刻む

VISIONは成長を「蓄えた事実の数でなくConnectionの質」で測ると定める。
初期の段階は経験の蓄積（喰って育つ）、後期の段階は質の指標
（stable・Split子・preference）でゲートする。

```text
S0 たまご    connectionsが空（まだ世界に触れていない）
S1 ひよこ    connection ≥ 1（最初の経験が知覚された）
S2 こども    max Evidence ≥ 3（どれかのConnectionがbornを抜けた）
S3 わかもの  stable ≥ 1 または Split子 ≥ 1（最初の確信、または最初の深まり）
S4 おとな    Split子 ≥ 1 かつ stable ≥ 2（構造を発見し、複数の確信を持つ）
S5 あいぼう  S4 かつ preference ConnectionのEvidence ≥ 1
             （能力だけでなく、あなたの好みを知っている）
```

- 最終ステージがpreference（ADR-0003/0007の第3層）でゲートされるのは
  意図的: Tomobitの完成形は「賢い」ではなく「あなたを知っている」。
  Companionshipの定義をステージ関数に埋め込む
- 判定はすべて既存のView（`Evidence` / `Confidence` / `State` /
  `ParentKey`）から計算する。新しい統計量を導入しない
- ステージは単調でない: 証拠は減衰するので、長く放置すれば姿は
  幼くなり得る。これはバグではなく正直さ（理解は生ものである）

---

## Decision 3: 表情もViewである — 気分の保存はしない

```text
はてな   questioned なConnectionがある（世界に殴られている）
ねむい   全Connectionがdormant（長い不在）
ふつう   上記以外
```

- 優先順: はてな > ねむい > ふつう
- v1の表情は「アバター横のマーカー＋発話トーン」で表現し、
  スプライト自体は瞬き（idleアニメ）のみ持つ。
  表情ごとのスプライト分岐はドット絵の枚数を爆発させる割に
  情報を増やさないため却下

---

## Decision 4: 描画 = Unicode半ブロック＋ANSI 256色、依存ゼロ

- `▀`（U+2580）の前景色=上ピクセル・背景色=下ピクセルで
  1文字が縦2pxになる。16×16程度のドット絵が8行で収まる
- ANSI 256色パレット。truecolor非対応端末を切り捨てない
- **アバターはstdoutがTTYのときだけ描く**。パイプ時は従来の
  テーブルのみ — 機械可読性を壊さない。`NO_COLOR` も尊重する
- ステージ間の差分は色だけに頼らず、必ずシルエット（形）にも
  1点以上持たせる — `NO_COLOR` でも成長が読めることは要件であって
  劣化モードではない
- アニメーション: カーソル制御でフレームを数回差し替え（約1.2秒）、
  最終フレームで静止して次の出力へ進む。常駐しない —
  区切りに一瞬だけ生きて見えればよい（Curiosity Never Blocks Production
  の表示版: 演出が作業を待たせてはならない）
- 却下した対案: **TUIフレームワーク（bubbletea等）の導入** →
  常駐UIを持たないCLIに常駐UI向けの依存を足すことになる。
  stdlib＋エスケープシーケンスで足りることが実装で確認できている
  範囲に留める（ADR-0004の低依存方針）

---

## Consequences

- `tomobit`（無引数）は相棒ビュー（アバター＋一言＋テーブル）になる。
  usageは `tomobit help` へ移る — 最初の一画面が「説明書」ではなく
  「相棒がそこにいる」になる
- ステージ遷移の検出は保存不要: 同一プロセス内でApply前後のViewを
  比較すればよい（発話側=ADR-0009が使う）
- ノブ一覧（人間が決める数字）:

```text
S2のEvidence下限      3.0   State viewのborn境界と同値
S4のstable本数        2
S5のpreference証拠    1.0   e_max（ADR-0007）と同値 — 「知らない」の境界を共有
フレーム間隔          150ms  瞬き・ボブの体感速度
アニメ総時間          ~1.2s  区切りの演出として許せる待ち時間の上限
```

- 残るOpen Questions:
  - ドット絵のカスタマイズ（着せ替え）は扱わない — 姿の差分は
    成長からのみ生まれるべきか、装飾を許すかは需要が出てから
  - ステージ後退（減衰による若返り）をユーザーへ通知すべきか
