#!/usr/bin/env python3
"""Accuracy registry — grade every predictor's CLAIM against its LIVE record.

Why this exists
---------------
Every predictor in SignalDeck ships an accuracy number. Some are backtested claims
that have never been graded on live forward data, and one of them — the directional
ensemble — has now accumulated enough live resolutions to show it is significantly
WORSE than a coin flip while still presenting itself as a prediction.

A claimed accuracy that nothing checks is not an accuracy, it is a decoration. This
enumerates every predictor and answers one question per row: does the live record
support the number being displayed?

It is strictly READ-ONLY (sqlite `mode=ro`) so it can run against the live database
while the daemon is writing.

Discipline enforced here, learned from the failures this repo already found:
  * INDEPENDENT observations only. Intraday predictions that map to the same forward
    move are collapsed to one row per (symbol, horizon, UTC-day), keeping the latest.
    Pooling them inflates n by ~60x and produces confident nonsense.
  * Per-BAND accuracy, never the population average. A low-conviction forecast quoting
    the all-decisions number is how "83%" ends up attached to a coin flip.
  * Wilson intervals, and a verdict driven by the interval — not the point estimate.
  * PENDING is a real verdict. A forecast whose horizon has not elapsed is not
    evidence, and saying so is the point.
  * PREQUENTIAL null. The majority-class baseline for each day is built from
    days strictly BEFORE it, never the graded window itself — a null computed
    in-sample gets hindsight the model never had.
  * SURVIVORSHIP boundary. symbols.delisted_at only exists since the 2026-07-24
    survivorship wave (store.go), so everything recorded before it was graded
    against a universe seeded from 2026 survivors. Pre-epoch rows never enter a
    tally here. survivorship_clean is then MEASURED, not asserted: over the
    symbols actually contributing graded rows, the flag is true only when every
    one of their listing statuses resolves (delisted_at set, or still active);
    otherwise the row publishes false plus survivorship_reason and coverage.
    Post-epoch attrition is BOUNDED, not declared unmeasurable:
    tools/backfill_delistings.py --survivorship-bound counts symbols that left
    the universe (SEC EDGAR Form 25 record UNION the live detector's stamps)
    against symbols graded, publishes survivorship_bound — max accuracy
    inflation in pp if every dropped symbol had been wrong — in the JSON, and
    the summary prints it beside the design-effect disclosure.
  * HINDSIGHT NULL RETIRED. The hindsight null (best constant guess over the
    finished sample) used information not available at prediction time. It
    ran for exactly one dual-null transition cycle — published beside the
    prequential null with the stricter of the two driving the verdict — and
    was then dropped, as promised. The regrade against the committed repro
    snapshot recorded ZERO verdict changes across the switch
    (audits/2026-07-27-null-transition.md), so the walk-forward prequential
    majority is now the only null a verdict is read against.

Usage:  python3 tools/accuracy_registry.py [--db PATH] [--json OUT]
        python3 tools/accuracy_registry.py --snapshot repro   # grade from the
            committed reproducibility snapshot instead of the (gitignored) DB,
            verifying every CSV against its manifest hash first. This is the
            path an outside reader uses — see REPRODUCE.md.
        python3 tools/accuracy_registry.py --snapshot repro --json out.json
            # same, plus the full-precision JSON a third party diffs against
            # the published accuracy_registry.json in the public anchors repo
            # — the complete outsider procedure (anchors.log digest line,
            # prereg.log chain head) is "Verify the anchors" in REPRODUCE.md.
"""
from __future__ import annotations

import argparse
import csv
import datetime as dt
import hashlib
import json
import math
import os
import sqlite3
import statistics
import sys

DEFAULT_DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          "data", "signaldeck.db")

# Below this many independent observations no verdict is claimed either way.
MIN_INDEPENDENT_N = 30

# Below this many DISTINCT UTC days no interval is published at all. Mirrors
# clusterstat.MinDistinctDays in the Go daemon, and exists for the same reason:
# a between-day variance estimated from three days is not a correction, it is a
# different way to be overconfident.
MIN_DISTINCT_DAYS = 10

# STRUCTURAL predictors only. regime_outcomes freezes one call per
# (symbol, kind, UTC-day) at horizon_days=21, so 21 consecutive call days share
# at least 17 of their 21 forward sessions: they are ONE independent forward
# window wearing 21 costumes. Clustering on the call day therefore still
# pseudo-replicates, exactly the failure PREDICTION_PROCESS.md point 8 names and
# that internal/volregime and the PAIRS study already avoid with non-overlapping
# 63d windows. Structural grading clusters on integer horizon BLOCKS
# (day // horizon_days) instead, and this is the floor on how many
# NON-OVERLAPPING blocks must exist before any interval is published.
# Directional grading is untouched: at horizon 1 a block IS a day, and the
# auto-retire rule's frozen digest still pins MIN_DISTINCT_DAYS.
MIN_DISTINCT_BLOCKS = 10

# --------------------------------------------------------------------------- #
# MULTIPLICITY — what the published interval's error rate actually is
# --------------------------------------------------------------------------- #
#
# Every interval on this surface was a hard-coded z=1.96, i.e. a nominal 95%,
# and that number was never true of the surface as a whole. Two multiplicities
# are paid here and neither was priced:
#
#   * FAMILY. One run publishes ~10 rows simultaneously (directional horizons,
#     their prequential benchmarks, every structural kind, every persistence
#     twin). "The 95% interval" describes ONE of them; the chance that at least
#     one of ten independent 95% intervals excludes its null is ~40%, not 5%.
#   * LOOKS. ops/com.signaldeck.accuracy.plist re-grades the SAME accruing rows
#     every day. Repeatedly re-testing a growing sample and reading a verdict
#     off whichever look crosses the bar is the oldest optional-stopping error
#     there is, and only the FAILED direction had any frozen stopping rule.
#
# researchx.DiscoverConfig.Divisor() already prices the grid and the prior
# searches INSIDE discovery. This is the same discipline applied to the surface
# that actually publishes verdicts.
#
# The correction is strictly interval-WIDENING by construction (see
# corrected_z): it can turn VALIDATED into NO SKILL or HOLDING into WIDE and
# never the reverse, and it cannot move a point estimate by a single basis
# point. It makes the bar harder, and it is fixed by pre-registration rather
# than by outcome — the divisor is a rule frozen on the chain, not a knob.
MAX_ALPHA = 0.05

# The rule itself, frozen on the pre-registration chain and compared
# byte-for-byte against prereg.MultiplicityRule in the Go daemon. A grader
# running a different rule than the chain froze does not grade (see
# grader_registration_error).
#
# NOTE ON THE HALF-ALPHA. The interval is TWO-SIDED — verdict_for reads both
# ends (hi < null -> FAILED, lo > null -> VALIDATED) — so the corrected alpha
# is split across the two tails, exactly as the uncorrected 1.96 = probit(1 -
# 0.05/2) always was. Spending the whole corrected alpha in one tail would make
# the divisor=1 case z=1.645, i.e. NARROWER than what it replaced, which would
# be a loosening dressed as a correction. The explicit floor below makes that
# impossible in either direction.
MULTIPLICITY_RULE = (
    "Published intervals are Bonferroni-corrected for both multiplicities this "
    "surface pays: FAMILY (rows published in the same grading cycle) and LOOKS "
    "(grading cycles taken over the same accruing rows). "
    "divisor = family_size * looks; corrected_alpha = maxAlpha / divisor; "
    "z = probit(1 - corrected_alpha/2), floored at the uncorrected two-sided z "
    "so the correction can only ever WIDEN an interval, never narrow one. "
    "family_size is the number of rows the cycle actually publishes, measured "
    "from the rows themselves, then folded with max() against the largest "
    "family ever published on the grading-look chain and the family the last "
    "published registry JSON declared, so publishing fewer rows can never "
    "refund multiplicity a wider family already spent. "
    "looks is the monotone count of 'grading-look' "
    "records on the pre-registration chain, folded with max() over the record "
    "count, the counters those records carry, and the looks already published "
    "in the registry JSON, so neither log rotation nor a re-cut snapshot can "
    "refund a look already taken."
)

# The uncorrected two-sided z this file used to hard-code, kept as the FLOOR.
_UNCORRECTED_Z = statistics.NormalDist().inv_cdf(1 - MAX_ALPHA / 2)

# Current cycle's multiplicity. Defaults to the uncorrected single-test case so
# that a direct call to wilson() from a test or another tool behaves exactly as
# it did before; main() sizes it from the real family and the real look count
# before any published number is computed.
_MULT = {"family_size": 1, "looks": 1, "divisor": 1,
         "corrected_alpha": MAX_ALPHA, "z": _UNCORRECTED_Z}

# The largest family EVER published on this chain. The look count is already
# monotone; the family term was not, and a divisor that can fall between cycles
# buys statistical power back by publishing less — a kind withheld under
# INSUFFICIENT BLOCKS, a retired ensemble dropping out — which is the same
# optional-stopping failure the look counter exists to price, wearing the other
# half of the product. main() raises this floor from the chain and the last
# published registry before any interval exists; it defaults to 1 so a direct
# call from a test behaves exactly as it did.
_FAMILY_FLOOR = 1

# The chain kind carrying the append-only look counter. Mirrors
# prereg.LookKind in the Go daemon.
LOOK_KIND = "grading-look"


def corrected_z(family_size: int, looks: int) -> tuple[float, int, float]:
    """(z, divisor, corrected_alpha) for a family of `family_size` over `looks`.

    Floored at the uncorrected z: a divisor can only ever make the interval
    wider. Nothing here can be tuned to lift a number — raising the divisor
    strictly loses verdicts.
    """
    divisor = max(1, int(family_size)) * max(1, int(looks))
    alpha = MAX_ALPHA / divisor
    z = max(_UNCORRECTED_Z, statistics.NormalDist().inv_cdf(1 - alpha / 2))
    return z, divisor, alpha


def set_family_floor(n: int) -> int:
    """Raise the monotone family floor. It only ever goes up."""
    global _FAMILY_FLOOR
    _FAMILY_FLOOR = max(_FAMILY_FLOOR, 1, int(n))
    return _FAMILY_FLOOR


def set_multiplicity(family_size: int, looks: int) -> dict:
    """Price this cycle's multiplicity; every later interval uses it.

    The family term is folded with max() against the largest family ever
    published, exactly as chain_looks() folds the look count: a divisor that
    could fall because fewer rows cleared the evidence floors would hand back
    power that a wider family already spent. Folding is strictly
    interval-widening — it can only lose verdicts, and it moves no estimate.
    """
    family_size = max(int(family_size), _FAMILY_FLOOR)
    z, divisor, alpha = corrected_z(family_size, looks)
    _MULT.update({"family_size": max(1, int(family_size)),
                  "looks": max(1, int(looks)), "divisor": divisor,
                  "corrected_alpha": alpha, "z": z})
    return dict(_MULT)


def multiplicity() -> dict:
    """The multiplicity every published interval in this cycle was priced at."""
    return dict(_MULT)


def current_z() -> float:
    return _MULT["z"]


def stamp_multiplicity(rows: list[dict]) -> None:
    """Put the divisor on every row, so no number travels without its price."""
    m = multiplicity()
    for r in rows:
        r["family_size"] = m["family_size"]
        r["looks"] = m["looks"]
        r["divisor"] = m["divisor"]
        r["corrected_alpha"] = m["corrected_alpha"]
        r["ci_z"] = m["z"]


def grade_with_multiplicity(grade_once, looks: int) -> list[dict]:
    """Grade, size the family from what was actually published, regrade.

    Two passes, because family_size is a property of the output. It is a fixed
    point rather than a circularity: WHICH rows get published depends only on
    the evidence floors (n, distinct days, distinct blocks) and never on z, so
    the second pass emits the same rows as the first. The loop only ever raises
    the family size, so if that assumption were ever violated the divisor moves
    in the widening direction.
    """
    rows = grade_once()
    fam = len(rows)
    for _ in range(3):
        set_multiplicity(fam, looks)
        rows = grade_once()
        if len(rows) <= fam:
            stamp_multiplicity(rows)
            return rows
        fam = len(rows)
    set_multiplicity(max(fam, len(rows)), looks)
    rows = grade_once()
    stamp_multiplicity(rows)
    return rows


def chain_looks(con: sqlite3.Connection) -> int:
    """The monotone number of grading looks taken, read off the chain.

    Folded with max() over three sources for the same reason
    ResearchLoop.priorSearches folds meta / worker_runs / the durable append-only
    tables: a look already taken must never be refundable. The record COUNT
    alone would be refunded by a truncated chain; the counters the records carry
    survive a missing row; and both are maxed against whatever the last
    published registry already declared. A read that comes back short can only
    fail to raise the bar, never lower it.
    """
    try:
        rows = con.execute("SELECT spec_json FROM prereg_records WHERE kind = ?",
                           (LOOK_KIND,)).fetchall()
    except sqlite3.OperationalError:
        return 0
    n = len(rows)
    for (blob,) in rows:
        try:
            n = max(n, int(json.loads(blob).get("counter", 0)))
        except (TypeError, ValueError):
            continue
    return n


def chain_families(con: sqlite3.Connection) -> int:
    """The largest family ever published, read off the grading-look chain.

    Same durability argument as chain_looks: a look record carries the family
    the grade it observed published, so the widest family already charged
    survives log rotation and a re-cut snapshot. Look records written before
    the family was carried report 0 and simply fail to raise the floor, which
    is the safe direction.
    """
    try:
        rows = con.execute("SELECT spec_json FROM prereg_records WHERE kind = ?",
                           (LOOK_KIND,)).fetchall()
    except sqlite3.OperationalError:
        return 0
    n = 0
    for (blob,) in rows:
        try:
            n = max(n, int(json.loads(blob).get("family", 0)))
        except (TypeError, ValueError):
            continue
    return n


