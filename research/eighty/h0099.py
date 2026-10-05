import sqlite3
import sys
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    cur = conn.cursor()
    
    # Check for required tables
    required_tables = {'bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores'}
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    if not required_tables.issubset(tables):
        print("INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
    
    # Check for earnings data: none of the provided tables have earnings columns
    # The hypothesis requires earnings announcement data to compute SUE
    # Since no earnings table exists in the schema, we cannot compute SUE
    print("INSUFFICIENT=1")
    conn.close()
    sys.exit(0)

if __name__ == "__main__":
    main()