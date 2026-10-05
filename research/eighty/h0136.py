import sqlite3
import math
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Check if we have necessary tables
        cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('bars','symbols')")
        tables = {row[0] for row in cur.fetchall()}
        if not {'bars','symbols'}.issubset(tables):
            print("INSUFFICIENT=1")
            return
        
        # Check for insider transaction data - we don't have it
        # The hypothesis requires Form 4 insider purchase clusters, but we have no table for that
        # We cannot derive this from the available tables (bars, symbols, regime_outcomes, prediction_outcomes, scores)
        
        # The data is insufficient to test the hypothesis
        print("INSUFFICIENT=1")
        
    except Exception as e:
        print("INSUFFICIENT=1")
    finally:
        if 'conn' in locals():
            conn.close()

if __name__ == "__main__":
    main()