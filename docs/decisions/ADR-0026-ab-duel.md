# ADR-0026: A/B実走 — 好奇心が「問い」から「比較実験」へ

- Status: **Accepted**（2026-07-17、本人決定。5分岐承認、うち起動方式のみ変更:
  「コマンドを打たせるのは論外。Tomoが『試していいか?』と申し出てY/nで答える」）
- Date: 2026-07-17
- 関連: [ADR-0007](ADR-0007-curiosity-question.md)（好奇心の問い — 本ADRが「問い」に
  「実験」を並置する）, [ADR-0016](ADR-0016-curiosity-priority-voi.md)（VoI — どのペアを
  実走するかの優先順位）, [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md)
  （決定エンジン — 実走の判定が育てる preference 台帳）, [ADR-0003](ADR-0003-outcome-and-preference.md)
  （回答＝preference経験）, [ADR-0023](ADR-0023-task-split.md)（Decision 4「並列化は将来論点」
  を、速度目的でなく好奇心目的で本ADRが解禁）, [ADR-0020](ADR-0020-face-window.md)
  （顔窓は表示専用の第二レンダラ — 本ADRの可視化はその制約を崩さない）

---

## Context

VISION の Curiosity 節（`docs/core/VISION.md`）は好奇心をこう定義している:

> When resources permit, Tomobit quietly explores.
> **It compares. It experiments. It validates assumptions.**

しかし現状の実装は「比較」も「実験」もしていない。好奇心が Preference Gap
（2つのProviderが capability で見分けられず・頻出し・preference 未知）を見つけたとき、
Tomo にできるのは **ユーザーに問うこと**（`curiosity.Ask` → `user.preference` 経験）
だけである。両Providerを実際に走らせて成果を突き合わせる経路は存在しない。

つまり「どっちが好み?」を**想像で聞く**ことはできるが、**実際に並べて確かめる**ことが
できない。VISION が明言する compare / experiment が実装に無い。これが本ADRの埋める穴。

並列実行そのものは [ADR-0023](ADR-0023-task-split.md) Decision 4 が「並列化は将来論点」
として保留した。その将来論点を、**速度・スループット目的ではなく好奇心の比較実験として**
解禁する。速度目的の並列化は VISION（`Tomobit is not an orchestrator.`）が否定する一線で
あり、本ADRはそれを越えない。

前提の計測（実装可能性）:

- **provider.output は既にDBに記録されている**（`cmd/tomobit/main.go` providerSink →
  `store.AppendEvent`）。表示専用の顔窓は真実を侵さずポーリングで読める。
- **store は `SetMaxOpenConns(1)`（`internal/store/store.go`）** かつ seq は session 単位で
  atomic に採番される。別 session_id への2 goroutine 並行 append は追加 mutex なしで
  正しく直列化される — A/B の並走を素直に組める。

---

## Decision 1: A/B実走は「好奇心の比較実験」であって、オーケストレーションではない

- 動機は速度でもスループットでもない。**同一タスクを2つのProviderで並走させ、両成果を
  ユーザーが判定し、その判定を preference 経験として台帳に残す** — VISION の
  「It compares. It experiments.」を実装で満たすこと。ここが唯一の目的。
- したがって A/B実走は capability を伸ばす手段ではなく **preference を接地させる手段**。
  片方が速い/安いは選定理由にしない（それは orchestrator の関心）。
- 「Curiosity with Responsibility / Work comes first」に従属する: 本番作業を止めない・
  opt-in・予算/資源の許す範囲でのみ。好奇心が生産を阻害してはならない。

**Why not**: 「速いから並列で回す」設計は VISION が名指しで否定する orchestrator 化。
逐次split（ADR-0023）が経験の因果連鎖を守るために逐次を選んだのと同様、本ADRの並走は
**因果の無い独立実験**（同一プロンプトを独立に2回試す）に限る。後段依存のある分割は
逐次のまま（ADR-0023）で、本ADRは触らない。

---

