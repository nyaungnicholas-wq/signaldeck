#!/usr/bin/env python3
"""Export the minimal grading inputs behind every published SignalDeck number.

Why this exists
---------------
The database is gitignored, so until this snapshot existed no published number
was reproducible by anyone but the machine holding data/signaldeck.db. That is
the opposite of the discipline the rest of this repo enforces: a claim nobody
else can regenerate is a decoration, however carefully it was graded.

What ships in the snapshot (repro/, committed):
  * directional_days.csv  — per-(horizon, UTC-day) tallies: n, correct, up-days,
    plus the high-conviction slice. These are the EXACT inputs to every
    directional registry row; grading them reproduces the published intervals
    bit for bit (see tools/accuracy_registry.py --snapshot).
  * structural_days.csv   — per-(kind, horizon, call-day) resolved tallies.
  * structural_claims.csv — each structural predictor's frozen backtest claim,
    forecast count, and first-call timestamp (drives PENDING dates).
  * pairs_inputs.csv      — one datasetver-style version row per symbol series
    the pairs study (tools/pairs_trading.py) consumes: bounds, row count, and
    the SHA-256 of the canonical (ts, close, volume) serialization. The series
    themselves are NOT exported — bar data is license-classified (A10) and not
    redistributable — but anyone with their own licensed bars can verify they
    hold the identical dataset before comparing pairs-study results.
  * xsfactor_inputs.csv   — the same versioning for the cross-sectional factor
    derivation (tools/xsfactor_edge.py): one row per symbol series that study
    consumes, under ITS universe (every stock ever tracked, series >=
    MIN_VOL_CLOSES bars) and ITS view of a bar (UTC-day, close, volume). The
    active flag ships in the row because the study's --universe split turns
    on it. Matching hashes mean a reader holds the identical dataset before
    comparing against daemon/internal/xsfactor/derivation.json.
  * MANIFEST.json         — a hash per CSV under the same canonical scheme, so
    the graders can refuse a tampered snapshot.

Day tallies are dozens of rows per predictor and contain no bar data, so the
A10 data-license guard is not implicated.

Hashing is byte-compatible with daemon/internal/datasetver (HashRecords):
    sha256("v1|{name}|{kind}|{N}\\n" + "".join(field + "|" for field in rec) + "\\n" per record)
with floats pinned to %.10g. The Go side pins parity with a shared test vector
(TestHashRecordsMatchesPythonImplementation); change either implementation and
that test fails before the drift can invalidate a committed manifest.

Usage:
    python3 tools/make_repro_snapshot.py             # write repro/ from the DB
    python3 tools/make_repro_snapshot.py --verify    # committed snapshot still
        matches what the DB produces today? (extension after new resolutions is
        expected; a hash change on an UNCHANGED range is the alarm)
"""
from __future__ import annotations

import argparse
import csv
import datetime as dt
import hashlib
import json
import os
import sqlite3
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import accuracy_registry as reg  # noqa: E402 — single source of truth for the SQL
import xsfactor_edge as xsf  # noqa: E402 — single source of truth for the study's floor

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_OUT = os.path.join(REPO, "repro")

# file -> kind bound into its hash. accuracy_registry.load_snapshot imports
# this mapping, so loader and exporter cannot disagree about identity.
FILES = {
    "directional_days.csv": "directional-day-tallies",
    "structural_days.csv": "structural-day-tallies",
    "structural_naive_days.csv": "structural-naive-persistence-day-tallies",
    "structural_claims.csv": "structural-claims",
    "pairs_inputs.csv": "pairs-input-series-versions",
    "xsfactor_inputs.csv": "xsfactor-input-series-versions",
    "prereg_claims.csv": "prereg-frozen-claims",
    "grading_protocol.csv": "prereg-grading-protocol",
}

HEADERS = {
    "directional_days.csv": ["horizon", "day", "n", "correct", "up_days",
                             "hc_n", "hc_correct", "hc_up_days"],
    "structural_days.csv": ["kind", "horizon_days", "day", "n", "correct"],
    "structural_naive_days.csv": ["kind", "horizon_days", "day", "n", "correct"],
    "structural_claims.csv": ["kind", "horizon_days", "forecasts_recorded",
                              "claimed_accuracy", "first_ts"],
    "pairs_inputs.csv": ["symbol", "timeframe", "first_ts", "last_ts", "n", "sha256"],
    "xsfactor_inputs.csv": ["symbol", "timeframe", "active", "first_day", "last_day",
                            "n", "sha256"],
    "prereg_claims.csv": ["kind", "claimed_accuracy", "spec_hash", "registered_ts"],
    "grading_protocol.csv": ["seq", "grader_sha256", "grader_commit",
                             "min_independent_n", "min_distinct_days",
                             "min_distinct_blocks", "max_alpha",
                             "multiplicity_rule", "looks"],
}