def published_looks(path: str | None) -> int:
    """Looks already declared by the previously published registry JSON."""
    return _published_int(path, "looks")


def published_family(path: str | None) -> int:
    """Family size already declared by the previously published registry JSON."""
    return _published_int(path, "family_size")


def _published_int(path: str | None, key: str) -> int:
    if not path or not os.path.exists(path):
        return 0
    try:
        with open(path, encoding="utf-8") as f:
            return int(json.load(f).get(key) or 0)
    except (OSError, json.JSONDecodeError, AttributeError, TypeError, ValueError):
        return 0


# --------------------------------------------------------------------------- #
# WHICH GRADER IS THIS, AND WHICH CODE WROTE THE ROWS IT IS GRADING
# --------------------------------------------------------------------------- #
#
# PREREGISTRATION.md §2 says this grader is pinned by commit AND content
# SHA-256, and that an edited grader re-digests differently. Nothing here ever
# checked that. The script had no self-hash at all — its only sha256 work was
# the snapshot manifest — so it would happily publish verdicts while being a
# version no chained record names. A protocol nobody verifies is a protocol
# nobody is bound by, in both directions.
#
# So: at startup this file hashes ITSELF, reads the newest chained
# grading-protocol record, and refuses to run when they disagree, or when the
# record's frozen thresholds disagree with the constants above. Every branch is
# strictly restrictive — it can only stop a verdict from being published.

SELF_PATH = os.path.abspath(__file__)
REPO_ROOT = os.path.dirname(os.path.dirname(SELF_PATH))


def self_sha256() -> str:
    """SHA-256 of this grader, byte-for-byte — the digest the chain must name."""
    with open(SELF_PATH, "rb") as fh:
        return hashlib.sha256(fh.read()).hexdigest()


def newest_grading_protocol(con: sqlite3.Connection) -> dict | None:
    """The newest chained grading-protocol record, parsed. None if absent."""
    try:
        row = con.execute(
            """SELECT seq, spec_json FROM prereg_records WHERE kind = 'grading-protocol'
               ORDER BY seq DESC LIMIT 1""").fetchone()
    except sqlite3.OperationalError:
        return None
    if not row:
        return None
    try:
        rec = json.loads(row[1])
    except (TypeError, ValueError):
        return None
    rec["_seq"] = int(row[0])
    return rec


def grader_registration_error(rec: dict | None, origin: str) -> str | None:
    """The registration check itself, over a parsed grading-protocol record.

    Split out of require_registered_grader so the SNAPSHOT path can run the
    identical check against the record carried in repro/grading_protocol.csv.
    Two callers, one rule: a grader nothing names produces no verdict, whether
    the record was read from the chain in the DB or from the shipped bundle.
    Returns the refusal text, or None if the record registers this grader.
    """
    mine = self_sha256()
    if rec is None:
        return ("UNREGISTERED GRADER: no grading-protocol record in "
                f"{origin}. This grader hashes {mine}; nothing names it, so no "
                "verdict it produces can be tied to a frozen protocol.")
    theirs = rec.get("graderSha256") or ""
    if theirs != mine:
        return ("UNREGISTERED GRADER: the grading protocol in "
                f"{origin} (seq {rec.get('_seq')}) pins graderSha256 "
                f"{theirs or '(absent)'} at commit "
                f"{rec.get('graderCommit') or '(absent)'}, but this file hashes {mine}. "
                "Refusing to grade: the pinned grader and the running grader are "
                "different code, and PREREGISTRATION.md §2 makes the chain "
                "authoritative.")
    expected = {"minIndependentN": MIN_INDEPENDENT_N,
                "minDistinctDays": MIN_DISTINCT_DAYS,
                "minDistinctBlocks": MIN_DISTINCT_BLOCKS,
                # The multiplicity price is a frozen floor like any other: an
                # unregistered maxAlpha or a rule that differs by one byte from
                # the one the chain froze means the published error rate is not
                # the one anybody committed to.
                "maxAlpha": MAX_ALPHA,
                "multiplicityRule": MULTIPLICITY_RULE}
    for key, want in expected.items():
        got = rec.get(key)
        if got != want:
            return ("UNREGISTERED GRADER: the grading protocol in "
                    f"{origin} (seq {rec.get('_seq')}) freezes {key}={got!r} but this "
                    f"grader enforces {want!r}. Refusing to grade: the evidence floor "
                    "deciding the refusals is not the floor that was registered.")
    return None


def require_research_liveness(con: sqlite3.Connection, db_path: str) -> None:
    """Exit non-zero unless every narrated grid search has a judgment ledger.

    ops/accuracy-registry.sh already runs tools/research_liveness.py before the
    grader and refuses to publish when it fails. That guard protects ONE
    script. The artifact it protects — data/accuracy_registry.json — is what
    README.md and the /accuracy page read, and it can also be produced by
    invoking this grader directly, which is how it was produced at
    2026-07-27T01:01 while the liveness check was failing on two claims.

    So the precondition belongs to the GRADER, not to one caller of it. Moving
    it here makes the refusal a property of the artifact: no path produces a
    registry while a narrated search has no judgment ledger, and the shell
    guard becomes a fast pre-check rather than the only one.

    Read-only, like the check it delegates to: this can suppress a publication,
    never repair a ledger.
    """
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    try:
        import research_liveness
    except ImportError as e:  # pragma: no cover - the tool ships beside this one
        sys.exit(f"research-loop liveness: cannot import the checker: {e}")
    try:
        # partition_violations, not check_liveness alone: acknowledged-unverifiable
        # narrations are enumerated in meta and mirrored on the prereg chain, and
        # BOTH gates must honour the same set. When only the standalone tool did,
        # the acknowledgement was written and chained and the grader still refused.
        violations, _acknowledged = research_liveness.partition_violations(
            con, research_liveness.check_liveness(con))
    except sqlite3.OperationalError as e:
        # A database with no worker_runs table has NARRATED nothing, so there is
        # nothing to corroborate — the fixture and snapshot paths are exactly
        # that. Every other query failure is a refusal: an unreadable ledger is
        # indistinguishable from an absent one, and this check exists because
        # they must not be confused. store.Open's schema contract guarantees the
        # table on any real database, so this branch is unreachable in
        # production by construction.
        if "no such table: worker_runs" not in str(e):
            sys.exit(f"research-loop liveness: query failed against {db_path}: {e}")
        return
    except sqlite3.Error as e:
        sys.exit(f"research-loop liveness: query failed against {db_path}: {e}")
    if not violations:
        return
    lines = [f"RESEARCH-LOOP LIVENESS FAILED — {len(violations)} claim(s) the "
             "database cannot corroborate:"]
    lines += [f"  {v}" for v in violations]
    lines.append("Refusing to grade: a search whose judgments cannot be counted "
                 "from the database is an unverifiable null result, and a registry "
                 "published beside one inherits its unverifiability.")
    sys.exit("\n".join(lines))


def require_registered_grader(con: sqlite3.Connection) -> dict:
    """Exit non-zero unless the chain names THIS grader and THESE thresholds.

    Three ways to fail, all of them the same failure wearing different clothes —
    the code deciding the verdicts is not the code the pre-registration froze:

      * no grading-protocol record at all,
      * a record whose graderSha256 is not this file's digest,
      * a record whose minIndependentN / minDistinctDays / minDistinctBlocks
        differ from the module constants that actually gate every refusal.

    minDistinctBlocks is included because it is the floor that decides STRUCTURAL
    verdicts; a chain that froze only the day floor left the strictest gate in
    the grader unregistered and therefore free to move.
    """
    rec = newest_grading_protocol(con)
    err = grader_registration_error(rec, "the pre-registration chain in this database")
    if err:
        sys.exit(err)
    return rec


# --------------------------------------------------------------------------- #
# CODE-REVISION GATE — what produced the rows being graded
# --------------------------------------------------------------------------- #
#
# The daemon has always known its own vcs.revision and has always thrown it
# away: it went into one meta row and nowhere else, so "which binary wrote this
# forecast" was unanswerable for every one of the 247,164 prediction_ledger
# rows in this database. That absence is the root cause of the only
# unrepairable data event here — nothing could be attributed to a build, so
# nothing could be bounded to one.
#
# regime_outcomes.revision / prediction_ledger.revision fix that going FORWARD
# only. Rows frozen before the column existed carry NULL, and NULL is honest:
# their code is not recoverable and must not be invented. So the gate applies
# from the epoch onward, and a post-epoch row whose stamp is missing, "+dirty"
# (vcs.modified=true), or names a commit this repository does not contain
# blocks the verdict for its predictor and prints the offending revision.
#
# ADVANCED 2026-08-04 from 2026-07-27, chained as prereg kind
# "revision-epoch-correction". The 07-27 window opened attribution enforcement
# on the GRADER side while the daemon-side guard still only refused DIRTY and
# UNSTAMPED builds. A clean build whose commit was later rebased away passed
# that guard, so between 07-31 and 08-03 eight unresolvable builds wrote 3,255
# post-epoch ledger rows (5.3% of the window) and the gate stripped the verdict
# from every directional row. Fixed-epoch rows never age out, so that was
# permanent: the flagship could never be graded again.
#
# The epoch moves FORWARD only, and only past the last contaminated row. What
# it changes is the date from which attribution is ENFORCED — not one accuracy
# figure, null, threshold or verdict rule. The runtime hole is closed
# separately by lineage.BuildReachable, wired into the daemon's startup gate;
# without that, advancing the epoch would only buy time before the same
# contamination recurred.
REVISION_EPOCH = dt.date(2026, 8, 4)
REVISION_EPOCH_TS = int(dt.datetime(2026, 8, 4, tzinfo=dt.timezone.utc).timestamp())


def revision_resolvable(rev: str) -> bool:
    """True only when rev names a commit THIS repository actually contains.

    A stamp that git cannot resolve is worth exactly as much as no stamp. When
    git itself cannot be run the answer is False, not True: an unverifiable
    provenance claim must block a verdict, never wave one through.
    """
    if not rev or rev.endswith("+dirty"):
        return False
    try:
        import subprocess
        return subprocess.run(["git", "cat-file", "-e", rev + "^{commit}"],
                              cwd=REPO_ROOT, capture_output=True).returncode == 0
    except Exception:
        return False


# Sentinel key meaning "every predictor in this family is unattributable".
# It exists so the one branch that cannot enumerate offenders per predictor —
# the revision column being absent outright — still FAILS CLOSED. Every other
# provenance branch is strictly restrictive; a branch that let verdicts through
# because it could check less than the others would invert the contract, and it
# is the branch a database written by an older daemon actually takes.
GATE_ALL = "*"
GATE_NO_COLUMN = "(no revision column)"


def revision_gate(con: sqlite3.Connection) -> dict:
    """Per-predictor offending revisions among post-epoch contributing rows.

    Returns {"structure": {kind: [revs]}, "direction": {horizon: [revs]},
             "epoch": ..., "checked": bool}. An empty list means every
    post-epoch row that feeds that predictor names code this repo contains.
    A GATE_ALL key means the whole family is unattributable.
    """
    out = {"structure": {}, "direction": {}, "epoch": REVISION_EPOCH.isoformat(),
           "checked": True}
    cache: dict[str, bool] = {}

    def bad(revs) -> list[str]:
        offenders = []
        for rev in revs:
            key = rev or ""
            if key not in cache:
                cache[key] = revision_resolvable(key)
            if not cache[key]:
                offenders.append(key or "(unstamped)")
        return sorted(set(offenders))

    try:
        rows = con.execute(
            """SELECT kind, revision FROM regime_outcomes
               WHERE ts >= ? GROUP BY kind, revision""", (REVISION_EPOCH_TS,)).fetchall()
    except sqlite3.OperationalError:
        # The column does not exist on this database at all, so no row can be
        # attributed. Every post-epoch verdict is unattributable — refuse the
        # whole family rather than printing a caveat and publishing anyway.
        out["checked"] = False
        out["structure"][GATE_ALL] = [GATE_NO_COLUMN]
        out["direction"][GATE_ALL] = [GATE_NO_COLUMN]
        return out
    by_kind: dict[str, list] = {}
    for kind, rev in rows:
        by_kind.setdefault(kind, []).append(rev)
    for kind, revs in by_kind.items():
        offenders = bad(revs)
        if offenders:
            out["structure"][kind] = offenders

    try:
        led = con.execute(
            """SELECT horizon, revision FROM prediction_ledger
               WHERE predicted_at >= ? GROUP BY horizon, revision""",
            (REVISION_EPOCH_TS,)).fetchall()
    except sqlite3.OperationalError:
        led = []
        out["checked"] = False
        out["direction"][GATE_ALL] = [GATE_NO_COLUMN]
    by_h: dict[str, list] = {}
    for h, rev in led:
        by_h.setdefault(h or "", []).append(rev)
    for h, revs in by_h.items():
        offenders = bad(revs)
        if offenders:
            out["direction"][h] = offenders
    return out


