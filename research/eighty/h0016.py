import sqlite3
import sys
import json
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return

    # Check for required data
    try:
        cur = conn.cursor()
        # Check we have US stocks with basic data
        cur.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks'")
        stock_count = cur.fetchone()[0]
        
        # Check we have price/volume bars
        cur.execute("SELECT COUNT(*) FROM bars WHERE tf='1d'")
        bar_count = cur.fetchone()[0]
        
        # Check we have prediction_outcomes with up labels
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE up IS NOT NULL")
        label_count = cur.fetchone()[0]
        
        if stock_count < 100 or bar_count < 1000 or label_count < 1000:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # We have basic data but cannot test the specific hypothesis due to missing required fields:
        # - No lockup expiry dates
        # - No float/outstanding shares data
        # - No VC/PE backing flag
        # - No earnings calendar
        # - No secondary offering announcements
        # - No volatility filters beyond basic price data
        # 
        # The database contains none of the required decision criteria fields.
        # We cannot fabricate lockup expiry dates, float data, or other required inputs.
        print("INSUFFICIENT=1")
        conn.close()
        
    except Exception:
        print("INSUFFICIENT=1")
        conn.close()

if __name__ == "__main__":
    main()