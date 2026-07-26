# ADR-0048: 姿の機械可読view — 資産は正本から配る

- Status: **Accepted**
- Date: 2026-07-25
- 関連: [ADR-0020](ADR-0020-face-window.md)（顔窓=第二のレンダラ / 資産は窓が持つ）,
  [ADR-0025](ADR-0025-face-autolaunch.md)（成長と気分は顔窓が担う）,
  [ADR-0032](ADR-0032-pipe-chat-first-class.md)（Decision 1: `--view` の語彙）,
  [ADR-0039](ADR-0039-status-machine-view.md)（相棒ビューの機械可読view — 同じ型の先例）,
  [ADR-0001(GUI)](https://github.com/Rererr/tomobit-gui/blob/main/docs/decisions/ADR-0001-gui-architecture.md)（Decision 5: 姿は顔窓のまま）

---

## Context

第三のレンダラ（tomobit-gui）が、チャット面の隣に Tomo の姿を置きたいという
要求を持ち込んだ。GUI ADR-0001 Decision 5 は「姿は顔窓のまま・GUIは姿を
再実装しない」と決めており、スプライトの正本は
`docs/design/SPRITES-WINDOW.md` と、その実装先である
`internal/facewin/sprite32.go` である。

GUI がこの要求を自力で満たす道は二つしか無く、どちらも壊れている:

- **資産を書き写す** → 正本が二つに割れる。スプライトは v1→v2 で
  ドキュメントごと差し替わった実績があり（ADR-0020 改訂履歴）、
  「動かない資産」という前提は成り立たない。ドリフトの帰結は
  ADR-0039 が名指したものと同じ — **GUI と顔窓が別の Tomo を見せる**
- **`internal/facewin` を import する** → `internal/` はモジュール境界で
  閉じている。公開しても、GUI に焼き込まれた版とインストール済みの版が
  ずれる問題は残る（ADR-0039 が同じ理由で公開パッケージ化を却下した）

一方 ADR-0039 は、この形の問題に既に答えを出している:
**導出を持つ者が機械可読viewで配り、読み手はデコードするだけにする**。
姿は導出ではなく資産だが、「正本を持つ者が配る」という構造は同じである。

## Decision

### Decision 1: `tomobit-face --view json` を追加する

`chat --view ndjson`（ADR-0032）・`status --view json`（ADR-0039）と同じ語彙で、
顔窓のバイナリに機械可読viewを生やす。stdout に JSON オブジェクトを1つ書いて
終わる — 窓は開かない:

```json
{"type":"sprite","size":32,"breed":"shiba",
 "palette":{"k":"#2E2E2E","e":"#1A1A1A","W":"#FAFAFA","l":"#D9D9D9","m":"#A6A6A6","d":"#666666"},
 "stages":[{"stage":0,"name":"毛玉","frames":[["...32行..."],["...32行..."]],
            "overlay_origin":{"?":[23,3],"z":[19,-1]}}],
 "overlays":[{"marker":"?","rows":["...8行..."]},{"marker":"z","rows":["...12行..."]}],
 "anim":{"blink_min_ms":3000,"blink_jitter_ms":1000,"blink_hold_ms":180,
         "bob_period_ms":3200,"bob_px":1}}
```

- **`--view` は他の副作用より先に返す**。顔ロックを取らず、stderr を差し替えず、
  台帳を開かない。姿を尋ねただけの読み手が、マシンに一つしかないマスコット枠を
  奪ったり、読まない台帳を作ったりしてはならない（ADR-0039 Decision 1 の
  「機械viewの観測で毛玉が生まれるのは観測ではない」と同じ姿勢）
- **`.` はパレットに載せない**。透明は色ではなく不在であり、エントリを与えると
  「透明という色で塗る」読み手を誘う
- **フレームの順序が契約**である: `frames[0]` が A（基本）、`frames[1]` が B（瞬き）
- **`overlay_origin` は本体が計算して配る**。気分記号の座は
  「右端から1px内・シルエットの開始行の1つ上」という規則で、ステージごとに
  違う（`topRow` に依存する）。規則を配って読み手に再導出させると、
  そこが次のドリフト面になる
- **`anim` を必ず載せる**。ADR-0020 Decision 3 は「そこに居る」ことを窓の
  存在理由と定めた。ノブを配らないviewは静止画の作り方を配ったのと同じで、
  別の相棒を作らせてしまう
- 未知フィールドは読み手が無視する — ADR-0032 Decision 1 と同じ前方互換契約

### Decision 2: 姿の口は `tomobit` ではなく `tomobit-face` に置く

`status --view json` に載せる案を退ける。資産を持つのは顔窓であり、
`internal/facewin` は Ebiten を引く — 主 CLI に import させると、端末だけを
使う人のバイナリにグラフィックス依存が入る。**正本を持つ者が配る**という
本ADRの筋からも、姿を尋ねる先は姿を描く者が正しい。

また `status --view json` は**セッション境界ごとに読まれる**（GUI の
refreshLedgerViews）のに対し、資産は動かない。動く状態（stage・気分marker）は
既に status が配っており、読み手は資産を起動時に一度取ってそれと組み合わせれば
よい — **静的な資産と動的な状態を別の口に分ける**ことで、境界ごとに 13KB の
格子を運ぶ無駄も消える。

### Decision 3: 犬種は引数のまま（導出しない）

`--breed` は既存のフラグをそのまま使う。ADR-0020 Decision 5 の
「外見の好みは導出できない数字」は機械viewでも変わらない — 読み手が
どの犬種を描くかは、台帳ではなく人が決める。

## 却下した対案

- **PNG/スプライトシート画像を吐く** → 整数拡大 nearest-neighbor という
  描画規約（ADR-0020 Decision 4）を読み手に守らせる手段が消え、
  「32×32の論理ピクセル」という資産の性質もラスタの中に埋もれる。
  格子のまま配れば、読み手の拡大率がいくつでも規約は保たれる
- **`internal/facewin` を公開パッケージ化して import させる** → ADR-0039 と
  同じ理由（版ずれ）で却下。加えて Ebiten 依存が読み手のバイナリへ伝播する
- **`status --view json` に `sprite` フィールドを足す** → Decision 2 の通り、
  Ebiten 依存の伝播と、動かない資産を境界ごとに運ぶ無駄
- **全犬種を一度に吐く** → 3倍の格子を運んで、読み手は1犬種しか描かない。
  犬種は引数で選べる（Decision 3）

## Consequences

- 本体: `cmd/tomobit-face` に `--view` フラグと `view.go`、
  `internal/facewin/sheet.go` に露出用の `Sheet` / `SpriteSheet` / `BreedName`。
  スプライト資産そのものは1バイトも移動していない — `sprite32.go` は正本のまま
- `window.go` の呼吸幅（ハードコードの `1`）を `bobPx` 定数に括り出した。
  view が配る値と窓が描く値が同じ一箇所から来る
- テストは (1) 全ステージ・全犬種で A/B フレームが順序どおり
  (2) 格子が使う全グリフをパレットが覆う（`.` は載せない）
  (3) `overlay_origin` が `Draw` の計算と一致
  (4) `--view json` が1行のオブジェクトを吐く / 未知の `--view` を拒む — を固定する
- 第三のレンダラは、姿を描いても正本を増やさない。GUI 側の追随は
  [GUI ADR-0001 Decision 5 の追記](https://github.com/Rererr/tomobit-gui/blob/main/docs/decisions/ADR-0001-gui-architecture.md)
- 旧版の `tomobit-face`（このフラグを知らない版）に対しては
  `flag: --view` のパースエラーで落ちる。読み手は姿を出さずに degrade する
  （ADR-0039 の「素のTomoで居るほうが正直」と同じ扱い）
