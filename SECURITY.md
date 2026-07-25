# セキュリティ / Security Policy

## 報告方法 / Reporting a vulnerability

**公開Issueに書かないでください。** GitHubの
[Private vulnerability reporting](https://github.com/Rererr/tomobit/security/advisories/new)
から非公開で報告してください（Security タブ → Report a vulnerability）。

Please do **not** open a public issue. Use GitHub's private vulnerability
reporting instead. 日本語・English どちらでも構いません。

個人開発プロジェクトのため、応答は数日〜数週間かかることがあります。

## このソフトウェアが触るもの / What this software touches

Tomobitはローカルで動きます。運用者が把握しておくべき接触点は以下です。

- **経験の台帳** — 既定 `~/.tomobit/tomobit.db`（単一SQLite）。**会話の生ログを含みます。**
  ここに書かれたものは既定では外へ出ません（[ADR-0018 経験主権](docs/decisions/ADR-0018-experience-sovereignty.md)）。
  削除は `tomobit forget`（物理削除+VACUUM、[ADR-0033](docs/decisions/ADR-0033-organ-of-forgetting.md)）。
- **Provider CLIの起動** — `claude` / `codex` を子プロセスとして起動します。それらが
  どこへ何を送るかは各Providerの規約に従います。Tomobitは仲介するだけで、
  独自の送信先を持ちません。
- **知覚バックエンド** — Ollama / mlx-lm のローカルHTTP端点のみ
  （[ADR-0029](docs/decisions/ADR-0029-perception-backend-choice.md)）。
- **Provider残量の観測（既定OFF・明示的なopt-in）** — `~/.tomobit/config.json` の
  `quota_observe` が `true` のときだけ動きます。**有効にするまで、Keychainは一度も
  読まず、外部端点も一度も叩きません**（[ADR-0049](docs/decisions/ADR-0049-quota-observation-is-opt-in.md)）。
  有効にした場合: macOS Keychain から**あなた自身の**OAuthトークンを読み
  （実際に起動するプロファイルの資格情報だけ。`claude_config_dir` から導出）、
  ベンダーのusage端点を参照します。取れなければ「不明」と出るだけで、推定値は作らず、
  判断にも混ぜません。
  **これはベンダーの非公式な端点であり、いつ消えてもおかしくありません。**
  設計上の根拠・却下した代替経路・実測は
  [ADR-0044](docs/decisions/ADR-0044-provider-quota-observation.md) に全て書いてあります。
  切り替えは `tomobit setup` の質問から。

## 対象範囲 / Scope

台帳の内容が意図せず外部へ出る経路、資格情報の取り違え・漏洩、子プロセス起動時の
引数・環境変数の注入経路は、報告の対象です。

Provider CLI 自体および各ベンダーAPIの脆弱性は対象外です — それぞれの提供元へ報告してください。
