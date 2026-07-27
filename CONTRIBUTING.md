# Contributing to Tomobit

日本語でも英語でも構いません。Issue・PRの言語は問いません。

## ライセンスと、CLAを採らない理由

- コード → **AGPL-3.0-only**（[LICENSE](LICENSE)）
- 文書（`docs/`・`README.md`・`VISION.ja.md`）→ **CC BY-SA 4.0**（[LICENSE-docs](LICENSE-docs)）

このプロジェクトは **CLA（コントリビューターライセンス契約）を採りません。** 代わりに
[Developer Certificate of Origin 1.1](https://developercertificate.org/) を使います。

これは意図的な選択です。CLAは寄稿者から「プロプライエタリに再ライセンスしてよい」という
非対称な許諾を集める仕組みで、**メンテナだけが後から閉じられる**状態を作ります。
Tomobitはそれをしません。**私を含め、誰もこのコードを閉じられない**——それが
[経験主権（ADR-0018）](docs/decisions/ADR-0018-experience-sovereignty.md)を掲げる
プロジェクトとして筋が通っていると考えています。

コミットに `Signed-off-by:` を付けてください:

```bash
git commit -s -m "..."
```

これは「この寄稿を提出する権利が自分にあり、上記ライセンスで提供する」ことの表明です。

## 設計に関わる変更は、まずADRを書く

Tomobitは判断の記録を第一級の資産として扱っています（[docs/decisions/](docs/decisions/)）。
挙動・思想・数式に関わる変更は、コードより先に **ADRのドラフト（Status: Proposed）** を
PRとして出してください。歓迎します。議論はそこでやります。

ADRに書いてほしいこと:

- **何が問題か** — できれば実測で。このプロジェクトのADRは、思いつきではなく
  観測から始まっているものが強いです（例:
  [ADR-0042](docs/decisions/ADR-0042-split-starvation-and-lexical-shadowing.md) は
  「11連敗のProviderが最頻選択されている」という実測から始まりました）
- **却下した代替案と、その理由**
- **この判断が間違っていたと分かる条件**

**判断が覆された記録も残します。** 消さずに、改版として積んでください
（[ADR-0037](docs/decisions/ADR-0037-merge-reachability.md) は「実測で頓挫した」ADRです）。

タイポ修正・バグ修正・テスト追加は、ADR無しでそのままPRで構いません。

## 守ってほしい原則

コードを読む前に、この2つだけ知っておいてください。

1. **Meaning by Model, Judgment by Math**（[ADR-0011](docs/decisions/ADR-0011-meaning-by-model-judgment-by-math.md)）
   — LLMの座席は知覚（extractor）だけです。**判断に LLM を呼ぶ変更は入りません。**
   Decision Engineは純関数のままにしてください。
2. **真実は追記専用** — `events` / `experiences` は追記のみ。射影（`connections` 等）は
   `tomobit rebuild` で必ず再構成できること。判断のためのフィールドを射影に独立保存しない
   （[ADR-0001 One Ledger](docs/decisions/ADR-0001-connection-granularity.md)）。

## 検証

PRを出す前に:

```bash
gofmt -l .        # 出力が空であること
go vet ./...
go test ./...
make docs-check   # ADRリンクの参照先が実在すること
```

CIでも同じものが走ります。

ドキュメントだけの変更でも `make docs-check` は通してください。ADRの
`関連:` ブロックは参照先の表題の記憶から書かれがちで、ファイル名とずれても
人間の目には読めてしまいます。

## 報告

- バグ・要望 → [Issues](https://github.com/Rererr/tomobit/issues)
- 脆弱性 → **公開Issueではなく** [SECURITY.md](SECURITY.md) の手順で
