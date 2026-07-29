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

### 節ラベルの意味

ADRの節は「いつ書かれたか」で分かれています。Status が「実装済み」なのに
`## 実装フェーズ（Proposed）` という節がある並びは矛盾ではありません。

| 節 | 誰の時制か |
|---|---|
| `## Context` / `## Decision N` / `## Consequences` | 決めた時点。以後は書き換えず、改版注記を足す |
| `## 実装フェーズ（Proposed）` / `## 実装時ノブ` | **起草時に提案した計画**。実際にそう進んだかは語らない |
| `## 実測（日付）` / `## 実装の記録（日付）` | 実際に起きたこと |
| `## 追記（日付）` | 後から分かったこと・後続ADRによる改版 |

計画の節を事後の結果で上書きしないでください。「何を予定し、実際はどうだったか」の
差が、このプロジェクトのADRが持っている情報です。

### 決定を覆すときは、覆す側に1行書く

Status は全て `Accepted` のままです（部分撤回が主なので、ADR単位の `Superseded` では
粒度が足りません）。代わりに、**改版する側**のヘッダに1行だけ書きます。

```
- 改版: ADR-0028 D3 D5, ADR-0023 D1
```

被改版側のADR冒頭に立つ「改版済み」のブロックは、この1行から
`make docs-sync` が**生成**します。手で書かないでください（正本が割れます）。
`make docs-check` は生成物が陳腐化していたら落ちます。

「改版」と呼ぶのは、当該Decisionの**現行の内容が置き換わった**場合だけです
（撤回・廃止・置換・改定・精密化）。元の決定がそのまま生きている追補・拡張は
書かないでください — 現行有効性の判定が濁ります。

改版の**範囲と理由**は、従来どおり該当Decisionの本文に改版注記
（`> **改版（[ADR-XXXX](...)）**: …`）として書きます。ヘッダの1行は索引であって、
説明ではありません。隣のリポジトリ（tomobit-gui）のADRを改版する場合は、
このスクリプトが書き換えられないので宣言は書かず、散文と `関連:` で書きます。

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
make docs-check   # ADRリンクの参照先が実在すること・改版マークが最新であること
```

CIでも同じものが走ります。

ドキュメントだけの変更でも `make docs-check` は通してください。ADRの
`関連:` ブロックは参照先の表題の記憶から書かれがちで、ファイル名とずれても
人間の目には読めてしまいます。

tomobit-gui を隣（このリポジトリの親ディレクトリ）に置いていれば、そちらを
指すURLの参照先も見ます。置いていなければその分は検査せず、何本見なかったかを
言います。探す場所は `ADR_LINK_SIBLINGS` で変えられます。

## 報告

- バグ・要望 → [Issues](https://github.com/Rererr/tomobit/issues)
- 脆弱性 → **公開Issueではなく** [SECURITY.md](SECURITY.md) の手順で
