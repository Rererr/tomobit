# ADR-0006: Executor統合 — `tomobit do` と最初のAdapter

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0004](ADR-0004-tech-stack.md)（Decision 4 段階論）, [EXECUTION_MODEL.md](../core/EXECUTION_MODEL.md), [SCHEMA.md](../design/SCHEMA.md)（R3/R4）, [ADR-0003](ADR-0003-outcome-and-preference.md)（第1層Outcome）

<!-- 改版:begin — tools/sync-adr-superseded.sh が生成する。手で編集しない -->
> **改版済み** — この決定の一部は後のADRが置き換えた。範囲は各Decisionの改版注記が持つ。
>
> - Decision 3 → [ADR-0052](ADR-0052-first-layer-is-observed.md)
<!-- 改版:end -->

---

## Context

最小コア（record→perceive→connect→rebuild）は動くが、eventsを生む入り口が
手入力の `record` しかない。学習ループは呼吸できるのに、Realityを吸い込む
器官がない。

ADR-0004 Decision 4のPhase 1形状——
「`tomobit do` → 実行 → 記帳 → 区切りの質問 → 終了」——の実装に必要な
確定事項は3つ: **最初のProvider / Adapterの責務境界 / stream→eventsの写像**。

---

## Decision 1: 最初のProvider = claude-code

検討: claude-code / codex / gemini

- 決定打: **dogfoodの量**。使用者の実タスクの大半はClaude Codeで行われる。
  実セッションが流れないProviderから始めても、学習ループは検証できない
- headless（`claude -p --output-format stream-json`）が構造化ストリームを
  吐く（実機でフラグ確認済み）
- Adapter登録名 `claude-code` はSCHEMA.md R3で予約済み

Provider 1つでもcapability Connectionは育つ。
preference Connection（ペア）は2つ目のAdapter（codex想定）を待つ。

却下した対案: **観測者方式**（hooksやセッションログの取込。実行を包まず
観測だけする）。摩擦は最小だが、Tomobitは「CLIプロセス群を操る器官群」
（ADR-0004）であり、将来Decision Engineが**選ぶ主体**になる経路と地続きに
ならない。観測だけでは実行と学習の接続（EXECUTION_MODEL）が閉じない。

---

## Decision 2: Adapter境界 — 「起動と翻訳」だけ

```text
Adapterが知ること       CLIの起動方法（コマンド・フラグ）
                        ストリーム形式 → 正準イベントへの翻訳
Adapterが知らないこと   DB / Connection / Outcome解釈 / 質問 / seq・tsの採番
共通Executorが担うこと  子プロセス管理（起動・タイムアウト・シグナル）
                        events記帳（seq採番・ts）・exit codeの観測
```

翻訳（stream 1行 → 0..n個の正準イベント）は**純関数**にする。
録画したstream-jsonフィクスチャで写像をテストでき、実CLIなしで
Adapterの回帰が検出できる。

---

## Decision 3: 写像 — R4カタログとの対応

`tomobit do [--cap <capability>] "<prompt>"` が生むイベント:

```text
task.started        {intent: <prompt>, source: "production"}
capability.started  {capability: <--cap>}  既定 "implement"
provider.selected   {provider: "claude-code", model: <initメッセージ>}
provider.output     {text}   assistantテキスト全文。tool呼出は {tool} 名のみ
provider.finished   {exit_code, duration_ms, cost_usd, num_turns,
                     provider_session_id}
provider.error      {message}   異常終了時
task.finished       {adopted, reverted}   採用プロンプトから（Decision 4）
task.cancelled      {}   Ctrl-C
```

Phase 1のdoが**生まないもの**（明記）:

```text
plan.generated   Planレイヤーは縮退（単一capabilityタスクのみ）
test.result      子プロセス内のテスト実行を外から決定的に識別できない
user.verdict     第2層は将来の verdict コマンド（ADR-0003）
tomo.asked       Curiosityの質問は別ADR（→ ADR-0007）
```

`--cap` の既定 `implement` はトレードオフ: 毎回の明示は摩擦がdogfoodを殺し、
黙った既定はreviewタスクをimplementの島に混ぜる。単独使用者の規律
（implement以外は `-c` を付ける）で後者を抑える方を取る。