## Decision 2: 起動は Tomo の申し出 → ユーザーの Y/n。コマンドは打たせない

- **フラグは無い**。`do --duel` のようにユーザーにコマンドを打たせる設計は却下（本人:
  「論外」）。デュエルは Tomo が自発的に**申し出て**、ユーザーが Y/n で答える。これは
  好奇心の作法そのもの（ADR-0007: 予算内で Tomo が問う）で、A/B実走を「Tomo が試したいと
  言い、人が許す」実験に位置づける。
- 引き金: 通常の `do` の実行**直前**、Tomo はタスク scope を覆う **open Preference Gap**
  （`curiosity.Gaps` の VoI 先頭）が予算内で在るかを見る。在れば申し出る:
  「この scope、A と B のどちらが good かまだ分からない。両方に同じことをやらせて確かめて
  いい?（2本分のコストがかかる）[Y/n]」。
  - **Y** → その Gap のペア (A, B) でデュエル実走。
  - **n**（既定・Enter/EOF 含む）→ 通常どおり `decide.Choose` で1本を選んで実行。
- 比べる対 (A, B) は **Gap が決める**（VoI 先頭ペア）。ユーザーは対を指定しない — 何を
  確かめたいかは台帳（VoI）が知っている。Gap が無ければ**申し出ない**（確かめたい未知が
  無いのに2本走らせない）。
- 申し出は **好奇心予算に従う**（ADR-0007 Decision 3 と同じ窓 `BudgetWindowMs`）。予算を
  使い切っていれば申し出ず通常実行。n も予算を消費する（頻繁な申し出で作業を妨げない）。
- 両Providerは capability ゲートを両方通っていること（Gap の成立条件が既にこれを保証する
  — ADR-0007 Decision 2 の even gate は capability の見分けがつかないペアだけを Gap にする）。

**Why not**: フラグ方式は Ren の軸「ユーザーにコマンドを覚えさせない・低負荷」に反し、
何より「相棒が自分から試したいと言う」という擬人化・companion 性を殺す。Y/n の同意ゲートは
「Curiosity with Responsibility（好奇心は特権であって強制ではない）」を UI で体現する。

---

## Decision 3: 判定はユーザーの一次審判、結果は `user.preference` 経験

- 両子タスクの成果を提示し、`curiosity.Ask` と同型の一問で判定を採る:
  `[1=A / 2=B / Enter=引き分け]`。
  - `1`/`2` → `user.preference{preferred, over}` 経験を作り `Engine.Apply` で preference
    connection を更新。**`curiosity.Ask` の記帳経路（`RecordAndPerceive` 系）を、想像の
    問いではなく実成果に接地させて再利用する**。
  - `Enter`（引き分け/スキップ）→ preference は記録しない（差が無いという判定は、まだ
    preference 経験にしない — ADR-0007 の EMax と整合）。
- **判定の scope は実行された scope**（Gap 由来ならその scope、明示ペアなら perceive が
  子セッションから導く context）。curiosity.Ask が Gap.Scope を継ぐのと同じ流儀。
- 片方が成果を出せなかった場合（provider.error / exit≠0 / timeout）: **preference は
  記録しない**（公平に比べられない）。ただし両子は通常どおり execution 経験になり、
  失敗した側の capability connection は正直に負の outcome を受ける（ADR-0018 経験主権）。

**Why not**: 自動判定（tests passed / exit code で勝敗）は MVP から除外。「テスト通過 ≠
好み」であり、機械が preference を捏造するのは正直さの軸に反する（VISION: experiment ≠
自動採点）。将来 outcome シグナルを**補助表示**（どちらがテストを通したか）として judge に
添えることはあり得るが、決定権はユーザー。

---

## Decision 4: 並行実行モデル — 兄弟セッション、単一接続が直列化する

- 親 `duel` セッションを開き `task.duel`（payload: `pair=[A,B]`, `scope`）を記帳。その下に
  **2つの兄弟タスクセッション**（各自 session_id・`task.started`/`capability.started`/
  `provider.selected` は openSubtask と同型、ただし parent は duel 親）。split の親子構造に
  倣うが、**兄弟は逐次でなく並走**する点だけが違う。
