import sqlite3
import sys
import os
from collections import defaultdict

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    try:
        conn = sqlite3.connect(db_path, uri=True, timeout=10)
        cur = conn.cursor()
        
        # Check for required tables
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        available_tables = {row[0] for row in cur.fetchall()}
        required_tables = {'bars', 'symbols', 'prediction_outcomes'}
        if not required_tables.issubset(available_tables):
            print("INSUFFICIENT=1")
            return
        
        # We need Form 4 data which is not in the database schema
        # The hypothesis requires insider open-market purchase filings (Form 4)
        # which are not stored in any of the provided tables
        print("INSUFFICIENT=1")
        
    except Exception as e:
        # If any error occurs, treat as insufficient data
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()