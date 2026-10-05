import sqlite3
import sys
from collections import defaultdict
from datetime import datetime, timezone
from math import sqrt

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
    except Exception:
        print("INSUFFICIENT=1")
        return 0

    try:
        # Get all symbols with market='stocks' and at least 12 months of history
        cur = conn.cursor()
        cur.execute("SELECT id, symbol, market FROM symbols WHERE market='stocks'")
        symbols = {row['id']: dict(row) for row in cur.fetchall()}
        
        if not symbols:
            print("INSUFFICIENT=1")
            return 0
            
        # Get bars for all stock symbols, ordered by symbol and timestamp
        # We'll need this for all calculations
        cur.execute("SELECT symbol_id, tf, ts, open, high, low, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
        bars_by_symbol = defaultdict(list)
        for row in cur.fetchall():
            bars_by_symbol[row['symbol_id']].append(dict(row))
        
        # Check we have enough data
        min_bars = 252  # 12 months
        valid_symbols = []
        for sym_id, sym_info in symbols.items():
            if sym_id in bars_by_symbol and len(bars_by_symbol[sym_id]) >= min_bars:
                valid_symbols.append((sym_id, sym_info['symbol']))
        
        if len(valid_symbols) < 10:
            print("INSUFFICIENT=1")
            return 0
            
        # Since we don't have Form 4 data, prediction_outcomes, or regime_outcomes,
        # we cannot test the specific hypothesis. The database lacks the required
        # Form 4 insider purchase data.
        print("INSUFFICIENT=1")
        return 0
        
    except Exception:
        print("INSUFFICIENT=1")
        return 0
    finally:
        conn.close()

if __name__ == "__main__":
    sys.exit(main())