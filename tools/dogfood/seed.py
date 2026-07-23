#!/usr/bin/env python3
"""Synthesize weeks of tomobit sessions by inserting past-ts events directly
into the ledger (tomobit record has no --ts flag; ts is time.Now() there, so
direct INSERT is the only way to backdate truth events).

Event shape mirrors what a real `tomobit do` writes:
  task.started / capability.started / provider.selected
  [user.preference] [provider.error] task.finished

Usage: seed.py <db> <phase>   phase in {ancient, phase1, phase2, backdated}
"""
import json
import sqlite3
import sys
import time

DAY = 86400_000
NOW = int(time.time() * 1000)


def session(db, sid, days_ago, cap, provider, intent, finished, pref=None, perr=False):
    ts = NOW - days_ago * DAY
    evs = [
        ("task.started", {"intent": intent, "source": "production"}),
        ("capability.started", {"capability": cap}),
        ("provider.selected", {"provider": provider}),
    ]
    if pref:
        evs.append(("user.preference", {"preferred": pref[0], "over": pref[1]}))
    if perr:
        evs.append(("provider.error", {"message": "stub failure"}))
    evs.append(("task.finished", finished))
    for i, (typ, payload) in enumerate(evs):
        db.execute(
            "INSERT INTO events (session_id, seq, ts, type, payload) VALUES (?,?,?,?,?)",
            (sid, i + 1, ts + i * 1000, typ, json.dumps(payload)),
        )


OK = {"adopted": "as-is", "reverted": False}
EDITS = {"adopted": "with-edits", "reverted": False}
FAIL = {"adopted": "", "reverted": True}

PHASES = {}

# Ancient cluster (~7 months ago): codex reviews python — dormant-state probe.
PHASES["ancient"] = [
    dict(sid=f"anc{i:02d}", days_ago=d, cap="review", provider="codex",
         intent=f"review the pylang data pipeline batch {i}", finished=OK)
    for i, d in enumerate([215, 212, 210, 208])
]

# Phase 1 (8..6 weeks ago): codex era on cap=implement / lang=go.
PHASES["phase1"] = (
    [dict(sid=f"p1c{i:02d}", days_ago=d, cap="implement", provider="codex",
          intent=f"implement golang worker {i}", finished=OK,
          pref=("codex", "claude-code") if d in (50, 46) else None)
     for i, d in enumerate([56, 54, 52, 50, 48, 46, 44, 42])]
    + [dict(sid=f"p1a{i:02d}", days_ago=d, cap="implement", provider="claude-code",
            intent=f"implement golang handler {i}", finished=FAIL)
       for i, d in enumerate([55, 51, 47, 43])]
    + [dict(sid="p1a90", days_ago=45, cap="implement", provider="claude-code",
            intent="implement golang metrics endpoint", finished=EDITS)]
)

# Phase 2 (4..0 weeks ago): reversal — claude era; codex declines; claude
# fails specifically on rust (split candidate under cap=implement).
PHASES["phase2"] = (
    [dict(sid=f"p2a{i:02d}", days_ago=d, cap="implement", provider="claude-code",
          intent=f"implement golang service piece {i}", finished=OK,
          pref=("claude-code", "codex") if d in (12, 8, 5, 2) else None)
     for i, d in enumerate([26, 24, 22, 20, 17, 14, 12, 8, 5, 2])]
    + [dict(sid=f"p2c{i:02d}", days_ago=d, cap="implement", provider="codex",
            intent=f"implement golang cli flag {i}", finished=FAIL)
       for i, d in enumerate([25, 21, 15, 9, 4])]
    + [dict(sid=f"p2r{i:02d}", days_ago=d, cap="implement", provider="claude-code",
            intent=f"implement rustlang parser stage {i}", finished=FAIL, perr=True)
       for i, d in enumerate([18, 13, 7, 3])]
)

# Backdated session (out-of-order perception probe): finished 30 days ago but
# only lands in the ledger now — the "Ollama was down that day" scenario.
PHASES["backdated"] = [
    dict(sid="late00", days_ago=30, cap="implement", provider="codex",
         intent="implement golang retry queue", finished=OK)
]


# Phase 3: pile up recent rust failures to summon the split judgment on
# cap=implement/claude-code.
PHASES["phase3"] = [
    dict(sid=f"p3r{i:02d}", days_ago=d, cap="implement", provider="claude-code",
         intent=f"implement rustlang codec pass {i}", finished=FAIL, perr=True)
    for i, d in enumerate([6, 5, 4, 3, 2, 1, 0])
]


def main():
    db_path, phase = sys.argv[1], sys.argv[2]
    db = sqlite3.connect(db_path)
    for s in PHASES[phase]:
        session(db, **s)
    db.commit()
    db.close()
    print(f"seeded {phase}: {len(PHASES[phase])} sessions")


if __name__ == "__main__":
    main()

