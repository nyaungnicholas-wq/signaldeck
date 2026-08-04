import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
    except Exception:
        print("INSUFFICIENT=1")
        return 0
    
    # Check if we have any table that can provide dividend initiation events
    required_tables = ['bars', 'symbols']
    try:
        cursor = conn.cursor()
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cursor.fetchall()}
        if not all(t in tables for t in required_tables):
            print("INSUFFICIENT=1")
            return 0
    except Exception:
        print("INSUFFICIENT=1")
        return 0
    
    # We cannot test dividend initiation hypothesis without dividend event data
    # The available schema has no dividend-related tables or columns
    print("INSUFFICIENT=1")
    conn.close()
    return 0

if __name__ == "__main__":
    sys.exit(main())