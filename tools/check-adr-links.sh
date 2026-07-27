#!/usr/bin/env bash
# ADR相対リンクの参照先検査。
#
# gitが追跡する全 *.md から `ADR-NNNN-*.md` を指す相対リンクを抜き出し、
# 参照先ファイルが実在することを確かめる。壊れたリンクが1件でもあれば
# 非ゼロで終了する。`make docs-check` から呼ばれる。
#
# この検査が要る理由: ADRの `関連:` ブロックは、参照先の「表題」の記憶から
# 書かれることが多く、ファイル名とずれる。人間の目には読めてしまうので、
# 機械で見るしかない。
#
# 対象外:
#   - 絶対URL(http/https) — 他リポジトリのADRを指す正当なリンク
#   - 絶対パス(/で始まる)
set -uo pipefail

# gitが読めないと ls-files が空を返し、「壊れたリンクは無い」と
# 区別がつかなくなる。空振りを成功と呼ばないよう、ここで落とす。
root=$(git rev-parse --show-toplevel 2>/dev/null) || root=
if [ -z "$root" ]; then
  echo "gitリポジトリの中で実行してください。" >&2
  exit 1
fi
cd "$root" || exit 1

# インラインリンク中の ADR-NNNN…md への相対リンクのうち、
# 参照先が実在しないものを "  file -> target" の形で出す。
scan_broken_links() {
  local f dir target
  while IFS= read -r -d '' f; do
    dir=$(dirname "$f")
    # 抽出したリンク先から #anchor を落としてファイルの実在を見る。
    while IFS= read -r target; do
      case "$target" in
        http://*|https://*|/*) continue ;;
      esac
      [ -f "$dir/$target" ] || printf '  %s -> %s\n' "$f" "$target"
    done < <(
      grep -oE '\]\([^)]*ADR-[0-9]{4}[^)]*\.md[^)]*\)' "$f" \
        | sed -E 's/^\]\(//; s/\)$//; s/#.*$//'
    )
  done < <(git ls-files -z '*.md')
}

scanned=$(git ls-files '*.md' | wc -l | tr -d ' ')
if [ "$scanned" -eq 0 ]; then
  echo "*.md が1つも見つからない。検査が空振りしている。" >&2
  exit 1
fi

broken=$(scan_broken_links)

if [ -n "$broken" ]; then
  {
    echo "参照先の無いADRリンク:"
    echo "$broken"
    echo
    echo "docs/decisions/ の実ファイル名に合わせてください。"
  } >&2
  exit 1
fi

echo "ADRリンク: すべて解決した (${scanned}ファイル)"
