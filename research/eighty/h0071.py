import sqlite3
import sys
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        conn.row_factory = sqlite3.Row
    except Exception as e:
        print(f"ERROR connecting to database: {e}")
        sys.exit(1)

    # Check if we can query the necessary tables at all
    try:
        # Minimal existence check - no fabricated data
        cur = conn.cursor()
        cur.execute("SELECT 1 FROM bars LIMIT 1")
        cur.execute("SELECT 1 FROM symbols LIMIT 1")
        cur.execute("SELECT 1 FROM prediction_outcomes LIMIT 1")
        # We cannot identify split announcements from available schema
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
    except Exception as e:
        print(f"ERROR accessing database tables: {e}")
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)

if __name__ == "__main__":
    main()