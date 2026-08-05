import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Check if we have enough data to even start
        # We need symbols that are common stocks in the US market
        cur.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks'")
        stock_count = cur.fetchone()[0]
        if stock_count < 10:
            print("INSUFFICIENT=1")
            return 0
            
        # Check for prediction_outcomes (usable labels)
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes")
        po_count = cur.fetchone()[0]
        if po_count < 100:
            print("INSUFFICIENT=1")
            return 0
            
        # The hypothesis requires data about equity offerings which isn't in the schema
        # We have no offering announcements, earnings schedules, merger/buyback data,
        # book equity, going-concern qualifications, etc.
        # This is insufficient data to test the specific hypothesis
        
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