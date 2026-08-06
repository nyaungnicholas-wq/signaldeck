import sqlite3
import math

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
cur = conn.cursor()

# Check for required data availability
# 1. We need inst_holdings for 13F data and symbol_id mapping
# 2. We need bars for price data and volume
# 3. We need prediction_outcomes for labels
try:
    # Check inst_holdings exists and has data
    cur.execute("SELECT COUNT(*) FROM inst_holdings WHERE cik IS NOT NULL")
    ih_count = cur.fetchone()[0]
    if ih_count == 0:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
    
    # Check bars for daily data
    cur.execute("SELECT COUNT(*) FROM bars WHERE tf = '1d'")
    bars_count = cur.fetchone()[0]
    if bars_count == 0:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
    
    # Check prediction_outcomes exists and has data with horizons
    cur.execute("SELECT COUNT(DISTINCT horizon) FROM prediction_outcomes")
    po_horizons = cur.fetchone()[0]
    if po_horizons == 0:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
    
    # Check symbols table
    cur.execute("SELECT COUNT(*) FROM symbols")
    sym_count = cur.fetchone()[0]
    if sym_count == 0:
        print("INSUFFICIENT=1")
        conn.close()
        exit(0)
    
except Exception as e:
    print("INSUFFICIENT=1")
    conn.close()
    exit(0)

# The mechanism requires 13F filing dates, but inst_holdings has only period (quarter end)
# and we know 13Fs are filed up to 45 days later. Without exact filing dates,
# we cannot determine T (first trading day after disclosure).
# The filings table exists but has only 2026-02-05..now and no 13F forms.
# Therefore the required data to identify 13F disclosure dates is missing.

print("INSUFFICIENT=1")
conn.close()
exit(0)