- 2つの `executor.Executor` を goroutine で並走。各自の `providerSink` が**自セッションへ**
  provider.output を書く。store は `MaxOpenConns(1)` で単一接続、seq は session 単位採番
  なので、2 goroutine の並行 AppendEvent は自動的に直列化され破綻しない（**追加 mutex 不要**。
  計測: store.go の SetMaxOpenConns(1) と AppendEvent の per-session seq）。
- SIGINT（`ctx` cancel）は両子へ配布。**片方が失敗してももう片方は走り切る** — 逐次split の
  「失敗で残りを止める」（ADR-0023 Decision 4）とは逆。ここは実験であり、両者の結末こそが
  比べたい経験だから、片方の失敗で他方を打ち切らない。timeout は各自に適用。
- 端末出力: 2本の provider ストリームが1つの端末を共有する。既存 `turnView` の spinner は
  1本前提なので、デュエル時は各行に Provider 名を前置する簡易ラベル表示に切り替える
  （実況の主役は顔窓側。端末は台帳の担保）。

---

## Decision 5: 顔窓の可視化は A/B実走の帰結 —「考える」吹き出し（⚪︎つなぎ）

- 顔窓は events tail をポーリングする既存の第二レンダラ（ADR-0020）。**表示専用・真実は
  DB という制約を崩さない**。「今 thinking なセッション」を tail から導出する:
  `task.started` 済みかつ `task.finished`/`task.cancelled` 未了で、直近に provider.output が
  流れている session を active とみなす。
- active な各セッションに **「考える」吹き出しを1つずつ**描画する。喋る吹き出し（現行の
  stepped-slab tail = `window.go` drawBubble）とは別資産で、tail は **⚪︎つなぎ**（Tomo から
  雲へと小円が繋がって昇る）。中身は最新 provider.output の断片、Provider 名で識別。
- これは「N個の独立エージェントの作業モニタ」ではなく **「Tomo が複数の考えを同時に
  巡らせている」単一人格の内面表現**。この写像を守る限り VISION（not an orchestrator）と
  衝突しない。踏み外して各Providerを名前付きダッシュボードで並べた瞬間に逸脱する。
- レイアウト: 現行は吹き出し1個・窓サイズ固定（`spriteSize*scale*2`）。2つの思考吹き出しを
  Tomo 上部の headroom に左右配置する。1本（通常 do）や 0本（idle）でも破綻しないよう、
  active 数 0/1/2 で連続にスケールする描画にする。

**Why not**: provider.output をそのまま「喋る」吹き出しに流すのは ADR-0020 Decision 2
（回答チャネルは端末）に反する。デュエルの可視化は**喋りではなく「考え中」の記号**に
留める — 生の回答は端末、思考の気配だけが顔窓。この線引きが顔窓の相棒性を保つ。

**追記（2026-07-17）: 「直近に provider.output」の判定を厳密化**。active の判定は
「task 未完了 かつ 出力あり」ではなく **turn 進行中**（session の turn-lifecycle 最終イベントが
`provider.output` であること。後続の `provider.finished`/`provider.error` が来た時点で消す）。
chat セッション（ADR-0022）は `/exit` されるまで task.started のままなので、緩い判定だと
ターン完了後やアイドル中、さらに端末を閉じ忘れた孤児セッションの最終回答が**幻の思考として
何時間も残る**（実測: 10.5時間前の未クローズ chat が「両方完了しました」を考え続けた）。
残課題: **ストリーム途中でのハードキル**（最終イベントが provider.output のまま孤児化）は
この判定でも残る。恒久対策は起動時の孤児 reap か直近性ガードだが、時間ノブを避けるため
現時点では未実装（計測された症状＝ターン完了後の残留は本追記で解消）。

---

## 検証（手動E2E）の再現