### 全文は記帳しない — ダイジェスト＋原本参照

- eventsに積むのは**知覚に十分なダイジェスト**: assistantテキスト全文
  （lang/framework/topic/size抽出の材料）・tool名・決定的メタデータ
- tool_result（ファイル内容・コマンド出力）は捨てる。かわりに
  `provider_session_id` で原本（Claude Code自身のセッションログ）への参照を残す
- トレードオフ: 原本の寿命はClaude Code側の保持ポリシー次第。参照切れ後の
  再知覚の上限はダイジェストまで。dogfood規模で数MB/セッションをSQLiteに
  複製する価値はないと判断し、これを受け入れる

---

## Decision 4: 区切りの1問 = 採用確認（第1層Outcomeの観測）

doの終了時、1行だけ聞く:

```text
採用? [Enter=そのまま / e=手直しあり / r=破棄 / s=わからない]
```

- Enter → `adopted="as-is"`（y=1.0）/ e → `"with-edits"`（0.7）/
  r → `reverted=true`（強い失敗）/ s → シグナルなし（学習に使わない）
- ここで聞かないと第1層が永遠に空になる。自動収穫できるのはexit codeだけで、
  **exit 0は「成果物が採用された」を意味しない**
- これはOutcome観測であってCuriosityの質問ではない。Tomoの1日1問は
  質問予算ごと別ADRで扱う（→ [ADR-0007](ADR-0007-curiosity-question.md)）

### 追記（2026-07-17）: 「採用確認」→「結果の質評価」へ言い換え

チャット化（ADR-0024）以降、doは完了までユーザーと対話し、納得いくまで
ループを回してから終わる。実装完了時点では**採用はすでに済んでいる**ため、
「採用したか（保持の行為）」を問うのは形骸化していた。台帳が第1層Outcomeとして
本当に欲しいのは「結果／セッションがどれだけ良かったか（質）」なので、設問の
ニュアンスをそちらへ寄せる。観測する信号（`core.Outcome.y`）は不変で、文言と
キー割当のみ変更:

```text
今回、どうだった? [1=文句なし / 2=まあまあ（手を焼いた） / 3=だめだった / Enter=まだ言えない]
```

- 1 → `adopted="as-is"`（y=1.0）/ 2 → `"with-edits"`（0.7）/
  3 → `reverted=true`（y=0）/ Enter・その他・EOF → シグナルなし
- **Enterの意味を y=1.0 から「シグナルなし」へ変更した**。トレードオフ:
  肯定のワンキー性を失う代わりに、(1) 惰性のEnterで甘い評価が台帳へ膨らむのを
  防ぎ、(2) 好奇心（ADR-0007）・鏡（ADR-0015）の問い（どちらもEnter=スキップ）と
  Enterの意味が揃う。代償として正例サンプルが減り、学習はやや保守的に振れる

---

## Decision 5: doの最後にbest-effort知覚

記帳後にその場でperceive（＋live apply）を試みる。Ollamaが落ちていれば
「pending — 後で `tomobit perceive`」と告げて**正常終了**する。
doの成否は知覚の成否に依存しない（Deferred Perceptionをそのまま使う）。

「実行の終わりは、学習の始まりである」をコマンドの動線として体現する。

---

## Consequences

- Realityを自動で吸い込む入り口ができ、dogfood開始の前提が揃う
- Phase 1のdoは非対話（headless）。途中で人間の判断が要るタスクは向かず、
  最初のdogfood対象は**委任できる一発タスク**に限られる。
  対話パススルーはPhase 2以降の論点
  → [ADR-0022](ADR-0022-chat-session.md) で回収（`tomobit chat`）。
  この行が予告した摩擦は実使用で現れた。doは非対話の経路として残る
- 2つ目のAdapter（codex）が入って初めてpreferenceが実データで育つ
- doは新しいイベントを生むだけで抽出プロンプト/schemaは不変 →
  extractor_verのバンプは不要
- 実装時ノブ: 権限フラグの透過（headless実行の許可モード）、タイムアウト、
  採用プロンプトのUX、ダイジェストに含めるassistantテキストの上限

