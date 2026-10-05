import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
    except:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Check for required data existence
    required_tables = ['bars', 'symbols']
    for t in required_tables:
        c.execute(f"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='{t}'")
        if c.fetchone()[0] == 0:
            print("INSUFFICIENT=1")
            sys.exit(0)

    # Get U.S. common stocks (exclude ADRs, REITs, SPACs)
    try:
        c.execute("SELECT id, symbol FROM symbols WHERE market='stocks' AND name NOT LIKE '%ADR%' AND name NOT LIKE '%REIT%' AND name NOT LIKE '%SPAC%'")
        symbols = c.fetchall()
    except:
        print("INSUFFICIENT=1")
        sys.exit(0)
    if len(symbols) == 0:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Get daily bars for price checks (1d timeframe)
    try:
        c.execute("SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d'")
        all_bars = c.fetchall()
    except:
        print("INSUFFICIENT=1")
        sys.exit(0)
    if len(all_bars) == 0:
        print("INSUFFICIENT=1")
        sys.exit(0)

    # Organize bars by symbol_id
    bars_by_symbol = defaultdict(list)
    for row in all_bars:
        symbol_id, ts, open_p, high, low, close, volume = row
        bars_by_symbol[symbol_id].append((ts, open_p, high, low, close, volume))

    # Sort each symbol's bars by timestamp
    for symbol_id in bars_by_symbol:
        bars_by_symbol[symbol_id].sort(key=lambda x: x[0])

    # We need earnings data but none exists in the schema.
    # The hypothesis requires SUE scores, earnings dates, market cap, book equity, etc.
    # None of these columns exist in the provided tables.
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == "__main__":
    main()