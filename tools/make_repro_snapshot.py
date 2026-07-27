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

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_OUT = os.path.join(REPO, "repro")

# file -> kind bound into its hash. accuracy_registry.load_snapshot imports
# this mapping, so loader and exporter cannot disagree about identity.
FILES = {
    "directional_days.csv": "directional-day-tallies",
    "structural_days.csv": "structural-day-tallies",
    "structural_claims.csv": "structural-claims",
    "pairs_inputs.csv": "pairs-input-series-versions",
}

HEADERS = {
    "directional_days.csv": ["horizon", "day", "n", "correct", "up_days",
                             "hc_n", "hc_correct", "hc_up_days"],
    "structural_days.csv": ["kind", "horizon_days", "day", "n", "correct"],
    "structural_claims.csv": ["kind", "horizon_days", "forecasts_recorded",
                              "claimed_accuracy", "first_ts"],
    "pairs_inputs.csv": ["symbol", "timeframe", "first_ts", "last_ts", "n", "sha256"],
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


def structural_records(con: sqlite3.Connection) -> tuple[list[list[str]], list[list[str]]]:
    totals, per_day = reg.fetch_structural(con)
    days = []
    for (kind, hd) in sorted(per_day):
        for day, n, hits in per_day[(kind, hd)]:
            days.append([str(kind), str(int(hd)), str(int(day)), str(int(n)), str(int(hits))])
    claims = [[str(kind), str(int(hd)), str(int(total)),
               g10(claimed) if claimed is not None else "", str(int(first_ts))]
              for kind, hd, total, claimed, first_ts in totals]
    return days, claims


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


def build_all(con: sqlite3.Connection) -> dict[str, list[list[str]]]:
    s_days, s_claims = structural_records(con)
    return {
        "directional_days.csv": directional_records(con),
        "structural_days.csv": s_days,
        "structural_claims.csv": s_claims,
        "pairs_inputs.csv": pairs_input_records(con),
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


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=reg.DEFAULT_DB)
    ap.add_argument("--out", default=DEFAULT_OUT, help="snapshot directory (default repro/)")
    ap.add_argument("--verify", action="store_true",
                    help="compare the committed snapshot to the live DB instead of writing")
    args = ap.parse_args()

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
