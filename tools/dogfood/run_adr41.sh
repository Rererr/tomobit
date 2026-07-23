#!/bin/bash
# Dogfood 2 / Part 4: ADR-0041 regression with the real binary.
# 1) in-order batch: live apply, no rebuild switch
# 2) out-of-order batch (backdated session): rebuild switch logged, live == canonical
# 3) generation-supersede batch (extractor_ver rolled back, then re-perceived):
#    rebuild switch logged, live == canonical
set -euo pipefail
DOG="$(cd "$(dirname "$0")" && pwd)"
source "$DOG/env.sh"
DB="$DOG/adr41.db"
rm -f "$DB" "$DB"-wal "$DB"-shm
P=(--backend ollama --url http://127.0.0.1:11499 --model stub)

"$TB" rebuild --db "$DB" >/dev/null
python3 "$DOG/seed.py" "$DB" phase1
python3 "$DOG/seed.py" "$DB" phase2

echo "== batch 1: in-order =="
"$TB" perceive --db "$DB" "${P[@]}" >/dev/null 2> "$DOG/adr41_err1.txt"
if grep -q "out-of-order" "$DOG/adr41_err1.txt"; then echo "FINDING: in-order batch triggered rebuild"; else echo "no rebuild switch (correct)"; fi

echo "== batch 2: backdated session lands late (30d ago < newest current) =="
python3 "$DOG/seed.py" "$DB" backdated
"$TB" perceive --db "$DB" "${P[@]}" >/dev/null 2> "$DOG/adr41_err2.txt"
cat "$DOG/adr41_err2.txt"
grep -q "out-of-order batch — rebuilding projections" "$DOG/adr41_err2.txt" \
  && echo "rebuild switch logged (correct)" || echo "FINDING: no rebuild log for out-of-order batch"
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta,ifnull(parent_key,'') FROM connections ORDER BY 1,2,3" > "$DOG/adr41_live2.txt"
"$TB" rebuild --db "$DB" >/dev/null
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta,ifnull(parent_key,'') FROM connections ORDER BY 1,2,3" > "$DOG/adr41_canon2.txt"
diff "$DOG/adr41_live2.txt" "$DOG/adr41_canon2.txt" && echo "out-of-order: live == canonical" || echo "FINDING: divergence after out-of-order batch"

echo "== batch 3: generation supersede (extractor_ver rolled back to 3) =="
# experiences carries an append-only guard trigger; lift it only to forge the
# state an older-generation binary would have left, then restore it.
sqlite3 "$DB" "DROP TRIGGER experiences_no_update;
UPDATE experiences SET extractor_ver=3;
CREATE TRIGGER experiences_no_update BEFORE UPDATE ON experiences
  BEGIN SELECT RAISE(ABORT, 'experiences is append-only'); END;"
"$TB" perceive --db "$DB" "${P[@]}" >/dev/null 2> "$DOG/adr41_err3.txt"
cat "$DOG/adr41_err3.txt"
grep -q "out-of-order batch — rebuilding projections" "$DOG/adr41_err3.txt" \
  && echo "rebuild switch logged (correct)" || echo "FINDING: no rebuild log for supersede batch"
sqlite3 "$DB" "SELECT count(*), extractor_ver FROM experiences GROUP BY extractor_ver"
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta,ifnull(parent_key,'') FROM connections ORDER BY 1,2,3" > "$DOG/adr41_live3.txt"
"$TB" rebuild --db "$DB" >/dev/null
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta,ifnull(parent_key,'') FROM connections ORDER BY 1,2,3" > "$DOG/adr41_canon3.txt"
diff "$DOG/adr41_live3.txt" "$DOG/adr41_canon3.txt" && echo "supersede: live == canonical" || echo "FINDING: divergence after supersede batch"