def apply_revision_gate(rows: list[dict], gate: dict) -> list[str]:
    """Strip the verdict from any predictor with unattributable contributing rows.

    Returns the human-readable refusals, for printing. Dropping the field
    entirely (rather than downgrading its text) is deliberate and matches the
    legacy-snapshot path: an absent verdict cannot be quoted, a reworded one can.

    `retire` goes with it. That flag is computed in emit() as
    `v.startswith("FAILED")` — it is a READING of the verdict, not an
    independent measurement — and it is what the daemon's model-health worker
    consumes to stop publishing. Leaving it behind published a machine-actionable
    claim sourced from a verdict this function had just refused to stand behind,
    so the kill switch decided on evidence the grader had disowned: silently
    fail-open while the flag reads false, and silently auto-retire the flagship
    on an unattributable FAILED the moment it read true. The row still carries
    `revision_gate`, which is what the daemon reads instead.
    """
    refusals = []
    for r in rows:
        offenders = None
        if r["family"].startswith("structure"):
            key = r["predictor"].split(PERSISTENCE_SUFFIX)[0]
            offenders = gate["structure"].get(GATE_ALL) or gate["structure"].get(key)
        elif r["family"].startswith("direction"):
            offenders = gate["direction"].get(GATE_ALL)
            if not offenders:
                for h, offs in gate["direction"].items():
                    if h and h != GATE_ALL and h in r["predictor"]:
                        offenders = offs
                        break
        if not offenders:
            continue
        r["revision_gate"] = offenders
        r.pop("verdict", None)
        r.pop("claim_verdict", None)
        r.pop("retire", None)
        if offenders == [GATE_NO_COLUMN]:
            refusals.append(f"  * {r['predictor']}: this database carries no per-row "
                            "revision column, so no contributing row can be attributed "
                            "to any build")
        else:
            refusals.append(f"  * {r['predictor']}: contributing rows written by "
                            f"{', '.join(offenders)} — not a commit in this repository")
    return refusals


# Conviction bands. A predictor's accuracy is only meaningful within its band.
BANDS = [(0.0, 0.5, "all"), (0.5, 0.8, "conv>0.5"), (0.8, 0.9, "conv>0.8"), (0.9, 1.01, "conv>0.9")]

# The daemon commits the prequential-majority BENCHMARK as its own tracked
# predictor: pipeline/predict.go seeds prediction_outcomes rows under the
# "<horizon>#pm" namespace for the same symbols at the same ts as the ensemble,
# so those rows arrive here through the same dedup/survivorship SQL and are
# graded by the same day-clustered machinery. First-class on purpose — a
# baseline that skips the grading rules is not a baseline.
BENCHMARK_SUFFIX = "#pm"

# "<kind>#persist" namespaces the STRUCTURAL naive-persistence benchmark rows —
# the "nothing changes" null frozen at call time beside every regime call.
PERSISTENCE_SUFFIX = "#persist"

# Reliability-diagram bins: fixed-width bins over predicted P(up). The
# high-conviction gate (|p-0.5| >= 0.15) lives in the outer bins, so an
# anti-calibrated conviction tier shows up here — per-bin predicted vs
# realized — while it is still correctable (raise the threshold, isotonic
# recalibration) rather than only when the auto-retire gate fires.
CALIBRATION_BINS = 10

# --------------------------------------------------------------------------- #
# AUTO-RETIRE RULE — pre-registered 2026-07-26, while every directional verdict
# was still INSUFFICIENT (1d: 42.9% vs a 75.0% prequential null; 1w high
# conviction: 33.3% vs 83.3%). Both live rows show negative skill but are
# unfalsifiable until the evidence floors are met — which is exactly the moment
# a kill criterion is cheap to promise and expensive to keep. So it is committed
# NOW, before the data can argue back:
#
#   FAILED-forward. The first time a directional row reaches MIN_INDEPENDENT_N
#   independent observations over MIN_DISTINCT_DAYS distinct UTC days with the
#   upper bound of its effective-N Wilson 95% interval below the prequential
#   null, the verdict is FAILED, the row publishes retire=true in the registry
#   JSON, and the daemon's model-health worker (pipeline/modelhealth.go) stops
#   publishing that horizon's predictions. No grace period, no re-window, no
#   threshold revision after the evidence arrives.
#
# The rule's digest is chained by the prereg registrar (kind "auto-retire-rule",
# daemon/internal/pipeline/prereg.go) and the chain head is pushed to the public
# anchors repo by ops/anchor-publish.sh, so the threshold is provably older than
# the data it will judge. tools/test_accuracy_registry.py and the Go side pin
# the SAME digest constant — the enforced rule and the chained rule are one rule.
AUTO_RETIRE_MODEL = "directional-ensemble"
AUTO_RETIRE_REGISTERED = "2026-07-26"
AUTO_RETIRE_CRITERION = (
    f"The first time a directional row reaches {MIN_INDEPENDENT_N} independent "
    "(symbol, horizon, UTC-day) observations spread over "
    f"{MIN_DISTINCT_DAYS} distinct UTC days, if the upper bound of its "
    "effective-N day-clustered Wilson 95% interval is below the "
    "prequential-majority null, the verdict is FAILED and the row carries "
    "retire=true. No grace period, no re-window, no threshold revision after "
    "the evidence arrives.")
AUTO_RETIRE_ACTION = (
    "The daemon's model-health worker reads retire from "
    "data/accuracy_registry.json and stops publishing the flagged horizon's "
    "predictions. The flag is recomputed on every grade under these same "
    "frozen thresholds; only a record whose interval clears the null lifts it.")


def auto_retire_rule_digest() -> str:
    """Canonical digest of the frozen rule.

    Byte-identical to prereg.AutoRetireRule().Hash() in the Go daemon — same
    field order, same separator — so the chain record and this grader provably
    freeze the same thresholds. Both sides pin the digest in their tests.
    """
    canon = (f"model={AUTO_RETIRE_MODEL}"
             f"|minIndependentN={MIN_INDEPENDENT_N}"
             f"|minDistinctDays={MIN_DISTINCT_DAYS}"
             f"|criterion={AUTO_RETIRE_CRITERION}"
             f"|action={AUTO_RETIRE_ACTION}"
             f"|registered={AUTO_RETIRE_REGISTERED}")
    return hashlib.sha256(canon.encode()).hexdigest()


def auto_retire_rule() -> dict:
    """The frozen rule as published in the registry JSON, digest included."""
    return {
        "model": AUTO_RETIRE_MODEL,
        "minIndependentN": MIN_INDEPENDENT_N,
        "minDistinctDays": MIN_DISTINCT_DAYS,
        "criterion": AUTO_RETIRE_CRITERION,
        "action": AUTO_RETIRE_ACTION,
        "registered": AUTO_RETIRE_REGISTERED,
        "sha256": auto_retire_rule_digest(),
    }

# The day symbols.delisted_at started being recorded (store.go survivorship
# wave). Rows created before this were graded against a survivor-seeded
# universe and are unfit for a published verdict — both graders filter them
# out at the SQL layer. That filter is a property of the QUERY, not of the
# sample: it says nothing about whether the listing history of the symbols
# actually graded is known. survivorship_clean is therefore MEASURED per
# grade by measure_universe_completeness() below, never asserted.
SURVIVORSHIP_EPOCH = dt.date(2026, 7, 24)
SURVIVORSHIP_EPOCH_TS = int(dt.datetime(2026, 7, 24, tzinfo=dt.timezone.utc).timestamp())


def measure_universe_completeness(con: sqlite3.Connection | None,
                                  table: str) -> dict:
    """Fraction of the graded symbols whose listing status is RESOLVABLE.

    Over exactly the symbols contributing resolved post-epoch rows to `table`,
    a symbol counts as resolvable when either symbols.delisted_at is set (it
    left the universe on a known date) or symbols.active = 1 (it is provably
    still listed at grading time). An inactive symbol with no delisted_at is
    NOT resolvable: it stopped being tracked at an unknown moment, which is
    exactly the shape survivorship bias takes. A symbol_id with no symbols row
    is unresolvable too.

    survivorship_clean is true only at complete coverage. Anything short of
    that publishes false plus a reason naming the shortfall, because an
    unverifiable claim of cleanliness is strictly worse than an admitted gap.
    """
    if con is None:
        return {"clean": False, "coverage": None,
                "reason": ("unmeasured — graded from a snapshot, which carries "
                           "per-day tallies but no symbols/listing table")}
    q = f"""
    SELECT COUNT(*),
           SUM(CASE WHEN s.id IS NULL THEN 1 ELSE 0 END),
           SUM(CASE WHEN s.id IS NOT NULL AND s.delisted_at IS NULL
                         AND s.active = 0 THEN 1 ELSE 0 END)
    FROM (SELECT DISTINCT symbol_id FROM {table}
          WHERE resolved_at IS NOT NULL AND ts >= ?) g
    LEFT JOIN symbols s ON s.id = g.symbol_id
    """
    try:
        n, unknown, undated = con.execute(q, (SURVIVORSHIP_EPOCH_TS,)).fetchone()
    except sqlite3.OperationalError as e:
        return {"clean": False, "coverage": None,
                "reason": f"unmeasured — listing status unreadable ({e})"}
    n, unknown, undated = n or 0, unknown or 0, undated or 0
    if not n:
        return {"clean": False, "coverage": None,
                "reason": "unmeasured — no graded post-epoch symbols"}
    resolvable = n - unknown - undated
    cov = resolvable / n
    out = {"clean": resolvable == n,
           "symbols_graded": n,
           "symbols_resolvable": resolvable,
           "symbols_inactive_undated": undated,
           "symbols_unknown": unknown,
           "coverage": cov,
           "method": ("delisted_at set, or active=1 at grading time, over the "
                      "distinct symbols contributing resolved post-epoch rows"),
           "reason": None}
    if not out["clean"]:
        parts = []
        if undated:
            parts.append(f"{undated} inactive symbol(s) with no delisted_at")
        if unknown:
            parts.append(f"{unknown} symbol_id(s) absent from the symbols table")
        out["reason"] = (f"listing status resolvable for {resolvable}/{n} graded "
                         f"symbols ({cov * 100:.1f}%): " + "; ".join(parts))
    return out


def survivorship_stamp(surv: dict | None) -> dict:
    """Row fields carrying the measured (never assumed) survivorship state."""
    # No measurement supplied means the caller had no symbols table to measure
    # against — the snapshot path. Unmeasured, never assumed clean.
    surv = surv or measure_universe_completeness(None, "")
    stamp = {"survivorship_clean": bool(surv.get("clean"))}
    if not stamp["survivorship_clean"]:
        stamp["survivorship_reason"] = surv.get("reason") or "unmeasured"
        stamp["survivorship_coverage"] = surv.get("coverage")
    return stamp

# The amendment that introduced regime_outcomes.naive_label. From this instant
# every frozen structural call must carry a matched naive-persistence baseline,
# and the daemon's store refuses the write otherwise (store.NullAmendmentEpoch).
# A post-epoch row with a NULL baseline therefore means the deployed binary is
# not the source — this script exits non-zero on it so the daily LaunchAgent
# turns the divergence into a visible failure instead of a line of prose.
# Rows BEFORE this instant are left alone on purpose: a persistence label
# computed once the outcome is known is a hindsight baseline, not a null.
NULL_AMENDMENT_EPOCH = dt.date(2026, 7, 27)
NULL_AMENDMENT_EPOCH_TS = int(dt.datetime(2026, 7, 27, tzinfo=dt.timezone.utc).timestamp())

# The kinds whose write path REQUIRES a frozen naive baseline -- byte-identical
# to structuralNullKinds in daemon/internal/store/regimeoutcomes.go.
STRUCTURAL_NULL_KINDS = (
    "trend21", "trend63", "vol21", "liquidity21",
    "trend21-crypto", "liquidity21-crypto",
)


def unmatched_null_clause(con: sqlite3.Connection) -> tuple[str, tuple]:
    """The ONE definition of "a row whose missing baseline means divergence".

    Returns (sql_fragment, params) to append after `WHERE ts >= ? AND
    naive_label IS NULL`, narrowing on the two axes the store's own guard uses:

      1. Only kinds whose write path requires a baseline. filingsdrift21 has no
         resolver that can compute a null for it, so the store never demanded
         one — 7 such rows are exempt by kind, not evidence of divergence.
      2. Not the frozen quarantine — 1,165 enumerated, digest-covered,
         chain-recorded historical rows that keep their NULL label and stay out
         of every denominator.

    null_amendment_probe already narrowed on both and its comment warned this
    was "the third independent copy of this rule; each one that drifts invents
    its own false alarm". fetch_chain_presence was a FOURTH copy that never got
    narrowed: it counted the raw 1,172, so the grader exited 1 every single run
    and ops/accuracy-registry.sh withheld the whole table. The alarm designed to
    catch a diverged binary was firing on rows already formally exempted.

    Rather than narrow the fourth copy and wait for a fifth, both callers now
    share this one.

    The quarantine table is absent from fixtures and repro snapshots. A missing
    exemption list must mean "exempt nothing", never "there is nothing to
    report", so its absence widens the count rather than zeroing it.
    """
    has_quarantine = con.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",
        ("regime_outcome_quarantine",)).fetchone() is not None
    frag = f" AND kind IN ({','.join('?' * len(STRUCTURAL_NULL_KINDS))})"
    if has_quarantine:
        frag += " AND id NOT IN (SELECT outcome_id FROM regime_outcome_quarantine)"
    return frag, tuple(STRUCTURAL_NULL_KINDS)


