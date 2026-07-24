# ADR-0043: 既定を auto へ — 判断の器官は、呼ばれなければ育たない

- Status: **Accepted**（2026-07-24 起草・実台帳の実測が発端。同日、選ばれるProviderが変わることの了承つきで所有者が配備裁定 — ADR-0038 / ADR-0042 と同じ手続き）
- Date: 2026-07-24
- 関連: [ADR-0010](ADR-0010-codex-adapter.md)（Decision 1 の「既定 claude-code」を本ADRが改版 — 当該Decisionが自ら定めた解除条件に到達した）,
  [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md)（決定則。Decision 2 の非対称性が本ADRの計測対象）,
  [ADR-0007](ADR-0007-curiosity-question.md)（好みの証拠を作る器官 その1）,
  [ADR-0026](ADR-0026-ab-duel.md)（好みの証拠を作る器官 その2）,
  [ADR-0018](ADR-0018-experience-sovereignty.md)（Decision 2「humanに特別ルールを作らない」を Decision 4 が改版）,
  [ADR-0037](ADR-0037-merge-reachability.md)（同族: 選ばれない→証拠が来ない→直らない）,
  [ADR-0040](ADR-0040-decision-audit-view.md)（監査行。0件だったことが発見の入口）

---

## Context — 実運用の台帳が示したもの（2026-07-24 計測）

所有者の実台帳（`~/.tomobit/tomobit.db`、2026-07-16〜19 の41セッション）を
read-only で計測した:

| 観測 | 値 |
|---|---|
| 実行経験 | **41行（現行世代27件）すべて `claude-code`**（差分は再知覚で世代交代した旧行） |
| capability connection | 20本、**すべて target=claude-code** |
| preference connection | **0本** |
| `tomo.decided`（判断の監査行） | **0件** |
| `tomo.asked`（好みの質問） | **0件** |
| `tomo.duel_offered`（A/B実走の申し出） | **0件** |
| stage | 4「おとな」 |

**判断の器官は、実運用で一度も呼ばれていない。** ADR-0011/0012/0038/0040/0042 が
積み上げてきた決定則・悲観ゲート・監査行は、既定経路では1回も発火していない。

### 閉路の全体像

原因は単一のフラグ既定ではなく、そこから閉じた輪である:

```
do/chat の --provider 既定 = "claude-code" 固定
  → autoDecide に到達しない（tomo.decided 0件）
  → codex の capability connection が生まれない
  → どの scope も targets が1つ（curiosity.go: len(targets) < 2 は gap にしない）
  → Preference Gap が一度も立たない
  → 質問(ADR-0007)も duel(ADR-0026)も発火しない（実台帳で0件を確認）
  → preference 証拠がゼロのまま
  → 順位付けが無情報のくじになる
```

ADR-0037 が扱った「選ばれない→証拠が来ない→直らない」の同族で、
今回は Connection ではなく **Provider まるごと**が閉め出されている。

### 計測: 既定を auto にしただけでは何が起きるか

閉路の最後の輪を実測した。`decide.Choose` は純関数なので、実台帳の
Connection 形状（20本）をそのまま食わせて 4000 seed 実行した
（プローブは本ADR末尾の再現手順）:

| 候補集合 | claude-code | codex | human |
|---|---|---|---|
| 実台帳形状・size未指定 | 49.1% | 25.1% | 25.8% |
| 実台帳形状・size=medium | 48.4% | 25.9% | 25.7% |
| 実台帳形状・human除外 | 49.0% | **51.0%** | — |
| preference証拠あり(claude~codex=Beta(9,2)) | 73.8% | 0.5% | 25.7% |
| 同・human除外 | **99.2%** | 0.8% | — |

読み取れること:

- **capability の証拠は順位付けに1ビットも効いていない**。41戦の実績を持つ
  claude-code と、一度も走っていない codex が **49 対 51 の五分**になる。
  これは実装バグではなく ADR-0012 Decision 2 の設計どおり（能力＝ゲート、
  好み＝Thompson）。ただし「宣言」と「体感」の距離は明示する価値がある
- **preference の証拠だけが順位を決める**（4行目: 証拠が入ると 0.5% まで落ちる）
- **human は preference のペアが埋まるまで 1/4 を取り続ける**（3行目と5行目の差）

つまり既定を auto にすると、当面は「無情報のくじ」で本番タスクが配られる。
これは Thompson Sampling の探索そのものであり、閉路を破る唯一の入口でもある
（codex が走る → capability が生まれる → gap が立つ → 質問/duel が発火する →
preference が育つ → 選択が鋭くなる）。**システムはもともと auto を通って
ブートストラップする設計で、その auto が一度も呼ばれていなかった。**

