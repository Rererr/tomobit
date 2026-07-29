# ADR-0029: 知覚バックエンドの選択 — MLX LM Server / Ollama

- Status: **Accepted**
- Date: 2026-07-18
- 改版: ADR-0004 D3
- 関連: [ADR-0004](ADR-0004-tech-stack.md)（ローカルOllamaの選定 — 本ADRが「ローカル知覚サーバー」へ一般化）,
  [ADR-0005](ADR-0005-perception-model-and-schema-boundary.md)（schema=形・プロンプト=意味 — 本ADRが境界を拡張）,
  [ADR-0021](ADR-0021-onboarding.md)（setupの配線質問）

---

## Context

知覚（ADR-0004/0005）はローカルOllama＋qwen3:8bに固定されている。
Apple Silicon上では、Appleの公式MLフレームワークMLXに基づく
**MLX LM Server**（`mlx_lm.server`）がローカルLLMサービングとして主流になりつつあり、
Metal最適化により同クラスのモデルをOllamaより効率よく回せる。

知覚は「ローカルのHTTPサーバーに小さな抽出を頼む」だけの疎結合であり、
バックエンドを選べるようにすることはADR-0004の**拡張**であって転換ではない。
完全ローカルという前提（VISION / ADR-0018）は動かない。

実装前にmlx-lm本体のソース（`mlx_lm/server.py`, main branch, 2026-07-18時点）で
確認した事実:

```text
エンドポイント   POST /v1/chat/completions（OpenAI互換形）
                 GET  /v1/models（HFキャッシュ済モデルの列挙）/ GET /health
応答             choices[0].message.content。textが空だとcontentキー自体が無い。
                 思考テキストはサーバー側で分離され message.reasoning に入る
構造化出力       response_format / json_schema は未実装（grepで不在を確認）
思考制御         chat_template_kwargs をリクエスト単位で渡せる
                 → {"enable_thinking": false} でQwen3の思考をOFFにできる
モデル指定       model はHF repo id（例 mlx-community/Qwen3-8B-4bit）。
                 未キャッシュなら初回リクエスト時にHFからダウンロードされる
既定             port 8080 / temperature 0.0 / max_tokens 512
```

---

## Decision 1: バックエンドは `ollama` | `mlx-lm` の2択。汎用OpenAI互換クライアントにはしない

`perceive.MLXLM` を `perceive.Ollama` と並ぶ `Extractor` 実装として追加する。
「OpenAI互換ならなんでも繋がる」汎用クライアントは作らない。

却下した代替案 — **汎用OpenAI互換バックエンド**:

- 「互換」の実態はサーバーごとに違う（mlx-lmだけでも `chat_template_kwargs`・
  `reasoning` 分離・空contentキー省略という非互換がある）。汎用を名乗ると
  この差異を吸収する責務が際限なく増える
- リモートAPIへの接続を招き入れる玄関になる。知覚は完全ローカルが前提であり、
  コンセプト純度に反するスコープクリープの典型
- 対応バックエンドを名指しで持てば、非互換は名指しで吸収できる

`Extractor.Name()`（= extractor_model）はモデルIDをそのまま返す。
`qwen3:8b` と `mlx-community/Qwen3-8B-4bit` は文字列として自然に区別されるため、
どのバックエンド・モデルで知覚したかは既存の記帳だけで追える。

## Decision 2: MLX経路では「形」の保証もプロンプト＋Go側検証へ移る（ADR-0005の拡張）

ADR-0005はOllamaの `format`（JSON schema→GBNF）を前提に
「schema=形・プロンプト=意味」と境界を引いた。
mlx-lm serverには構造化出力が**無い**ため、MLX経路ではこの境界が動く:

```text
              形（型・required・enum）            意味
Ollama        format のschemaが文法で保証         プロンプト
MLX LM        プロンプトで指示＋Go側で検証        プロンプト
```

- プロンプト: systemプロンプト末尾に、返すべきJSONの形（4キー・sizeのenum・
  JSON以外を出力しない）を明示するブロックをMLX経路でのみ追加する
- Go側検証: 応答から最初のJSONオブジェクトを取り出し（コードフェンス除去を含む）、
  4キーが揃いsizeがenum内であることを検証する。**不正はエラーにする**
  （握り潰して""へ矯正しない）。知覚はDeferredでReplay可能（ADR-0004）だから、
  失敗したセッションはpendingのまま再試行すればよい。
  黙って歪んだContextを記帳することが最悪の結果（ADR-0005: 射影は静かに歪む）
