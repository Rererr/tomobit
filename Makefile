.PHONY: docs-check docs-sync

# ADR相対リンクの参照先が実在するか、改版マークが最新かを検査する。
docs-check:
	@bash tools/check-adr-links.sh
	@bash tools/sync-adr-superseded.sh --check

# 「- 改版:」宣言から、被改版ADRの冒頭マークを生成し直す。
docs-sync:
	@bash tools/sync-adr-superseded.sh
