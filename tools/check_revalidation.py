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

FRESHNESS HAS TWO ENDS (audit F06, 2026-09-20). The age test was
`(now - when).days > MAX_AGE_DAYS`, which only ever looked at the old end. A
snapshot dated 2099-01-01 produced `OK: revalidation -26401d old` and exit 0:
`.days` on a negative timedelta truncates toward minus infinity, and a hugely
negative number is comfortably under any ceiling. A snapshot from the future is
not fresh, it is wrong — a clock, a typo or a fabrication — so it is refused,
with a tolerance for real machine skew and nothing more.

    python tools/check_revalidation.py
    python tools/check_revalidation.py --selfcheck

Exit 0 clean, 1 violation. Reads two files, writes nothing.
"""
from __future__ import annotations

import argparse
import contextlib
import datetime as dt
import io
import json
import math
import os
import sys
import tempfile

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
STATUS = os.path.join(REPO, "ops", "revalidation-status.json")

# Monthly cadence with a week of slack. The structural horizon is 21 trading
# days, so a revalidation older than that is describing a different sample than
# the one the platform is now carrying.
MAX_AGE_DAYS = 35

# How far ahead of this machine's clock a snapshot may be stamped. It absorbs
# ordinary skew between the box that computed the snapshot and the box running
# the gate; it is NOT a tolerance for a date in the future, which is a broken
# clock or a forged file either way.
MAX_FUTURE_SKEW_DAYS = 1.0


def _reject_constant(token: str):
    # json.load takes bare NaN/Infinity literals by default. An accuracy that is
    # not a number must not reach the range check as a float that compares
    # false against everything.
    raise ValueError(f"JSON constant {token} is not a number")


def _is_rate(x) -> bool:
    """A finite number in [0, 1]. bool is excluded: it subclasses int."""
    return (isinstance(x, (int, float)) and not isinstance(x, bool)
            and math.isfinite(x) and 0.0 <= x <= 1.0)


def main(status_path: str = STATUS) -> int:
    rel = os.path.relpath(status_path, REPO)
    if not os.path.exists(status_path):
        print(f"FAIL: {rel} is missing. Run:\n"
              f"  python tools/revalidate_structural.py --json ops/revalidation-status.json\n"
              f"on the machine holding the database, and commit the result.",
              file=sys.stderr)
        return 1
    try:
        with open(status_path, encoding="utf-8") as f:
            d = json.load(f, parse_constant=_reject_constant)
    except (OSError, ValueError) as e:
        print(f"FAIL: {rel} is unreadable: {e}", file=sys.stderr)
        return 1

    gen = d.get("generated")
    if not gen:
        print("FAIL: revalidation snapshot carries no `generated` timestamp, so its "
              "age cannot be checked and it cannot be trusted to be current.",
              file=sys.stderr)
        return 1
    try:
        when = dt.datetime.fromisoformat(str(gen).replace("Z", "+00:00"))
    except ValueError:
        print(f"FAIL: unparseable `generated` timestamp {gen!r}", file=sys.stderr)
        return 1
    if when.tzinfo is None:
        when = when.replace(tzinfo=dt.timezone.utc)

    # Float days, not timedelta.days: the latter truncates toward minus infinity,
    # which is what turned a future date into a very small "age".
    age = (dt.datetime.now(dt.timezone.utc) - when).total_seconds() / 86400.0
    if age < -MAX_FUTURE_SKEW_DAYS:
        print(f"FAIL: the revalidation snapshot is dated {abs(age):.1f} days in the "
              f"FUTURE ({gen}). That is a clock error or a forged file, not a fresh "
              f"validation; at most {MAX_FUTURE_SKEW_DAYS:g}d of skew is tolerated.",
              file=sys.stderr)
        return 1
    # Inside the skew band the snapshot is current, and a negative age is noise.
    age = max(age, 0.0)
    if age > MAX_AGE_DAYS:
        print(f"FAIL: the structural revalidation is {age:.1f} days old "
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
    for label, value in (("active_only", active), ("survivorship_clean", clean)):
        if not _is_rate(value):
            print(f"FAIL: {label} accuracy is {value!r}, which is not a finite "
                  f"number in [0, 1]. A malformed snapshot is not a measurement.",
                  file=sys.stderr)
            return 1

    # Not a pass/fail threshold — a mandatory disclosure. The number is printed
    # every run so a drift in the survivorship effect is visible in CI output
    # rather than buried in a JSON file nobody opens.
    #
    # The sentence describes the MEASURED DIFFERENCE and infers nothing about
    # how hard any subgroup was. It used to read "dead names were harder;
    # excluding them was conservative" on every non-positive delta, so 80% active
    # against 85% clean — where including the delisted cohort measured HIGHER —
    # printed the opposite of its own inputs, and a delta of exactly zero got the
    # same sentence (audit F07).
    delta_pp = (active - clean) * 100
    if delta_pp > 0:
        direction = (f"excluding the delisted cohort RAISED measured accuracy by "
                     f"{abs(delta_pp):.2f}pp, so the active-only claim is inflated")
    elif delta_pp < 0:
        direction = (f"including the delisted cohort RAISED measured accuracy by "
                     f"{abs(delta_pp):.2f}pp, so the active-only claim is not "
                     f"inflated by survivorship")
    else:
        direction = "the delisted cohort made no measurable difference"
    print(f"OK: revalidation {age:.1f}d old ({gen})")
    print(f"    active-only {active*100:.2f}%  survivorship-clean {clean*100:.2f}%")
    print(f"    survivorship effect {delta_pp:+.2f}pp — {direction}")
    return 0


# ──────────────────────────────────────────────────────────────────────────────
# Self-check
# ──────────────────────────────────────────────────────────────────────────────

def _snapshot(generated, active=0.80, clean=0.85) -> str:
    return json.dumps({
        "generated": generated,
        "arms": {"active_only": {"accuracy": active},
                 "survivorship_clean": {"accuracy": clean}},
    })


def _run(tmp: str, name: str, text: str) -> tuple[int, str, str]:
    p = os.path.join(tmp, name)
    with open(p, "w", encoding="utf-8") as fh:
        fh.write(text)
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        rc = main(p)
    return rc, out.getvalue(), err.getvalue()


def _ago(days: float) -> str:
    return (dt.datetime.now(dt.timezone.utc) - dt.timedelta(days=days)).isoformat()


def _selfcheck() -> None:
    with tempfile.TemporaryDirectory() as tmp:
        # (a) a fresh snapshot whose clean arm measured HIGHER
        rc, out, err = _run(tmp, "a.json", _snapshot(_ago(1)))
        assert rc == 0, (rc, err)
        assert "not inflated by survivorship" in out, out
        assert "dead names were harder" not in out, out
        assert "-5.00pp" in out, out

        # the other two branches of the same sentence
        rc, out, _ = _run(tmp, "a2.json", _snapshot(_ago(1), active=0.85, clean=0.80))
        assert rc == 0 and "is inflated" in out and "RAISED measured accuracy by 5.00pp" in out, out
        rc, out, _ = _run(tmp, "a3.json", _snapshot(_ago(1), active=0.83, clean=0.83))
        assert rc == 0 and "no measurable difference" in out, out

        # (b) the future
        rc, _, err = _run(tmp, "b.json", _snapshot("2099-01-01T00:00:00Z"))
        assert rc == 1, rc
        assert "future" in err.lower(), err
        # but ordinary clock skew is not a forgery
        rc, out, err = _run(tmp, "b2.json", _snapshot(_ago(-0.25)))
        assert rc == 0, (rc, err)
        assert "0.0d old" in out, out

        # (c) too old
        rc, _, err = _run(tmp, "c.json", _snapshot(_ago(400)))
        assert rc == 1 and "days old" in err, err
        # and the boundary is not crossed by truncation any more
        rc, _, _ = _run(tmp, "c2.json", _snapshot(_ago(MAX_AGE_DAYS + 0.5)))
        assert rc == 1, "a 35.5-day-old snapshot must fail a 35-day limit"

        # (d) out-of-range accuracy
        rc, _, err = _run(tmp, "d.json", _snapshot(_ago(1), active=1.5))
        assert rc == 1 and "not a finite number" in err, err

        # (e) a NaN literal in the file text
        rc, _, err = _run(tmp, "e.json",
                          '{"generated":"' + _ago(1) + '","arms":'
                          '{"active_only":{"accuracy":NaN},'
                          '"survivorship_clean":{"accuracy":0.85}}}')
        assert rc == 1 and "unreadable" in err, err

        # (f) missing file
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            rc = main(os.path.join(tmp, "does-not-exist.json"))
        assert rc == 1 and "is missing" in err.getvalue(), err.getvalue()


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--selfcheck", action="store_true",
                    help="run built-in assertions and exit")
    args = ap.parse_args()
    if args.selfcheck:
        _selfcheck()
        print("REVALIDATION SELFCHECK OK")
        raise SystemExit(0)
    raise SystemExit(main())
