import sqlite3
import sys
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
    except Exception:
        print("INSUFFICIENT=1")
        return

    # Check if we have the necessary tables and columns
    try:
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cur.fetchall()}
        required = {'bars', 'symbols'}
        if not required.issubset(tables):
            print("INSUFFICIENT=1")
            conn.close()
            return

        # Check for earnings data - we need an earnings announcement table with consensus estimates
        # The provided schema has no such table. We cannot fabricate earnings data.
        # We must exit with INSUFFICIENT.
        print("INSUFFICIENT=1")
        conn.close()
        return

    except Exception:
        print("INSUFFICIENT=1")
        return

if __name__ == "__main__":
    main()