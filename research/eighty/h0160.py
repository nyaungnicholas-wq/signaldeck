import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        print(f"Cannot open database: {e}", file=sys.stderr)
        sys.exit(0)

    # Check for required tables
    required_tables = {'bars', 'symbols'}
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    existing_tables = {row['name'] for row in cur.fetchall()}
    if not required_tables.issubset(existing_tables):
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)

    # Load symbols (only US stocks, active)
    try:
        cur.execute("""
            SELECT id, symbol, market
            FROM symbols
            WHERE active = 1
        """)
        symbols = {row['id']: dict(row) for row in cur.fetchall()}
    except:
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)

    if not symbols:
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)

    # We need dividend declaration dates. The database has no dividend table.
    # Without dividend data, we cannot identify the events.
    # Therefore, insufficient data.
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)

if __name__ == "__main__":
    main()