def g10(x: float) -> str:
    """The pinned float format — must match datasetver.canonicalFloatFmt."""
    return "%.10g" % float(x)


def hash_records(name: str, kind: str, records: list[list[str]]) -> str:
    """Python twin of datasetver.HashRecords. Do not change one without the other."""
    if not records:
        return hashlib.sha256(f"empty|{name}|{kind}".encode()).hexdigest()
    h = hashlib.sha256()
    h.update(f"v1|{name}|{kind}|{len(records)}\n".encode())
    for rec in records:
        h.update(("".join(str(f) + "|" for f in rec) + "\n").encode())
    return h.hexdigest()


# ------------------------------------------------------------------ builders
# Each builder returns fully string-formatted records — the SAME strings are
# hashed and written, so a CSV read back through csv.reader re-hashes to the
# manifest value exactly.

def directional_records(con: sqlite3.Connection) -> list[list[str]]:
    recs = []
    by_h = reg.fetch_directional_days(con)
    for horizon in sorted(by_h):
        for day, n, hits, ups, hc_n, hc_hits, hc_ups in by_h[horizon]:
            recs.append([str(horizon)] + [str(int(x)) for x in
                                          (day, n, hits, ups, hc_n, hc_hits, hc_ups)])
    return recs


def structural_records(con: sqlite3.Connection):
    """(day tallies, claims, naive-persistence day tallies).

    The naive tallies are the FROZEN "nothing changes" null — exported so an
    outside reader can reproduce the skill verdict, not just the accuracy.
    """
    totals, per_day, naive_per_day = reg.fetch_structural(con)

    def day_rows(src) -> list[list[str]]:
        out = []
        for (kind, hd) in sorted(src):
            for day, n, hits in src[(kind, hd)]:
                out.append([str(kind), str(int(hd)), str(int(day)), str(int(n)), str(int(hits))])
        return out

    days = day_rows(per_day)
    claims = [[str(kind), str(int(hd)), str(int(total)),
               g10(claimed) if claimed is not None else "", str(int(first_ts))]
              for kind, hd, total, claimed, first_ts in totals]
    return days, claims, day_rows(naive_per_day)


def pairs_input_records(con: sqlite3.Connection) -> list[list[str]]:
    """Version every symbol series the pairs study can consume.

    Mirrors pairs_trading.load_universe's universe (stocks with a SIC sector)
    and its per-bar filter (close present and positive). Hashes, not bars: the
    raw series stay under their data license.
    """
    try:
        syms = con.execute("""
            SELECT s.id, s.symbol FROM symbols s
            LEFT JOIN companies c ON c.ticker = s.symbol
            WHERE s.market = 'stocks' AND COALESCE(substr(c.sic, 1, 2), '') != ''
            ORDER BY s.symbol""").fetchall()
    except sqlite3.OperationalError:
        return []  # reduced test databases have no bars universe to version
    recs = []
    for sid, sym in syms:
        series = con.execute(
            "SELECT ts, close, volume FROM bars WHERE tf = '1d' AND symbol_id = ? "
            "AND close IS NOT NULL AND close > 0 ORDER BY ts", (sid,)).fetchall()
        if not series:
            continue
        h = hash_records(sym, "pairs-1d-close-volume",
                         [[str(int(ts)), g10(c), g10(v or 0.0)] for ts, c, v in series])
        recs.append([str(sym), "1d", str(int(series[0][0])), str(int(series[-1][0])),
                     str(len(series)), h])
    return recs


def xsfactor_input_records(con: sqlite3.Connection) -> list[list[str]]:
    """Version every symbol series the cross-sectional factor study consumes.

    Mirrors xsfactor_edge.load exactly: the --universe all universe (every
    stock ever tracked, active or not), its floor (a series shorter than
    MIN_VOL_CLOSES bars is never constructed), and its view of a bar —
    (UTC-day, close, volume), because ts/86400 is all the study ever reads.
    Hashes, not bars: the raw series stay under their data license.
    TestXsfactorSnapshotRoundTrip pins this mirror against the study's own
    loader, so the two cannot drift silently.
    """
    try:
        syms = con.execute(
            "SELECT s.id, s.symbol, s.active FROM symbols s "
            "WHERE s.market = 'stocks' ORDER BY s.symbol").fetchall()
    except sqlite3.OperationalError:
        return []  # reduced test databases have no bars universe to version
    recs = []
    for sid, sym, active in syms:
        series = con.execute(
            "SELECT ts/86400, close, volume FROM bars WHERE tf = '1d' "
            "AND symbol_id = ? ORDER BY ts", (sid,)).fetchall()
        if len(series) < xsf.MIN_VOL_CLOSES:
            continue  # below the study's floor: xsfactor_edge.load drops it too
        h = hash_records(sym, "xsfactor-1d-day-close-volume",
                         [[str(int(d)), g10(c), g10(v or 0.0)] for d, c, v in series])
        recs.append([str(sym), "1d", str(int(active or 0)),
                     str(int(series[0][0])), str(int(series[-1][0])),
                     str(len(series)), h])
    return recs


