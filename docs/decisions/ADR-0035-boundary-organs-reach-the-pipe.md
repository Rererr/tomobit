# ADR-0035: 境界の器官はviewストリームの向こうの人に届く — 対人ゲートの改版

- Status: **Accepted**
- Date: 2026-07-21
- 関連: [ADR-0032](ADR-0032-pipe-chat-first-class.md)（pipe chatの一級市民化 — 本ADRはその Decision 3 と同じ改版を stdin 側に施す）,
  [ADR-0007](ADR-0007-curiosity-question.md)（Tomoの質問 — 発火条件を本ADRが改版）,
  [ADR-0015](ADR-0015-reflection.md)（鏡 — 同上）,
  [ADR-0022](ADR-0022-chat-session.md)（区切りの器官）,
  [ADR-0019](ADR-0019-companionship-is-derived.md)（相棒らしさは導出される）,
  [VISION](../core/VISION.md)（Curiosity with Responsibility）

---

## Context

ADR-0032 は「**pipe の向こうに人が居る場合がある**」を認め、
stdout 側（機械可読な view ストリーム）と顔窓の TTY ゲートを改版した。
stdin 側の対人ゲートは、そのとき点検されなかった。

区切りの器官（ADR-0022）は3つある。GUI 経由ではそのうち1つしか動かない:

| 器官 | ゲート | GUI（`chat --view ndjson`、stdin は pipe）での挙動 |
|---|---|---|
| Feedback（ADR-0006 D4） | 無条件 | **届く** |
| Tomoの質問（ADR-0007） | `isTTY(os.Stdin)` | **永久に発火しない** |
| 鏡（ADR-0015） | `isTTY(os.Stdin)` | **永久に発火しない** |

`askWithIO` / `reflectWithIO` は先頭で `if !interactive { return }` し、
`interactive` には `isTTY(os.Stdin)` が渡っている（main.go）。
GUI は子プロセスの stdin を pipe で握るので、この条件は構造上永久に偽である。

配管は既に通っている。ADR-0032 が新設した flush-on-read は
「器官側の実装を知らないフックなので、将来の器官が増えても配線は要らない」と書かれており、
`curiosity.Ask` も `reflection.Ask` も Feedback と同じ「部分行を書いて stdin を待つ」形をしている。
**閉じているのはゲートだけである。**

帰結が重い。GUI を主な入口にすると:

- `tomo.asked` が一度も記帳されず、好奇心の予算（1問/24h）が永久に未消費のまま積まれる
- `reflection` の鏡が一度も出ず、ADR-0015 の「核は双方向性」が片側通行になる
- Preference Gap が導出されても問われないので、ADR-0003 の「好みの Connection」は
  duel（ADR-0026）以外の入口を失う

VISION の Companionship は「命令と実行の関係ではない、相棒という関係である」と置いている。
質問しない・鏡を出さない Tomo は、GUI の中では**実行器に退化している**。
README「/new か /exit で区切ると Feedback→知覚→Tomoの質問→鏡 が走る」も、GUI 上では偽になっている。

---

## Decision 1: 対人の信号は `--view ndjson`（argv）であって、TTY でも env でもない

境界の器官の発火条件を `isTTY(os.Stdin)` から
**`isTTY(os.Stdin) || --view ndjson`** に改める。

信号に argv を選ぶ理由は ADR-0032 Decision 1 がワイヤ形式に argv を選んだ理由と同じである。
「この pipe の先に人が居る」は**呼び出しごとの宣言**であり、呼び出し側の argv が持つのが正しい。

却下した信号を先に書く:

- **`TOMOBIT_FACE=1` に相乗りする** → 顔窓は副作用ゼロの表示だが、
  対人ゲートは**台帳への記帳を伴う**（`tomo.asked` / `reflection`）。
  同じ信号に載せると、`TOMOBIT_FACE=1` を常時 export している人が
  スクリプトを pipe で流した瞬間に予算が焼ける。
  さらに GUI 側（tomobit-gui ADR-0001）は「親環境が既に `TOMOBIT_FACE` を明示していれば触らない」ので、
  ユーザーが `TOMOBIT_FACE=0` を出していると**人が居るのに器官が黙る**。
  信号として両方向に壊れている
