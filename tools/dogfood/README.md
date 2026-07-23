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
- `replay/` — Go プローブ3種: seed再現（decide.Choose の決定性）/
  sample（選択分布の計測）/ probe（Split反実仮想 lnBF・飢餓の計測）

## 鉄則

- 実台帳・実 `~/.tomobit` を使わない（env.sh が HOME を隔離する）
- 発見は「再現手順＋実測値＋期待との差」で記録し、もっともらしい説明を
  結論にしない（計測で裏取りしてから ADR へ）
