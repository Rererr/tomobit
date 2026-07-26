# ADR-0039: 相棒ビューの機械可読view — レンダラはひとつの導出を読む

- Status: **Accepted**（2026-07-24 起草・goal 指示の自律セッションが先行適用、同日所有者が配備裁定で追認）
- Date: 2026-07-24
- 関連: [ADR-0001(GUI)](https://github.com/Rererr/tomobit-gui/blob/main/docs/decisions/ADR-0001-gui-architecture.md)（Decision 5: テキストViewの移植を暫定許容）,
  [ADR-0017](ADR-0017-stage-function-calibration.md)（ステージ関数の改版 — 較正ノブは動く）,
  [ADR-0025](ADR-0025-face-autolaunch.md)（成長と気分は顔窓が担う）,
  [ADR-0032](ADR-0032-pipe-chat-first-class.md)（Decision 1: `--view ndjson` の語彙）

---

## Context

GUI の Tomo名ヘッダは、本体 `internal/face/stage.go` とその依存
（core の Beta 数学・decide のゲートまで含む約570行）を**手動移植**して
ステージを導出している（GUI ADR-0001 Decision 5 の暫定許容）。

この移植には構造的な欠陥がある:

- **較正ノブは動く前提である**。ADR-0017 は式の改版を明示的に予定し、実際に
  ADR-0036/0037/0038 は決定の数学を直近3日で3回動かした。移植側の追随は
  opt-in 照合テストの手動実行だけで守られている
- ドリフトの帰結は「クラッシュ」ではなく「**GUI と顔窓が別のステージを言う**」。
  相棒の姿が台帳と食い違うのは、Companionship の静かな毀損である
- `internal/` はモジュール境界で閉じており import できない（GUI ADR-0001 が
  却下済みの対案）。公開パッケージ化しても、GUI バイナリに焼き込まれた版と
  **インストール済み CLI の版がずれる**問題は残る — 台帳に書く者と導出する者が
  別バイナリである限り、ずれは消えない

一方、知覚・忘却・訂正で GUI が既に確立した型がある:
**器官はインストール済みバイナリをサブプロセスで呼ぶ**（GUI ADR-0001 Decision 3）。
台帳を書いているまさにそのバイナリが導出も担えば、ずれは定義上存在しない。

## Decision

### Decision 1: `tomobit status --view json` を追加する

`chat --view ndjson`（ADR-0032）と同じ語彙で、status に機械可読viewを生やす。
stdout に JSON オブジェクトを1つ書いて終わる:

```json
{"type":"status","exists":true,"stage":3,"stage_name":"わかもの",
 "mood":{"name":"じしん","marker":"!"},"speak":"..."}
```

- `exists: false` のとき他フィールドは持たない。**台帳が無ければ作らない** —
  `os.Stat` を openStore より先に引く。人間向け status は台帳を作ってきたが、
  機械viewの観測で毛玉が生まれるのは観測ではない
- `speak` は `voice.Suggest` が黙れば省略（null ではなく欠落）
- 未知フィールドは読み手が無視する — ADR-0032 Decision 1 と同じ前方互換契約
- `--view json` では顔窓の自動起動・挨拶の記帳（`tomo.greeted`）・TTY装飾を
  一切行わない。機械viewは対人ではない（ADR-0035 の対偶）
- chat の `--view ndjson` と違い、TTY での実行は拒否しない。あちらの拒否は
  「端末の器官が全て閉じている前提のストリーム」を守るゲートであり、
  こちらは器官を無条件に閉じた単発出力 — 端末で jq に通しても何も壊れない

### Decision 2: ステージと気分を機械viewに載せることは ADR-0025 と矛盾しない

ADR-0025 が端末からステージ表示を退けたのは「**人間向け表示の重複**」の話である。
機械viewはレンダラへのデータ供給であり、表示ではない。むしろ ADR-0020 の
「両レンダラはひとつの真実を読む」を、導出まで含めて初めて成立させる。

### Decision 3: GUI は移植を捨て、このviewを読む（GUI 側の追随）

- GUI `stage.go`（移植570行）と較正ノブの複製を削除し、`GetTomoStatus` を
  `tomobit status --view json` のサブプロセス呼び出しに置き換える
- 旧本体（このviewを知らない版）では呼び出しが失敗する。ヘッダは素の
  「Tomo」に落ち、エラーは既存のヘッダ取得失敗と同じ経路で表に出す —
  黙って毛玉を名乗るより、素のTomoで居るほうが正直である
- GUI ADR-0001 Decision 5 の暫定許容と BACKLOG の「本体側の設計待ち」は
  本ADRで役目を終える

## 却下した対案

- **`internal/face` の公開パッケージ化**（GUI が Go module として import）→
  API 安定性の負債に加え、GUI に焼き込まれた版とインストール済み CLI の版ずれが
  残る。「台帳を書くバイナリ自身が導出する」保証はサブプロセスでしか得られない
- **chat の NDJSON viewストリームに stage イベントを流す** → ヘッダは chat が
  走っていない時（起動直後・セッション一覧閲覧中）にも要る。chat の寿命に
  ヘッダの真実を縛る理由が無い
- **新サブコマンド `tomobit stage`** → status は既に相棒ビューの器官であり、
  ステージはその一部（ADR-0025 以前は status が表示していた）。器官を増やさず
  viewを増やす
- **connections 一覧も JSON に載せる** → GUI はメモリViewとして SQLite を
  読み取り専用で直接読む型を既に持つ（GUI ADR-0001 Decision 2）。導出を伴わない
  生の射影まで載せるのはviewの肥大で、必要になった時に足せる（前方互換契約）

## Consequences

- 本体: `cmd/tomobit/main.go` の status 経路に `--view` フラグ。json 時は
  `face.StageFrom` / `face.Mood` / `voice.Suggest` を束ねて1オブジェクトを書く。
  テストは (1) 台帳ありで stage/stage_name が顔窓と一致
  (2) 台帳なしで `exists:false` かつ**DBファイルが生まれない**
  (3) json 時に `tomo.greeted` が記帳されない — を固定する
- GUI: `stage.go` / `stage_test.go` の移植コードを削除（純減 ~1,000行）。
  照合テストの維持義務が消える。README の前提に本体の最低版を明記
- README の ADR 索引に本ADRを追加する
