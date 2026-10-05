import sqlite3
import sys
from collections import defaultdict

def main():
    # Check database exists and read-only
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        cur = conn.cursor()
    except Exception as e:
        print(f"INSUFFICIENT=1")
        return

    # Check if we have IPO/lockup data - need at minimum: IPO dates, lockup expiry dates, earnings schedule
    # These are required to construct the universe and entry rules
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    
    # Required tables for IPO lockup hypothesis
    required = {'bars', 'symbols', 'prediction_outcomes'}
    if not required.issubset(tables):
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Check for IPO/lockup data in symbols table
    cur.execute("PRAGMA table_info(symbols)")
    symbol_cols = {row[1] for row in cur.fetchall()}
    needed_cols = {'id', 'symbol', 'market'}
    if not needed_cols.issubset(symbol_cols):
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Check bars for required columns
    cur.execute("PRAGMA table_info(bars)")
    bars_cols = {row[1] for row in cur.fetchall()}
    bars_needed = {'symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'}
    if not bars_needed.issubset(bars_cols):
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Check prediction_outcomes for required columns
    cur.execute("PRAGMA table_info(prediction_outcomes)")
    pred_cols = {row[1] for row in cur.fetchall()}
    pred_needed = {'symbol_id', 'horizon', 'ts', 'prob', 'up', 'fwd_return', 'resolved_at', 'basis_epoch'}
    if not pred_needed.issubset(pred_cols):
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Check if we have IPO date, lockup expiry date, earnings schedule, corporate events data
    # These are absolutely necessary for the hypothesis
    cur.execute("SELECT COUNT(*) FROM symbols WHERE market = 'stocks'")
    stock_count = cur.fetchone()[0]
    
    # Check if we have enough stocks and bars data
    cur.execute("SELECT COUNT(*) FROM bars WHERE tf = '1d'")
    daily_bars = cur.fetchone()[0]
    
    # Minimum data threshold
    if stock_count < 100 or daily_bars < 10000:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Since we don't have IPO dates, lockup expiry dates, earnings schedule, or corporate events data,
    # we cannot construct the universe or apply entry/abstention rules as specified.
    # The hypothesis requires data that is not in the database.
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == '__main__':
    main()