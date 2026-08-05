import sqlite3
import sys
from datetime import datetime
from collections import defaultdict
import math

def main():
    try:
        db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = db.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)

    try:
        # Check if required tables exist
        tables = [row[0] for row in cur.execute(
            "SELECT name FROM sqlite_master WHERE type='table' AND name IN ('bars', 'symbols')"
        ).fetchall()]
        if set(['bars', 'symbols']).issubset(set(tables)):
            # Get symbols
            cur.execute("SELECT id, symbol FROM symbols WHERE market='stocks'")
            symbols = cur.fetchall()
            if len(symbols) == 0:
                print("INSUFFICIENT=1")
                sys.exit(0)
            
            # Count total bars
            cur.execute("SELECT COUNT(*) FROM bars")
            bar_count = cur.fetchone()[0]
            if bar_count == 0:
                print("INSUFFICIENT=1")
                sys.exit(0)
            
            # Since we don't have Form 4 insider purchase data in the schema,
            # we cannot identify the required universe. The hypothesis requires
            # specific insider purchase data that isn't in the available tables.
            print("INSUFFICIENT=1")
            sys.exit(0)
        else:
            print("INSUFFICIENT=1")
            sys.exit(0)
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)
    finally:
        db.close()

if __name__ == "__main__":
    main()