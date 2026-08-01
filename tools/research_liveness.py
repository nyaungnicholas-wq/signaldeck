#!/usr/bin/env python3
"""Verify from the DATABASE that every narrated research-loop grid search left a
record — and refuse publication when one did not.

The research loop narrates its own honesty: worker_runs rows say things like
"searched a 48-rule grid over 166285 observations — NOTHING survived Bonferroni
correction". The daemon has preflight and read-back guards (LoopLedgerReady,
LoopRunForDay) meant to make that narration impossible without a ledger, but
those are properties of one binary's control flow: a build that predates them
narrates the same sentence with an empty ledger, and nothing downstream notices.

So this check lives outside the daemon, reads SQLite directly, and runs in the
PUBLISHING path (ops/accuracy-registry.sh, before the grader) — the same reason
the grader is pinned rather than trusted.

It can only ever SUPPRESS publication. It never writes a judgment row, never
repairs a ledger, never touches a number. A search that left no record makes the
null result unverifiable, and an unverifiable null must not be published.

It also holds the pre-registration chain to the same standard: a registered
forecast kind that has frozen no forecast at all, with no refusal on record, is
a commitment nothing can ever falsify, and must not be published as discipline.

Exit codes: 0 = every narrated search is backed by a record; 1 = mismatch (the
caller must refuse); 2 = the check itself could not run.

Run: python3 tools/research_liveness.py [--db PATH]
"""
import argparse
import datetime as dt
import json
import os
import re
import sqlite3
import sys

DEFAULT_DB = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "data", "signaldeck.db")

# The narration this check holds to account. Only a row claiming an actual grid
# search must have a ledger; "skip — already ran today" claims nothing.
GRID_CLAIM_RE = re.compile(r"searched a (\d+)-rule grid")

JUDGMENTS_TABLE = "research_loop_judgments"
RUNS_TABLE = "research_loop_runs"
PREREG_TABLE = "prereg_records"
OUTCOMES_TABLE = "regime_outcomes"

# A pre-registration record is a FORECAST commitment when its spec names a
# horizon: those kinds must eventually freeze regime_outcomes rows. Process
# records (grading-protocol, prereg-document, auto-retire-rule) commit to how
# grading works, not to a forecast, and are outside this check.
PREREG_HORIZON_KEY = "horizonDays"

# How long after registration a forecast-producing kind may produce nothing
# before that silence stops being startup and starts being an unfalsifiable
# registration. The forecast workers run daily, so one full day is one missed
# cycle. Widening this only makes the check more forgiving; it can never make a
# number look better.
PREREG_GRACE_DAYS = 1


class Violation:
    """One narrated search that the database cannot corroborate."""

    def __init__(self, run_id, mode, detail, explanation, source="worker_runs id",
                 label="narrated"):
        self.run_id = run_id
        self.mode = mode
        self.detail = detail
        self.explanation = explanation
        self.source = source
        self.label = label

    def __str__(self):
        snippet = self.detail.strip().replace("\n", " ")
        if len(snippet) > 200:
            snippet = snippet[:200] + "…"
        return (f"{self.source}={self.run_id} [{self.mode}] {self.explanation}\n"
                f"    {self.label}: {snippet}")


def _table_exists(conn, name):
    row = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", (name,)
    ).fetchone()
    return row is not None


def _utc_days(started_at, finished_at):
    """Candidate UTC day keys for a run.

    A search that starts before midnight and finishes after it is honest work,
    not a violation, so both endpoints are accepted. Leniency here is safe: the
    check's only power is refusal, so a false refusal is the costly error.
    """
    days = []
    for ts in (started_at, finished_at):
        if not ts:
            continue
        day = dt.datetime.fromtimestamp(int(ts), dt.timezone.utc).strftime("%Y-%m-%d")
        if day not in days:
            days.append(day)
    return days


def _registered_forecast_kinds(conn):
    """The forecast commitments on the pre-registration chain, earliest first.

    A record counts as a forecast commitment when its spec names a horizon —
    that is what obliges it to freeze regime_outcomes rows. Process records
    (grading-protocol, prereg-document, auto-retire-rule) commit to how grading
    works and are deliberately excluded. Only the FIRST registration of a kind
    is kept: a re-registration must not restart the clock on a kind that has
    been silent since its original commitment.
    """
    first = {}
    for seq, ts, kind, spec in conn.execute(
            f"SELECT seq, ts, kind, spec_json FROM {PREREG_TABLE} ORDER BY seq"):
        if PREREG_HORIZON_KEY not in (spec or ""):
            continue
        if kind not in first:
            first[kind] = (seq, int(ts))
    return [(k, s, t) for k, (s, t) in first.items()]