デュエルは実 Provider 2つの並走＋顔窓 GUI 目視が要り、自動テストに置換できない。
毎回この状態を作る使い捨て seed スクリプトは**コミットしない**（Go の testdata は
入力データ用でスクリプトの置き場ではなく、cmd/ に検証専用バイナリを常設するのは
コンセプト純度に反する）。資産はコードでなく**seed 条件**なのでここに残す。

申し出（Decision 1）の唯一のトリガーは「open な Preference Gap」なので、それを
最小構成で作る:

- 同一 scope（例 `cap=implement`）に 2 Provider（`claude-code` と `codex`）を
  **同型の実行実績**で育てる。実測で gap が出た配合は **各 4 採用 / 1 破棄**
  （`Outcome{Adopted:"as-is"}` ×4、`Outcome{Reverted:true}` ×1、`Source:"production"`）。
- 両者が同記録なら capability は拮抗・頻度は互角・preference 未確定＝open gap。
  seed 後は `curiosity.Gaps()` に `cap=implement` の `claude-code` vs `codex` が
  現れることを**確認してから**（推測でなく計測）実走する。育成ロジックは
  `internal/curiosity` のテストヘルパーと同型。
- 実走: 同じ `TOMOBIT_DB` を指すこと。`do --provider auto "<副作用のない指示>"`
  → 申し出に `y` → 端末で両出力を見て採点（引き分けは Enter）→ preference が
  台帳へ1件入る。`ExtractorVer` は cmd/tomobit と一致させる。
- 後片付け: seed DB（`~/.tomobit/duel-smoke.db*`）は本番台帳と同居しうるので
  名指しで消す（ワイルドカードで薙がない）。

---

## Consequences

- **新イベント型**: `task.duel`（親セッション, payload: pair, scope）。子セッションは既存の
  provider.selected / provider.output / provider.finished / task.finished をそのまま使う。
- **perceive**: 各子セッションは通常の execution 経験として既存 `perceive` がそのまま処理
  （変更不要）。preference 経験は Decision 3 の判定からのみ生まれる。
- **decide ループが閉じる**: 実走の判定で preference connection が育ち、次回 `decide.Choose`
  の Thompson lottery がその経験を読む。「問うだけ」だった好奇心が「試して覚える」に
  なる。
- **申し出の抑制**: デュエルの申し出は通常の `do` でのみ現れる。`--provider X`（対を人が
  固定した）・`--provider human`（provider ストリームが無い）・`--split`（実行構造を別に
  組み替える）のときは申し出ない。新しいフラグは足さない（Decision 2）。
- **コスト**: 2Provider同時実行 = API コスト約2倍・並行負荷。opt-in（Decision 2）と将来の
  予算ゲートで抑制。正直に「デュエルは2本分のコストがかかる」と起動時に明示する。
- **顔窓**: 吹き出し複数対応・思考 tail（⚪︎つなぎ）新規描画・窓レイアウト可変化。
  `docs/design/SPRITES-WINDOW.md` に思考吹き出し資産を追記して整合させる。
- **索引**: `README.md` の ADR 索引に本 ADR を追加する。

---

## 実装フェーズ（Proposed）

1. **申し出 + A/B実走エンジン**（`cmd/tomobit` + curiosity + executor 並走）: `do` 直前に
   open Gap を見て「試していい? [Y/n]」を申し出（Decision 2）、Y で `task.duel` 記帳・兄弟
   セッション×2の goroutine 並走・SIGINT 配布・両者走り切り。まず端末で「申し出→Y→2本並走
   して両成果が出る」を実機確認。
2. **判定と経験化**（Decision 3）: 両成果提示 → 一問審判 → `user.preference` 経験 → Apply。
   preference connection が実走で育つことを台帳で確認。
3. **顔窓可視化**（Decision 5）: active セッション検出 → 思考吹き出し（⚪︎つなぎ）複数描画 →
   レイアウト可変化。実機（faceウィンドウ）で2つの思考が同時に浮かぶことを目視確認。

各フェーズ末で実環境検証（実 Provider・実 face 窓）を完了条件に含める。
