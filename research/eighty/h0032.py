import sqlite3, sys, math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

try:
    conn = sqlite3.connect(DB_PATH, uri=True)
    cur = conn.cursor()
    
    # Verify required tables exist and check columns
    cur.execute("SELECT name FROM sqlite_master WHERE type='table';")
    tables = [row[0] for row in cur.fetchall()]
    
    required = ['bars', 'symbols', 'regime_outcomes', 'prediction_outcomes', 'scores']
    if not all(t in tables for t in required):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Check we can read the required columns from the key tables
    cur.execute("PRAGMA table_info(bars);")
    bar_cols = {row[1] for row in cur.fetchall()}
    if not {'symbol_id', 'tf', 'ts', 'open', 'high', 'low', 'close', 'volume'}.issubset(bar_cols):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    cur.execute("PRAGMA table_info(symbols);")
    sym_cols = {row[1] for row in cur.fetchall()}
    if not {'id', 'symbol', 'market'}.issubset(sym_cols):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    cur.execute("PRAGMA table_info(prediction_outcomes);")
    po_cols = {row[1] for row in cur.fetchall()}
    if not {'symbol_id', 'horizon', 'ts', 'prob', 'up', 'fwd_return', 'resolved_at', 'basis_epoch'}.issubset(po_cols):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # The hypothesis requires activist filing events and historical data
    # that are not present in the database schema provided.
    # We have no table for 13D filings, activist histories, earnings calendars,
    # proxy contests, volatility filters, etc.
    # We cannot construct the opportunity set or test the specific hypothesis
    # without fabricating data.
    
    # According to the instructions: "If the data is insufficient, print INSUFFICIENT=1 and exit 0."
    print("INSUFFICIENT=1")
    sys.exit(0)
    
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)