- `chat_template_kwargs: {"enable_thinking": false}` を常に渡す（Ollamaの
  `think:false` と対）。思考が返ってもサーバーが `reasoning` に分離するため
  contentの解析は壊れないが、防御的にフェンス・前置きテキストは剥がす

extractor_verは**バンプしない**。抽出の意味論（フィールド定義・語彙渡し）は
両バックエンドで同一であり、形の保証手段が違うだけ。バックエンドの別は
extractor_modelが記帳する。

## Decision 3: configは `perceive_backend` ＋ バックエンド別のURL/モデルキー

```json
{
  "perceive_backend": "mlx-lm",
  "ollama_url":  "...", "ollama_model": "...",
  "mlx_url":     "...", "mlx_model":    "..."
}
```

- 既存キー `ollama_url` / `ollama_model` はそのまま。新設は
  `perceive_backend`（`"ollama"` | `"mlx-lm"`）と `mlx_url` / `mlx_model`
- URL/モデルをバックエンド別に持つのは、往復切替時に前の配線が消えないため。
  既定URLも異なる（11434 / 8080）ので汎用キーは誤配線を招く
- 既定値: `mlx_url` = `http://localhost:8080`,
  `mlx_model` = `mlx-community/Qwen3-8B-4bit`
  （ADR-0005の qwen3:8b と同族・同クラス。Ollamaのq4_K_Mと同じ4bit量子化。
  決定打だった日本語性能はモデル由来でありサービング層で変わらない）

### `perceive_backend` 不在時の解決（後方互換）

```text
1. ollama_url か ollama_model が設定済み        → ollama
2. それ以外のフィールドが1つでも設定済み        → ollama（キー新設前のconfig）
3. configが完全に空（ファイル無し・空 = 処女機）→ darwin: mlx-lm / それ以外: ollama
```

1と2が先なのはSplitProtocol（ADR-0028）と同じ原理 — **キー新設より前のconfigを
持つ機械の挙動を、キー不在の解釈で黙って変えない**。MLXが既定になるのは
本当に何も配線されていない新規のMacだけ。

2は実機で検出した穴を塞ぐ（当初案は1と3のみだった）:

```text
現象  本開発機（Mac・Ollamaを既定値運用）の実configは
      claude_config_dir と claude_args のみ。ollama_url/ollama_model は
      既定値で足りていたため書かれていない
      → 当初案では darwin 既定の mlx-lm に解決され、localhost:8080 に
        居た無関係のプロセスへ飛んで 404（実測）
教訓  「Ollamaを使ってきた」ことは ollama_* キーの存在では検出できない。
      既定値で動く配線は config に痕跡を残さない
```

2が成立するには「**新バイナリが書くconfigは必ず perceive_backend を持つ**」
という不変条件が要る（さもなくば新規Macで claude だけ配線したconfigが
旧config と見分けられない）。よって:

- setup の知覚質問は Enter（提示既定の受諾）でも **perceive_backend を明示的に
  書き込む**（ADR-0021 の「プロファイルは継承でも明示が必要」と同じ思想。
  Enterで書かない慣習（ADR-0025/0027）はキー不在の意味が安定している場合の
  ものであり、本キーの不在の意味は 2 によりconfig全体の状態に依存するため、
  不在のまま残すことが逆に将来の解釈を不安定にする）
- `do` 途中の askClaudeProfile（部分保存）も、保存直前のon-disk configで
  解決したバックエンドを同時に書き込む（保存時点の挙動をそのまま固定するので
  挙動変更ゼロ。処女Macなら mlx-lm が、旧机なら ollama が固定される）
- setup が提示する既定値は、質問時点の作業中コピーでなく**起動時にディスクから
  読んだconfig**で解決する（同一setup内で先に claude を答えると作業中コピーが
  非空になり、処女機が 2 に誤マッチするため）

## Decision 4: フラグは `--backend` を追加。`--url`/`--model` は選択済みバックエンドの値として解釈

- `do` / `chat` / `perceive` に `--backend ollama|mlx-lm` を追加（1回限りの上書き。
  flag > env > config の既存原則のうち、envは知覚にセッション単位の用途が
  現れるまで増やさない — TOMOBIT_OLLAMA_* も元々存在しない）
- `--url` / `--model` の既定値はバックエンド解決後にそのバックエンドの
  config・既定から引く。フラグの意味は「知覚サーバーのURL/モデル」で不変
