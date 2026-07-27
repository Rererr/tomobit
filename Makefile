.PHONY: docs-check

# ADR相対リンクの参照先が実在するかを検査する。
docs-check:
	@bash tools/check-adr-links.sh
