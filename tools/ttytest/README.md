# ttytest — 実端末での検証

ラインエディタ（`internal/lineedit`）と対話セッション（`cmd/tomobit/chat.go`）は、
raw modeの端末という**外部環境**を相手にする。バッファ・キー解読・再描画の各関数は
`go test` が守るが、「実際のpty上でBackspaceが効くか」はそこでは守れない。
ここに置くのはその層の検証で、実際にptyを開いて実キーストロークを送る。

これまでに見つかった穴はすべてこの層のもの:
`/`コマンドが履歴に入っていなかった / 複数行を貼ったあとCtrl-Uで全部消せなかった。

## 走らせ方

```sh
go build -o /tmp/tomobit ./cmd/tomobit

# ラインエディタ（外部サービス不要・数秒）
expect tools/ttytest/lineedit.exp /tmp/tomobit

# Ctrl-Zサスペンド（外部サービス不要・数秒）。バイナリ直spawnはセッションリーダーになり
# orphaned process groupへの停止シグナルをカーネルが破棄するため、対話シェルを挟んで
# 実ユーザーと同じ「シェルのジョブ」として検証する
expect tools/ttytest/suspend.exp /tmp/tomobit

# 対話フロー全体（実claude・実ollamaを叩く。数十秒・数セント）
export TOMOBIT_CLAUDE_ARGS="--model haiku"
expect tools/ttytest/flow.exp /tmp/tomobit

# 実行中のCtrl-C（実claudeが要る。SIGINTが生きた子に届く経路そのもの）
expect tools/ttytest/cancel.exp /tmp/tomobit
```

`cancel.exp` が守るのはADR-0022の約束「Ctrl-Cはそのターンの中断であって、
タスクの中断ではない」——子はSIGINTで最終行をflushし、チャットは生き残り、
次のターンが同じスレッドを継ぎ、セッションは1つのまま境界に着く。

`lineedit.exp` は40桁の端末を張って折り返しも通る。
すべてのexpectはマッチ必須（timeout = FAIL）——素通りするテストは何も守らない。
