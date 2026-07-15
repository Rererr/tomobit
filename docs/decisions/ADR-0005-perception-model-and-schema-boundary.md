# ADR-0005: 知覚の実装 — モデル確定とschemaの責務境界

- Status: **Accepted**
- Date: 2026-07-15
- 関連: [ADR-0004](ADR-0004-tech-stack.md)（実装時ノブの充填）, [ADR-0001](ADR-0001-connection-granularity.md), [PERCEPTION_ENGINE.md](../core/PERCEPTION_ENGINE.md), [EXPERIENCE.md](../core/EXPERIENCE.md)

---

## Context

[ADR-0004](ADR-0004-tech-stack.md) は知覚のLLMをローカルOllamaと決めたが、
二つを「実装時ノブ」として保留した — **モデル選定**と**抽出プロンプト**。

本ADRはその二つを、実機（Apple M4 Pro / RAM 48GB）での実測により確定する。

---

## Decision 1: モデル = qwen3:8b

ADR-0004の枠（8Bクラスのinstruct、qwen系/llama系）内で選定した。

```text
qwen3:8b          5.2GB   採用
llama3.1:8b       4.9GB   構造化出力の実績は厚いが日本語で劣る
```

決定打は**日本語**。設計ドキュメントもTomoの質問も日本語であり、
Sessionに現れる人間の発話は日本語である。

環境（`brew install ollama` = CLI＋サーバ、GUI無し / `localhost:11434`）。

実測（抽出タスク相当、`temperature=0`・`think:false`）:

```text
レイテンシ   2.4秒 / 87 tok の抽出
スループット 38 tok/s
決定性       同一入力3回で完全に同一のJSON
```

Deferred Perception（ADR-0004）の前提により、この速度は要件を満たす。
知覚は遅延・Replay可能であり、レイテンシは決定経路に乗らない。

---

## Decision 2: schemaは「形」、プロンプトは「意味」

**Ollamaの `format` に渡したJSON schemaの `description` は、モデルに到達しない。**
GBNF文法への変換時に落ちるため、制約は出力の構造にしか効かない。

計測で確定した事実（推測ではない）:

```text
現象  Context の language が "Japanese" を返した
      （会話が日本語だった。正しくは変更ファイルの言語 "Rust"）

仮説  schema の各フィールドに description を足せば直る
反証  3回とも "Japanese" のまま。description はモデルに届いていない

解決  同じ意味論を system プロンプトへ移す → 3/3 で "Rust"
```

したがって責務を分ける:

```text
JSON schema   出力の形（型・enum・required）のみを保証する
プロンプト    フィールドの意味論を担う。schemaに書いても届かない
```

`extract-experience` のフィールド意味論は、必ずプロンプト側に置く。

### なぜこれがADRに値するか

Context属性は[ADR-0001](ADR-0001-connection-granularity.md)により
**Connectionの粒度そのものを決める**。
`Language=Japanese` で記帳された経験は、`Language=Rust` の経験と別の島に育つ。

しかもこの誤りは**沈黙する** — JSONは妥当、パースは成功、exit codeは0。
schemaが「形」を保証したことが、「意味」も保証されたかのように見せる。
射影は静かに歪み、Splitは使用者が痛みを経験していない場所で起きる。

「決定的にパースできるものはLLMに聞かない」（ADR-0004）の裏返しとして、
**LLMに聞くものは、schemaでは守れない**。

---

## Consequences

- ADR-0004の実装時ノブのうち、モデル選定と抽出プロンプトの方針が閉じた
- `extract-experience` の実装は、schemaとプロンプトを別物として保守する。
  フィールド追加時に**schemaだけ更新するとサイレントに壊れる**
- 抽出品質はExperienceとして記帳される（ADR-0004）ため、
  この種の取り違えはTomobit自身のdogfoodで検出できる余地がある
- モデル差し替え時は、schemaの妥当性ではなく**意味論の正しさ**で再評価する
  （形は文法が保証するので、どのモデルでも必ず通る＝退行の検出器にならない）
- 残るノブ: 抽出プロンプトの具体文面、few-shotの要否、rebuildの実行タイミング
