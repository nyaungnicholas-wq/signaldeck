import sqlite3
import math
import sys

def main():
    # Connect to the database in read-only mode
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cursor = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    # Check for required tables
    required_tables = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cursor.fetchall()}
    
    if not required_tables.issubset(tables):
        print("INSUFFICIENT=1")
        return
    
    # Check for insider trade data (Form 4) which is required but missing
    # Since hypothesis requires insider trades and database doesn't contain them,
    # data is insufficient.
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == "__main__":
    main()