- extractor生成は1箇所（`newExtractor` 相当）に集約する。現在4箇所に散った
  `&perceive.Ollama{...}` の重複が、バックエンド分岐の重複になるのを防ぐ

## Decision 5: setupの知覚質問はバックエンド選択から始め、診断もバックエンド別

1. バックエンド選択（現在値または上記解決の結果を既定に、Enterで維持）
2. URL・モデル（選択したバックエンドの現在値/既定を提示）
3. 診断:
   - ollama: `/api/tags` でモデルの有無（現行どおり）
   - mlx-lm: `GET /v1/models` で到達性とモデルのキャッシュ有無。
     未キャッシュは**エラーではなく**「初回知覚時にHFからダウンロードされる」旨を
     案内する（mlx-lmはオンデマンドロード）
- `reportCLIs` の診断対象に `mlx_lm.server` を加える（存在は機械の事実、
  無くてもリモートURLを配線できるので情報提供のみ）

---

## Consequences

- 新規Macの既定知覚がMLX LM Serverになる。Ollama配線済みの機械は不変
- ADR-0005の「schemaだけ更新するとサイレントに壊れる」は、MLX経路では
  「**プロンプトの形ブロックとGo側検証の更新漏れ**で壊れる」に変わる。
  フィールド追加時はschema・プロンプト意味論・MLX形ブロック・Go検証の
  4点を揃えて更新し、extractor_verをバンプする
- mlx-lm serverはproduction非推奨（公式明記）のローカル用途ツール。
  知覚はlocalhostの自分専用サーバーという前提に合致するが、
  リモート共用サーバーとしての配線は想定しない
- 実測値（Apple M4 Pro / RAM 48GB、mlx-lm 0.31系 / mlx-community/Qwen3-8B-4bit、
  抽出タスク相当・`temperature=0`・`enable_thinking:false`、2026-07-18）:

  ```text
  レイテンシ   1.05秒 / 34 tok の抽出（ロード後定常。参考: Ollama実測は2.4秒 — ADR-0005）
  ロード       キャッシュ済モデルで+2.4秒（初回のみ。mlx-lmはLRUで常駐）
  スループット 32 tok/s
  決定性       同一入力3回で完全に同一のJSON
  意味品質     rust/axum/borrow-checker/small — 全フィールド正答
  ```

  0.6B（mlx-community/Qwen3-0.6B-4bit）も形の契約（フェンス剥がし・検証）は
  通るが全フィールド""に倒れがちで、既定を8Bにする判断を実測が支持した

### 既定モデル対抗馬の実測評価（2026-07-18、同機・同条件）

27B級低bitモデル prism-ml/Ternary-Bonsai-27B-mlx-2bit（8.5GB、対FP16
94.6%を謳う）を既定モデル候補として実測評価した。同一プロンプト・
`temperature=0`・`enable_thinking:false`・各3回:

```text
                      Qwen3-8B-4bit     Ternary-Bonsai-27B-2bit
定常レイテンシ(英)     1.05s / 32 tok/s   2.72s / 14 tok/s
定常レイテンシ(日)     0.91s / 32 tok/s   2.70s / 14 tok/s
初回ロード             6.7s               12.3s
決定性(3回同一)        両ケース ○         両ケース ○
抽出品質(英 rust/axum) 全項目正答         全項目正答
抽出品質(日 go/stdlib) 全項目正答         全項目正答
  日本語会話の言語罠    lang=go ○         lang=go ○（ADR-0005の罠を両者回避）
指示追従の細部          topic をハイフン化  topic "borrow checker" と空白区切り
                      （指示どおり）      （ハイフン指示に従わず。schema上は妥当）
```

評価: 4属性分類というこの用途では27Bの品質は活きず（両者全問正答で差が
出ない。唯一の差は topic の語彙選択の妙と、低bit化で最も劣化する指示追従の
微細な綻び）、レイテンシだけが2.6倍になる。**既定モデルは
mlx-community/Qwen3-8B-4bit を維持**。なお 1bit 版
（prism-ml/Bonsai-27B-mlx-1bit）は stock mlx が bits=1 未対応
（`mx.quantize` は 2,3,4,5,6,8 のみ、0.32.0 実測）で、PrismML の MLX fork
（要 full Xcode ソースビルド）でしか動かず、候補から外れた。
27B級ローカルモデルは、将来の生成寄り器官（要約・質問生成等）で改めて
評価する価値が残る。
- 残るノブ: mlx-lmの構造化出力が将来入った場合、Decision 2のGo側検証は
  防御として残したまま形ブロックだけschemaへ戻せる