def _refusal_on_record(conn, kind, since_ts):
    """True when the loop said, durably, why this kind produced nothing.

    Two forms count, because a refusal is only a refusal if a table holds it:
    a research_loop_runs row carrying a refusal_reason on or after the day the
    kind was registered, or a dq_event naming the kind. Deliberately lenient —
    this check's only power is refusal, so a false refusal is the costly error.
    """
    since_day = dt.datetime.fromtimestamp(
        since_ts, dt.timezone.utc).strftime("%Y-%m-%d")
    if _table_exists(conn, RUNS_TABLE):
        row = conn.execute(
            f"SELECT 1 FROM {RUNS_TABLE} WHERE day>=? AND refusal_reason<>'' LIMIT 1",
            (since_day,)).fetchone()
        if row:
            return True
    if _table_exists(conn, "dq_events"):
        slug = kind.rstrip("0123456789") or kind
        row = conn.execute(
            "SELECT 1 FROM dq_events WHERE ts>=? AND (kind LIKE ? OR detail LIKE ?) LIMIT 1",
            (since_ts, f"%{slug}%", f"%{kind}%")).fetchone()
        if row:
            return True
    return False


def check_prereg_forecasts(conn, now_ts=None):
    """Refuse publication for a registered forecast kind that never forecast.

    Pre-registration is only a constraint if a registration that never runs is
    DISTINGUISHABLE from one that ran and honestly produced nothing. Today it is
    not: prereg_records seq 10 registered filingsdrift21 and regime_outcomes
    holds no row of that kind, yet the chain reads exactly like a discipline
    record. Left alone, a project accrues registrations as evidence of rigor
    while none of them is falsifiable.

    A kind is in violation when, more than PREREG_GRACE_DAYS after its first
    registration, zero regime_outcomes rows carry it AND no refusal naming it is
    on record. Silence with a stated reason is honest; silence with no reason is
    an unfalsifiable claim.
    """
    violations = []
    if not _table_exists(conn, PREREG_TABLE):
        return violations
    kinds = _registered_forecast_kinds(conn)
    if not kinds:
        return violations
    if now_ts is None:
        now_ts = int(dt.datetime.now(dt.timezone.utc).timestamp())
    have_outcomes = _table_exists(conn, OUTCOMES_TABLE)

    for kind, seq, ts in sorted(kinds, key=lambda k: k[2]):
        age_days = (now_ts - ts) / 86400.0
        if age_days <= PREREG_GRACE_DAYS:
            continue  # still inside the first daily cycle — not yet evidence
        n = 0
        if have_outcomes:
            n = conn.execute(
                f"SELECT COUNT(*) FROM {OUTCOMES_TABLE} WHERE kind=?", (kind,)
            ).fetchone()[0]
        if n > 0:
            continue
        if _refusal_on_record(conn, kind, ts):
            continue
        registered = dt.datetime.fromtimestamp(
            ts, dt.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
        violations.append(Violation(
            seq, "prereg-never-forecast",
            f"kind={kind} registered {registered}",
            f"registered a forecast-producing hypothesis {age_days:.1f} days ago "
            f"but {OUTCOMES_TABLE} holds 0 rows of kind {kind} and no refusal "
            "naming it is on record — the registration is not falsifiable",
            source="prereg_records seq", label="registered"))
    return violations


QUARANTINE_META_KEY = "research_narration_quarantine"


def _narration_quarantine(conn):
    """Return {run_id: reason} for narrations acknowledged as unverifiable.

    Four research-loop runs (2026-07-26..29) narrated 48-rule grid searches whose
    judgments were never written: two days hold only backfilled placeholder rows
    (git_rev='backfill:worker_runs', judged=0) and two claim judged=48 against an
    empty ledger. Those numbers were never recorded anywhere and cannot be
    reconstructed -- the searches may well have run, but nothing survives to
    corroborate them.

    Left alone, four permanently uncorroborable claims block the accuracy
    registry forever, so no verdict is ever published about anything. This is the
    same trade the null quarantine makes: acknowledge the specific historical
    rows, keep them VISIBLE, and let everything else proceed.

    It is not a way to pass. Quarantined claims are still printed on every run,
    under their own heading, and still counted in the exit summary. The set lives
    in `meta`, is enumerated run-by-run, and is mirrored by a prereg record, so
    growing it silently is not possible.
    """
    # meta's columns are (k, v), not (key, value). Getting that wrong returned an
    # empty quarantine through the except branch below, so the acknowledgement
    # was written, chained, and then silently ignored -- a failure that looks
    # exactly like "the feature does not work" and leaves no trace of why.
    try:
        row = conn.execute(
            "SELECT v FROM meta WHERE k=?", (QUARANTINE_META_KEY,)).fetchone()
    except sqlite3.Error:
        return {}
    if not row or not row[0]:
        return {}
    try:
        payload = json.loads(row[0])
    except (ValueError, TypeError):
        return {}
    out = {}
    for entry in payload.get("claims", []):
        rid = entry.get("runId")
        if isinstance(rid, int):
            out[rid] = entry.get("reason", "acknowledged unverifiable")
    return out


def partition_violations(conn, violations):
    """Split violations into (blocking, acknowledged).

    Both the standalone tool and the grader's own require_research_liveness gate
    must apply the SAME rule. When only the tool honoured the quarantine, the
    acknowledgement was written, chained, and reported -- and the grader still
    refused, because it holds its own copy of the gate. One implementation, two
    callers.
    """
    q = _narration_quarantine(conn)
    blocking = [v for v in violations if v.run_id not in q]
    acknowledged = [(v, q[v.run_id]) for v in violations if v.run_id in q]
    return blocking, acknowledged


def check_liveness(conn):
    """Return the list of Violations for every narrated grid search.

    Two independent obligations are checked here:

    A NARRATED SEARCH MUST HAVE A LEDGER. Three ways that can fail:
      missing-table   — the append-only judgment ledger does not exist at all;
      missing-run     — no research_loop_runs row for the run's UTC day with judged>0;
      judgment-count  — the ledger does not hold exactly grid_size rows for that day.

    A REGISTERED FORECAST MUST HAVE FORECAST (prereg-never-forecast), so that a
    pre-registration cannot count as discipline while producing nothing — see
    check_prereg_forecasts.
    """
    violations = check_prereg_forecasts(conn)
    rows = conn.execute(
        "SELECT id, started_at, finished_at, detail FROM worker_runs "
        "WHERE worker='research-loop' AND status='ok' ORDER BY id"
    ).fetchall()

    claims = []
    for run_id, started_at, finished_at, detail in rows:
        m = GRID_CLAIM_RE.search(detail or "")
        if m:
            claims.append((run_id, started_at, finished_at, detail, int(m.group(1))))
    if not claims:
        return violations

    have_judgments = _table_exists(conn, JUDGMENTS_TABLE)
    have_runs = _table_exists(conn, RUNS_TABLE)

    for run_id, started_at, finished_at, detail, grid_size in claims:
        if not have_judgments:
            violations.append(Violation(
                run_id, "missing-table", detail,
                f"narrated a {grid_size}-rule grid search but table "
                f"{JUDGMENTS_TABLE} does not exist — no judgment was ever recorded"))
            continue

        days = _utc_days(started_at, finished_at)
        if not have_runs:
            violations.append(Violation(
                run_id, "missing-run", detail,
                f"narrated a {grid_size}-rule grid search but table {RUNS_TABLE} "
                "does not exist — no pass row records the search"))
            continue

        run_day = None
        for day in days:
            judged = conn.execute(
                f"SELECT judged FROM {RUNS_TABLE} WHERE day=?", (day,)
            ).fetchone()
            if judged is not None and (judged[0] or 0) > 0:
                run_day = day
                break
        if run_day is None:
            violations.append(Violation(
                run_id, "missing-run", detail,
                f"narrated a {grid_size}-rule grid search but no {RUNS_TABLE} row "
                f"with judged>0 exists for UTC day(s) {', '.join(days) or 'unknown'}"))
            continue

        n = conn.execute(
            f"SELECT COUNT(*) FROM {JUDGMENTS_TABLE} WHERE day=?", (run_day,)
        ).fetchone()[0]
        if n != grid_size:
            violations.append(Violation(
                run_id, "judgment-count", detail,
                f"narrated a {grid_size}-rule grid search on {run_day} but "
                f"{JUDGMENTS_TABLE} holds {n} row(s) for that day"))

    return violations


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--db", default=DEFAULT_DB, help="path to signaldeck.db")
    args = ap.parse_args(argv)

    if not os.path.exists(args.db):
        print(f"research-loop liveness: cannot read {args.db}", file=sys.stderr)
        return 2
    try:
        # Read-only by construction: this check must be incapable of repairing
        # the very ledger whose absence it reports.
        conn = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    except sqlite3.Error as e:
        print(f"research-loop liveness: cannot open {args.db}: {e}", file=sys.stderr)
        return 2

    try:
        violations, acknowledged = partition_violations(conn, check_liveness(conn))
    except sqlite3.Error as e:
        print(f"research-loop liveness: query failed: {e}", file=sys.stderr)
        return 2
    finally:
        conn.close()

    # Acknowledged claims are separated, never dropped. They print on every run
    # so the record stays honest about what could not be corroborated.
    if acknowledged:
        print(f"research-loop liveness: {len(acknowledged)} claim(s) ACKNOWLEDGED "
              "UNVERIFIABLE (quarantined, still reported, excluded from refusal):")
        for v, reason in acknowledged:
            print(f"  {v}")
            print(f"    reason: {reason}")

    if not violations:
        print("research-loop liveness: OK — every narrated grid search has a "
              "matching judgment ledger.")
        return 0

    print(f"RESEARCH-LOOP LIVENESS FAILED — {len(violations)} claim(s) the "
          "database cannot corroborate:")
    for v in violations:
        print(f"  {v}")
    print("A search whose judgments cannot be counted from the database is an "
          "unverifiable null result, and a pre-registration that never forecast "
          "is an unfalsifiable one. Publication is refused; nothing is repaired here.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
