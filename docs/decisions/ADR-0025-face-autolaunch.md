# ADR-0025: 端末アバターの廃止と顔窓の自動起動

- Status: **Accepted**（2026-07-17、本人決定「描画を止める方向で。ただし設定で明示的に
  止めない限り、tomobit起動時にfaceも起動することをデフォルトに」）
- Date: 2026-07-17
- 関連: [ADR-0008](ADR-0008-appearance.md)（Decision 4の端末描画を本ADRが廃止）,
  [ADR-0020](ADR-0020-face-window.md)（Decision 1「端末レンダラは廃止しない」を改版）,
  [ADR-0021](ADR-0021-onboarding.md)（config.json / `tomobit setup` — 配線は経験ではない）,
  [ADR-0027](ADR-0027-face-lifetime.md)（本ADR Decision 2の「明示的に止めない限り常駐」の
  *常駐* を「出すか」＝`face_auto_launch` と「残すか」＝`face_resident` の二軸へ分化。本ADRは
  「窓を出す」規律として存続し、ADR-0027 が「出した窓の寿命」を足す）

---

## Context

端末の16×12半ブロックアバターが「怖く見える」と報告された。ANSI 256色の実RGB値で
スプライトを画像再構成して計測した結果、原因は解像度ではなく**背景色を制御できない
媒体にグレースケール資産を置いたこと**だった:

- ダーク背景（#1e1e1e前後が典型）では目 `e`（234=#1c1c1c）が背景と同化して
  「眼窩の穴」になり、輪郭 `k`（236=#303030）がほぼ消えて灰色の塊が浮遊する
- ライト背景では白 `W`（231）が同化して胸・マズルが抜ける
- 白マズルの塊はこの解像度では「剥き出しの歯」に読める（背景色によらない）

ダークに合わせればライトで破綻し、逆も然り — 端末側の改修は上限が低い。
姿の正本は既に窓レンダラ（ADR-0020、32×32・自前背景・truecolor）にある。

---

## Decision 1: 端末アバターを廃止する — 姿は窓レンダラに一本化

- 削除: `internal/face` の render.go / animate.go / sprite.go（+各テスト）、
  `cmd/tomobit` の printAvatar、`docs/design/SPRITES.md`（16×12資産の正本）
- 残す: stage.go / mood.go — `StageFrom` / `StageName` / `Mood` は
  status・voice・facewin が使い続ける。**姿のView導出は生きる。死ぬのは端末の絵だけ**
- 端末での成長の可読性は `Tomo · <ステージ名>` のテキスト行が担う（既存）。
  ADR-0008 Decision 4の「NO_COLOR でも成長が読める」要件は、絵ではなく
  このテキストが満たす形に置き換わる
- 却下した対案: **端末スプライトの改修**（目にグリント・輪郭を明るく・マズル形状修正）
  → 背景依存が原理的に残る（ダークで目が消えるかライトで胸が消えるかの二択）。
  さらに16×12資産は窓v2との「同じ子に見える」同期義務を負っており、
  上限の低い表現のために保守コストを払い続けることになる

---

## Decision 2: 顔窓の自動起動 — 明示的に止めない限り、起動時に窓が出る

姿の表示先が窓だけになったので、「窓が出ていること」を既定にする。
相棒の姿が opt-in では、Companionship（VISION中核）が opt-in になってしまう。

- **対象は対話的な入口**: TTYでの `chat` / `do` / `status`（bare `tomobit` は
  cmdHome経由でこのいずれかに落ちる）。パイプ・リダイレクト時は起動しない —
  機械可読文脈に副作用を持ち込まない（ADR-0008のTTY判定と同じ理由）。
  `record` / `perceive` / `rebuild` は plumbing なので対象外。
  `setup` は最後に status を呼ぶので自然に起動する（「setup後の最初の画面はTomo」
  の思想がそのまま窓に接続される）
- **探索**: `os.Executable()` と同ディレクトリ → PATH の順で `tomobit-face` を探す。
  見つからない・起動失敗は stderr 1行の警告で続行（致命にしない）。
  警告は「インストールするか設定で止めるか」を促す正直なシグナルであり、握り潰さない
- **detach**: 新セッションで起動し、stdout/stderr は `~/.tomobit/face.log`
  （起動ごとに上書き）へ。親の終了・Ctrl-Cに巻き込まない。Wait は goroutine で
  回収（ゾンビ防止）。/dev/null でなくログなのは、起動後の失敗（フォント・
  DBオープン・ウィンドウ生成）の診断経路を残すため — 自動起動が既定ONに
  なった以上、この経路は全ユーザーの主経路であり握り潰せない
- **起動は引数バリデーションの後**: 拒否される呼び出し（provider の typo 等）が
  GUIプロセスという痕跡を残してはならない（ADR-0023「rejected combination
  must leave no trace」の規律を副作用一般に適用）
- **二重起動防止**: `~/.tomobit/face.lock` の排他ロック（unix=flock /
  windows=LockFileEx のビルドタグ分割。windows側は既存間接依存 `x/sys` の
  直接化のみで新規モジュールなし。fd保持でプロセス終了時にOSが解放、という
  意味論は共通。GOOS=windows のクロスビルドが通る状態を既存慣習どおり維持する）。
  **face自身が起動時に取得**し、
  取れなければ既に居るとして静かに終了する — 手動の二重起動もこれで防げる。
  CLI側は spawn 前に取得を試みて、取れたら解放してから spawn（無駄な起動の節約）。
  CLI同士がレースしても face 側のロックが最終ガードなので二重には出ない。
  ロックは機械に1つ（DB毎ではない）: マスコットはデスクトップに1匹
- **設定**: config.json に `face_auto_launch`（`*bool`、省略=ON）。
  env `TOMOBIT_FACE=0|1` がセッション上書き（env > config、ADR-0021の解決順。
  その他の値は警告して既定へ）。フラグは足さない — 3入口すべてに生やすコストに
  対して env で足りる。必要になったら足せばよい
- `tomobit setup` に質問を1つ追加（顔窓の自動起動 on/off、Enterで維持）—
  config.json を書くのは setup、の一貫性を保つ
- 却下した対案: **launchd 等での常駐 daemon 化** → プラットフォーム専用の配線が
  config.json の外に増える。CLI起点の spawn は OS 非依存で、
  「作業を始めた瞬間に相棒が居る」というタイミングにも合う

---

## Consequences

- レンダラの責務が完全分離する: 端末=声とテキスト、窓=姿。
  「発話はView」（ADR-0009）は端末に残り、姿のViewは窓だけが描く
- SPRITES.md 消滅により端末⇔窓のスプライト同期義務が消える
- `tomobit-face` を入れていない環境では対話的な起動のたびに警告1行が出る
  （止めたければ `face_auto_launch: false`）
- ADR-0008 Decision 4 は歴史的記録になる（本ADRが廃止を記録）。
  ADR-0020 Decision 1 の「端末レンダラは廃止しない」も本ADRで改版
