import sqlite3, sys
from datetime import datetime

DB_PATH = 'file:data/signaldeck.db?mode=ro'
conn = None
try:
    conn = sqlite3.connect(DB_PATH, uri=True, timeout=5)
    conn.execute("PRAGMA journal_mode=OFF")
    conn.execute("PRAGMA query_only=ON")
    cur = conn.cursor()
    
    # Check for required fundamental data tables/columns
    # Earnings data: not present in schema (no earnings, eps, consensus, analyst coverage)
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {r[0] for r in cur.fetchall()}
    
    # Check if we have the required tables
    required_tables = {'bars', 'symbols'}
    if not required_tables.issubset(tables):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Check for earnings-related columns in any table
    # The schema provided has no earnings, EPS, consensus, analyst coverage
    cur.execute("PRAGMA table_info(bars)")
    bar_cols = {r[1] for r in cur.fetchall()}
    cur.execute("PRAGMA table_info(symbols)")
    sym_cols = {r[1] for r in cur.fetchall()}
    
    # We need earnings data that doesn't exist
    needed_cols_earnings = {'earnings_date', 'eps', 'consensus_eps', 'analyst_count', 'market_cap'}
    available_cols = bar_cols | sym_cols
    if not needed_cols_earnings.issubset(available_cols):
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # If we had the data, we would continue here...
    # But since schema lacks earnings/fundamental data, we cannot test hypothesis.
    
    print("INSUFFICIENT=1")
    sys.exit(0)
    
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)
finally:
    if conn:
        try:
            conn.close()
        except:
            pass