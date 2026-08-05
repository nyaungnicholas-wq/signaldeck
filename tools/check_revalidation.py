#!/usr/bin/env python3
"""Gate: the structural revalidation must have been run, recently.

WHY THIS EXISTS. `STRATEGY_DECK.md` §8 recorded the defect in one sentence:
"Walk-forward validation (`tools/revalidate_structural.py`) runs on demand only.
Nothing schedules it and no gate consumes its output." A validation nobody reads
is indistinguishable from one that was never run, and this repository has found
that shape repeatedly — `TradableAt` returning empty for its whole life,
`MarkDelisted` with no callers, a docs gate that no CI job invoked.

THE SPLIT, and why it matches `ops/data-integrity.json`. The database is ~4 GB
and gitignored, so a CI runner cannot re-run the revalidation. The answer is
computed where the database lives and committed as `ops/revalidation-status.json`;
this gate verifies the committed answer is present and fresh. A MISSING OR STALE
SNAPSHOT IS A VIOLATION, NEVER A SKIP — that is the whole reason the split is
safe, because it cannot degrade into silence.

    python tools/check_revalidation.py

Exit 0 clean, 1 violation. Reads two files, writes nothing.
"""
from __future__ import annotations

import datetime as dt
import json
import os
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
STATUS = os.path.join(REPO, "ops", "revalidation-status.json")

# Monthly cadence with a week of slack. The structural horizon is 21 trading
# days, so a revalidation older than that is describing a different sample than
# the one the platform is now carrying.
MAX_AGE_DAYS = 35


def main() -> int:
    if not os.path.exists(STATUS):
        print(f"FAIL: {os.path.relpath(STATUS, REPO)} is missing. Run:\n"
              f"  python tools/revalidate_structural.py --json ops/revalidation-status.json\n"
              f"on the machine holding the database, and commit the result.",
              file=sys.stderr)
        return 1
    try:
        with open(STATUS, encoding="utf-8") as f:
            d = json.load(f)
    except (OSError, ValueError) as e:
        print(f"FAIL: {os.path.relpath(STATUS, REPO)} is unreadable: {e}", file=sys.stderr)
        return 1

    gen = d.get("generated")
    if not gen:
        print("FAIL: revalidation snapshot carries no `generated` timestamp, so its "
              "age cannot be checked and it cannot be trusted to be current.",
              file=sys.stderr)
        return 1
    try:
        when = dt.datetime.fromisoformat(gen)
    except ValueError:
        print(f"FAIL: unparseable `generated` timestamp {gen!r}", file=sys.stderr)
        return 1
    if when.tzinfo is None:
        when = when.replace(tzinfo=dt.timezone.utc)
    age = (dt.datetime.now(dt.timezone.utc) - when).days
    if age > MAX_AGE_DAYS:
        print(f"FAIL: the structural revalidation is {age} days old "
              f"(limit {MAX_AGE_DAYS}). Re-run it and commit "
              f"ops/revalidation-status.json.", file=sys.stderr)
        return 1

    arms = d.get("arms") or {}
    active = (arms.get("active_only") or {}).get("accuracy")
    clean = (arms.get("survivorship_clean") or {}).get("accuracy")
    if active is None or clean is None:
        print("FAIL: the snapshot is missing the active-only or survivorship-clean "
              "arm, so the survivorship effect cannot be read from it.", file=sys.stderr)
        return 1

    # Not a pass/fail threshold — a mandatory disclosure. The number is printed
    # every run so a drift in the survivorship effect is visible in CI output
    # rather than buried in a JSON file nobody opens.
    delta_pp = (active - clean) * 100
    direction = ("the published claim is INFLATED by excluding dead names"
                 if delta_pp > 0 else
                 "dead names were harder; excluding them was conservative")
    print(f"OK: revalidation {age}d old ({gen})")
    print(f"    active-only {active*100:.2f}%  survivorship-clean {clean*100:.2f}%")
    print(f"    survivorship effect {delta_pp:+.2f}pp — {direction}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
