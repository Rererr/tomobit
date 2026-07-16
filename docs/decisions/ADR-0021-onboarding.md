# ADR-0021: 初期導入 — 配線は経験ではない

- Status: **Accepted**
- Date: 2026-07-16
- 関連: [ADR-0018](ADR-0018-experience-sovereignty.md)（単一SQLite=持ち運べる経験）,
  [ADR-0006](ADR-0006-executor-integration.md)（実装追記: claude-codeプロファイルのenv必須方式）,
  [ADR-0008](ADR-0008-appearance.md)（最初の画面はcompanion view）,
  [ADR-0009](ADR-0009-voice.md)（カタログ#6: FirstMeeting）

---

## Context

初期導入はこれまで未設計の空白だった。ゼロタッチで動くのはDBだけ
（`openStore`がMkdirAll+migration）で、それ以外は全部が暗黙の前提:
claude/codex/ollamaがPATHにある、qwen3:8bがpull済み、そして
ADR-0006実装追記で`TOMOBIT_CLAUDE_CONFIG_DIR`が必須になった。

「起動時に対話式で設定したい」には論理的な制約が一つある:
**子プロセスは親shellのenvを書き換えられない**。対話で決めた値を
その場限りにしないためには、env以外の永続先が要る。つまり対話式
セットアップは設定ファイルの導入とセットでしか設計できない。

---

## Decision 1: 配線の置き場所 = 経験DBの外の `~/.tomobit/config.json`

claude-codeのプロファイル、Ollama URL/モデル、DBパス —— これらは
**機械の配線であって経験ではない**。単一SQLiteは持ち運べる経験
（ADR-0018）であり、DBを新しい機械に持っていったとき、古い機械の
パスがついてくるのは間違い。真実テーブル（追記専用）でも射影
（rebuild可能）でもない第三の性質のものをDBに混ぜない。

形式はJSON（stdlibのみ、ADR-0004/0020の依存規律のまま）。コメントが
書けない欠点は「configはsetupが書くもの」なので実害がない。

## Decision 2: 優先順位 = flag > env > config、envは「上書き面」

envとconfigは同じ設定の二重置き場ではない。**スコープ（寿命）が違う**:

| 層 | 寿命 | 役割 |
|---|---|---|
| config | 機械の恒久配線 | `tomobit setup`が書く「この機械ではこう」 |
| env | 1 shell / 1 daemon unit / 1テスト | 恒久状態を触らずに違う配線で走らせる |
| flag | 1回の実行 | その場の指定 |

envを消すと「一時的に違うプロファイル・違うDBで走らせる」手段が
「本物のconfigを書き換えて戻す」（事故りうる）か「全コマンドに
フラグ連打」しかなくなる。E2E検証（偽プロファイル+隔離DB）も
launchd/cron unitごとの注入も、envの席が担う。configが「正」、
envは「上書き」—— 正の置き場所は常に一つ。

プロファイル未選択（envにもconfigにもない）のままclaude-code/auto
の`do`は**記帳前に拒否**（ADR-0006実装追記のまま）。継承したい
場合も「空文字を明示」—— shellがたまたま持っているプロファイルを
黙って継承する事故こそ、この仕組みが防ぐもの。

## Decision 3: `tomobit setup` — 対話式・冪等・再実行が診断

聞くのは選択が要るものだけ、事実は報告だけ:

1. claude-codeのプロファイル（`~/.claude*`をスキャンして候補提示、
   0=明示的な継承、パス直接入力も可）
2. 毎回付けるフラグ（任意）
3. CLI存在チェック（claude/codex/ollama — 報告のみ、選択ではない）
4. Ollama URL/知覚モデル（`/api/tags`で実在確認、居なければ
   `ollama pull`を提案。届かなくても知覚はDeferredなので続行）

再実行すれば現在値を初期値に編集になる（doctorを兼ねる）。

## Decision 4: 初回`do`の動線 — 欠けた選択が質問になる

プロファイル未選択で`do`を叩いたとき、端末（TTY）ならその場で
プロファイルの質問だけをして、configに保存して実行を続ける。
非TTY（デーモン・cron・パイプ）は明示エラーのまま（自動化の中で
対話が始まるのは事故）。

## Decision 5: セットアップの最後の画面 = companion view

setupは説明書で終わらず、Tomoで終わる。まっさらなDBでは
companion viewが既に`FirstMeeting`（カタログ#6:
「はじめまして。まだなにも知らないんだ」）を話す ——
**「はじめまして」は空のネットワークのViewであって、新しい
イベントではない**。挨拶も導出される（ADR-0019と同じ原則）。
「Tomoを迎える日」に必要な儀式は、既に全部Viewとして存在していた。

---

## Consequences

- `internal/config`（stdlibのみ）: `ClaudeConfigDir *string`で
  「未選択(nil)」と「明示的な継承("")」を区別。壊れたconfigは
  エラー（黙ってデフォルトに降格しない）、欠けたconfigはゼロ値
- `cmd/tomobit`: `cfg`をパッケージ初期化で読み、`wireClaude()`が
  env > configの解決をAdapterに適用（setup後は同一プロセス内で再適用）。
  `--db`/`--model`/`--url`のフラグ既定値もconfigを経由
- `cmd/tomobit-face`もconfigのDBパスを読む（二つのレンダラは同じ真実を見る）
- 環境変数の席: `TOMOBIT_DB` / `TOMOBIT_CLAUDE_CONFIG_DIR` /
  `TOMOBIT_CLAUDE_ARGS`（上書き面）、`TOMOBIT_DEBUG`（診断、config対象外）
- 将来のデーモン化（Phase 2）はconfigを読むだけで同じ配線になる
