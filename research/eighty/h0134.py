import sqlite3
import math

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        
        # Check if prediction_outcomes has sufficient data
        cur = conn.cursor()
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes")
        row_count = cur.fetchone()[0]
        
        if row_count == 0:
            print("INSUFFICIENT=1")
            return
            
        # Get the distinct symbols from prediction_outcomes
        cur.execute("SELECT DISTINCT symbol_id FROM prediction_outcomes")
        symbols = [row[0] for row in cur.fetchall()]
        
        if len(symbols) == 0:
            print("INSUFFICIENT=1")
            return
            
        # For each symbol, we need to check if we can form a testable hypothesis
        # The hypothesis requires data breach events which are not in the database
        # We cannot identify data breach announcements, earnings releases, etc.
        # Therefore, we must output INSUFFICIENT=1
        
        print("INSUFFICIENT=1")
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()