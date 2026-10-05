#!/usr/bin/env python3
import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)

    try:
        # Check if we have the required tables
        tables = [row[0] for row in conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table'").fetchall()]
        required = ['bars', 'symbols', 'prediction_outcomes']
        for t in required:
            if t not in tables:
                print("INSUFFICIENT=1")
                conn.close()
                return

        # Check if prediction_outcomes has the 'up' column
        cols = [row[1] for row in conn.execute("PRAGMA table_info(prediction_outcomes)").fetchall()]
        if 'up' not in cols or 'fwd_return' not in cols:
            print("INSUFFICIENT=1")
            conn.close()
            return

        # We cannot identify S&P 500 additions, announcement dates, or corporate actions
        # from the available schema. The hypothesis requires specific event data not present.
        print("INSUFFICIENT=1")
    except Exception:
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == '__main__':
    main()