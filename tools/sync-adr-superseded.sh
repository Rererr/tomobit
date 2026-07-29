#!/usr/bin/env bash
# ADRの改版マークの生成と検査。
#
# 「今どれが現行か」は Status 欄からは読めない。このリポジトリのADRは全文撤回より
# **部分撤回**が主で、Status は例外なく Accepted のまま、改版の事実は本文の追記・
# 改版注記にだけ残る。読めば追えるが、ファイルを開いた瞬間には分からないし、
# 機械には判定できない。check-adr-links.sh と同じ理由 —— 人間の目には読めて
# しまうので、機械で見るしかない。
#
# 正本は **改版した側** のヘッダに置く1行だけ:
#
#     - 改版: ADR-0028 D3 D5, ADR-0023 D1
#
# 決定した瞬間に、決定した本人が、1箇所だけ書く。被改版側の冒頭へは、この行から
# **生成**したブロックを機械が差し込む。被改版側を手で書かせない＝正本を割らない。
#
#   make docs-sync    生成・更新する
#   make docs-check   生成物が陳腐化していたら落とす
#
# 記法:
#   ADR-NNNN          そのADRの決定全体が改版された
#   ADR-NNNN D3 D5    Decision 3 と Decision 5 が改版された
#   複数の被改版ADRは `,` で区切る
#
# 「改版」と呼ぶのは、当該Decisionの**現行の内容が置き換わった**場合（撤回・廃止・
# 置換・改定・精密化）に限る。追補・延伸・拡張のように、元の決定がそのまま生きて
# いるものは書かない —— 現行有効性の判定を濁らせるため。
#
# 対象外: 隣のリポジトリのADRへの改版（本体⇄GUI）。このスクリプトは自分の
# リポジトリのファイルしか書き換えられないので、宣言もここには書かない。
# 従来どおり本文の散文と `関連:` で書く。**見なかったものを「無い」とは呼ばない**
# ので、最後に対象外の件数を数えて言う（ADR-0052 Decision 4 と同じ規律）。
set -uo pipefail

BEGIN_MARK='<!-- 改版:begin — tools/sync-adr-superseded.sh が生成する。手で編集しない -->'
END_MARK='<!-- 改版:end -->'

mode=write
case "${1:-}" in
  --check) mode=check ;;
  "") ;;
  *) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

root=$(git rev-parse --show-toplevel 2>/dev/null) || root=
if [ -z "$root" ]; then
  echo "gitリポジトリの中で実行してください。" >&2
  exit 1
fi
dir="$root/docs/decisions"
[ -d "$dir" ] || { echo "$dir が無い。" >&2; exit 1; }
cd "$dir" || exit 1

