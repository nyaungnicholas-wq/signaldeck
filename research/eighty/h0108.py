import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    # Check if database exists and is readable
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
        
        # Check if we have the required tables
        required_tables = ['bars', 'symbols', 'prediction_outcomes']
        cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
        existing_tables = {row[0] for row in cursor.fetchall()}
        
        for table in required_tables:
            if table not in existing_tables:
                print("INSUFFICIENT=1")
                return 0
        
        # Verify we have enough data to perform the test
        # Count distinct symbol_ids in prediction_outcomes
        cursor.execute("SELECT COUNT(DISTINCT symbol_id) FROM prediction_outcomes")
        distinct_symbols = cursor.fetchone()[0]
        
        if distinct_symbols < 10:  # Need at least some data
            print("INSUFFICIENT=1")
            return 0
            
        conn.close()
        print("INSUFFICIENT=1")
        return 0
        
    except Exception as e:
        print("INSUFFICIENT=1")
        return 0

if __name__ == "__main__":
    sys.exit(main())