def null_amendment_probe(con: sqlite3.Connection) -> dict:
    """Read the write-path invariant off the LIVE table, per structural kind.

    The guard that is supposed to make a post-epoch NULL baseline impossible
    lives in the daemon's store. Whether it is RUNNING is not knowable from the
    source; it is knowable from the data. This counts the rows that prove it is
    not, and names the newest one — a recent ts means the divergent binary is
    still writing, which no amount of reading the code path would reveal.

    Returns {"count": n, "newest_ts": ts|None, "kinds": [kind, ...]}.
    """
    out = {"count": 0, "newest_ts": None, "kinds": []}
    try:
        # Mirror the store's guard EXACTLY, on both axes it narrows by:
        #
        #   1. Only kinds whose write path requires a baseline. filingsdrift21
        #      has no resolver that can compute a null for it, so the store
        #      never demanded one and the registry grades it NO BASELINE.
        #   2. Not the frozen quarantine -- an enumerated, digest-covered,
        #      chain-recorded set of historical rows that keep their NULL label
        #      and stay out of every denominator.
        #
        # Without those, this counted 1172 rows -- 1165 already quarantined plus
        # 7 exempt by kind -- and reported the deployed binary as diverged when
        # it was not. That is the third independent copy of this rule in the
        # codebase; each one that drifts invents its own false alarm.
        # The quarantine table is absent from fixtures and from repro snapshots.
        # Referencing it unconditionally raised OperationalError, which the
        # except below turned into count=0 -- silently HIDING divergence instead
        # of reporting it. A missing exemption list must mean "exempt nothing",
        # never "there is nothing to report".
        # Both narrowings now live in unmatched_null_clause, shared with
        # fetch_chain_presence, so the two cannot drift apart again.
        frag, params = unmatched_null_clause(con)
        rows = con.execute(
            "SELECT kind, COUNT(*), MAX(ts) FROM regime_outcomes "
            "WHERE ts >= ? AND naive_label IS NULL" + frag + " GROUP BY kind",
            (NULL_AMENDMENT_EPOCH_TS, *params)).fetchall()
    except sqlite3.OperationalError:
        # No naive_label column at all: the "NOT FROZEN" path already grades
        # every structural kind NO BASELINE, so there is nothing to add here.
        return out
    for kind, n, newest in rows:
        out["count"] += n
        out["kinds"].append(kind)
        if newest is not None and (out["newest_ts"] is None or newest > out["newest_ts"]):
            out["newest_ts"] = newest
    out["kinds"] = sorted(k for k in out["kinds"] if k)
    return out


def apply_null_amendment_probe(rows: list[dict], probe: dict) -> list[str]:
    """Refuse a verdict for every structural kind the probe found contaminated.

    Strictly restrictive, like every other gate here: it can only withhold.
    """
    refusals = []
    if not probe["count"]:
        return refusals
    kinds = set(probe["kinds"])
    for r in rows:
        if not r["family"].startswith("structure"):
            continue
        key = r["predictor"].split(PERSISTENCE_SUFFIX)[0]
        if key not in kinds:
            continue
        r["null_amendment_gate"] = True
        r.pop("verdict", None)
        r.pop("claim_verdict", None)
        refusals.append(f"  * {r['predictor']}: post-amendment rows exist with no frozen "
                        "naive baseline, so the deployed writer is not this source")
    return refusals


def wilson(k: int, n: int, z: float | None = None) -> tuple[float, float]:
    """Wilson score interval — behaves at small n and near 0/1, unlike normal approx.

    z defaults to THIS CYCLE's multiplicity-corrected z (see corrected_z), not
    to a hard-coded 1.96: the surface publishes a family of rows and re-grades
    them daily, so a fixed 95% was never its operating error rate.

    The p = 0 and p = 1 endpoints are returned EXACTLY. They are exact
    algebraically — at p = 1, centre + half = (1 + z²/2n + z²/2n)/(1 + z²/n) = 1
    — and float64 returns 0.9999999999999999 for them instead. That epsilon is
    not cosmetic: verdict_for compares the bound against the null with a strict
    inequality, so an upper bound one ulp under a 100% null read as "the whole
    interval lies below the null" for EVERY record, including a perfect one.
    Handling it here rather than only at the comparison keeps every other
    consumer of this function honest too.
    """
    if z is None:
        z = current_z()
    if n <= 0:
        return (0.0, 0.0)
    p = k / n
    d = 1 + z * z / n
    centre = (p + z * z / (2 * n)) / d
    half = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / d
    lo, hi = max(0.0, centre - half), min(1.0, centre + half)
    if p <= 0:
        lo = 0.0
    if p >= 1:
        hi = 1.0
    return (lo, hi)


def design_effect(days: list[tuple[int, int]]) -> float | None:
    """Measured clustering penalty over per-day (n, hits) tallies.

    Deduplicating to one row per (symbol, UTC-day) removes intraday
    pseudo-replication and leaves the larger problem untouched: on any given day
    ~1,000 symbols share ONE market move. A binomial interval over those rows
    asserts thousands of independent trials in a sample that holds a handful of
    days.

    This is the survey-linearization ("ultimate cluster") variance of a ratio
    estimator, which is what handles the very unequal day sizes here — one day
    holds 7 observations and the next holds 1,046. It is the same estimator as
    clusterstat.DesignEffect in the Go daemon, deliberately: a registry verdict
    and a canary decision must never disagree about the same numbers.

    Returns None when it cannot be measured; never returns below 1.0, because a
    value under 1 is sampling noise and using it would make the interval
    NARROWER than the independence assumption it was brought in to correct.
    """
    k = len(days)
    if k < 2:
        return None
    n = sum(dn for dn, _ in days)
    hits = sum(dh for _, dh in days)
    if n <= 0:
        return None
    p = hits / n
    # A degenerate proportion carries no between-day variance to measure, but it
    # is also the most perfectly clustered sample possible — every day is
    # internally uniform. The honest reading is the worst case, one independent
    # observation per day, not the flattering 1.0 the arithmetic would give.
    if p <= 0 or p >= 1:
        return n / k
    s = sum((dh - dn * p) ** 2 for dn, dh in days)
    cluster_var = k / ((k - 1) * n * n) * s
    binom_var = p * (1 - p) / n
    if binom_var <= 0 or cluster_var <= 0:
        return 1.0
    return max(1.0, cluster_var / binom_var)


def wilson_eff(p: float, eff_n: float, z: float | None = None) -> tuple[float, float]:
    """Wilson interval at an EFFECTIVE sample size (n / design effect).

    Passing the raw row count here is the bug this function exists to prevent.
    z defaults to the cycle's family- and look-corrected z, as in wilson().

    The p = 0 and p = 1 endpoints are exact here for the same reason they are
    exact in wilson(): float64 lands one ulp inside a bound that is algebraically
    ON the boundary, and verdict_for's strict inequality reads that ulp as a
    separated interval.
    """
    if z is None:
        z = current_z()
    if eff_n <= 0:
        return (0.0, 1.0)
    p = min(1.0, max(0.0, p))
    d = 1 + z * z / eff_n
    centre = (p + z * z / (2 * eff_n)) / d
    half = z * math.sqrt(p * (1 - p) / eff_n + z * z / (4 * eff_n * eff_n)) / d
    lo, hi = max(0.0, centre - half), min(1.0, centre + half)
    if p <= 0:
        lo = 0.0
    if p >= 1:
        hi = 1.0
    return (lo, hi)


def horizon_blocks(day_rows: list[tuple[int, int, int]],
                   horizon_days: int) -> list[tuple[int, int, int]]:
    """Fold per-CALL-DAY tallies into non-overlapping forward-horizon blocks.

    day_rows are (day, n, hits) with day the trading-day index the daemon
    folds on (md.TradingDay / trading_day above). Two call days inside the same block share nearly all of their
    forward window, so they are pooled into ONE cluster rather than counted as
    two. Returns (anchor_day, n, hits) sorted by anchor; horizon 1 is identity.

    The blocks are built by a greedy chronological sweep, NOT by a fixed epoch
    grid: a block opens at the first unassigned call day d and absorbs every day
    in [d, d + hd). A calendar grid would split days 1007 and 1008 across two
    "non-overlapping" blocks even though their forward windows share 20 of 21
    sessions — the pseudo-replication this function exists to remove. The sweep
    guarantees consecutive anchors are at least hd days apart, so no forward
    window is ever counted twice.
    """
    hd = max(1, int(horizon_days or 1))
    agg: dict[int, tuple[int, int]] = {}
    for day, n, hits in day_rows:
        pn, ph = agg.get(day, (0, 0))
        agg[day] = (pn + n, ph + hits)
    blocks: list[tuple[int, int, int]] = []
    anchor: int | None = None
    for day in sorted(agg):
        n, h = agg[day]
        if anchor is None or day >= anchor + hd:
            anchor = day
            blocks.append((anchor, n, h))
        else:
            a, pn, ph = blocks[-1]
            blocks[-1] = (a, pn + n, ph + h)
    return blocks


def clustered_ci_blocks(day_rows: list[tuple[int, int, int]],
                        horizon_days: int) -> dict:
    """clustered_ci with the cluster unit set to a non-overlapping horizon block.

    Same estimator, honest unit: the design effect and effective n are measured
    BETWEEN blocks, so overlapping call days can no longer inflate the evidence.
    Publishes distinct_blocks and block_span beside distinct_days, and withholds
    the interval below MIN_DISTINCT_BLOCKS.
    """
    blocks = horizon_blocks(day_rows, horizon_days)
    out = clustered_ci([(n, h) for _b, n, h in blocks],
                       min_clusters=MIN_DISTINCT_BLOCKS,
                       unit="non-overlapping horizon blocks",
                       method="block-clustered-wilson")
    out["distinct_blocks"] = len(blocks)
    out["distinct_days"] = len({d for d, _n, _h in day_rows})
    hd = max(1, int(horizon_days or 1))
    # Anchors are call days, so the span is reported in block-widths: how many
    # non-overlapping forward windows the sample stretches across.
    out["block_span"] = (
        (blocks[-1][0] - blocks[0][0] + hd) // hd if blocks else 0
    )
    out["horizon_days"] = max(1, int(horizon_days or 1))
    return out


def clustered_ci(days: list[tuple[int, int]], min_clusters: int = MIN_DISTINCT_DAYS,
                 unit: str = "distinct days",
                 method: str = "day-clustered-wilson") -> dict:
    """Grade per-day tallies into a publishable, day-resampled interval.

    Returns a dict carrying the interval AND the evidence behind it — distinct
    days, measured design effect, effective n — because "6,957 observations,
    effective 4,153 over 11 days" is the honest description and the row count
    alone is not.

    ci is None when the sample covers fewer than MIN_DISTINCT_DAYS days. That is
    a refusal, not a wide interval, and it must never be rendered as a number.
    """
    n = sum(dn for dn, _ in days)
    hits = sum(dh for _, dh in days)
    out = {
        "n": n,
        "hits": hits,
        "distinct_days": len(days),
        "acc": (hits / n) if n else None,
        "ci": None,
        "design_effect": None,
        "effective_n": None,
        "ci_method": "withheld",
    }
    if n <= 0:
        return out
    if len(days) < min_clusters:
        out["ci_reason"] = (f"withheld: {len(days)}/{min_clusters} {unit} — "
                            "too few to measure between-cluster variance")
        return out
    deff = design_effect(days)
    if deff is None:
        out["ci_reason"] = "withheld: design effect not measurable"
        return out
    eff = n / deff
    lo, hi = wilson_eff(hits / n, eff)
    out["ci"] = [lo, hi]
    out["design_effect"] = deff
    out["effective_n"] = eff
    out["ci_method"] = method
    return out


def prequential_null(days: list[tuple[int, int]]) -> dict:
    """Out-of-sample majority null over chronological per-day (n, ups) tallies.

    The old null was max(base, 1-base) with base computed over the SAME window
    being graded — hindsight the model never had, which retroactively credited
    the null with any mid-window class flip. Here the constant guess for day d
    is the majority class over days strictly BEFORE d, with expected accuracy
    0.5 when there is no prior evidence (day one, or a tied prior). The
    guess-sequence is graded through the same clustered_ci machinery as the
    model it benchmarks.
    """
    null_days: list[tuple[int, float]] = []
    prior_n = prior_ups = 0
    for n, ups in days:
        if prior_n == 0 or prior_ups * 2 == prior_n:
            hits = n / 2  # no majority to lean on yet — a coin flip
        elif prior_ups * 2 > prior_n:
            hits = float(ups)  # constant "up" guess
        else:
            hits = float(n - ups)  # constant "down" guess
        null_days.append((n, hits))
        prior_n += n
        prior_ups += ups
    return clustered_ci(null_days)


# The day fold, mirroring daemon/internal/marketdata/tradingday.go.
#
# The grader and the daemon must agree about what ONE independent observation
# is, or the surface that publishes a number and the surface that produced it
# are counting different things. The daemon folds on md.TradingDay; this is the
# same fold, and test_trading_day_offset_matches_go pins the two constants
# together so a change on one side fails on the other.
#
# The boundary is off UTC midnight because the US extended session closes at
# 20:00 ET — 00:00Z under EDT, 01:00Z under EST — so a midnight cut puts the
# tail of a session in the NEXT day and counts it as a second observation of the
# same move. Measured on the live corpus before the fold moved: 630 phantom
# stock-days, 3.94% of effective N.
TRADING_DAY_OFFSET_SECS = 5 * 3600
SECONDS_PER_DAY = 86400


def trading_day(ts: int) -> int:
    """Fold a unix timestamp to its trading-day index.

    Python's // is already floor division, so this matches the Go side's
    explicit floor without further work.
    """
    if ts is None:
        return None
    return (ts - TRADING_DAY_OFFSET_SECS) // SECONDS_PER_DAY


def register_fold(con: sqlite3.Connection) -> sqlite3.Connection:
    """Make trading_day() callable from SQL on `con`.

    Called at every point of USE rather than only in connect(), because the
    grader is handed connections it did not open — snapshots, in-memory test
    fixtures — and a fold that silently is not registered would fail loudly on
    some paths and not others. Re-registering is a harmless overwrite.
    """
    con.create_function("trading_day", 1, trading_day)
    return con


def connect(path: str) -> sqlite3.Connection:
    if not os.path.exists(path):
        sys.exit(f"database not found: {path}")
    # Same trick the Go store uses: register the fold as a SQL function rather
    # than restating it as inline arithmetic, so SQL and Python cannot drift.
    return register_fold(sqlite3.connect(f"file:{path}?mode=ro", uri=True))


