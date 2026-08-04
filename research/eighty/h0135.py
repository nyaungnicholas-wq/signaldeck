import sqlite3, sys
from datetime import datetime

DB_PATH = 'file:data/signaldeck.db?mode=ro'
TWO_YEARS_IN_DAYS = 365*2
TEN_SESSIONS = 10
TWENTY_SESSIONS = 20
SIXTY_SESSIONS = 60
HORIZON_DAYS = 20
THRESHOLD_UP = 0.05
THRESHOLD_DOWN = -0.05
THRESHOLD_VOL = 0.60
THRESHOLD_PRICE = 5.0
THRESHOLD_MCAP_LOW = 300e6
THRESHOLD_MCAP_HIGH = 20e9
THRESHOLD_ADV = 10e6
THRESHOLD_PRE_T_RISE = 0.30
THRESHOLD_TOP_DECILE_VOL = 0.9
THRESHOLD_PRECISION = 0.80
THRESHOLD_ABSTAIN_RATE = 0.98
THRESHOLD_EDGE = 0.10

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True, timeout=10)
    except Exception as e:
        print("INSUFFICIENT=1")
        return
    
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Check if we have the needed tables
    c.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in c.fetchall()}
    required = {'bars', 'symbols', 'prediction_outcomes'}
    if not required.issubset(tables):
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # We need to identify Russell 3000 additions. No table provides this directly.
    # We'll attempt to proxy via `symbols` but there's no column indicating index membership.
    # Since we cannot identify additions without external data, we must declare insufficient.
    # However, let's verify we have the needed columns in each table.
    c.execute("PRAGMA table_info(bars)")
    bar_cols = {row[1] for row in c.fetchall()}
    if not {'symbol_id', 'tf', 'ts', 'close', 'volume'}.issubset(bar_cols):
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    c.execute("PRAGMA table_info(symbols)")
    sym_cols = {row[1] for row in c.fetchall()}
    if not {'id', 'symbol', 'market'}.issubset(sym_cols):
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    c.execute("PRAGMA table_info(prediction_outcomes)")
    pred_cols = {row[1] for row in c.fetchall()}
    if not {'symbol_id', 'horizon', 'ts', 'up'}.issubset(pred_cols):
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Without a way to identify Russell 3000 addition events, we cannot proceed.
    # The schema does not contain index constituent data.
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == "__main__":
    main()