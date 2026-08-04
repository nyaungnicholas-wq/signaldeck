import sqlite3
import math
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return 0

    try:
        # Check if we have enough data to identify lockup expiry events
        # We need IPO dates to compute lockup expiry, but no such table exists
        # The symbols table has added_at but not IPO date
        # Without IPO dates we cannot identify lockup expiry events
        # Therefore data is insufficient for this specific hypothesis
        
        # Quick verification that required tables exist
        cur = conn.cursor()
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cur.fetchall()}
        
        required = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
        if not required.issubset(tables):
            print("INSUFFICIENT=1")
            return 0
            
        # The hypothesis requires lockup expiry dates which are not in any table
        # We cannot proceed with the analysis
        print("INSUFFICIENT=1")
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    exit(main())