---

## 実装追記（2026-07-16）: 起動プロファイルもAdapterの責務

`Adapter.Command` は `(name, args, extraEnv)` を返す形に拡張した。起動環境も
「CLIを知っている」ことの一部であり、プロファイル選択（どのアカウント・設定で
走るか）をtomobitの外側のshell alias/direnvに漏らさないため。

- claude-code: `Adapter.ConfigDir` → 子プロセスの `CLAUDE_CONFIG_DIR`、
  `Adapter.ExtraArgs` → 毎回の起動に追記するフラグ。配線は `cmd/tomobit` が持ち、
  **env必須方式**（本人指示でハードコード既定を撤回）: `TOMOBIT_CLAUDE_CONFIG_DIR`
  未設定なら claude-code/auto の `do` は記帳前に明示エラーで拒否する。
  shellがたまたま持っているプロファイルを黙って継承する事故こそ防ぎたいものなので、
  継承したい場合も「空文字を明示的に設定」させる。`TOMOBIT_CLAUDE_ARGS` は任意
  （未設定=フラグなし）。その後 [ADR-0021](ADR-0021-onboarding.md) で解決は
  env > config（`~/.tomobit/config.json`、`tomobit setup`が書く）に拡張。
  「どこにも選択がなければ拒否」は不変、端末上では欠けた選択がその場の質問になる
- Provider名は道具名のみ（SCHEMA.md R3）のまま: プロファイルは「誰でログインして
  いるか」を変えるだけで、どの道具が走ったかは変えない
- codex: extraEnv=nil（変更なし）

---

## 追記（2026-07-26）: ADR-0052 — `test.result` が Decision 3 の「生まないもの」から外れる

Decision 3 は `test.result` を理由つきで先送りした:

> `test.result` — **子プロセス内のテスト実行を外から決定的に識別できない**

**この理由は今も正しい。** 他人のストリームを覗いて「今のはテストだった」と決定的に
見分けることはできない。[ADR-0052](ADR-0052-first-layer-is-observed.md) が変えたのは
理由の妥当性ではなく、**識別が要らない経路を作った**ことである — tomobit 自身が
タスク境界でコマンドを1本走らせれば、返ってくる終了コードは識別ではなく観測になる。

Decision 3 の禁止リストから `test.result` が外れ、`do`・`chat`・分割の親の境界で
生まれるようになった（分割の子は依然として生まない — ADR-0052 Decision 3）。

**`user.verdict` は先送りのまま**である。第2層（ADR-0003 の 👍/👎）は今も書き手が
存在せず、「将来の verdict コマンド」という Decision 3 の記述がそのまま生きている。

実測（2026-07-26）: Decision 4 が「ここで聞かないと第1層が永遠に空になる」と書いた
とおり、実台帳の `test.result` は**この日まで0件**だった。書き手が入った直後の
通し確認で、主観Feedbackを一度も与えないまま Beta が動いた（ADR-0052「実測」）。

---

## 追記（2026-07-27）: `user.verdict` の先送りが終わった

Decision 3 の「Phase 1 の do が生まないもの」から、最後の1行が外れる:

```text
user.verdict     第2層は将来の verdict コマンド（ADR-0003）
```

[ADR-0055](ADR-0055-verdict-is-a-veto.md) がその「将来の verdict コマンド」で、
名前もそのまま `tomobit verdict <session-id> up|down|clear` になった。
書き手はもう1つあり、境界で**赤テスト × 1=文句なし**の矛盾が出た日にだけ立つ
1問である（同 Decision 1）。

Decision 3 の禁止リストは、これで `plan.generated`（Plan レイヤーの縮退）と
`tomo.asked`（ADR-0007 が別途実装済み）だけが理由付きで残る形になった。

**先送りが正しかったことも記録しておく。** 第2層は第1層への拒否権であり、
`test.result` に書き手が無い間は上書きすべき対象が存在しなかった。
2026-07-26 に ADR-0052 が第1層を生やし、その翌日に第2層が必要になった —
Phase 1 で両方を同時に作っていたら、片方は6年間空回りしていた。