shopt -s nullglob
files=(ADR-*.md)
if [ ${#files[@]} -eq 0 ]; then
  echo "ADRが1つも見つからない。検査が空振りしている。" >&2
  exit 1
fi

# ADR番号 -> ファイル名。参照先が実在しなければ空を返す。
# （連想配列は使わない —— macOS 同梱の bash 3.2 に無い。check-adr-links.sh と同じ制約）
path_of() {
  local hit=(ADR-"$1"-*.md)
  [ -f "${hit[0]:-}" ] && printf '%s' "${hit[0]}"
}

# 収集: target番号 -> "D番号\t改版した側の番号" の行の並び。
# 決定番号を持たない宣言は D=0（＝全体）として持つ。
decls=""
cross_repo=0
errors=""
for f in "${files[@]}"; do
  src=${f#ADR-}; src=${src%%-*}
  line=$(grep -m1 '^- 改版: ' "$f") || continue
  body=${line#- 改版: }
  case "$body" in
    *,) errors="${errors}  ${f}: 「- 改版:」は1行で書く（行末の , は継続にならない）"$'\n' ;;
  esac
  IFS=',' read -r -a entries <<< "$body"
  for e in "${entries[@]}"; do
    # shellcheck disable=SC2206
    toks=($e)
    [ ${#toks[@]} -gt 0 ] || continue
    tgt=${toks[0]}
    case "$tgt" in
      ADR-[0-9][0-9][0-9][0-9]) ;;
      *) errors="${errors}  ${f}: 宣言の書式が読めない → 「${e}」"$'\n'; continue ;;
    esac
    tn=${tgt#ADR-}
    if [ -z "$(path_of "$tn")" ]; then
      errors="${errors}  ${f}: 参照先のADRが無い → ${tgt}"$'\n'
      continue
    fi
    if [ ${#toks[@]} -eq 1 ]; then
      decls="${decls}${tn}"$'\t'"0"$'\t'"${src}"$'\n'
      continue
    fi
    for d in "${toks[@]:1}"; do
      case "$d" in
        D[0-9]|D[0-9][0-9]) decls="${decls}${tn}"$'\t'"${d#D}"$'\t'"${src}"$'\n' ;;
        *) errors="${errors}  ${f}: Decision の書式が読めない → 「${d}」（D3 の形で書く）"$'\n' ;;
      esac
    done
  done
done

# 隣のリポジトリを指す改版の散文は、宣言の対象外であることを数えて言う。
cross_repo=$(grep -lE '本体 ADR-[0-9]{4}|GUI ADR-[0-9]{4}' "${files[@]}" 2>/dev/null | wc -l | tr -d ' ')

if [ -n "${errors//[[:space:]]/}" ]; then
  { echo "「- 改版:」宣言に問題がある:"; printf '%s' "$errors"; } >&2
  exit 1
fi

# 被改版ADRごとの生成ブロックを組み立てる。
block_for() { # $1=target番号
  local tn=$1 rows d src line=""
  # 同じDecisionを複数のADRが改版していることがある（例: ADR-0012 D3 は 0037→0038 の2世代）。
  # `sort -n -u` は数値キーが等しい行を1本に潰してしまうので、両方のキーで並べる。
  rows=$(printf '%s' "$decls" | awk -F'\t' -v t="$tn" '$1==t {print $2"\t"$3}' \
    | sort -t"$(printf '\t')" -k1,1n -k2,2 -u)
  [ -n "$rows" ] || return 1
  printf '%s\n' "$BEGIN_MARK"
  printf '> **改版済み** — この決定の一部は後のADRが置き換えた。範囲は各Decisionの改版注記が持つ。\n>\n'
  while IFS=$'\t' read -r d src; do
    [ -n "$d" ] || continue
    if [ "$d" = "0" ]; then line="決定全体"; else line="Decision ${d}"; fi
    printf '> - %s → [ADR-%s](%s)\n' "$line" "$src" "$(path_of "$src")"
  done <<< "$rows"
  printf '%s\n' "$END_MARK"
}

# 既存ブロックを外し、望むブロックを差し込んだ内容を stdout へ。
# 差し込み位置はヘッダ（タイトル＋箇条書き）の直後 —— 2行目以降で最初に現れる
# `---` か `## ` の手前。Status を読む目が、そのまま次に読む場所になる。
# ブロックは複数行なので awk -v では渡さない（BSD awk は値の中の改行で落ちる。
# その落ち方は「空の出力」なので、気づかずに元ファイルを空にできてしまう）。
render() { # $1=ファイル $2=ブロックの入ったファイル(空ファイルなら削除のみ)
  awk -v bf="$2" -v b="$BEGIN_MARK" -v e="$END_MARK" '
    BEGIN {
      inblk=0; done=0; block=""
      while ((getline l < bf) > 0) { block = block l "\n" }
      sub(/\n$/, "", block)
    }
    $0 == b { inblk=1; next }
    inblk { if ($0 == e) { inblk=0; skipblank=1 } next }
    skipblank && $0 == "" { skipblank=0; next }
    {
      skipblank=0
      if (!done && NR>=2 && ($0 == "---" || $0 ~ /^## /)) {
        if (block != "") { printf "%s\n\n", block }
        done=1
      }
      print
    }
  ' "$1"
}

tmpblk=$(mktemp) || exit 1
tmpout=$(mktemp) || exit 1
trap 'rm -f "$tmpblk" "$tmpout"' EXIT

changed=""
for f in "${files[@]}"; do
  n=${f#ADR-}; n=${n%%-*}
  block_for "$n" > "$tmpblk" || : > "$tmpblk"
  if ! render "$f" "$tmpblk" > "$tmpout"; then
    echo "生成に失敗した: $f" >&2
    exit 1
  fi
  # 生成が空振りしたときに元を消さない。awk が落ちる形は「空の出力」で、
  # そのまま書けば ADR が消える。行数では見ない —— 宣言を減らせば正当に縮む。
  if [ ! -s "$tmpout" ] || [ "$(head -1 "$tmpout")" != "$(head -1 "$f")" ]; then
    echo "生成結果が壊れている。書き込まない: $f" >&2
    exit 1
  fi
  if ! cmp -s "$tmpout" "$f"; then
    changed="${changed}  ${f}"$'\n'
    [ "$mode" = check ] || cp "$tmpout" "$f"
  fi
done

marked=$(printf '%s' "$decls" | awk -F'\t' '{print $1}' | sort -u | grep -c '' || true)
declared=$(printf '%s' "$decls" | grep -c '' || true)

if [ "$mode" = check ]; then
  if [ -n "${changed//[[:space:]]/}" ]; then
    {
      echo "改版マークが陳腐化している:"
      printf '%s' "$changed" | sed '/^[[:space:]]*$/d'
      echo
      echo "make docs-sync で再生成してください。"
    } >&2
    exit 1
  fi
  echo "改版マーク: 最新 (${declared}件の改版を ${marked}本のADRへ反映済み)"
else
  if [ -n "${changed//[[:space:]]/}" ]; then
    echo "改版マークを更新した:"
    printf '%s' "$changed" | sed '/^[[:space:]]*$/d'
  else
    echo "改版マーク: 変更なし"
  fi
  echo "改版マーク: ${declared}件の改版を ${marked}本のADRへ反映 (全${#files[@]}本)"
fi

# 隣のリポジトリを跨ぐ改版は、この検査の外にある。黙って通したことにしない。
if [ "$cross_repo" -gt 0 ]; then
  echo "隣のリポジトリを指す改版の記述がある${cross_repo}本は、この検査の対象外（散文のまま）。"
fi
