import sqlite3
import sys
from collections import defaultdict
from math import sqrt

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Check for required tables
    required = {'bars', 'symbols', 'prediction_outcomes'}
    try:
        tables = {r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type='table'").fetchall()}
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)
    if not required.issubset(tables):
        print("INSUFFICIENT=1")
        sys.exit(0)

    # We need repurchase announcements. No such table exists in schema.
    # Therefore data is insufficient.
    print("INSUFFICIENT=1")
    sys.exit(0)

if __name__ == "__main__":
    main()