### ADR-0010 は自らの解除条件を書いていた

> Phase 1のDecision Engineは「人間がフラグで選ぶ」に縮退する。
> **Connectionが十分育つまで**、自動選択は根拠を持てない —
> 自動選択こそがConnectionの果実であり、前提にしてはならない

この縮退は 2026-07-15、`internal/decide`（ADR-0012）が実装される前の判断である。
今日、Connection は20本・stage は「おとな」に育った。しかし育ったのは
claude-code の側だけで、**その偏りこそが縮退を解除しなかった帰結**である。
「果実を待って前提にしない」は正しかったが、待つだけでは果実は実らない。

---

## Decision 1: `do` / `chat` の `--provider` 既定を `auto` にする

- `cmd/tomobit/main.go`（do）と `cmd/tomobit/chat.go`（chat）の既定文字列を
  `"claude-code"` → `"auto"` に変える
- `--provider claude-code` は今までどおり効く。**主権は奪わない** —
  変わるのは「何も言わなかったとき誰が決めるか」だけである
- ADR-0010 Decision 1 の「既定 claude-code」は本ADRが置換する。
  Decision 2/3/4（起動形・写像・検証の限界）は不変

VISION の *Strategy is Generated*（「すべての決定は、その時の文脈と蓄積された
経験から生成されるべきである」）に対して、固定既定は正面から反する。
Living Harness の中核器官を既定で迂回する構成を、これ以上続けない。

## Decision 2: auto の候補は「起動できる Provider」に限る

`autoDecide` の候補列挙（`providerNames() + human`）を、**実際に起動できる
アダプタ**に絞る。`providers` マップの登録は静的なまま変えない
（`--provider codex` は「PATH に codex が無い」と正直に落ちるべきで、
「そんな Provider は無い」ではない）。

- 未インストールの Provider を auto が引くと、`cmd.Start()` が
  「executable file not found」で失敗する。現状これは `provider.error` として
  記帳され、知覚を経て `outcome.Failed` → y=0 の execution 経験になる
- **環境の不備を、Provider の能力の証拠にしてはならない。** 走らせられなかった
  ことと、走らせて失敗したことは別の事実である。混ぜると、codex を入れていない
  マシンで codex の capability が不当に沈み、後で入れたときに掘り返せない
  （減衰でしか戻らない — ADR-0012 Decision 3）

## Decision 3: 起動できなかった実行は、握り潰さず、経験にもしない

`cmd.Start()` が失敗した実行（`Result.Started == false` かつエラー）は:

- **エラーとして終了する**（現状は `provider.error` を記帳したまま `cmdDo` が
  `nil` を返し、ユーザーには「何も起きずに正常終了した」ように見える）
- capability の証拠として記帳しない（Decision 2 と同じ理由）

Decision 2 が入れば auto 経由でこの経路に落ちることは無くなるが、
`--provider codex` の明示指定では今日も到達できる**現存の欠陥**である。
既定変更とは独立に直す。

## Decision 4: human は「知っているとき」だけ auto の候補になる

auto の候補に `human` を含めるのは、**そのタスクの文脈に human の
capability connection が既にあるとき**に限る。空白（無知）のときは含めない。

- ADR-0018 Decision 2「humanに特別ルールを作らない（Rule禁止）」を、
  本Decisionが**改版する**。台帳・減衰・悲観ゲート・名誉回復は一切変えない。
  変えるのは「無知の状態で本番タスクを人に投げ返すか」だけである
- 根拠は human の**構造的な非対称性**であって、人間だからではない:
  - 探索の対価が「Tomo が1回走る」ではなく **「使用者が働く」**。VISION の
    *Curiosity with Responsibility*（好奇心が本番作業を妨げてはならない）に
    照らして、無知からのくじで作業を突き返すのは探索ではなく放棄である
  - human はブートストラップに探索を必要としない。明示指定 `--provider human`
    で走れば証拠は普通に入る。codex は auto に選ばれない限り証拠が入らない
    （＝ Decision 1 が必要だった理由）が、human にその閉路は無い
  - 前例がある: ADR-0026 の duel は `runnableProvider` で human を実験の側から
    既に外している（走らせるストリームが無いため）
- ADR-0018 Decision 2 が約束した「これはあなたがやった方が早い」という
  ルーティングは**残る**。証拠が貯まれば human は候補に戻り、勝てば選ばれる。
  失うのは「知らないから投げ返す」だけである
