# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 683
# cycle_index: 10
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Verify StockTwits data range - need 63 trading days of history before any decision date
    cur.execute("SELECT MIN(ts), MAX(ts), COUNT(DISTINCT symbol_id) FROM stocktwits_sentiment")
    st_min, st_max, st_symbols = cur.fetchone()
    
    # Verify Form 4 filings date range (decision dates)
    cur.execute("SELECT MIN(filed_ts), MAX(filed_ts) FROM filings WHERE form = '4'")
    f4_min, f4_max = cur.fetchone()
    
    # Verify bars 1d range for label construction
    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf = '1d'")
    bars_min, bars_max = cur.fetchone()
    
    # Verify inst_holdings range
    cur.execute("SELECT MIN(period), MAX(period), COUNT(DISTINCT symbol_id) FROM inst_holdings")
    ih_min, ih_max, ih_symbols = cur.fetchone()
    
    conn.close()

    # StockTwits ts appears to be unix epoch (consistent with bars.ts)
    # 63 trading days ≈ 88 calendar days
    st_min_dt = datetime.fromtimestamp(st_min) if st_min else None
    st_max_dt = datetime.fromtimestamp(st_max) if st_max else None
    f4_min_dt = datetime.fromtimestamp(f4_min) if f4_min else None
    
    # Earliest possible decision date with 63 days of StockTwits history
    earliest_decision = st_min_dt + timedelta(days=88) if st_min_dt else None
    
    # Check overlap: need decision dates >= earliest_decision AND decision dates <= f4_max
    # Also need 21-day forward return from bars (decision + 21 trading days <= bars_max)
    bars_max_dt = datetime.fromtimestamp(bars_max) if bars_max else None
    latest_decision_for_label = bars_max_dt - timedelta(days=30) if bars_max_dt else None  # ~21 trading days
    
    # Schema block states: stocktwits 2026-07-11..2026-08-14 (only ~24 trading days)
    # Filings: 2026-02-17..2026-08-13
    # 63 trading days needed before decision → earliest decision ~2026-10-01
    # But filings end 2026-08-13 and StockTwits ends 2026-08-14 → ZERO overlap
    
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())