# --------------------------------------------------------------------------- #
# The frozen claim — read from the pre-registration chain, never recomputed
# --------------------------------------------------------------------------- #

def fetch_prereg_claims(con: sqlite3.Connection) -> dict[str, dict]:
    """Newest chained pre-registration record per kind -> its frozen claim.

    The graded universe is ALL decisions of a kind (PREREGISTRATION.md §6), so
    the target is the min-conviction band's claim — Bands[0], floor 0.0 — and
    nothing else. Recomputing the target instead (AVG(historical_accuracy) over
    regime_outcomes) reads a conviction-mix-weighted number out of the same
    table the outcomes live in: it drifts with the mix, after outcomes are
    visible, without breaking a single hash. That would make the chain
    decorative on the one code path that emits verdicts, which is the whole
    thing pre-registration exists to prevent.

    The NEWEST row is deliberate: an amended claim is appended, never updated,
    and grading against a superseded target would ignore a correction that is
    itself on the record.
    """
    try:
        rows = con.execute("""
            SELECT kind, spec_json, spec_hash, ts FROM prereg_records
            WHERE seq IN (SELECT MAX(seq) FROM prereg_records GROUP BY kind)""").fetchall()
    except sqlite3.OperationalError:
        return {}  # reduced/older DBs carry no chain; callers fall back loudly
    out: dict[str, dict] = {}
    for kind, spec_json, spec_hash, ts in rows:
        try:
            bands = json.loads(spec_json).get("bands") or []
        except (json.JSONDecodeError, AttributeError):
            continue
        if not bands:
            continue  # non-claim records (grading protocol, document digests)
        base = min(bands, key=lambda b: b.get("minConviction", 0.0))
        if base.get("minConviction", 0.0) != 0.0:
            continue  # no all-decisions band: refuse to invent one
        out[kind] = {"claimed": float(base["claimedAccuracy"]),
                     "spec_hash": spec_hash, "registered_ts": int(ts)}
    return out


def fetch_chain_presence(con: sqlite3.Connection) -> dict:
    """Verify, by reading the DB, the two anteriority claims the summary makes.

    The report used to assert both as present-tense fact: that every regime
    call carries a naive-persistence label frozen beside it, and that the
    auto-retire rule is on the pre-registration chain. Neither was looked up —
    the digest was recomputed from this script's own constants, which proves
    the constants hash to themselves and nothing about anteriority. A claim of
    priority that no read backs is exactly the failure pre-registration exists
    to prevent, so the summary is now gated on these reads and prints the
    negative when they fail. Where prose and chain disagree, the chain governs.
    """
    out = {"null_frozen": False, "null_coverage": None, "unmatched_nulls": None,
           "retire_rule_chained": False, "retire_rule_chain_seq": None,
           "retire_rule_chain_hash": None}
    try:
        cols = [r[0] for r in con.execute(
            "SELECT name FROM pragma_table_info('regime_outcomes')")]
    except sqlite3.OperationalError:
        cols = []
    if "naive_label" in cols:
        # Column presence is not coverage: a table that has the column but no
        # frozen labels grades NO BASELINE just as surely as one without it.
        total, labelled = con.execute(
            """SELECT COUNT(*), SUM(CASE WHEN naive_label IS NOT NULL THEN 1 ELSE 0 END)
               FROM regime_outcomes WHERE resolved_at IS NOT NULL AND ts >= ?""",
            (SURVIVORSHIP_EPOCH_TS,)).fetchone()
        if total:
            out["null_coverage"] = (labelled or 0) / total
            out["null_frozen"] = (labelled or 0) > 0
        # Write-path invariant, read back: rows frozen at/after the amendment
        # with no baseline cannot exist unless the deployed daemon diverges from
        # source. Counted over ALL such rows, graded or not — a divergence must
        # surface before the rows become due, not after.
        #
        # Narrowed by the SHARED rule: unnarrowed this counted 1,172 (1,165
        # quarantined + 7 exempt by kind) and exited 1 on every run, which made
        # ops/accuracy-registry.sh withhold the entire accuracy table as though
        # the grader had refused. See unmatched_null_clause.
        frag, params = unmatched_null_clause(con)
        out["unmatched_nulls"] = con.execute(
            "SELECT COUNT(*) FROM regime_outcomes "
            "WHERE ts >= ? AND naive_label IS NULL" + frag,
            (NULL_AMENDMENT_EPOCH_TS, *params)).fetchone()[0]
    try:
        row = con.execute(
            """SELECT seq, spec_hash FROM prereg_records WHERE kind LIKE '%retire%'
               ORDER BY seq DESC LIMIT 1""").fetchone()
    except sqlite3.OperationalError:
        row = None
    if row:
        out["retire_rule_chain_seq"] = int(row[0])
        out["retire_rule_chain_hash"] = row[1]
        # Chained under some other thresholds is NOT chained under these ones.
        out["retire_rule_chained"] = row[1] == auto_retire_rule_digest()
    return out


# --------------------------------------------------------------------------- #
# Directional ensemble — the one predictor with a real live record
# --------------------------------------------------------------------------- #

def fetch_directional_days(con: sqlite3.Connection) -> dict[str, list[tuple]]:
    """Per-day tallies behind every directional grade, keyed by horizon.

    Each value is (day, n, correct, up_days, hc_n, hc_correct, hc_up_days) —
    exactly what the reproducibility snapshot (tools/make_repro_snapshot.py)
    exports, so a grade from the DB and a grade from the committed snapshot
    start from identical inputs.
    """
    register_fold(con)
    # Per-DAY tallies, not per-horizon totals. The dedup below still collapses
    # intraday repeats to one row per (symbol, horizon, trading-day); the day
    # grouping is what lets the interval resample days instead of rows.
    q = """
    WITH dedup AS (
      SELECT symbol_id, horizon, prob, up, ts,
             ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, trading_day(ts)
                                ORDER BY ts DESC) rn
      FROM prediction_outcomes
      WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL
        AND ts >= ?  -- survivorship boundary: pre-epoch rows are survivor-seeded
    )
    SELECT horizon, trading_day(ts) AS day,
           COUNT(*),
           SUM(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END),
           SUM(CASE WHEN up = 1 THEN 1 ELSE 0 END),
           SUM(CASE WHEN ABS(prob - 0.5) >= 0.15 THEN 1 ELSE 0 END),
           SUM(CASE WHEN ABS(prob - 0.5) >= 0.15 AND (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END),
           SUM(CASE WHEN ABS(prob - 0.5) >= 0.15 AND up = 1 THEN 1 ELSE 0 END)
    FROM dedup WHERE rn = 1 GROUP BY horizon, day ORDER BY horizon, day
    """
    by_h: dict[str, list] = {}
    for horizon, day, n, hits, ups, hc_n, hc_hits, hc_ups in con.execute(q, (SURVIVORSHIP_EPOCH_TS,)):
        by_h.setdefault(horizon, []).append((day, n, hits, ups, hc_n, hc_hits, hc_ups))
    return by_h


def fetch_calibration_bins(con: sqlite3.Connection) -> dict:
    """Per-horizon reliability-diagram bins: predicted P(up) vs realized up-rate.

    The graded rows above can only say the high-conviction slice is doing worse
    than the base row; they cannot say WHERE the probabilities are wrong. These
    bins can: each holds (mean predicted probability, realized up-frequency, n)
    over the same independent (symbol, horizon, trading-day) observations, post-epoch
    only, so an anti-calibrated conviction tier is visible per-bin — and
    correctable — before the auto-retire gate ever fires.

    n and distinct_days are published beside every bin because a three-row bin
    is noise, not a calibration measurement.
    """
    register_fold(con)
    q = f"""
    WITH dedup AS (
      SELECT symbol_id, horizon, prob, up, ts,
             ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, trading_day(ts)
                                ORDER BY ts DESC) rn
      FROM prediction_outcomes
      WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL
        AND ts >= ?  -- survivorship boundary, same as the graded rows
    )
    SELECT horizon,
           CAST(MIN(prob * {CALIBRATION_BINS}, {CALIBRATION_BINS} - 1) AS INTEGER) AS bin,
           COUNT(*),
           AVG(prob),
           SUM(CASE WHEN up = 1 THEN 1 ELSE 0 END),
           COUNT(DISTINCT trading_day(ts))
    FROM dedup WHERE rn = 1
    GROUP BY horizon, bin ORDER BY horizon, bin
    """
    horizons: dict[str, list[dict]] = {}
    for horizon, b, n, mean_p, ups, days in con.execute(q, (SURVIVORSHIP_EPOCH_TS,)):
        # The prequential-majority benchmark ("<horizon>#pm") is a constant
        # guess, not a probability model — binning it would put a fake
        # perfectly-confident predictor on the reliability diagram.
        if horizon.endswith(BENCHMARK_SUFFIX):
            continue
        horizons.setdefault(horizon, []).append({
            "p_lo": b / CALIBRATION_BINS,
            "p_hi": (b + 1) / CALIBRATION_BINS,
            "mean_predicted": mean_p,
            "realized_up_freq": (ups / n) if n else None,
            "n": n,
            "distinct_days": days,
        })
    return {
        "method": (f"{CALIBRATION_BINS} fixed-width bins over predicted P(up); one "
                   "observation per (symbol, horizon, UTC-day), post-epoch only"),
        "conviction_threshold": 0.15,
        "horizons": horizons,
    }


def grade_directional(con: sqlite3.Connection) -> list[dict]:
    """Grade prediction_outcomes on independent (symbol, horizon, UTC-day) rows."""
    return grade_directional_days(
        fetch_directional_days(con),
        measure_universe_completeness(con, "prediction_outcomes"))


def grade_directional_days(by_h: dict[str, list[tuple]],
                           surv: dict | None = None) -> list[dict]:
    """Grade directional per-day tallies from either the DB or a snapshot."""
    rows = []

    def emit(name: str, band: str, days: list[tuple[int, int, int]], note: str,
             family: str = "direction", retirable: bool = True) -> None:
        g = clustered_ci([(n, hits) for n, hits, _ in days])
        if not g["n"]:
            return
        # The honest null for a directional call is the best constant guess a
        # bettor WITHOUT hindsight could have made — the prequential majority,
        # not the whole window's. Beating 50% still means nothing if up-days
        # run 55%, but the null only learns that rate as the days arrive.
        null_g = prequential_null([(n, ups) for n, _, ups in days])
        # The hindsight null (best constant guess over the finished sample) is
        # RETIRED. It ran for one dual-null transition cycle so any verdict
        # change would be attributable to the null definition alone, and the
        # switchover regrade recorded zero verdict changes
        # (audits/2026-07-27-null-transition.md). Verdicts are read against
        # the walk-forward prequential majority only.
        null_acc = null_g["acc"]
        lo, hi = (g["ci"] if g["ci"] else (None, None))
        v = verdict_for(g["acc"], lo, hi, g["n"], null_acc, None,
                        distinct_days=g["distinct_days"])
        rows.append({
            "predictor": name,
            "family": family,
            "band": band,
            "claimed": None,
            "live_n": g["n"],
            "live_acc": g["acc"],
            "ci": g["ci"],
            "ci_method": g["ci_method"],
            "distinct_days": g["distinct_days"],
            "design_effect": g["design_effect"],
            "effective_n": g["effective_n"],
            "null_prequential": null_acc,
            "null_acc": null_acc,
            "null_method": "prequential-majority (walk-forward)",
            "null_ci": null_g["ci"],
            "skill": g["acc"] - null_acc,
            "verdict": v,
            # The pre-registered FAILED-forward flag (auto-retire rule, chained
            # 2026-07-26). This flag — not a human reading the report — is what
            # the daemon's model-health worker consumes to stop publishing.
            # Benchmark rows are never retirable: the rule was pre-registered
            # for the directional ensemble alone, and a majority-follower that
            # fails its own null is a market observation, not a model to kill.
            "retire": retirable and v.startswith("FAILED"),
            "note": note,
            **survivorship_stamp(surv),
        })

    for horizon, per_day in sorted(by_h.items()):
        if horizon.endswith(BENCHMARK_SUFFIX):
            # The tracked prequential-majority benchmark (pipeline/predict.go,
            # "<horizon>#pm" rows): same dedup, same survivorship boundary,
            # same day-clustered interval, same walk-forward null as the
            # ensemble it shadows. Its skill column reads its live record
            # against the registry's own null — NO SKILL by construction is
            # the honest expected verdict, and any gap between its accuracy
            # and the ensemble's is the ensemble's deficit, measured live.
            base = horizon[:-len(BENCHMARK_SUFFIX)]
            emit(f"prequential-majority ({base})", "all",
                 [(d[1], d[2], d[3]) for d in per_day],
                 "live-committed running-majority benchmark; graded under the "
                 "identical dedup/survivorship rules as the ensemble",
                 family="benchmark", retirable=False)
            continue
        emit(f"directional-ensemble ({horizon})", "all",
             [(d[1], d[2], d[3]) for d in per_day],
             "live forward record; independent symbol-days, day-resampled interval")

    # High-conviction slice — the tier a user would actually act on. Graded PER
    # HORIZON: the same symbol on the same day appears in both the 1d and the 1w
    # record, and pooling them counted one correlated call twice.
    for horizon, per_day in sorted(by_h.items()):
        if horizon.endswith(BENCHMARK_SUFFIX):
            continue  # a constant guess has no conviction tiers
        days = [(d[4], d[5], d[6]) for d in per_day if d[4] > 0]
        if not days:
            continue
        emit(f"directional-ensemble ({horizon}, high conviction)", "|p-0.5|>=0.15",
             days, "the tier a user would actually trade")
    return rows


