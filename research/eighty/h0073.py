import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return 0
    
    cursor = conn.cursor()
    
    # Check for existence of required tables and columns
    required_tables = {'bars', 'symbols', 'prediction_outcomes'}
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table'")
    available_tables = {row[0] for row in cursor.fetchall()}
    if not required_tables.issubset(available_tables):
        print("INSUFFICIENT=1")
        return 0
    
    # Check if prediction_outcomes has the required columns
    cursor.execute("PRAGMA table_info(prediction_outcomes)")
    pred_cols = {row[1] for row in cursor.fetchall()}
    if not {'symbol_id', 'horizon', 'ts', 'prob', 'up', 'fwd_return', 'resolved_at', 'basis_epoch'}.issubset(pred_cols):
        print("INSUFFICIENT=1")
        return 0
    
    # Get symbols that are stocks (US-listed common stocks)
    cursor.execute("SELECT id FROM symbols WHERE market = 'stocks' AND active = 1")
    stock_ids = {row[0] for row in cursor.fetchall()}
    if not stock_ids:
        print("INSUFFICIENT=1")
        return 0
    
    # Get bars with tf='1d' for these stocks
    # We need to compute per stock daily stats and then look for insider signals
    # But we don't have Form 4 insider transaction data in this database
    # The hypothesis requires insider transaction data which is not present
    # Therefore, we cannot form entry signals
    
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())