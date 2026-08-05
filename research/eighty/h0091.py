import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timezone

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Check if we have enough data in key tables
        cur.execute("SELECT COUNT(*) FROM bars WHERE tf = '1d'")
        if cur.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            return 0
            
        cur.execute("SELECT COUNT(*) FROM symbols WHERE market = 'stocks'")
        if cur.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            return 0
            
        # We cannot implement the hypothesis because we lack ownership data
        # (mutual fund holdings tables are not in the database)
        print("INSUFFICIENT=1")
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    sys.exit(main())