# --------------------------------------------------------------------------- #
# Structural regime predictors — claims awaiting their first live grade
# --------------------------------------------------------------------------- #

def fetch_structural(con: sqlite3.Connection):
    """Claims plus per-day tallies behind every structural grade.

    Returns (totals, per_day, naive_per_day): totals rows are
    (kind, horizon_days, forecasts_recorded, claimed_accuracy, first_ts),
    per_day maps (kind, horizon_days) -> [(day, n, correct)], and naive_per_day
    the same for the FROZEN naive-persistence null (regime_outcomes.naive_label,
    committed at call time by the regime-outcome worker, never recomputed) —
    the exact shapes the reproducibility snapshot exports.

    The naive tally counts only rows that actually carry a frozen baseline. A
    row without one leaves the benchmark denominator rather than being scored
    as a null miss, which would flatter the model.
    """
    # Totals and first-call time per predictor.
    q = """
    SELECT kind, horizon_days, COUNT(*), AVG(historical_accuracy), MIN(ts)
    FROM regime_outcomes WHERE ts >= ?
    GROUP BY kind, horizon_days ORDER BY kind
    """
    # Resolved outcomes tallied PER CALL-DAY. regime_outcomes is already unique
    # on (symbol_id, kind, day), so each row is one symbol-day — but ~870
    # symbols share each call day, and grading those as 870 independent trials
    # is how a single market day becomes a confident verdict on a 82% claim.
    qd = """
    SELECT kind, horizon_days, day, COUNT(*), SUM(CASE WHEN correct = 1 THEN 1 ELSE 0 END)
    FROM regime_outcomes WHERE resolved_at IS NOT NULL AND ts >= ?
    GROUP BY kind, horizon_days, day ORDER BY kind, day
    """
    per_day: dict[tuple, list[tuple[int, int, int]]] = {}
    for kind, hd, day, n, hits in con.execute(qd, (SURVIVORSHIP_EPOCH_TS,)):
        per_day.setdefault((kind, hd), []).append((day, n, hits or 0))
    totals = [tuple(r) for r in con.execute(q, (SURVIVORSHIP_EPOCH_TS,))]
    # The frozen naive-persistence null, tallied per call-day so it flows
    # through the IDENTICAL clustered_ci path as the model it benchmarks.
    qn = """
    SELECT kind, horizon_days, day, COUNT(*),
           SUM(CASE WHEN naive_label = actual THEN 1 ELSE 0 END)
    FROM regime_outcomes
    WHERE resolved_at IS NOT NULL AND ts >= ? AND naive_label IS NOT NULL
    GROUP BY kind, horizon_days, day ORDER BY kind, day
    """
    naive_per_day: dict[tuple, list[tuple[int, int, int]]] = {}
    try:
        for kind, hd, day, n, hits in con.execute(qn, (SURVIVORSHIP_EPOCH_TS,)):
            naive_per_day.setdefault((kind, hd), []).append((day, n, hits or 0))
    except sqlite3.OperationalError:
        pass  # database predates naive_label: no baseline exists, and rows say so
    return totals, per_day, naive_per_day


def grade_structural(con: sqlite3.Connection) -> list[dict]:
    totals, per_day, naive_per_day = fetch_structural(con)
    return grade_structural_days(totals, per_day, fetch_prereg_claims(con), naive_per_day,
                                 measure_universe_completeness(con, "regime_outcomes"))


def grade_structural_days(totals: list[tuple], per_day: dict[tuple, list[tuple]],
                          prereg_claims: dict[str, dict] | None = None,
                          naive_per_day: dict[tuple, list[tuple]] | None = None,
                          surv: dict | None = None) -> list[dict]:
    """Grade structural claims from either the DB or a snapshot.

    The target C comes from the pre-registration chain (fetch_prereg_claims).
    The DB-derived average that USED to be the target is kept as a separate,
    clearly-labelled diagnostic (`claimed_db_avg`) so the two stay comparable
    and a drift between them is visible rather than silently absorbed. When no
    chain record exists for a kind, the row says so in `claimed_source` instead
    of pretending the number was frozen.

    Each kind is graded TWICE: against the frozen claim (has live accuracy
    decayed?) and against the FROZEN NAIVE-PERSISTENCE NULL (is there any skill
    beyond "nothing changes"?). The null is the load-bearing one — without a
    baseline a structural predictor could never receive a failing verdict, and
    this repo's own liquidity caveat states that naive persistence scores the
    SAME accuracy. `verdict` therefore carries the null comparison; the claim
    comparison is kept beside it as `claim_verdict`, so nothing previously
    reported is lost.
    """
    prereg_claims = prereg_claims or {}
    naive_per_day = naive_per_day or {}
    rows = []
    for kind, hd, total, db_avg, first_ts in totals:
        pre = prereg_claims.get(kind)
        claimed = pre["claimed"] if pre else db_avg
        claimed_source = "prereg chain" if pre else "DB average (NO CHAIN RECORD)"
        # Cluster on non-overlapping horizon blocks, not on call days: at
        # horizon_days=21 twenty-one consecutive call days are one forward
        # window, and grading them as 21 clusters is pseudo-replication.
        g = clustered_ci_blocks(per_day.get((kind, hd), []), hd)
        resolved = g["n"]
        lo = hi = None
        acc = None
        extra = {}
        ng = clustered_ci_blocks(naive_per_day.get((kind, hd), []), hd)
        null_acc = ng["acc"] if ng["n"] >= MIN_INDEPENDENT_N else None
        # Coverage of the matched null: what fraction of the graded rows the
        # model is scored on actually carry a frozen baseline. Anything below
        # 1.0 means the null was measured on a self-selected subset, which is
        # not a matched null and cannot support a skill verdict.
        null_coverage = (ng["n"] / resolved) if resolved else None
        claim_v = None
        if resolved >= MIN_INDEPENDENT_N:
            acc = g["acc"]
            if g["ci"]:
                lo, hi = g["ci"]
            claim_v = verdict_for(acc, lo, hi, resolved, None, claimed,
                                  distinct_blocks=g["distinct_blocks"])
            if null_acc is None:
                # No frozen baseline to grade against. Say so, rather than let
                # the claim comparison masquerade as a verdict on skill.
                v = "NO BASELINE — naive-persistence null not frozen for these calls"
            else:
                v = verdict_for(acc, lo, hi, resolved, null_acc, None,
                                distinct_blocks=g["distinct_blocks"],
                                null_coverage=null_coverage)
            note = "live-graded, interval resampled over non-overlapping horizon blocks"
            extra = {
                "ci_method": g["ci_method"],
                "distinct_days": g["distinct_days"],
                "distinct_blocks": g["distinct_blocks"],
                "block_span": g["block_span"],
                "horizon_days": g["horizon_days"],
                "design_effect": g["design_effect"],
                "effective_n": g["effective_n"],
            }
        else:
            # A horizon-day forecast cannot be graded before its horizon elapses.
            eligible = dt.date.fromtimestamp(first_ts) + dt.timedelta(days=hd)
            v = f"PENDING (first grade {eligible.isoformat()}, {resolved}/{MIN_INDEPENDENT_N} resolved)"
            note = "claim is backtested, not yet a live record"
            # Evidence accrual is visible while still PENDING: how many
            # non-overlapping forward windows exist so far, not just call days.
            extra = {
                "distinct_days": g["distinct_days"],
                "distinct_blocks": g["distinct_blocks"],
                "block_span": g["block_span"],
                "horizon_days": g["horizon_days"],
            }
        rows.append({
            "predictor": kind,
            "family": "structure",
            "band": "all",
            "claimed": claimed,
            # Provenance of C travels with every row: which chained record it
            # came from, and what the old recomputed average would have been.
            "claimed_source": claimed_source,
            "claimed_spec_hash": pre["spec_hash"] if pre else None,
            "claimed_registered_ts": pre["registered_ts"] if pre else None,
            "claimed_db_avg": db_avg,  # DIAGNOSTIC ONLY — never the grading target
            "live_n": resolved,
            "live_acc": acc,
            "ci": [lo, hi] if lo is not None else None,
            "null_prequential": None,
            "null_acc": null_acc,
            "null_method": "naive-persistence (frozen at call time, same resolver)",
            "null_n": ng["n"],
            "null_coverage": null_coverage,
            "null_ci": ng["ci"],
            "skill": (acc - null_acc) if (acc is not None and null_acc is not None) else None,
            "verdict": v,
            "claim_verdict": claim_v,
            "note": note,
            "forecasts_recorded": total,
            **survivorship_stamp(surv),
            **extra,
        })
        # The null as a FIRST-CLASS benchmark row through the identical
        # clustered_ci path, so a reader sees the baseline's own accuracy, its
        # interval and the days behind it instead of a bare number.
        if ng["n"]:
            rows.append({
                "predictor": f"{kind}{PERSISTENCE_SUFFIX}",
                "family": "structure-benchmark",
                "band": "all",
                "claimed": None,
                "live_n": ng["n"],
                "live_acc": ng["acc"],
                "ci": ng["ci"],
                "ci_method": ng["ci_method"],
                "distinct_days": ng["distinct_days"],
                "distinct_blocks": ng["distinct_blocks"],
                "block_span": ng["block_span"],
                "horizon_days": ng["horizon_days"],
                "design_effect": ng["design_effect"],
                "effective_n": ng["effective_n"],
                "null_prequential": None,
                "null_acc": None,
                # How much of the graded model sample this benchmark actually
                # covers — 1.0 is the only value that makes it a matched null.
                "null_coverage": null_coverage,
                "skill": None,
                "verdict": ("BENCHMARK — the frozen naive-persistence null itself"
                            if ng["n"] >= MIN_INDEPENDENT_N
                            else f"BENCHMARK (INSUFFICIENT {ng['n']}/{MIN_INDEPENDENT_N})"),
                "note": ('"nothing changes" guess frozen at call time and graded by the '
                         "same resolver as the call; never recomputed afterwards"),
                "retire": False,
                **survivorship_stamp(surv),
            })
    return rows


# --------------------------------------------------------------------------- #
# Reproducibility snapshot — grading without the (gitignored) database
# --------------------------------------------------------------------------- #

def load_snapshot(snap_dir: str, allow_legacy: bool = False):
    """Load grading inputs from a committed snapshot (see REPRODUCE.md).

    Every CSV is verified against its MANIFEST.json hash before a single row
    is graded — the same canonical scheme as daemon/internal/datasetver — so a
    tampered or hand-edited snapshot refuses to grade rather than quietly
    publishing different numbers.

    COMPLETENESS IS AN INTEGRITY PROPERTY, NOT A CONVENIENCE. The frozen
    grading target (prereg_claims.csv) and the frozen null
    (structural_naive_days.csv) used to be loaded only `if os.path.exists(...)`,
    and grade_structural_days silently fell back to the DB-derived average as
    the target. That fallback recomputes the very number the pre-registration
    exists to freeze — on the ONLY grading path a third party can run. A
    snapshot missing any file the exporter declares now aborts exactly as a
    hash mismatch does. `allow_legacy` (--allow-legacy-snapshot) exists for
    reading pre-freeze archives: it tolerates the absence, but the caller must
    then publish no verdict at all (see main()).
    """
    from make_repro_snapshot import FILES, hash_records  # same tools/ dir

    man_path = os.path.join(snap_dir, "MANIFEST.json")
    if not os.path.exists(man_path):
        sys.exit(f"snapshot manifest not found: {man_path}")
    with open(man_path, encoding="utf-8") as f:
        manifest = {e["file"]: e for e in json.load(f)["files"]}

    missing = [f for f in FILES
               if f not in manifest or not os.path.exists(os.path.join(snap_dir, f))]
    if missing and not allow_legacy:
        sys.exit(f"incomplete snapshot: {snap_dir} is missing {', '.join(sorted(missing))} "
                 "— refusing to grade. The frozen target and frozen null must travel "
                 "with the tallies; without them a grade recomputes the target it was "
                 "supposed to be held to. Re-cut with tools/make_repro_snapshot.py, or "
                 "pass --allow-legacy-snapshot to inspect an archive with NO verdicts.")

    def read(fname: str) -> list[list[str]]:
        path = os.path.join(snap_dir, fname)
        if not os.path.exists(path):
            sys.exit(f"snapshot file missing: {path}")
        with open(path, newline="", encoding="utf-8") as f:
            recs = list(csv.reader(f))[1:]  # drop the header row
        ent = manifest.get(fname)
        if ent is None:
            sys.exit(f"snapshot file not in manifest: {fname}")
        got = hash_records(fname, FILES[fname], recs)
        if got != ent["sha256"]:
            sys.exit(f"snapshot integrity failure: {fname} hashes {got}, manifest "
                     f"says {ent['sha256']} — refusing to grade a modified snapshot")
        return recs

    by_h: dict[str, list] = {}
    for horizon, *nums in read("directional_days.csv"):
        by_h.setdefault(horizon, []).append(tuple(int(x) for x in nums))

    per_day: dict[tuple, list] = {}
    for kind, hd, day, n, hits in read("structural_days.csv"):
        per_day.setdefault((kind, int(hd)), []).append((int(day), int(n), int(hits)))
    totals = [(kind, int(hd), int(total), float(claimed) if claimed else None, int(first_ts))
              for kind, hd, total, claimed, first_ts in read("structural_claims.csv")]

    # The frozen claims travel with the snapshot as a committed copy of the
    # chain's newest record per kind, hashed like every other file. Absence is
    # an abort above; under --allow-legacy-snapshot it leaves the map empty and
    # the caller suppresses verdicts entirely.
    prereg_claims: dict[str, dict] = {}
    if "prereg_claims.csv" not in missing:
        for kind, claimed, spec_hash, ts in read("prereg_claims.csv"):
            prereg_claims[kind] = {"claimed": float(claimed), "spec_hash": spec_hash,
                                   "registered_ts": int(ts)}

    # The frozen naive-persistence null travels the same way.
    naive_per_day: dict[tuple, list] = {}
    if "structural_naive_days.csv" not in missing:
        for kind, hd, day, n, hits in read("structural_naive_days.csv"):
            naive_per_day.setdefault((kind, int(hd)), []).append((int(day), int(n), int(hits)))

    # THE GRADER PIN TRAVELS WITH THE BUNDLE. The DB path refuses to compute a
    # single number until the chain names this exact file and these exact
    # thresholds; this path used to check nothing and publish verdicts anyway,
    # on the only route REPRODUCE.md offers a third party. The record now ships
    # as a manifest-hashed file, so the same refusal applies here. A record that
    # names different code is a hard exit, exactly as in the DB path. An ABSENT
    # record (legacy archive) is not an exit — it leaves protocol None and the
    # caller drops the verdict field entirely, the same treatment a missing
    # frozen target already gets. Do NOT resolve a refusal by writing a new
    # chain record or re-pinning graderSha256: the refusal is the finding.
    protocol = None
    if "grading_protocol.csv" not in missing:
        rows = read("grading_protocol.csv")
        if rows:
            (seq, sha, commit, min_n, min_days, min_blocks,
             max_alpha, mult_rule, looks_col) = rows[0]
            # An empty threshold column means the chained record never froze
            # that floor. It reads back as None so the check below refuses,
            # rather than being filled in from this grader's own constant.
            num = lambda v: int(v) if v != "" else None
            protocol = {"_seq": int(seq), "graderSha256": sha,
                        "graderCommit": commit or None,
                        "minIndependentN": num(min_n),
                        "minDistinctDays": num(min_days),
                        "minDistinctBlocks": num(min_blocks),
                        "maxAlpha": float(max_alpha) if max_alpha != "" else None,
                        "multiplicityRule": mult_rule or None,
                        # The look count travels WITH the bundle, hashed like
                        # every other field. A re-cut snapshot that came back
                        # with fewer looks than the one before it must not be
                        # able to refund a look, so the loader treats this as a
                        # lower bound and main() maxes it against the looks the
                        # published registry already declared.
                        "_looks": num(looks_col) or 0}
        err = grader_registration_error(
            protocol, f"the snapshot bundle {snap_dir}/grading_protocol.csv")
        if err and protocol is not None:
            sys.exit(err)
    return by_h, totals, per_day, prereg_claims, naive_per_day, protocol