- **config の常設フラグ** → ADR-0032 Decision 1 と同じ却下理由。
  常設の既定はスクリプト・CI・テストを巻き込む
- **素テキスト pipe も対人とみなす** → 既存のテスト・スクリプトが
  一斉に質問を浴び、予算を焼き、EOF で無回答が記帳される。
  ADR-0032 が「素テキスト pipe の互換は守ったまま、オプトインで口径を開ける」とした線を越える

## Decision 2: 述語は2つある — 「人が居るか」と「端末に描けるか」を混ぜない

現在 chat は `c.interactive`（stdin と stdout の両方が端末）という単一の述語を持ち、
コメントは "whether a human is watching" と書いている。ADR-0032 でこの定義は陳腐化した。
2つに分ける:

- **`humanPresent`** = `isTTY(os.Stdin) || viewMode` —
  「対話の相手が居るか」。境界の器官（質問・鏡）のゲート
- **`c.interactive`** = 従来どおり両端 TTY —
  「端末に描けるか」。並走の y/N 確認（ADR-0028）・gutter・gap・markdown-lite のゲート

並走確認を `humanPresent` に載せない理由を明記しておく:
ADR-0028 の並走ゲートは**コスト概算を表示して y/N を取る**もので、
端末の組版（表示）とセットで意味を持つ。view ストリームの消費者に
同じ確認を出すなら、それは `note` ではなく型付きの語彙（`{"type":"confirm",...}`）を
足す話になり、ADR-0032 の語彙拡張の議題である。ここで一緒に動かさない。

`finishTask` は現在 `isTTY(os.Stdin)` を関数内部で直接読んでいる。
chat が持つ文脈（自分が view モードかどうか）が届かないので、
**`humanPresent` を引数で渡す**形に変える。`do` は宣言の口を持たないので
`isTTY(os.Stdin)` を渡す — 挙動は不変である（ADR-0032 が
「do にも同時に載せる」を YAGNI で却下した線と整合）。

## Decision 3: 予算は「人が居ないところでは焼かない」を維持する

`askWithIO` のコメントが警戒しているのは
「headless run が誰も邪魔していないのに 24h の予算を焼く」ことである。
この規律は変えない。変わるのは**headless の定義**だけ:

- 素テキスト pipe = headless（従来どおり無音・予算不変）
- view ストリーム = 人が居る（質問する・予算を焼く）

view モードで stdin が即 EOF を返す（消費者が入力を閉じた）場合は、
Feedback が既にそうしているのと同じく無信号として扱う —
`curiosity.Ask` の `answered=false` 経路がそのまま該当する。

---

## Consequences

- GUI で区切ると Tomo が質問し、鏡が出る。どちらも ADR-0032 の
  flush-on-read によって `{"type":"note","await":true}` として届くので、
  GUI 側の配線変更は不要（既に Feedback の質問を同じ形で受けている）
- ADR-0007 / ADR-0015 に本ADRへの改版参照を追記する。
  ADR-0015 の実装追記「反応UIは…TTYのみ」は本ADRが改版する
- `chat.go` の `interactive` のコメント
  （"whether a human is watching (both stdin and stdout are a terminal)"）を
  Decision 2 の2述語に沿って書き直す
- README の chat 説明と tomobit-gui ADR-0001 の境界器官の記述はそのまま真になる
  （偽だった記述が実装側から真に追いつく）
- 検証は実環境で行う: Preference Gap を仕込んだ台帳に対して
  `chat --view ndjson` を pipe 実行し、質問が `await` 付き note として届き、
  回答で `tomo.asked` が記帳されるところまでを E2E で確認する。
  ユニットの `interactive=true` 分岐だけでは、ゲートが実際の view モードで
  開くことを守れない（この所見自体が「配管はあるがゲートが閉じている」型のバグだった）
