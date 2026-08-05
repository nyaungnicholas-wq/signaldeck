import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return

    try:
        cur = conn.cursor()
        
        # Check if we have the required tables
        cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('bars', 'symbols')")
        tables = {row[0] for row in cur.fetchall()}
        if not {'bars', 'symbols'}.issubset(tables):
            print("INSUFFICIENT=1")
            return
            
        # Check for S&P 500 index data - we need to know which stocks are in S&P 500 and when they were added
        # The schema has no table for index membership or addition events
        # We cannot derive S&P 500 membership from the given tables
        print("INSUFFICIENT=1")
        
    except Exception:
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == '__main__':
    main()