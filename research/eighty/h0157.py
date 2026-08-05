#!/usr/bin/env python3
import sqlite3
import sys

DB_URI = "file:data/signaldeck.db?mode=ro"

def insufficient():
    print("INSUFFICIENT=1")
    sys.exit(0)

def main():
    try:
        con = sqlite3.connect(DB_URI, uri=True)
        cur = con.cursor()

        tables = [r[0] for r in cur.execute(
            "SELECT name FROM sqlite_master WHERE type IN ('table','view')"
        )]

        table_keywords = ("13d", "schedule", "filing", "filings", "activist", "activism")
        column_keywords = (
            "13d", "schedule", "filing", "filer", "activist",
            "beneficial", "purpose", "control", "stake",
        )

        found_13d_source = False

        for t in tables:
            tl = t.lower()
            if any(k in tl for k in table_keywords):
                found_13d_source = True
                break

            try:
                cols = [r[1].lower() for r in cur.execute(
                    'PRAGMA table_info("%s")' % t.replace('"', '""')
                )]
            except sqlite3.Error:
                continue

            if any(any(k in c for k in column_keywords) for c in cols):
                found_13d_source = True
                break

        if not found_13d_source:
            insufficient()

        # No usable Schedule 13D event source exists in the documented schema.
        # Any further computation would require inventing data.
        insufficient()

    except Exception:
        insufficient()

if __name__ == "__main__":
    main()