# VERDICT_EPS is the smallest interval-vs-null gap that may decide anything.
#
# It is a float64 resolution guard, not a materiality threshold: at 1e-12 it is
# ~4,000x the double-precision epsilon near 1.0 and ~10 orders of magnitude
# below any accuracy difference this platform could measure, so it can only ever
# suppress a verdict that arithmetic noise produced. It is applied SYMMETRICALLY
# in verdict_for — a gap inside the band yields NO SKILL, never VALIDATED and
# never FAILED.
VERDICT_EPS = 1e-12


def verdict_for(acc, lo, hi, n, null_acc, claimed, distinct_days=None,
                null_coverage=None, distinct_blocks=None) -> str:
    """Verdicts come from the interval, never the point estimate.

    The [lo, hi] handed in is the MULTIPLICITY-CORRECTED interval — priced for
    the family of rows this cycle publishes and for every grading look already
    taken (see corrected_z) — not a fixed 95%. Nothing in this function names a
    coverage, and that is deliberate: the widening happens where the interval is
    computed, so every caller inherits it and none can opt out by passing a
    friendlier number. Since the correction only ever widens, it can turn
    VALIDATED into NO SKILL or HOLDING into WIDE and never the reverse.
    """
    if n < MIN_INDEPENDENT_N:
        return f"INSUFFICIENT ({n}/{MIN_INDEPENDENT_N})"
    # No interval, no verdict. A sample spread over too few market days has no
    # measurable between-day variance, and the row count is not a substitute:
    # 408 forecasts resolving on one day are one market observation, however
    # many symbols they cover. Reading a verdict off the point estimate here is
    # exactly the failure the interval discipline exists to prevent.
    if lo is None or hi is None:
        # Structural rows are gated on NON-OVERLAPPING horizon blocks: their
        # call days overlap by construction, so counting days here would let a
        # single forward window pass a ten-cluster floor.
        if distinct_blocks is not None:
            return (f"INSUFFICIENT BLOCKS ({distinct_blocks}/{MIN_DISTINCT_BLOCKS} "
                    "non-overlapping horizon blocks) — no interval, so no verdict")
        if distinct_days is not None:
            return (f"INSUFFICIENT DAYS ({distinct_days}/{MIN_DISTINCT_DAYS} distinct days) — "
                    "no interval, so no verdict")
        return "NO INTERVAL — no verdict"
    # A null measured on only some of the rows the model is measured on is not
    # a matched null: the uncovered rows are self-selected (the baseline was
    # incomputable exactly where the state was degenerate or the history thin),
    # so any skill verdict read off it is a comparison between two different
    # samples. Refuse the verdict rather than qualify it in prose.
    if null_coverage is not None and null_coverage < 1.0:
        return f"PARTIAL BASELINE ({null_coverage:.1%} of graded rows carry a frozen null)"
    # Against a stated null (direction): the whole interval must clear it, by a
    # margin the arithmetic can actually represent.
    #
    # VERDICT_EPS is symmetric on purpose. It cannot hand out a VALIDATED any
    # more than it can hand out a FAILED, so it is not a loosened threshold —
    # it is a refusal to decide anything on a difference smaller than the last
    # bit of a float64. Without it, a null of exactly 1.0 made "hi < null_acc"
    # true for every conceivable record, so FAILED (and with it retire=true) was
    # algebraically constant regardless of the model's accuracy.
    if null_acc is not None:
        if hi < null_acc - VERDICT_EPS:
            return "FAILED — significantly worse than the naive baseline"
        if lo > null_acc + VERDICT_EPS:
            return "VALIDATED — beats baseline"
        return "NO SKILL — indistinguishable from baseline"
    # Against a frozen claim (structure): has live accuracy decayed below it?
    if claimed is not None:
        if hi < claimed - 0.05:
            return f"DECAYED — live materially below the {claimed:.0%} claim"
        if lo >= claimed - 0.05:
            return f"HOLDING — live supports the {claimed:.0%} claim"
        return f"WIDE — cannot confirm or reject the {claimed:.0%} claim yet"
    return "UNGRADED"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--json", help="write the registry as JSON here")
    ap.add_argument("--snapshot", metavar="DIR",
                    help="grade from a committed reproducibility snapshot (repro/) "
                         "instead of the gitignored DB; verifies manifest hashes first")
    ap.add_argument("--allow-legacy-snapshot", action="store_true",
                    help="read a snapshot cut before the frozen target/null shipped. "
                         "Structural rows are stamped 'DB average (NO CHAIN RECORD)' and "
                         "NO verdict is published from such a snapshot.")
    args = ap.parse_args()

    # Where a previously published registry would be, read BEFORE anything is
    # graded: it is one of the durable sources the look counter is maxed over.
    reg_path = args.json or os.path.join(os.path.dirname(DEFAULT_DB),
                                         "accuracy_registry.json")

    if args.snapshot:
        by_h, totals, per_day, pre, naive_per_day, protocol = load_snapshot(
            args.snapshot, allow_legacy=args.allow_legacy_snapshot)
        looks = max(1, (protocol or {}).get("_looks") or 0,
                    published_looks(reg_path))
        rows = grade_with_multiplicity(
            lambda: (grade_directional_days(by_h)
                     + grade_structural_days(totals, per_day, pre, naive_per_day)),
            looks)
        if protocol is None:
            # No pinned grader in the bundle: nothing in this artifact says the
            # code producing these verdicts is the code the protocol froze. Drop
            # every verdict rather than publish one no record backs — an absent
            # verdict cannot be quoted, a downgraded one can.
            for r in rows:
                r.pop("verdict", None)
                r.pop("claim_verdict", None)
        if args.allow_legacy_snapshot and not pre:
            # A legacy archive cannot say what the target WAS, so it must not
            # publish what the record IS relative to one. Drop the verdict
            # field entirely rather than emit a downgraded string a reader
            # could quote — an absent verdict cannot be misread as a grade.
            for r in rows:
                if r["family"].startswith("structure"):
                    r["claimed_source"] = "DB average (NO CHAIN RECORD)"
                    r.pop("verdict", None)
                    r.pop("claim_verdict", None)
        # The snapshot exports per-day tallies, not per-prediction probabilities,
        # so reliability bins cannot be rebuilt from it. Saying so beats an
        # empty dict pretending the calibration was measured and came up clean.
        calibration = None
        # A snapshot carries tallies, not the chain or the outcome schema, so
        # neither anteriority claim can be verified from it. Unverified is not
        # verified: the summary says so rather than asserting either.
        chain = None
        # A snapshot carries neither the chain nor per-row revisions, so neither
        # the grader registration nor the code provenance can be checked from
        # one. Unchecked is not clean; the summary says so below.
        gate = {"structure": {}, "direction": {}, "epoch": REVISION_EPOCH.isoformat(),
                "checked": False}
        revision_refusals = []
        # A snapshot carries tallies, not regime_outcomes, so the live
        # write-path invariant cannot be probed from one.
        probe = {"count": 0, "newest_ts": None, "kinds": []}
        null_refusals = []
        # A snapshot has no symbols/listing table, so universe completeness is
        # UNMEASURED here rather than assumed clean.
        survivorship_completeness = {
            "direction": measure_universe_completeness(None, "prediction_outcomes"),
            "structure": measure_universe_completeness(None, "regime_outcomes"),
        }
        source = f"snapshot {args.snapshot} (manifest hashes verified)"
    else:
        con = connect(args.db)
        # Before a single number is computed: is THIS the grader the chain
        # registered, under THESE thresholds? A grader nothing names cannot
        # produce a pre-registered verdict, so this exits rather than grades.
        protocol = require_registered_grader(con)
        # And: has the research loop actually recorded the judgments it narrated?
        require_research_liveness(con, args.db)
        # And before a single number is published: does the live table still
        # satisfy the write-path invariant the store is supposed to enforce?
        probe = null_amendment_probe(con)
        # Price the multiplicity BEFORE any interval exists: how many looks have
        # been taken at these rows, and how many rows this cycle publishes.
        looks = max(1, chain_looks(con), published_looks(reg_path))
        set_family_floor(max(chain_families(con), published_family(reg_path)))
        rows = grade_with_multiplicity(
            lambda: grade_directional(con) + grade_structural(con), looks)
        null_refusals = apply_null_amendment_probe(rows, probe)
        chain = fetch_chain_presence(con)
        chain["grading_protocol_seq"] = protocol["_seq"]
        chain["grader_sha256"] = self_sha256()
        chain["grader_commit"] = protocol.get("graderCommit")
        calibration = fetch_calibration_bins(con)
        # And: what code wrote the rows behind each verdict?
        gate = revision_gate(con)
        revision_refusals = apply_revision_gate(rows, gate)
        survivorship_completeness = {
            "direction": measure_universe_completeness(con, "prediction_outcomes"),
            "structure": measure_universe_completeness(con, "regime_outcomes"),
        }
        source = f"database {args.db}"
        con.close()

    print("=" * 104)
    print(f"SIGNALDECK ACCURACY REGISTRY — {dt.date.today()}")
    print("Every predictor, its claim, and what the live record actually supports.")
    print(f"Graded from {source}")
    print("=" * 104)
    m = multiplicity()
    print(f"Interval coverage: {1 - m['corrected_alpha']:.4%} — maxAlpha {MAX_ALPHA} "
          f"divided by {m['divisor']} ({m['family_size']} rows published this cycle "
          f"x {m['looks']} grading looks taken), z={m['z']:.4f}. NOT a fixed 95%: this "
          "surface publishes a family of rows and re-grades them daily, and both are "
          "priced. The correction only ever widens.")
    print("=" * 104)
    hdr = "%-40s %8s %9s %9s %-19s %s"
    print(hdr % ("PREDICTOR", "CLAIM", "LIVE n", "LIVE ACC", "CORRECTED CI", "VERDICT"))
    print("-" * 104)
    for r in rows:
        claim = f"{r['claimed']:.1%}" if r["claimed"] is not None else "—"
        acc = f"{r['live_acc']:.1%}" if r["live_acc"] is not None else "—"
        ci = f"[{r['ci'][0]:.3f}, {r['ci'][1]:.3f}]" if r["ci"] else "—"
        print(hdr % (r["predictor"][:40], claim, f"{r['live_n']:,}", acc, ci,
                     r.get("verdict", "NO VERDICT — withheld, see notes below")))

    if args.snapshot and protocol is None:
        print("NO VERDICT — UNREGISTERED GRADER. This snapshot carries no "
              "grading-protocol record (repro/grading_protocol.csv), so nothing in it "
              f"names the code that graded it; this grader hashes {self_sha256()}. "
              "The verdict field is dropped rather than downgraded. Re-cut the snapshot "
              "from a database whose chain registers this grader — do NOT write a new "
              "chain record or re-pin graderSha256 to make the refusal go away.")
        print()

    if revision_refusals:
        print("NO VERDICT — unattributable code provenance. These predictors have rows "
              f"frozen on/after {gate['epoch']} whose writing binary cannot be resolved "
              "to a commit in this repository (a '+dirty' stamp means the binary was "
              "built from a modified checkout; '(unstamped)' means it recorded nothing):")
        for line in revision_refusals:
            print(line)
        print("    The verdict field is dropped, not downgraded — an absent verdict "
              "cannot be quoted as one.")
        print()
    elif not gate["checked"]:
        print(f"Code provenance: NOT CHECKED from this source (no per-row revision "
              "available). Absence of a check is not a clean check.")
        print()

    if null_refusals:
        newest = (dt.datetime.fromtimestamp(probe["newest_ts"], dt.timezone.utc)
                  .isoformat().replace("+00:00", "Z")) if probe["newest_ts"] else "unknown"
        print(f"NO VERDICT — write-path invariant violated in the live data. "
              f"{probe['count']:,} regime_outcomes row(s) frozen at/after "
              f"{NULL_AMENDMENT_EPOCH.isoformat()} carry no naive_label; newest offending "
              f"ts {newest}. The store refuses such writes, so the binary that wrote them "
              "is not this source and its calls cannot be graded:")
        for line in null_refusals:
            print(line)
        print("    Do NOT backfill the column — a persistence label computed after the "
              "outcome is a hindsight baseline. Rebuild and restart the daemon.")
        print()

    failed = [r for r in rows if r.get("verdict", "").startswith("FAILED")]
    pending = [r for r in rows if r.get("verdict", "").startswith("PENDING")]
    print()
    if failed:
        print("ACTION REQUIRED — these are shipping a prediction the live record contradicts:")
        for r in failed:
            print(f"  * {r['predictor']}: {r['live_acc']:.1%} over {r['live_n']:,} independent "
                  f"observations, entire CI below the {r['null_acc']:.1%} prequential baseline.")
        print("    Retire, invert, or relabel as experimental. Do not display as a forecast.")
        print("    Directional FAILED rows publish retire=true — the daemon's model-health")
        print("    worker enforces the pre-registered retirement automatically.")
        print()
    if pending:
        print(f"{len(pending)} structural predictor(s) not yet gradable — their numbers are")
        print("backtest claims. They become real evidence on the dates shown above.")
        print()
    srcs = {r.get("claimed_source") for r in rows if r["family"] == "structure"}
    if srcs:
        print("Structural claim C is READ from the pre-registration chain (the "
              "min-conviction, all-decisions band), never recomputed at grading time; "
              f"sources this run: {', '.join(sorted(s for s in srcs if s))}.")
        print("Each row also carries claimed_db_avg — the conviction-mix-weighted average "
              "over regime_outcomes — as a DIAGNOSTIC. It is not the target; a gap between "
              "it and C is mix drift, which is exactly what the freeze is meant to survive.")
        print()
    print(f"Independence rule: one observation per (symbol, horizon, UTC-day).")
    print(f"Verdict threshold: {MIN_INDEPENDENT_N} independent observations minimum, "
          f"on at least {MIN_DISTINCT_DAYS} distinct UTC days.")
    print(f"Survivorship boundary: rows before {SURVIVORSHIP_EPOCH.isoformat()} were graded "
          "against a survivor-seeded universe and are excluded from every tally above.")
    print("Intervals resample DAYS, not rows: on any one day ~1,000 symbols share one")
    print("market move, so the row count overstates the evidence. Each graded row below")
    print("reports its measured design effect and effective n in the JSON output.")
    print("Directional null: PREQUENTIAL only — each day's constant guess is the majority")
    print("class over days strictly BEFORE it (a coin flip on day one or a tied prior),")
    print("graded through the same day-clustered machinery as the model it benchmarks.")
    if chain is None:
        print("Structural null: UNVERIFIED — graded from a snapshot, which carries tallies")
        print("but not the outcome schema; whether a baseline was frozen cannot be read here.")
    elif chain["null_frozen"]:
        print("Structural null: NAIVE PERSISTENCE — the \"nothing changes\" label frozen at call")
        print("time beside every regime call (regime_outcomes.naive_label) and graded by the same")
        print("resolver, published as its own '<kind>#persist' row. A structural verdict is read")
        print("against it; the frozen-claim comparison rides along as claim_verdict. Calls frozen")
        print("before 2026-07-27 carry no baseline and grade NO BASELINE rather than a skill claim.")
        print(f"Verified this run: {chain['null_coverage']:.1%} of graded outcomes carry a "
              "frozen baseline.")
        print(f"Write-path invariant: {chain['unmatched_nulls']} post-"
              f"{NULL_AMENDMENT_EPOCH.isoformat()} rows with no baseline (must be 0; the "
              "store refuses such writes and this run exits non-zero otherwise).")
    else:
        print("Structural null: NOT FROZEN — regime_outcomes carries no naive_label column;")
        print("every structural kind grades NO BASELINE. No claim of a frozen \"nothing")
        print("changes\" benchmark is supported by this database.")
    print("The hindsight null was retired after its one dual-null transition cycle; the")
    print("switchover regrade against the committed repro snapshot recorded ZERO verdict")
    print("changes — see audits/2026-07-27-null-transition.md.")
    if chain is None:
        print("Auto-retire rule: UNVERIFIED — the pre-registration chain is not carried in a")
        print("snapshot, so anteriority cannot be checked from this grade.")
    elif chain["retire_rule_chained"]:
        print(f"Auto-retire rule (pre-registered {AUTO_RETIRE_REGISTERED}, chained as "
              f"'auto-retire-rule' at chain seq {chain['retire_rule_chain_seq']}):")
        print("a directional row that meets both evidence floors with its effective-N Wilson")
        print("upper bound below the prequential null grades FAILED, publishes retire=true, and")
        print("the daemon stops publishing it. Chained digest matches the graded rule: "
              f"{auto_retire_rule_digest()}.")
    else:
        print("Auto-retire rule: NOT ON CHAIN — digest computed locally, anteriority unproven.")
        print("The thresholds below are the ones this run graded under, but nothing in the")
        print(f"pre-registration chain freezes them: local digest {auto_retire_rule_digest()}"
              + (f", chain carries {chain['retire_rule_chain_hash']}."
                 if chain["retire_rule_chain_hash"] else ", chain carries no such record."))
    for r in rows:
        if r.get("design_effect"):
            print(f"  {r['predictor']}: n={r['live_n']:,} over {r['distinct_days']} days, "
                  f"design effect {r['design_effect']:.1f}x -> effective n {r['effective_n']:.0f}")

    if calibration and any(calibration["horizons"].values()):
        print()
        print("Reliability (predicted P(up) vs realized up-frequency, independent symbol-days):")
        for horizon, bins in sorted(calibration["horizons"].items()):
            for b in bins:
                conv = " <- conviction tier" if (
                    b["p_hi"] <= 0.5 - calibration["conviction_threshold"] + 1e-9
                    or b["p_lo"] >= 0.5 + calibration["conviction_threshold"] - 1e-9) else ""
                print(f"  {horizon}: p in [{b['p_lo']:.1f},{b['p_hi']:.1f}) "
                      f"mean {b['mean_predicted']:.3f} -> realized {b['realized_up_freq']:.3f} "
                      f"(n={b['n']}, {b['distinct_days']} days){conv}")
        print("A conviction-tier bin whose realized frequency sits on the wrong side of its")
        print("predicted probability is the anti-calibration to fix (threshold or isotonic")
        print("recalibration) BEFORE the auto-retire gate fires on the graded slice.")
    elif calibration is None:
        print()
        print("Reliability bins: unavailable from a snapshot grade — the committed snapshot")
        print("carries per-day tallies, not per-prediction probabilities. Grade from the DB")
        print("to publish calibration.")

    print()
    for fam, m in survivorship_completeness.items():
        if m.get("clean"):
            print(f"Universe completeness ({fam}): listing status resolvable for all "
                  f"{m['symbols_graded']:,} graded symbols — survivorship_clean.")
        else:
            print(f"Universe completeness ({fam}): NOT CLEAN — {m['reason']}. "
                  "Rows carry survivorship_clean=false.")

    # Post-epoch attrition is MEASURED, not declared unmeasurable: the bound is
    # computed against an external delistings record (SEC EDGAR Form 25) by
    # tools/backfill_delistings.py --survivorship-bound, which owns this field
    # in the registry JSON. This block only reports what was measured.
    sb = None
    if os.path.exists(reg_path):
        try:
            with open(reg_path, encoding="utf-8") as f:
                sb = json.load(f).get("survivorship_bound")
        except (OSError, json.JSONDecodeError, AttributeError):
            sb = None
    if sb and sb.get("bound_pp") is not None:
        print(f"Survivorship bound (measured {sb['as_of']}, {sb['source']}): "
              f"{sb['symbols_dropped']} symbol(s) left the tracked universe since "
              f"{sb['epoch']} vs {sb['symbols_graded']:,} graded. If every dropped symbol")
        print(f"had kept being graded and been WRONG every time, headline accuracy would "
              f"fall by at most {sb['bound_pp']:.2f} pp.")
    elif sb:
        print(f"Survivorship bound: measured {sb['as_of']} but not computable — "
              f"{sb.get('reason', 'no graded post-epoch record')}.")
    else:
        print("Survivorship bound: NOT YET MEASURED this cycle — run")
        print("  python3 tools/backfill_delistings.py --survivorship-bound")
        print("to bound accuracy inflation from dropped symbols against SEC EDGAR Form 25 filings.")

    if args.json:
        now = dt.datetime.now().isoformat(timespec="seconds")
        payload = {"generated": now,
                   # graded_at is the timestamp of the grade that produced THESE
                   # rows, and refused_since is null on a successful grade. A
                   # refusal envelope (written by ops/accuracy-registry.sh when
                   # this grader exits non-zero) carries the stale graded_at
                   # forward and sets refused_since, so a consumer can see the
                   # outage without inferring it from file mtime.
                   "graded_at": now,
                   "refused_since": None,
                   "min_independent_n": MIN_INDEPENDENT_N,
                   # The published interval's actual error rate, and the two
                   # multiplicities it is paying for. A consumer reading `ci`
                   # off any row can read here what that interval covers.
                   "max_alpha": MAX_ALPHA,
                   "family_size": multiplicity()["family_size"],
                   "looks": multiplicity()["looks"],
                   "divisor": multiplicity()["divisor"],
                   "corrected_alpha": multiplicity()["corrected_alpha"],
                   "ci_z": multiplicity()["z"],
                   "multiplicity_rule": MULTIPLICITY_RULE,
                   "survivorship_epoch": SURVIVORSHIP_EPOCH.isoformat(),
                   # Measured, per graded sample: the share of contributing
                   # symbols whose listing status is resolvable. Every row's
                   # survivorship_clean flag is read off this, never asserted.
                   "survivorship_completeness": survivorship_completeness,
                   "null_policy": ("prequential-majority only: each day's constant guess "
                                   "is the majority class over days strictly before it. "
                                   "The hindsight null was retired after the dual-null "
                                   "transition cycle; the switchover regrade recorded zero "
                                   "verdict changes (audits/2026-07-27-null-transition.md)."),
                   "min_distinct_blocks": MIN_DISTINCT_BLOCKS,
                   "auto_retire_rule": auto_retire_rule(),
                   # Provenance, both directions: the digest of the grader that
                   # produced this file (checked against the chained protocol
                   # before anything was graded), and which predictors were
                   # refused a verdict because the code behind their rows could
                   # not be resolved to a commit.
                   "grader_sha256": self_sha256(),
                   "grading_protocol_seq": chain.get("grading_protocol_seq") if chain else None,
                   "revision_epoch": gate["epoch"],
                   "revision_gate_checked": gate["checked"],
                   "revision_gate_offenders": {"structure": gate["structure"],
                                               "direction": gate["direction"]},
                   # The live probe of the naive-baseline write-path invariant:
                   # how many post-amendment rows carry no frozen baseline, the
                   # newest one, and which structural kinds were refused for it.
                   "null_amendment_probe": probe,
                   # Verified by reading the DB, not asserted: whether a naive
                   # baseline is actually frozen beside the graded outcomes, and
                   # whether the auto-retire digest above is on the prereg chain.
                   # null means the grade could not check (snapshot source).
                   "null_frozen": chain["null_frozen"] if chain else None,
                   "null_frozen_coverage": chain["null_coverage"] if chain else None,
                   "retire_rule_chained": chain["retire_rule_chained"] if chain else None,
                   "retire_rule_chain_seq": chain["retire_rule_chain_seq"] if chain else None,
                   # Reliability-diagram bins (per horizon: predicted probability,
                   # realized frequency, n). null when graded from a snapshot,
                   # which carries no per-prediction probabilities.
                   "calibration": calibration,
                   "rows": rows}
        # backfill_delistings.py --survivorship-bound owns survivorship_bound;
        # regenerating the registry must not silently discard the measurement.
        if os.path.exists(args.json):
            try:
                with open(args.json, encoding="utf-8") as f:
                    prev = json.load(f).get("survivorship_bound")
            except (OSError, json.JSONDecodeError, AttributeError):
                prev = None
            if prev:
                payload["survivorship_bound"] = prev
        with open(args.json, "w", encoding="utf-8") as f:
            json.dump(payload, f, indent=1)
        print(f"\nwrote {args.json}")

    # FAIL LOUDLY on a broken write-path invariant. Everything above is already
    # written and reported honestly; this only changes the exit status, so the
    # daily LaunchAgent shows a failed run rather than burying the divergence in
    # prose nobody reads.
    if chain and chain.get("unmatched_nulls"):
        print(f"\nINVARIANT VIOLATION: {chain['unmatched_nulls']} regime_outcomes rows frozen "
              f"at/after {NULL_AMENDMENT_EPOCH.isoformat()} carry no naive_label.")
        print("The daemon's store refuses such writes, so the deployed binary is not this")
        print("source. Do NOT backfill the column — a persistence label computed after the")
        print("outcome is a hindsight baseline. Rebuild and restart the daemon.")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
