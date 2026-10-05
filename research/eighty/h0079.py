import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return 0

    # Check for convertible notes event data
    try:
        cur = conn.cursor()
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cur.fetchall()}
        
        # We need corporate actions/events data to identify convertible notes offerings
        needed = {'corporate_actions', 'events', 'offerings', 'filings', 'news'}
        if not needed.intersection(tables):
            print("INSUFFICIENT=1")
            return 0
    except Exception:
        print("INSUFFICIENT=1")
        return 0
    finally:
        conn.close()
    
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())