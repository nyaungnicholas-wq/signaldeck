import sqlite3
import math
import sys

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Check if we have enough data: need at least 252 days of daily bars for some symbol
    c.execute("SELECT COUNT(DISTINCT symbol_id) FROM bars WHERE tf='1d'")
    n_symbols = c.fetchone()[0]
    if n_symbols == 0:
        print("INSUFFICIENT=1")
        return 0
    
    # Get all daily bars with basic filters
    # We'll process each symbol's history sequentially
    c.execute("""
        SELECT b.symbol_id, b.ts, b.close, b.volume
        FROM bars b
        WHERE b.tf='1d'
        ORDER BY b.symbol_id, b.ts
    """)
    
    # Structure: symbol_data = {symbol_id: [(ts, close, volume), ...]}
    symbol_data = {}
    for row in c:
        sym = row['symbol_id']
        if sym not in symbol_data:
            symbol_data[sym] = []
        symbol_data[sym].append((row['ts'], row['close'], row['volume']))
    
    # Precompute labels: map (symbol_id, horizon=20, ts) -> up
    labels = {}
    c.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon=20
    """)
    for row in c:
        key = (row['symbol_id'], row['ts'])
        labels[key] = (row['up'], row['fwd_return'])
    
    conn.close()
    
    # Now process each symbol's history to find candidate days T
    calls = []  # list of (symbol_id, T_ts, T_close, T_volume, prev_close, day_index)
    
    for sym, bars in symbol_data.items():
        n = len(bars