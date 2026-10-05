import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return

    try:
        cursor = conn.cursor()
        
        # Check if we can access required tables
        required_tables = ['bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores']
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        existing_tables = {row[0] for row in cursor.fetchall()}
        
        if not all(t in existing_tables for t in required_tables):
            print("INSUFFICIENT=1")
            return
        
        # Check if symbols table has data
        cursor.execute("SELECT COUNT(*) FROM symbols")
        symbols_count = cursor.fetchone()[0]
        
        # Check if bars table has data
        cursor.execute("SELECT COUNT(*) FROM bars")
        bars_count = cursor.fetchone()[0]
        
        # Check if prediction_outcomes table has data
        cursor.execute("SELECT COUNT(*) FROM prediction_outcomes")
        po_count = cursor.fetchone()[0]
        
        # We need dividend initiation events, which don't exist in the schema
        # The data lacks: dividend announcements, earnings calendar, market cap at time T,
        # book equity, going-concern qualifications, merger/secondary/buyback/13D/credit events
        
        # This hypothesis requires data that is not present in the database
        # We cannot identify first-ever dividend initiations from the available tables
        
        print("INSUFFICIENT=1")
        
    except Exception:
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()