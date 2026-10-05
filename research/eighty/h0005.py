import sys
import sqlite3
from datetime import datetime, timedelta
import collections
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0

    # Check if we have necessary data in symbols for U.S. stocks (market='stocks')
    cur.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks'")
    if cur.fetchone()[0] == 0:
        conn.close()
        print("INSUFFICIENT=1")
        return 0

    # Check if we have daily bars
    cur.execute("SELECT COUNT(*) FROM bars WHERE tf='1d'")
    if cur.fetchone()[0] == 0:
        conn.close()
        print("INSUFFICIENT=1")
        return 0

    # Get all stock symbol_ids
    cur.execute("SELECT id FROM symbols WHERE market='stocks'")
    stock_ids = [row['id'] for row in cur.fetchall()]

    # We need free float data - not in schema, so insufficient
    # We need earnings data - not in schema, so insufficient
    # We need institutional ownership - not in schema, so insufficient
    # Therefore, insufficient data
    conn.close()
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())