def prereg_claim_records(con: sqlite3.Connection) -> list[list[str]]:
    """Ship the frozen grading target itself, not just the outcomes.

    Without this, a snapshot grade could only recompute the target from the
    exported tallies — the exact drift the chain exists to prevent. One row per
    kind: the newest chained record's min-conviction (all-decisions) claim and
    the spec hash a reader can check against the daemon's own chain.
    """
    return [[kind, g10(c["claimed"]), c["spec_hash"], str(int(c["registered_ts"]))]
            for kind, c in sorted(reg.fetch_prereg_claims(con).items())]


def grading_protocol_records(con: sqlite3.Connection) -> list[list[str]]:
    """Ship the grader pin itself, so the reproduce path can enforce it.

    require_registered_grader used to run only against the chain in the
    gitignored database — i.e. only on the path nobody outside this machine can
    run. A third party following REPRODUCE.md graded with --snapshot, which
    checked no registration at all and printed verdicts anyway. Exporting the
    newest grading-protocol record as a manifest-hashed file puts the pin inside
    the reproducible bundle: the snapshot grade now fails closed on exactly the
    condition the DB path fails on, and an outside reader can read the pinned
    digest and thresholds from an artifact rather than taking them on trust.

    One row: the newest record. If the chain has none, the file is empty and the
    snapshot grade publishes no verdicts — which is the correct output, not a
    reason to write a chain record or re-pin the digest.
    """
    rec = reg.newest_grading_protocol(con)
    if rec is None:
        return []
    # A threshold the record never froze exports as EMPTY, not as the string
    # "None" and never as the grader's own constant — an unregistered floor must
    # read back as unregistered so the loader's check refuses on it.
    def fld(key: str) -> str:
        v = rec.get(key)
        return "" if v is None else str(int(v))

    return [[str(int(rec["_seq"])), str(rec.get("graderSha256") or ""),
             str(rec.get("graderCommit") or ""),
             fld("minIndependentN"), fld("minDistinctDays"),
             fld("minDistinctBlocks"),
             # The multiplicity price rides along: without maxAlpha and the
             # divisor rule the bundle cannot say what coverage its intervals
             # claim, and the loader refuses rather than assume 95%.
             "" if rec.get("maxAlpha") is None else g10(rec["maxAlpha"]),
             str(rec.get("multiplicityRule") or ""),
             # LOOKS TAKEN, counted off the chain at cut time. Exported so a
             # third-party snapshot grade prices the same optional stopping the
             # DB path does. The grader maxes it against the looks the published
             # registry already declared, so a re-cut cannot refund one.
             str(reg.chain_looks(con))]]


def build_all(con: sqlite3.Connection) -> dict[str, list[list[str]]]:
    s_days, s_claims, s_naive = structural_records(con)
    return {
        "directional_days.csv": directional_records(con),
        "prereg_claims.csv": prereg_claim_records(con),
        "grading_protocol.csv": grading_protocol_records(con),
        "structural_days.csv": s_days,
        "structural_naive_days.csv": s_naive,
        "structural_claims.csv": s_claims,
        "pairs_inputs.csv": pairs_input_records(con),
        "xsfactor_inputs.csv": xsfactor_input_records(con),
    }


# ------------------------------------------------------------------- actions

def write_snapshot(con: sqlite3.Connection, out_dir: str) -> dict:
    os.makedirs(out_dir, exist_ok=True)
    data = build_all(con)
    entries = []
    for fname, recs in data.items():
        with open(os.path.join(out_dir, fname), "w", newline="") as f:
            w = csv.writer(f)
            w.writerow(HEADERS[fname])
            w.writerows(recs)
        entries.append({"file": fname, "kind": FILES[fname], "rows": len(recs),
                        "sha256": hash_records(fname, FILES[fname], recs)})
    try:
        commit = subprocess.run(["git", "rev-parse", "HEAD"], cwd=REPO, text=True,
                                capture_output=True, check=True).stdout.strip()
    except Exception:
        commit = "unknown"
    manifest = {
        "generated": dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds"),
        "git_commit": commit,
        "survivorship_epoch": reg.SURVIVORSHIP_EPOCH.isoformat(),
        "hash_scheme": "datasetver v1 canonical records, sha256, floats %.10g "
                       "(daemon/internal/datasetver.HashRecords)",
        "note": "grading tallies + input-series versions only; no bar data is "
                "redistributed (A10). Regenerate the published numbers with "
                "`python3 tools/accuracy_registry.py --snapshot repro` — see REPRODUCE.md.",
        "files": entries,
    }
    with open(os.path.join(out_dir, "MANIFEST.json"), "w") as f:
        json.dump(manifest, f, indent=1)
        f.write("\n")
    return manifest


