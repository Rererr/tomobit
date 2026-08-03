.PHONY: docs-check docs-sync

# ADR相対リンクの参照先が実在するか、改版マークが最新か、README索引が
# ADR本文のStatusと合っているかを検査する。
docs-check:
	@bash tools/check-adr-links.sh
	@bash tools/sync-adr-superseded.sh --check
	@bash tools/check-readme-index.sh

# 「- 改版:」宣言から、被改版ADRの冒頭マークを生成し直す。
docs-sync:
	@bash tools/sync-adr-superseded.sh
