import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    cursor = conn.cursor()
    
    # Check if we have the required tables
    required_tables = {'bars', 'symbols', 'prediction_outcomes'}
    try:
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        existing_tables = {row[0] for row in cursor.fetchall()}
        if not required_tables.issubset(existing_tables):
            print("INSUFFICIENT=1")
            conn.close()
            sys.exit(0)
    except Exception:
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
    
    # We have no ASR announcements table, no earnings schedule, no market cap data,
    # no dollar volume data, no merger/liquidity event flags.
    # The required conditions cannot be evaluated from the given schema.
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == "__main__":
    main()