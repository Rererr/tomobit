# spritegen — 窓用スプライトの展開・検証器

[SPRITES-WINDOW.md](../../docs/design/SPRITES-WINDOW.md)（v2・犬種・斜めポーズ版）の
導出規則（S2/S4/S5・Frame B）を適用して全ステージのフルグリッドを生成し、
プレビューPNGを描く参照実装（Python、要Pillow）。設計セッションで
資産レビューに使ったもの。

- `breeds34.py` — S0毛玉+S3正本グリッド（柴犬/レトリーバー/ポメラニアン）+ 単体レンダラ
- `stages34.py` — S0〜S5への展開（S1正本グリッド+導出規則の実装）+ シート出力 + 検証

```sh
python3 stages34.py   # stages34_sheet_A.png / stages34_sheet_B.png / stages34.json
```

出力（シートPNG・json・`__pycache__`）は生成物なのでコミットしない。

Go実装時の役割: `internal/facewin/sprite32.go` の全グリッドリテラルは
この出力から転写し、テストが導出規則を再適用して一致を検証する
（SPRITES-WINDOW.md「実装規約」）。Go版 `tools/spritegen` に
置き換えたらこのPython版は削除してよい。
