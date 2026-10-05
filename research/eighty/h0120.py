import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    # Check for required tables and columns
    required = {
        'bars': ['symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'],
        'symbols': ['id', 'symbol', 'market', 'name', 'active', 'added_at', 'stream', 'delisted_at']
    }
    
    for table, columns in required.items():
        try:
            cur = conn.execute(f"PRAGMA table_info({table})")
            existing = {row[1] for row in cur.fetchall()}
            for col in columns:
                if col not in existing:
                    print("INSUFFICIENT=1")
                    return
        except:
            print("INSUFFICIENT=1")
            return

    # Need Form 4 data - not available in schema
    print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()