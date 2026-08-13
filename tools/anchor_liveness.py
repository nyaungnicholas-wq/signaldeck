#!/usr/bin/env python3
"""Measure the gap between local ledger-anchor signing and third-party publication.

SignalDeck signs a cryptographic ledger anchor locally and is supposed to publish
that anchor to a third-party public git repo. The guarantee rests on a digest
sitting in a third party's git history, which an operator who holds the signing
key cannot fabricate after the fact. Local anchors alone carry none of that
third-party guarantee.

Measured 2026-08-12: local signing works fine (ledger_anchors holds 10 rows,
newest 2026-08-10), but ops/anchor-publish.sh had not run since 2026-07-27 and
nothing measured the gap. This tool measures it. It is read-only except for the
single optional dq_events row on failure; it never publishes, signs, repairs,
or edits a ledger.
"""

import argparse
import datetime
import json
import os
import sqlite3
import sys

DEFAULT_DB = os.path.join(os.path.dirname(os.path.abspath(__file__)), os.pardir, "data", "signaldeck.db")


def iso_utc(ts):
    if ts is None:
        return None
    return datetime.datetime.fromtimestamp(ts, datetime.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


def main():
    p = argparse.ArgumentParser(description="Measure the local-anchor vs published-anchor gap.")
    p.add_argument("--db", default=DEFAULT_DB, help="Path to the SignalDeck SQLite database.")
    p.add_argument("--max-age-hours", type=int, default=48, help="Staleness ceiling for external publication (hours).")
    p.add_argument("--emit-dq-event", action="store_true", help="On failure, insert one row into dq_events.")
    p.add_argument("--json", action="store_true", help="Print machine-readable JSON instead of a human table.")
    args = p.parse_args()

    db_path = os.path.abspath(args.db)
    uri = "file:{}?mode=ro".format(db_path.replace("?", "%3f").replace("#", "%23"))
    try:
        conn = sqlite3.connect(uri, uri=True)
    except sqlite3.OperationalError:
        print("Cannot open database read-only: {}".format(db_path), file=sys.stderr)
        return 2

    cur = conn.cursor()
    cur.execute("SELECT MAX(created_at), COUNT(*) FROM ledger_anchors")
    newest_anchor, anchor_count = cur.fetchone()
    # The column is `v`, not `value` — see schema.sql's meta(k,v).
    cur.execute("SELECT v FROM meta WHERE k='anchor_last_published'")
    row = cur.fetchone()
    last_published = int(row[0]) if row and row[0] is not None else None
    conn.close()

    now = int(datetime.datetime.now(datetime.UTC).timestamp())

    if anchor_count == 0:
        verdict = "NO ANCHORS"
        ok = True
        age_hours = None
        exit_code = 0
    elif last_published is None:
        verdict = "NEVER PUBLISHED"
        ok = False
        age_hours = round((now - newest_anchor) / 3600, 2) if newest_anchor is not None else None
        exit_code = 1
    elif newest_anchor > last_published and (now - last_published) > args.max_age_hours * 3600:
        verdict = "STALE"
        ok = False
        age_hours = round((newest_anchor - last_published) / 3600, 2)
        exit_code = 1
    else:
        verdict = "OK"
        ok = True
        age_hours = round((now - last_published) / 3600, 2) if last_published is not None else None
        exit_code = 0

    if args.json:
        out = {
            "anchorCount": anchor_count,
            "newestAnchorTs": newest_anchor,
            "lastPublishedTs": last_published,
            "ageHours": age_hours,
            "verdict": verdict,
            "ok": ok,
        }
        print(json.dumps(out))
    else:
        print("anchor_count      : {}".format(anchor_count))
        print("newest anchor     : {}".format(iso_utc(newest_anchor) if newest_anchor is not None else "none"))
        print("last published    : {}".format(iso_utc(last_published) if last_published is not None else "never"))
        # The number means something different per verdict, so name which one
        # it is rather than printing a bare "gap" the reader has to guess at.
        gap_label = {
            "NEVER PUBLISHED": "newest anchor age",
            "STALE": "newest anchor is ahead of the last publish by",
        }.get(verdict, "time since last publish")
        print("{:<18}: {}".format(gap_label, age_hours if age_hours is not None else "n/a"))
        print("verdict           : {}".format(verdict))
        if not ok:
            print("")
            print("These anchors exist ONLY on this machine and carry no third-party timestamp.")
            print("Publish them with ops/anchor-publish.sh (see SIGNALDECK_ANCHOR_REPO).")

    if args.emit_dq_event and not ok:
        try:
            wconn = sqlite3.connect(db_path)
            wcur = wconn.cursor()
            detail = "verdict={} gap_hours={}".format(verdict, age_hours)
            wcur.execute("INSERT INTO dq_events(ts,kind,detail) VALUES(?,?,?)", (now, "anchor_publish_stale", detail))
            wconn.commit()
            wconn.close()
        except Exception as e:
            print("Warning: failed to record dq_events row: {}".format(e), file=sys.stderr)

    return exit_code


if __name__ == "__main__":
    sys.exit(main())