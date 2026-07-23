#!/bin/bash
# Synthetic long-term dogfood for tomobit — full reproduction script.
# Rebuilds everything from scratch into ./dog.db (deletes an existing one).
#
# Prereqs: go, python3, sqlite3. Never touches the repo or ~/.tomobit.
set -euo pipefail
DOG="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$DOG/../.." && pwd)"
source "$DOG/env.sh"
rm -f "$DB" "$DB"-wal "$DB"-shm

(cd "$REPO" && go build -o "$TB" ./cmd/tomobit)
python3 "$DOG/ollama_stub.py" & STUB=$!
trap 'kill $STUB' EXIT
sleep 0.5
P=(--backend ollama --url http://127.0.0.1:11499 --model stub)

"$TB" rebuild --db "$DB"                       # CP0: schema, stage 0
python3 "$DOG/seed.py" "$DB" ancient           # CP1: dormant cluster (~210d ago)
"$TB" perceive --db "$DB" "${P[@]}"
"$TB" rebuild --db "$DB"; "$TB" status --view json --db "$DB"

python3 "$DOG/seed.py" "$DB" phase1            # CP2: codex era (8-6 weeks ago)
"$TB" perceive --db "$DB" "${P[@]}" >/dev/null
"$TB" rebuild --db "$DB"; "$TB" status --view json --db "$DB"

python3 "$DOG/seed.py" "$DB" phase2            # CP3: reversal (claude era, rust failures)
"$TB" perceive --db "$DB" "${P[@]}" >/dev/null
"$TB" status --db "$DB"

# CP4: decide with stub providers (claude/codex on PATH)
export PATH="$DOG/bin:$PATH"
for i in $(seq 1 24); do
  "$TB" do --db "$DB" --provider auto --cap implement "${P[@]}" \
    "implement golang feature batch $i" </dev/null >/dev/null 2>&1
done
sqlite3 "$DB" "SELECT json_extract(payload,'$.provider') FROM events WHERE type='tomo.decided'" | sort | uniq -c

# CP5: out-of-order perception divergence probe
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta FROM connections ORDER BY 1,2,3" > /tmp/live_before.txt
python3 "$DOG/seed.py" "$DB" backdated
"$TB" perceive --db "$DB" "${P[@]}" >/dev/null
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta FROM connections ORDER BY 1,2,3" > /tmp/live.txt
"$TB" rebuild --db "$DB"
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta FROM connections ORDER BY 1,2,3" > /tmp/rebuilt.txt
diff /tmp/live.txt /tmp/rebuilt.txt || echo "^ FINDING 1: live projection diverged from canonical rebuild"

# CP6: amend -> forget (ADR-0034 no-generation-rollback)
OLD=$(sqlite3 "$DB" "SELECT id FROM experiences WHERE session_id='p2a00' AND kind='execution'")
"$TB" amend --db "$DB" --id "$OLD" --context '{"cap":"implement","lang":"go","topic":"queueing"}'
NEW=$(sqlite3 "$DB" "SELECT id FROM experiences WHERE session_id='p2a00' AND extractor_ver=5")
"$TB" forget --db "$DB" --id "$OLD" --yes && echo "BUG: superseded forget accepted" || true
"$TB" forget --db "$DB" --id "$NEW" --yes
sqlite3 "$DB" "SELECT count(*) FROM experiences WHERE session_id='p2a00'"  # want 0

# CP8: split starvation probe (rust failures pile up, split never fires)
python3 "$DOG/seed.py" "$DB" phase3
"$TB" perceive --db "$DB" "${P[@]}" >/dev/null
(cd "$DOG/replay" && go build -o probebin ./probe && ./probebin "$DB")
