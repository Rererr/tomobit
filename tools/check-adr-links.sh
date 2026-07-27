#!/usr/bin/env bash
# ADRリンクの参照先検査。
#
# gitが追跡する全 *.md から、次の2種類のリンクを抜き出して参照先の実在を
# 確かめる。壊れたリンクが1件でもあれば非ゼロで終了する。
# `make docs-check` から呼ばれる。
#
#   1. リポジトリ内の相対リンク    `ADR-NNNN-*.md`
#   2. 隣のリポジトリへの絶対URL   `https://github.com/<owner>/<repo>/blob/main/<path>`
#
# この検査が要る理由: ADRの `関連:` ブロックは、参照先の「表題」の記憶から
# 書かれることが多く、ファイル名とずれる。人間の目には読めてしまうので、
# 機械で見るしかない。実際にリポジトリ内で7本、cross-repoで4本壊れていた。
#
# 隣のリポジトリは「隣にチェックアウトがあれば」見る。無ければ検査せず、
# 何を見なかったかを言う。**見られなかったものを「壊れていない」とは呼ばない**
# （ADR-0052 Decision 4 と同じ規律）。探す場所は $ADR_LINK_SIBLINGS で、
# 既定はこのリポジトリの親ディレクトリ。
#
# 対象外:
#   - `blob/main` 以外を指すURL — SHAやタグで固定したリンクは不変な断面を
#     指しており、こちらの作業ツリーと突き合わせる意味が無い
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

siblings=${ADR_LINK_SIBLINGS:-$(dirname "$root")}

# 1. インラインリンク中の ADR-NNNN…md への相対リンクのうち、
#    参照先が実在しないものを "  file -> target" の形で出す。
scan_broken_relative() {
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

# 2. 隣のリポジトリの main を指すURL。ADRに限らずどのパスも見る
#    （本体は GUI の .ts も指している）。隣が無ければ "skip" として数える。
#    出力: "check<TAB>file<TAB>repo<TAB>path" / "skip<TAB>owner/repo"
scan_cross_repo() {
  local f url owner rest repo path
  while IFS= read -r -d '' f; do
    while IFS= read -r url; do
      rest=${url#https://github.com/}
      owner=${rest%%/*}
      rest=${rest#"$owner"/}
      repo=${rest%%/*}
      path=${rest#"$repo"/blob/main/}
      path=${path%%#*}
      [ -n "$path" ] || continue
      if [ -e "$siblings/$repo/.git" ]; then
        printf 'check\t%s\t%s\t%s\n' "$f" "$repo" "$path"
      else
        printf 'skip\t%s/%s\n' "$owner" "$repo"
      fi
    done < <(grep -oE 'https://github\.com/[^)"'"'"' ]+/blob/main/[^)"'"'"' ]+' "$f")
  done < <(git ls-files -z '*.md')
}

count_matches() { # パターンに一致した行数。1件も無ければ0。
  git ls-files -z '*.md' | xargs -0 grep -ohE "$1" 2>/dev/null | grep -c '' || true
}

scanned=$(git ls-files '*.md' | wc -l | tr -d ' ')
if [ "$scanned" -eq 0 ]; then
  echo "*.md が1つも見つからない。検査が空振りしている。" >&2
  exit 1
fi

broken=$(scan_broken_relative)
relative_n=$(count_matches '\]\([^)]*ADR-[0-9]{4}[^)]*\.md[^)]*\)')

cross_n=0
skipped=""
while IFS=$'\t' read -r kind a b c; do
  case "$kind" in
    check)
      cross_n=$((cross_n + 1))
      [ -e "$siblings/$b/$c" ] || broken="${broken}  ${a} -> ${c} (隣 ${b})"$'\n'
      ;;
    skip) skipped="${skipped}${a}"$'\n' ;;
  esac
done < <(scan_cross_repo)

if [ -n "${broken//[[:space:]]/}" ]; then
  {
    echo "参照先の無いADRリンク:"
    printf '%s' "$broken" | sed '/^[[:space:]]*$/d'
    echo
    echo "参照先の実ファイル名に合わせてください。"
  } >&2
  exit 1
fi

echo "ADRリンク: すべて解決した (${scanned}ファイル / 内 ${relative_n}本, 隣 ${cross_n}本)"

# 見なかったものを、黙って通したことにしない。
if [ -n "${skipped//[[:space:]]/}" ]; then
  echo "隣にチェックアウトが無いので検査しなかった:"
  printf '%s' "$skipped" | sed '/^[[:space:]]*$/d' | sort | uniq -c | sed 's/^/ /'
fi