def verify_snapshot(con: sqlite3.Connection, out_dir: str) -> int:
    """Compare the committed snapshot against what the DB produces right now."""
    man_path = os.path.join(out_dir, "MANIFEST.json")
    if not os.path.exists(man_path):
        print(f"no snapshot at {out_dir} — run without --verify to create one")
        return 1
    with open(man_path) as f:
        committed = {e["file"]: e for e in json.load(f)["files"]}
    fresh = build_all(con)
    rc = 0
    for fname, recs in fresh.items():
        now = hash_records(fname, FILES[fname], recs)
        ent = committed.get(fname)
        if ent is None:
            print(f"MISSING from manifest: {fname}")
            rc = 1
        elif ent["sha256"] == now:
            print(f"identical: {fname} ({len(recs)} rows)")
        else:
            print(f"CHANGED:   {fname} ({ent['rows']} committed rows -> {len(recs)} now)")
            print("           expected after new resolutions; regenerate and recommit.")
            print("           A change with NO new rows means history was rewritten —")
            print("           regrade before republishing any number.")
            rc = 1
    return rc


def verify_complete(out_dir: str) -> int:
    """Database-free audit of the SHIPPED snapshot — the CI gate.

    --verify needs the DB, so it can only ever run on the one machine that
    holds it; nothing checked that what a cold clone contains matches what
    REPRODUCE.md tells a reviewer to check. Three properties, all checkable
    from the repository alone:
      1. every FILES entry appears in MANIFEST.json AND on disk,
      2. each file re-hashes to its manifest value (canonical scheme),
      3. each file is git-TRACKED — a file present only in the working tree
         is documented-but-not-shipped, which is the failure this catches.
    """
    man_path = os.path.join(out_dir, "MANIFEST.json")
    if not os.path.exists(man_path):
        print(f"MISSING: {man_path}")
        return 1
    with open(man_path) as f:
        manifest = {e["file"]: e for e in json.load(f)["files"]}
    rc = 0
    for fname, kind in sorted(FILES.items()):
        path = os.path.join(out_dir, fname)
        ent = manifest.get(fname)
        if ent is None:
            print(f"NOT IN MANIFEST: {fname}")
            rc = 1
            continue
        if not os.path.exists(path):
            print(f"IN MANIFEST BUT NOT ON DISK: {fname}")
            rc = 1
            continue
        with open(path, newline="") as f:
            recs = list(csv.reader(f))[1:]
        got = hash_records(fname, kind, recs)
        if got != ent["sha256"]:
            print(f"HASH MISMATCH: {fname} is {got}, manifest says {ent['sha256']}")
            rc = 1
            continue
        tracked = subprocess.run(["git", "ls-files", "--error-unmatch", path],
                                 cwd=REPO, capture_output=True)
        if tracked.returncode != 0:
            print(f"UNTRACKED (present locally, absent from a clone): {fname}")
            rc = 1
            continue
        print(f"ok: {fname:30s} {len(recs):6d} rows  tracked  hash verified")
    extra = sorted(set(manifest) - set(FILES))
    if extra:
        print(f"manifest lists files the exporter does not produce: {', '.join(extra)}")
        rc = 1
    return rc


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=reg.DEFAULT_DB)
    ap.add_argument("--out", default=DEFAULT_OUT, help="snapshot directory (default repro/)")
    ap.add_argument("--verify", action="store_true",
                    help="compare the committed snapshot to the live DB instead of writing")
    ap.add_argument("--verify-complete", action="store_true",
                    help="database-free: assert the shipped snapshot is complete, "
                         "correctly hashed, and git-tracked (CI gate)")
    args = ap.parse_args()

    if args.verify_complete:
        return verify_complete(args.out)

    con = reg.connect(args.db)
    if args.verify:
        return verify_snapshot(con, args.out)
    manifest = write_snapshot(con, args.out)
    for e in manifest["files"]:
        print(f"wrote {e['file']:24s} {e['rows']:6d} rows  {e['sha256']}")
    print(f"manifest: {os.path.join(args.out, 'MANIFEST.json')} (commit {manifest['git_commit'][:12]})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
