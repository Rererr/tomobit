#!/usr/bin/env python3
"""Dogfood round 2 seeds: plan-learning history (ADR-0014), a retired plan
variant, and a duel-ready preference gap (ADR-0026 検証 recipe).

Usage: seed2.py <db> <phase>   phase in {planhist, oldplan, duelgap}
"""
import json
import sqlite3
import sys
import time

DAY = 86400_000
NOW = int(time.time() * 1000)

OK = {"adopted": "as-is", "reverted": False}
FAIL = {"adopted": "", "reverted": True}


def session(db, sid, days_ago, cap, provider, intent, finished, plan=None, perr=False):
    ts = NOW - days_ago * DAY
    evs = [
        ("task.started", {"intent": intent, "source": "production"}),
        ("capability.started", {"capability": cap}),
        ("provider.selected", {"provider": provider}),
    ]
    if plan:
        evs.append(("plan.selected", {"plan": plan, "cap": cap, "manual": False}))
    if perr:
        evs.append(("provider.error", {"message": "stub failure"}))
    evs.append(("task.finished", finished))
    for i, (typ, payload) in enumerate(evs):
        db.execute(
            "INSERT INTO events (session_id, seq, ts, type, payload) VALUES (?,?,?,?,?)",
            (sid, i + 1, ts + i * 1000, typ, json.dumps(payload)),
        )


def event(db, sid, days_ago, typ, payload):
    db.execute(
        "INSERT INTO events (session_id, seq, ts, type, payload) VALUES (?,?,?,?,?)",
        (sid, 1, NOW - days_ago * DAY, typ, json.dumps(payload)),
    )


PLAN_FULL = "analyze>implement>test>review"
PLAN_DIRECT = "implement>test"
PLAN_QUICK = "implement"
PLAN_OLD = "implement>test>review"  # first drop-mutation of FULL

PHASES = {}

# Recent plan history: three initial variants used on cap=implement/lang=go,
# differentiated outcomes so kind='plan' connections carry real posteriors.
PHASES["planhist"] = (
    [dict(sid=f"plf{i:02d}", days_ago=d, cap="implement", provider="claude-code",
          intent=f"implement golang module f{i}", finished=OK, plan=PLAN_FULL)
     for i, d in enumerate([20, 16, 12])]
    + [dict(sid=f"pld{i:02d}", days_ago=d, cap="implement", provider="claude-code",
            intent=f"implement golang module d{i}", finished=OK, plan=PLAN_DIRECT)
       for i, d in enumerate([18, 14, 10, 6])]
    + [dict(sid=f"plq{i:02d}", days_ago=d, cap="implement", provider="claude-code",
            intent=f"implement golang module q{i}", finished=FAIL, plan=PLAN_QUICK)
       for i, d in enumerate([19, 11, 5])]
)

# A proposed variant born ~200 days ago and never used since: its connection
# goes dormant (>2 half-lives quiet) => retired from the menu (Decision 5),
# and its name must stay permanently spent for Propose (commit 01c00c7).
PHASES["oldplan"] = [
    dict(sid=f"plo{i:02d}", days_ago=d, cap="implement", provider="claude-code",
         intent=f"implement golang legacy piece {i}", finished=OK, plan=PLAN_OLD)
    for i, d in enumerate([205, 202, 200])
]

# Duel gap (ADR-0026 検証): both providers with the same 4 adopted / 1
# reverted record on cap=implement, recent, no lang keyword in the intent so
# the scope stays exactly {cap=implement}.
PHASES["duelgap"] = (
    [dict(sid=f"dgc{i:02d}", days_ago=d, cap="implement", provider="claude-code",
          intent=f"implement service piece c{i}", finished=(FAIL if i == 4 else OK))
     for i, d in enumerate([10, 8, 6, 4, 2])]
    + [dict(sid=f"dgx{i:02d}", days_ago=d, cap="implement", provider="codex",
            intent=f"implement service piece x{i}", finished=(FAIL if i == 4 else OK))
       for i, d in enumerate([9, 7, 5, 3, 1])]
)


def main():
    db_path, phase = sys.argv[1], sys.argv[2]
    db = sqlite3.connect(db_path)
    if phase == "oldplan":
        event(db, "plgen0", 206, "plan.generated",
              {"cap": "implement", "plan": PLAN_OLD, "parent": PLAN_FULL, "op": "drop"})
    for s in PHASES[phase]:
        session(db, **s)
    db.commit()
    db.close()
    print(f"seeded {phase}: {len(PHASES[phase])} sessions")


if __name__ == "__main__":
    main()
