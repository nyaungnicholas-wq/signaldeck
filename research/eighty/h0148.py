#!/usr/bin/env python3
"""Test spin-off information diffusion hypothesis."""
import sqlite3
import sys
from collections import defaultdict

DB_PATH = "data/signaldeck.db"
ROURI = f"file:{DB_PATH}?mode=ro"

def main():
    try:
        conn = sqlite3.connect(ROURI, uri=True)
        conn.row_factory = sqlite3.Row
    except sqlite3.Error as e:
        print(f"ERROR: Cannot open database: {e}", file=sys.stderr)
        sys.exit(1)
    
    # Check if we have the required tables and columns
    try:
        cur = conn.cursor()
        cur.execute("SELECT symbol_id, tf, ts, open, high, low, close, volume FROM bars LIMIT 1")
        cur.execute("SELECT id, symbol, market, name, active FROM symbols LIMIT 1")
        cur.execute("SELECT symbol_id, horizon, ts, prob, up, fwd_return, resolved_at FROM prediction_outcomes LIMIT 1")
    except sqlite3.Error as e:
        print(f"INSUFFICIENT=1")
        conn.close()
        sys.exit(0)
    
    # We cannot identify spin-off events from available data
    # No corporate actions table, no spin-off identifiers in symbols
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == "__main__":
    main()