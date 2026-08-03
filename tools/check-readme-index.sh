#!/usr/bin/env bash
# README索引とADR本文の突き合わせ。
#
# 索引の1行は起草の日に書かれ、そのあと本文だけが動く。ADR-0050 / 0051 は
# 本文が「実装済み」になっても索引が「Accepted・未実装」のまま4週間残っていた。
# 既存の2本（check-adr-links.sh / sync-adr-superseded.sh）はリンク先の実在と
# 改版マークの鮮度しか見ないので、この種のずれは機械検査の外側にあった。
#
# 見るのは3つ:
#
#   1. 網羅   docs/decisions/ の全ADRが索引に1行だけある
#             （過去に ADR-0006 が索引から落ちた）
#   2. Status 索引がStatus語を書いているなら、本文の Status 行と同じ語であること
#   3. 実装   索引と本文が両方とも実装状態を書いているなら、
#             「未実装」か「実装あり」かが一致すること
#
# 3は「実装済み」と「配備済み」の差では落とさない。配備は実装の先の段で、
# 索引が先に進むことがある。落としたいのは**未実装と実装済みの食い違い**だけで、
# そこを越えて語の揺れまで縛ると、書き手が索引に一言足せなくなる。
#
# 2と3は**書いてある側どうし**しか比べない。索引の古い行（ADR-0001〜0038）は
# Statusを書かない書式で、全行に書かせるのは今回の目的ではないため。
# 比べなかった件数は最後に言う —— **見なかったものを「合っている」とは呼ばない**
# （sync-adr-superseded.sh と同じ規律）。
#
# `make docs-check` から呼ばれる。
set -uo pipefail

root=$(git rev-parse --show-toplevel 2>/dev/null) || root=
if [ -z "$root" ]; then
  echo "gitリポジトリの中で実行してください。" >&2
  exit 1
fi
cd "$root" || exit 1

readme=README.md
[ -f "$readme" ] || { echo "$readme が無い。" >&2; exit 1; }

shopt -s nullglob
files=(docs/decisions/ADR-*.md)
if [ ${#files[@]} -eq 0 ]; then
  echo "ADRが1つも見つからない。検査が空振りしている。" >&2
  exit 1
fi

# 索引に1行も見つからないのは、書式が変わったか readme を取り違えたかで、
# どちらも「ずれが無い」ではない。
index_n=$(grep -cE '^- \[ADR-[0-9]{4}\]' "$readme")
if [ "$index_n" -eq 0 ]; then
  echo "$readme にADR索引の行が1つも無い。検査が空振りしている。" >&2
  exit 1
fi

# Status行とその継続行（行頭が空白のもの）を1行に畳む。Status は複数行に
# またがることがあり（ADR-0044 は6行）、状態語が2行目以降に載る。
status_line() { # $1=ファイル
  awk '
    /^- Status:/ && !seen { buf=$0; seen=1; next }
    seen && /^[ \t]/ { buf = buf " " $0; next }
    seen { exit }
    END { if (seen) print buf }
  ' "$1"
}

# 一致した語のうち相異なるものを空白区切りで返す。0個なら空、2個以上なら
# 呼び出し側が「読めない」として落とす。
distinct() { # $1=対象文字列 $2=ERE
  printf '%s' "$1" | grep -oE "$2" | sort -u | tr '\n' ' ' | sed 's/ $//'
}

STATUS_RE='Accepted|Proposed|Superseded|Rejected|Deprecated'
IMPL_RE='未実装|配備済み|実装済み'

errors=""
compared_status=0
compared_impl=0
silent_status=0
silent_impl=0

for f in "${files[@]}"; do
  n=$(basename "$f"); n=${n#ADR-}; n=${n%%-*}

  hits=$(grep -cE "^- \[ADR-${n}\]" "$readme")
  if [ "$hits" -eq 0 ]; then
    errors="${errors}  ADR-${n}: 索引に無い（${f}）"$'\n'
    continue
  fi
  if [ "$hits" -gt 1 ]; then
    errors="${errors}  ADR-${n}: 索引に${hits}行ある（1行にまとめる）"$'\n'
    continue
  fi

  entry=$(grep -m1 -E "^- \[ADR-${n}\]" "$readme")
  status=$(status_line "$f")
  if [ -z "$status" ]; then
    errors="${errors}  ADR-${n}: 本文に「- Status:」の行が無い"$'\n'
    continue
  fi

  body_status=$(distinct "$status" "$STATUS_RE")
  entry_status=$(distinct "$entry" "$STATUS_RE")
  case "$body_status" in
    "") errors="${errors}  ADR-${n}: 本文の Status に状態語（${STATUS_RE}）が無い"$'\n' ;;
    *\ *) errors="${errors}  ADR-${n}: 本文の Status に語が複数ある → ${body_status}"$'\n' ;;
    *)
      case "$entry_status" in
        "") silent_status=$((silent_status + 1)) ;;
        *\ *) errors="${errors}  ADR-${n}: 索引の行に Status 語が複数ある → ${entry_status}"$'\n' ;;
        "$body_status") compared_status=$((compared_status + 1)) ;;
        *) errors="${errors}  ADR-${n}: 索引「${entry_status}」／本文「${body_status}」"$'\n' ;;
      esac
      ;;
  esac

  body_impl=$(distinct "$status" "$IMPL_RE")
  entry_impl=$(distinct "$entry" "$IMPL_RE")
  if [ -z "$body_impl" ] || [ -z "$entry_impl" ]; then
    silent_impl=$((silent_impl + 1))
    continue
  fi
  case "$body_impl$entry_impl" in
    *\ *) errors="${errors}  ADR-${n}: 実装状態の語が複数ある → 索引「${entry_impl}」／本文「${body_impl}」"$'\n'; continue ;;
  esac
  # 未実装かどうかだけを比べる。実装済みと配備済みの差は落とさない。
  [ "$body_impl" = 未実装 ] && body_impl=未実装 || body_impl=実装あり
  [ "$entry_impl" = 未実装 ] && entry_impl=未実装 || entry_impl=実装あり
  if [ "$body_impl" != "$entry_impl" ]; then
    errors="${errors}  ADR-${n}: 索引は「${entry_impl}」・本文は「${body_impl}」"$'\n'
    continue
  fi
  compared_impl=$((compared_impl + 1))
done

# 索引が指すADRが実在するかは check-adr-links.sh が見ているので、ここでは
# 索引の行数と本数の食い違いだけを言う（重複は上のループが個別に落とす）。
if [ "$index_n" -ne ${#files[@]} ]; then
  errors="${errors}  索引${index_n}行に対しADRは${#files[@]}本ある（実在しないADRの行が混じっている）"$'\n'
fi

if [ -n "${errors//[[:space:]]/}" ]; then
  {
    echo "README索引とADR本文がずれている:"
    printf '%s' "$errors" | sed '/^[[:space:]]*$/d'
    echo
    echo "本文の Status 行が正で、索引をそちらへ合わせてください。"
  } >&2
  exit 1
fi

echo "README索引: ADR${#files[@]}本すべてが索引にある (Status一致 ${compared_status}本 / 実装状態一致 ${compared_impl}本)"

# 比べなかったものを、黙って通したことにしない。
if [ "$silent_status" -gt 0 ] || [ "$silent_impl" -gt 0 ]; then
  echo "索引が書いていないので比べなかった: Status ${silent_status}本 / 実装状態 ${silent_impl}本"
fi
