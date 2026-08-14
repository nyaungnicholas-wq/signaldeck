import os
import sys
import sqlite3
import argparse

MIN_BARS_ROWS = 1000

def verify(path, live_db=None):
    """Return (ok: bool, problems: list[str])."""
    problems = []
    
    if not os.path.exists(path):
        return False, [f"missing file: {path}"]
    if not os.access(path, os.R_OK):
        return False, [f"file not readable: {path}"]

    conn = None
    try:
        # 1. & 2. SQLite validity and quick_check
        conn = sqlite3.connect("file:" + path + "?mode=ro", uri=True)
        cursor = conn.cursor()
        
        qc = cursor.execute("PRAGMA quick_check").fetchone()
        if not qc or qc[0] != "ok":
            problems.append(f"PRAGMA quick_check failed: {qc[0] if qc else 'no result'}")
            return False, problems

        # 3. Table bars exists and has >= 1000 rows
        try:
            bars_count = cursor.execute("SELECT COUNT(*) FROM bars").fetchone()[0]
            if bars_count < MIN_BARS_ROWS:
                problems.append(f"table bars has only {bars_count} rows, expected {MIN_BARS_ROWS}")
        except sqlite3.Error:
            problems.append("table bars does not exist")

        # 4. Table prediction_ledger exists and has >= 1 row
        ledger_count = 0
        try:
            ledger_count = cursor.execute("SELECT COUNT(*) FROM prediction_ledger").fetchone()[0]
            if ledger_count < 1:
                problems.append("table prediction_ledger is empty")
        except sqlite3.Error:
            problems.append("table prediction_ledger does not exist")

        # 5. Anchor consistency
        try:
            anchor_table_exists = cursor.execute(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name='ledger_anchors'"
            ).fetchone()
            
            if not anchor_table_exists:
                problems.append("missing ledger_anchors table (anchor)")
            else:
                anchor_row = cursor.execute(
                    "SELECT ledger_count FROM ledger_anchors ORDER BY seq DESC LIMIT 1"
                ).fetchone()
                
                if not anchor_row:
                    problems.append("ledger_anchors table has no rows (anchor)")
                else:
                    last_anchor_count = anchor_row[0]
                    if ledger_count < last_anchor_count:
                        problems.append(
                            f"prediction_ledger count {ledger_count} is less than "
                            f"anchored count {last_anchor_count} (ledger)"
                        )
        except sqlite3.Error as e:
            problems.append(f"error checking anchors: {e} (anchor)")

        # 6. Live DB comparison — FAILS CLOSED.
        #
        # This is the check that catches a backup which copied cleanly but holds
        # a truncated ledger, so it is the one whose silent absence is most
        # dangerous: a stale backup that passes verification is worse than no
        # backup, because nobody goes looking for it.
        #
        # It used to end in `except Exception: pass`, which skipped the whole
        # comparison on any error and left `problems` empty — the backup then
        # reported VERIFIED having never been compared. The live DB is a
        # multi-GB SQLite under continuous write load, so "could not read it"
        # is an ordinary outcome here, not a rare edge. --live is passed by
        # ops/signaldeck-backup-offline.sh on every run, so a comparison that
        # was REQUESTED and could not be made is a problem to report, never a
        # step to skip. Matches checks 3 and 4 above, which already append a
        # problem rather than swallowing sqlite3.Error.
        if live_db:
            live_conn = None
            try:
                if not os.path.exists(live_db):
                    problems.append(f"staleness check requested but live db is missing: {live_db}")
                elif not os.access(live_db, os.R_OK):
                    problems.append(f"staleness check requested but live db is not readable: {live_db}")
                else:
                    live_conn = sqlite3.connect("file:" + live_db + "?mode=ro", uri=True)
                    live_count = live_conn.execute(
                        "SELECT COUNT(*) FROM prediction_ledger").fetchone()[0]
                    if ledger_count < (live_count * 0.5):
                        problems.append(
                            f"backup prediction_ledger count {ledger_count} is too stale "
                            f"compared to live count {live_count} (ledger)"
                        )
            except Exception as e:
                problems.append(f"staleness check against live db {live_db} could not run: {e}")
            finally:
                if live_conn:
                    live_conn.close()

    except Exception as e:
        problems.append(f"unexpected error during verification: {str(e)}")
    finally:
        if conn:
            conn.close()

    return (len(problems) == 0, problems)

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Verify SignalDeck SQLite backup integrity.")
    parser.add_argument("backup", help="Path to the backup SQLite file")
    parser.add_argument("--live", help="Path to the live SQLite file for staleness check")
    
    try:
        args = parser.parse_args()
    except SystemExit:
        sys.exit(2)

    ok, problems = verify(args.backup, live_db=args.live)
    
    if ok:
        print(f"OK: {args.backup}")
        sys.exit(0)
    else:
        for p in problems:
            print(p, file=sys.stderr)
        sys.exit(1)
