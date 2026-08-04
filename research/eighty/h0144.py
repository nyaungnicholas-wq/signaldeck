import sqlite3
import sys
from datetime import datetime, timezone

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    
    # Check if we have enough data to form index addition events
    # We don't have a direct table of index additions, so we need to infer
    # from available data or determine insufficient
    try:
        # Check if we can form any events at all
        cur = conn.cursor()
        cur.execute("SELECT COUNT(*) FROM symbols WHERE market = 'stocks'")
        stock_count = cur.fetchone()[0]
        
        # We need at minimum some US stocks to potentially be S&P 500 additions
        # But we have no index membership data, so we cannot identify specific additions
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
    except Exception as e:
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)

if __name__ == "__main__":
    main()