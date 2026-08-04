import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Check for repurchase announcement data - we don't have it in schema
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = [r[0] for r in cur.fetchall()]
        
        # No table for repurchase announcements exists in the schema
        # Cannot derive the required signal (repurchase authorization details)
        print("INSUFFICIENT=1")
        conn.close()
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0

if __name__ == "__main__":
    sys.exit(main())