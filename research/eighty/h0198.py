import sqlite3
import math
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        cur = conn.cursor()
        
        # Get symbols with insider purchases and at least 252 daily bars
        cur.execute("""
        SELECT s.id, COUNT(DISTINCT b.ts) as bar_count
        FROM symbols s
        JOIN insider_trades it ON it.symbol_id = s.id AND it.code = 'P' AND it.value >= 250000
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        GROUP BY s.id
        HAVING bar_count >= 252
        """)
        candidate_symbols = [row[0] for row in cur.fetchall()]
        
        if not candidate_symbols:
            print("INSUFFICIENT=1")
            return
        
        # For each candidate symbol, get daily bars and precompute required metrics
        symbol_data = {}
        for sym_id in candidate_symbols:
            cur.execute("""
            SELECT ts, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
            """, (sym_id,))
            bars = cur.fetchall()
            if len(bars) < 252:
                continue
                
            # Convert to list of dicts for easier access
            bars_list = [{'ts': row[0], 'close': row[1], 'volume': row[2]} for row in bars]
            symbol_data[sym_id] = bars_list
        
        if not symbol_data:
            print("INSUFFICIENT=1")
            return
            
        # Get insider trades for all candidate symbols
        symbol_insiders = {}
        for sym_id in symbol_data.keys():
            cur.execute("""
            SELECT it.filed_ts, it.code, it.shares, b.close
            FROM insider_trades it
            JOIN bars b ON b.symbol_id = it.symbol_id 
                AND b.tf = '1d' 
                AND date(b.ts, 'unixepoch', 'utc') = date(it.filed_ts, 'unixepoch', 'utc')
            WHERE it.symbol_id = ? AND it.value >= 250000
            """, (sym_id,))
            trades = cur.fetchall()
            symbol_insiders[sym_id] = trades
        
        # Get all trading dates in order
        cur.execute("SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts")
        all_dates = [row[0] for row in cur.fetchall()]
        
        # Create date index mapping
        date_to_idx = {d: i for i, d in enumerate(all_dates)}
        
        # Main processing
        calls = []  # (symbol_id, date, hit)
        opportunities = 0
        
        for sym_id, bars in symbol_data.items():
            if sym_id not in symbol_insiders:
                continue
                
            insider_trades = symbol_insiders[sym_id]
            
            for i in range(252, len(bars)):
                T_ts = bars[i]['ts']
                T_close = bars[i]['close']
                T_volume = bars[i]['volume']
                
                # Check price >= $5
                if T_close < 5:
                    continue
                
                # Check trailing 20-session gain > 30%
                if i >= 20:
                    close_20_ago = bars[i-20]['close']
                    if close_20_ago > 0 and (T_close / close_20_ago - 1) > 0.30:
                        continue
                
                # Check 20-session volatility in top decile (simplified)
                # We'll approximate by checking if recent range is large
                if i >= 20:
                    highs = [bars[j]['close']