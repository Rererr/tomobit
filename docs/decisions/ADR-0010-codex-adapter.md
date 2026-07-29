# ADR-0010: 2つ目のAdapter — codex と Provider選択

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0006](ADR-0006-executor-integration.md)（Adapter境界・digest方針・写像表の先例）,
  [ADR-0007](ADR-0007-curiosity-question.md)（発火は2つ目のAdapter後）,
  [SCHEMA.md](../design/SCHEMA.md)（R3: Provider名は道具名のみ）

<!-- 改版:begin — tools/sync-adr-superseded.sh が生成する。手で編集しない -->
> **改版済み** — この決定の一部は後のADRが置き換えた。範囲は各Decisionの改版注記が持つ。
>
> - Decision 1 → [ADR-0043](ADR-0043-auto-by-default.md)
<!-- 改版:end -->

---

## Context

ADR-0007のTomoの質問は実装・E2E検証済みだが、互角ゲートは同一scopeで
**2つのProvider**の証拠が揃って初めて開く。claude-codeしかいない世界では
preferenceは永遠に育たない。2つ目のAdapterが相棒の完成条件である。

確定事項は4つ: Provider選択のUX / 起動形 / stream→events写像 / 検証の限界。

---

## Decision 1: 選択は `do --provider`（既定 claude-code）— 人間が選ぶ

> **改版（[ADR-0043](ADR-0043-auto-by-default.md) Decision 1・2026-07-24）**:
> 「既定 claude-code」は置換された — do/chat の `--provider` 既定は `auto`。
> 本Decisionが自ら書いた解除条件（Connectionが十分育つまで）に到達したための
> 改版で、`--provider claude-code` の明示指定は今までどおり効く。
> Decision 2/3/4（起動形・写像・検証の限界）は不変。

- 登録名は `codex`（R3: 道具名のみ。モデル名はContext属性へ）
- Phase 1のDecision Engineは「人間がフラグで選ぶ」に縮退する
  （ADR-0006のPlan縮退・ADR-0007のScheduler縮退と同型）。
  Connectionが十分育つまで、自動選択は根拠を持てない —
  自動選択こそがConnectionの果実であり、前提にしてはならない
- 却下した対案: **ラウンドロビン等の自動交互実行** → 「証拠を早く貯める」
  ためだけの機構は、ユーザーの仕事の道具選びを歪める。
  Curiosity Never Blocks Production

---

## Decision 2: 起動形 = `codex exec --json --skip-git-repo-check <prompt>`

- `--ephemeral` は**使わない**: `provider_session_id`（thread_id）で
  原本（Codex自身のセッションログ）への参照を残す（ADR-0006と同方針）
- `do --permission-mode` は codex では `--sandbox <mode>` へ写像する
  （read-only / workspace-write / danger-full-access）。
  Adapterは「起動と翻訳」だけの原則（ADR-0006 Decision 2）を維持
- モデルは指定しない（ユーザーの `~/.codex/config.toml` を尊重）。
  codexのJSONLはモデル名をエコーしないため、provider.selected の
  model は空になる — 観測できないものを推測で埋めない

---

## Decision 3: stream→events写像 — digest方針はADR-0006を踏襲

```text
thread.started      → provider.selected {provider:"codex", model:"",
                       provider_session_id: thread_id}
item.completed
  .item.type:
    agent_message      → provider.output {text}
    command_execution  → provider.output {tool:"command_execution"}
    file_change        → provider.output {tool:"file_change"}
    mcp_tool_call      → provider.output {tool:"mcp_tool_call"}
    web_search         → provider.output {tool:"web_search"}
    error              → provider.error  {message}
    reasoning / todo_list → 捨てる（claude-codeのthinking落としと同方針:
                            ユーザーが採用を判断する成果物ではない）
item.started / item.updated → 捨てる（完了アイテムだけを記帳し二重化を防ぐ）
turn.completed      → provider.finished {input_tokens, cached_input_tokens,
                       output_tokens}
turn.failed         → provider.error {message}
error（トップレベル）→ 捨てる（実採取でturn.failedと同文の重複を確認済み —
                       翻訳は行単位の純関数なのでdedupは片方を捨てる形で行う）
未知type            → 捨てる（前方互換。Debugでのみ可視化）
```

- exit_code は claude-code と同じく Executor が観測して埋める

---

## Decision 4: 検証の限界の明記 — 成功ストリームは実採取できていない

実装日時点で、このマシンのcodexアカウントは使用上限に達しており
（2026-07-21回復）、**成功ストリームの実採取は不可**。実採取できたのは:

- エラー封筒2種（`error`＋`turn.failed` の重複、`item.completed` の
  error item）— 写像とdedup方針はこの実データに基づく
- イベント封筒の形（`thread.started` / `turn.started` の実バイト列）

成功経路（agent_message / turn.completed）の写像は公式JSONL仕様に基づき、
フィクスチャで固定する（Translateは純関数 — ADR-0006 Decision 3の
テスト戦略をそのまま使う）。**上限回復後に実ストリームを採取して
フィクスチャを照合・更新すること**（それまで成功経路は「仕様準拠・実機未確認」
であると、このADRが正直に記録する）。

---

## Consequences

- preferenceが実データで育つ道が開く: 同一scopeで両Providerの実効証拠が
  n_min=3を超えると互角ゲートが開き、Tomoの質問が初めて実発火する
- `tomobit status` のテーブルに2つのProviderが並び、成長ステージS5
  （あいぼう）への到達が現実の軌道に乗る
- 3つ目以降のAdapter（gemini等）はこのADRの写像表のパターンを踏襲する
- 残るOpen Questions:
  - Decision Engineの自動Provider選択（Connectionの果実。別ADR）
  - codexの `review` サブコマンド等、cap別の起動形最適化
