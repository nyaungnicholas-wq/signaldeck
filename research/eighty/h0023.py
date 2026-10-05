import sqlite3
import sys

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Check if required tables exist
        required_tables = ['bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores']
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        existing_tables = {row[0] for row in cursor.fetchall()}
        
        missing_tables = [t for t in required_tables if t not in existing_tables]
        if missing_tables:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # Check if we have enough data to even attempt this analysis
        # Need at least some symbols with bars
        cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM bars")
        symbol_count = cursor.fetchone()[0]
        
        if symbol_count == 0:
            print("INSUFFICIENT=1")
            conn.close()
            return
            
        # Since we have the schema but no 13D filing data in the provided tables,
        # we cannot test this hypothesis with the available data.
        # The database contains price data and some derived scores/outcomes,
        # but no raw SEC filing data about 13D filings, earnings schedules,
        # or corporate events required by the hypothesis.
        print("INSUFFICIENT=1")
        conn.close()
        
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)

if __name__ == "__main__":
    main()