#!/bin/bash
# Dogfood 2 / Part 1: Plan learning (ADR-0014, --plan auto).
# Checks: plan.selected -> experiences.plan -> kind='plan' connections;
# retirement (dormant variant leaves the menu); permanent-spend of retired
# names in Propose (commit 01c00c7); 24h proposal budget; live == rebuild.
set -euo pipefail
DOG="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$DOG/../.." && pwd)"
source "$DOG/env.sh"
DB="$DOG/plan.db"
rm -f "$DB" "$DB"-wal "$DB"-shm

P=(--backend ollama --url http://127.0.0.1:11499 --model stub)
export PATH="$DOG/bin:$PATH"

"$TB" rebuild --db "$DB"
python3 "$DOG/seed2.py" "$DB" oldplan
python3 "$DOG/seed2.py" "$DB" planhist
"$TB" perceive --db "$DB" "${P[@]}" >/dev/null

echo "== experiences.plan projection =="
sqlite3 "$DB" "SELECT plan, count(*), sum(json_extract(outcome,'$.reverted')) FROM experiences WHERE kind='execution' GROUP BY plan ORDER BY plan"

echo "== plan probe BEFORE any do (retired variant must be off-menu) =="
(cd "$DOG/replay" && go build -o planprobebin ./planprobe)
"$DOG/replay/planprobebin" "$DB" implement

echo "== do --plan auto #1 (expect proposal analyze>test>review, NOT implement>test>review) =="
"$TB" do --db "$DB" --provider claude-code --plan auto --cap implement "${P[@]}" \
  "implement golang feature planauto 1" </dev/null 2>&1 | grep -E "proposed plan|plan:" || true

echo "== do --plan auto #2 (same day: 24h budget must suppress a second proposal) =="
"$TB" do --db "$DB" --provider claude-code --plan auto --cap implement "${P[@]}" \
  "implement golang feature planauto 2" </dev/null 2>&1 | grep -E "proposed plan|plan:" || true

echo "== plan.generated + plan.selected events =="
sqlite3 "$DB" "SELECT type, json_extract(payload,'$.plan') FROM events WHERE type IN ('plan.generated','plan.selected') ORDER BY id"

echo "== probe after do =="
"$DOG/replay/planprobebin" "$DB" implement

echo "== live vs rebuild (all connections, plan rows included) =="
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta,parent_key FROM connections ORDER BY 1,2,3" > "$DOG/plan_live.txt"
"$TB" rebuild --db "$DB"
sqlite3 "$DB" "SELECT kind,scope_key,target,alpha,beta,parent_key FROM connections ORDER BY 1,2,3" > "$DOG/plan_rebuilt.txt"
if diff "$DOG/plan_live.txt" "$DOG/plan_rebuilt.txt"; then echo "PLAN PROJECTION: live == rebuild"; else echo "FINDING: plan projection diverged"; fi

echo "== menu after rebuild (must be identical: menu is derived from truth) =="
"$DOG/replay/planprobebin" "$DB" implement
