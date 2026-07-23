# tools/dogfood — 合成長期dogfood

実LLMを一切呼ばずに「数ヶ月ぶんの経験を積んだ台帳」を合成し、器官間の
統合整合（stage遷移・減衰・decide追随・監査行・忘却・rebuild冪等性）を
実測で点検するハーネス。ttytest（実PTY検証）の姉妹で、こちらは時間軸を跨ぐ。

初出 2026-07-24。この実測が ADR-0041（順序外知覚の乖離）と
ADR-0042（Split飢餓・辞書順遮蔽）を発見した。較正材料（ADR-0017 追記）も
ここから出ている。

## 構成

- `run_dogfood.sh` — 一括再現。`./dog.db` を作り直し、全観察を流す
- `seed.py` — 過去タイムスタンプのイベントを SQLite へ直接合成
  （`record` は ts を現在時刻に固定するため。スキーマは docs/design/SCHEMA.md 準拠）
- `ollama_stub.py` — 知覚バックエンドの偽 HTTP サーバー（決定的応答）
- `bin/claude` / `bin/codex` — PATH に置く Provider スタブ
- `env.sh` — HOME 隔離（実 `~/.tomobit` に触れない）・顔窓抑止
- `replay/` — Go プローブ6種: seed再現（decide.Choose の決定性）/
  sample（選択分布）/ probe（Split反実仮想 lnBF・飢餓）/
  planprobe（planメニュー・引退・復帰）/ gapprobe（curiosity Gap）/
  reflectprobe（Reflection 5トリガ・予算・verdict の20チェック）
- `seed2.py` + `run_plan.sh` + `run_adr41.sh` — 第2弾（2026-07-24）:
  plan学習・reflection・duel・ADR-0041回帰
- `duel.exp` / `duel_budget.exp` — duel の実PTY駆動（ADR-0026 の手動E2Eの
  再現コスト削減。expect、timeout素通り禁止の流儀は tools/ttytest と同じ）

## 既知の観測（第2弾 2026-07-24 — バグではないが記録価値のある挙動）

- duel は verdict（ts=now）→子セッション知覚（過去ts）の順で記帳するため、
  ADR-0041 のガードが毎回発火して full rebuild になる（ADR-0041 Consequences）
- duel の親セッションは provider="" の無信号 execution 経験を1行残す
  （Apply は skip するので射影に影響なし。台帳のノイズとしてのみ）

## 鉄則

- 実台帳・実 `~/.tomobit` を使わない（env.sh が HOME を隔離する）
- 発見は「再現手順＋実測値＋期待との差」で記録し、もっともらしい説明を
  結論にしない（計測で裏取りしてから ADR へ）
