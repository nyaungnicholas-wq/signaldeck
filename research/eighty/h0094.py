import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return 0

    # Quick check for required tables and data existence
    try:
        cur = conn.cursor()
        # Check if bars has data
        cur.execute("SELECT COUNT(*) FROM bars")
        bars_count = cur.fetchone()[0]
        if bars_count == 0:
            print("INSUFFICIENT=1")
            return 0
        
        # Check if symbols has data
        cur.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks'")
        stock_count = cur.fetchone()[0]
        if stock_count == 0:
            print("INSUFFICIENT=1")
            return 0
            
        # Check if prediction_outcomes has data (for labels)
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes")
        pred_count = cur.fetchone()[0]
        if pred_count == 0:
            print("INSUFFICIENT=1")
            return 0
            
    except Exception:
        print("INSUFFICIENT=1")
        return 0
    finally:
        conn.close()
    
    # If we reach here, data exists but we cannot implement the full test with only
    # the available schema. The hypothesis requires market cap, earnings dates,
    # corporate actions, book equity, etc., which are not in the database.
    # According to the instructions, we must print INSUFFICIENT=1 and exit.
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())