- 計測（Context の表）: これで実台帳形状の human 25.8% が 0% になり、
  codex への探索が 25.1% → 51.0% に増える。閉路を破る速度も上がる
- 「知っている」の判定は `decide.KnowsCapability` — **判断そのものが読む
  最細一致の行が存在するか**だけを見て、その行の Evidence の量は見ない。
  これは意図的な選択（decide が読む行と同一の問いで一貫させる）だが、
  90日減衰で実質 Beta(1,1) 近傍まで薄れた行でも「知っている」となるため、
  本Decisionの趣旨（無知のくじで人に投げ返さない）とは漸近的なズレが残る
  （行は forget しない限り消えない）

## Decision 5: GUI は Provider を明示的に持ち、既定は auto

GUI は現在 `chat --view ndjson` を `--provider` 無指定で起動しており、
本体の既定にそのまま乗っている。Decision 1 で GUI の挙動が無言で変わるのは
不正直なので、GUI 側に選択を持たせる。

- `gui.json` に Provider 設定を追加。**未設定＝auto**（`FaceEnabled` /
  `TranscriptCache` と同じ tri-state の流儀に合わせる）
- 設定画面に選択UIを置く（claude-code / codex / human / auto）
- GUI ADR-0002 の「既定は claude-code」を改版する
- `chat_e2e_test.go` は `--provider claude-code` を明示に切り替える。
  あのテストの意図は「本体と繋がるか」であって「誰が選ばれるか」ではない。
  auto のまま走らせると候補集合が実行マシンの PATH に依存する
  （claude/codex の導入状況でくじの母集団が変わり、両方あれば無情報のくじで
  どちらが走るかも非決定になる）— E2E の再現性が環境ごとに壊れる。
  なお空台帳で human を引くことはない（Decision 4）

---

## 却下した対案

- **ラウンドロビン等で codex に強制的に経験を積ませる** → ADR-0010 Decision 1 が
  既に却下している（「証拠を早く貯めるためだけの機構は、ユーザーの仕事の
  道具選びを歪める」）。Thompson Sampling の探索は同じ仕事を分布としてやる
- **capability の事後平均を順位付けにも混ぜる** → ADR-0012 Decision 2 の
  非対称性（能力＝決定的ゲート、好み＝サンプリング）を放棄することになる。
  「41戦の実績が五分に見える」体感は本ADRが計測で可視化したが、これを直すなら
  決定則そのものの改版であり、本ADRのスコープではない。**別ADRの論点として残す**
- **既定は据え置き、`--provider auto` を README で薦める** → 実測が示したのは
  「既定でない経路は実運用で選ばれない」という事実そのものである。41セッション
  ぶんの証拠がそう言っている
- **auto の探索率にノブを付ける** → 探索率は事後分布から自動で決まるのが
  ADR-0012 Decision 1 の要点（「追加のノブほぼゼロ」）。ノブを付けた瞬間、
  減衰と探索の自己調整が二重管理になる

---

## 残す露出（明文化）

- **`--size` 既定 `""`（n=1）は本ADRのスコープ外**。size 付き Connection に
  本番経路が一生届かない問題（ADR-0017 追記が観測済み）は残る。size の
  自動推定は Task Perception（ADR-0036）の管轄で、別ADRの論点
- **移行期は選択が無情報である**。preference が育つまで claude/codex は
  ほぼ五分で配られる。これは仕様（Thompson の探索）だが、体感としては
  「急に codex を使い出した」に見える。ADR-0040 の監査行と、Provider別
  利用ビューが、その理由を開示する側の担保になる

## Consequences

- 選ばれる Provider が変わる。ADR-0038 / ADR-0042 と同じく**所有者の明示的な
  了承のもとで入れる**変更である
- ADR-0010 Decision 1 / ADR-0018 Decision 2 / GUI ADR-0002 に改版注記
- README の Getting Started に「既定は auto。Tomo が経験から選ぶ」を明記し、
  codex 未導入でも動くこと（Decision 2）を添える
- テスト: 既定値そのものを読むテスト（`flag.Lookup("provider").DefValue`）、
  Decision 2 の候補フィルタ、Decision 3 の非ゼロ終了と非記帳、Decision 4 の
  候補集合（空白 human を含まない / 証拠ある human を含む）

## 再現手順

Context の分布表は `decide.Choose` を実台帳の Connection 形状で 4000 seed
回して得た。台帳そのものは読み書きせず、`connections` を一度 TSV に
書き出して純関数へ流している（実台帳への副作用ゼロ）。プローブは
`tools/dogfood/replay/autoprobe`（本ADR採択時にハーネスへ収める）。
