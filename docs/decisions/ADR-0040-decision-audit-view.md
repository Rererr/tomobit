# ADR-0040: 判断の監査行をviewへ流す — 「なぜ君にしたか」は記帳済みである

- Status: **Proposed**（2026-07-24 起草。所有者の採否待ち）
- Date: 2026-07-24
- 関連: [ADR-0011](ADR-0011-meaning-by-model-judgment-by-math.md)（根拠3: 監査可能性 — 「なぜCodexを選んだか」に計算で答えられる）,
  [ADR-0012](ADR-0012-decision-rule-thompson-sampling.md)（決定則）,
  [ADR-0015](ADR-0015-reflection.md)（語る器官はReflectionだけ・語りは数式が選ぶ）,
  [ADR-0032](ADR-0032-pipe-chat-first-class.md)（Decision 1: viewストリームの前方互換契約）,
  [ADR-0039](ADR-0039-status-machine-view.md)（機械viewは表示ではなくレンダラへのデータ供給）

---

## Context

VISION は「Connectionの質の向上」が成長だと言う。しかしユーザーが今日それを
体感する経路は、1日1件のReflectionの語りと、statusテーブルのスナップショット
だけである。**判断そのもの**——「今回なぜこの Provider だったか」——は:

- `decide.Decision` が理由を**完全に**持っている
  （Candidates: 各候補の ScopeKey / Quantile / Passed / Wins、Seed、Fallback）
- その全量が `tomo.decided` イベントとして**台帳に記帳済み**である
- しかしユーザー表示経路には一切出ない。端末の声は wobble の二値
  （「自信ないけど」/「これは%sだね」）、ndjson の `provider` イベントは
  name/model のみ

ADR-0011 根拠3 が約束した監査可能性は、台帳を SQL で掘れる者にしか届いていない。
相棒が「なぜ君にしたの」と聞かれて答えられるのに黙っているのは、
Companionship の取りこぼしである。

一方で ADR-0015 は「語る器官は Reflection だけ・何を語るかは数式が決める」と
定めた。**声を饒舌にする方向は塞がれている**。塞いだ理由も正しい —
数表を読み上げる相棒は相棒ではない。

## Decision

### Decision 1: `chat --view ndjson` に `decided` イベントを追加する

タスクの Provider 判断（autoDecide）が下った直後、`tomo.decided` に記帳する
のと**同じ内容**を view にも流す:

```json
{"type":"decided","sid":"0019f8ff...","provider":"claude-code","n":3,"q":0.72,
 "fallback":false,"seed":"1721793600123",
 "candidates":[
   {"provider":"claude-code","scope":"cap=implement","quantile":0.72,"passed":true,"wins":41},
   {"provider":"codex","scope":"cap=implement","quantile":0.31,"passed":true,"wins":23}]}
```

- 内容の正本は `decide.Decision` そのもの。**viewのために新しい計算はしない** —
  記帳と同じ構造体を同じ場所で書くだけ（付加コストゼロ）。台帳の `tomo.decided`
  ペイロードにも `wins` を持たせ、view と台帳は同じキー名（`scope`）・同じ値で一致する
- `seed` は台帳と同じく文字列（int64 を JSON number にすると精度が落ちる）
- `wins:-1` はゲート未通過（トーナメント不参加）の意味
- `sid` は記帳先 ledger session の id。`decided` は view の `task.started` より
  先に流れうるため、読み手は順序でなく sid で相関する
- split（ADR-0023/0028）の下では各サブタスクが自分の sid 付きで独立に `decided` を流す
- 声（`voice.Decided` の note）は今のまま変えない。人格は語り、データは流れる —
  ADR-0030 の「表示専用受け取り」と同じ二層
- 未知 type を読み手が無視する前方互換契約（ADR-0032 Decision 1）により、
  旧GUIはこのイベントを黙って捨てる。破壊は無い

### Decision 2: 見せ方はレンダラの裁量。ただし既定は畳む

GUI はこのイベントで「なぜ」を**開示可能**にする（例: providerチップの展開で
候補ごとの分位点・ゲート通過・勝ち数を出す）。既定表示は今のチップのまま —
数表を常時見せるのは ADR-0015 が声に禁じたことを画面でやるだけである。
開いた者にだけ、記帳されている真実が見える。

## 却下した対案

- **`voice.Decided` に数値を語らせる** → ADR-0015 Decision 4 に反する。
  声の人格と監査行は別物であり、混ぜるとどちらも読めなくなる
- **GUI が台帳の `tomo.decided` を直接読む**（SQLite read-only view）→
  できるが、chatの進行と台帳ポーリングの突き合わせという職人芸をGUIに積む。
  判断が下った瞬間にストリームで届くのが chat の器官配置として素直
- **`do` のコンソール出力にも表を出す** → 端末は声の場（ADR-0025 が
  端末表示を削った方向に逆行）。端末で監査したい者には台帳と
  `tomo.decided` が既にある
- **専用サブコマンド `tomobit why`** → 器官を増やす。最後の判断の再表示は
  台帳クエリで足り、生きた文脈（今この判断）は view が届ける

## Consequences

- 本体: autoDecide の記帳箇所で view writer にも同構造を書く。
  テストは (1) ndjson で decided が1タスク1回流れ、candidates が
  `tomo.decided` 記帳と一致 (2) human 表示（note の声）が不変 — を固定
- GUI: ChatPane の provider チップに展開UIを追加（別コミット。
  イベント無視でも既存機能は不変なので、本体が先行してよい）
- README の ADR 索引に追加
