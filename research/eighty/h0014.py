import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Check if we have the necessary tables
        cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
        tables = {row[0] for row in cur.fetchall()}
        required_tables = {'bars', 'symbols', 'prediction_outcomes'}
        if not required_tables.issubset(tables):
            print("INSUFFICIENT=1")
            return
            
        # Check if prediction_outcomes has the required columns
        cur.execute("PRAGMA table_info(prediction_outcomes)")
        columns = {row[1] for row in cur.fetchall()}
        required_columns = {'symbol_id', 'horizon', 'ts', 'prob', 'up', 'fwd_return', 'resolved_at', 'basis_epoch'}
        if not required_columns.issubset(columns):
            print("INSUFFICIENT=1")
            return
            
        # Check if we have any rows in prediction_outcomes
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes")
        count = cur.fetchone()[0]
        if count == 0:
            print("INSUFFICIENT=1")
            return
            
        # Check for active stocks
        cur.execute("SELECT id, symbol FROM symbols WHERE market = 'stocks' AND active = 1")
        active_stocks = cur.fetchall()
        if not active_stocks:
            print("INSUFFICIENT=1")
            return
            
        # Since the hypothesis requires 13D filing data which we don't have in the schema,
        # we cannot test it. The prediction_outcomes table has usable labels but no
        # connection to activist filings. We need to report INSUFFICIENT.
        print("INSUFFICIENT=1")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()