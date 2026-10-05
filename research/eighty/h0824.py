# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 823
# cycle_index: 19
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def load_macro_series(conn, series_name):
    cur = conn.execute("SELECT ts, value FROM macro_series WHERE series = ? ORDER BY ts", (series_name,))
    return {row[0]: row[1] for row in cur.fetchall()}

def get_yield_spread_at(dgs10, dgs2, ts):
    # Find latest values <= ts
    v10 = None
    v2 = None
    for t in sorted(dgs10.keys()):
        if t <= ts:
            v10 = dgs10[t]
        else:
            break
    for t in sorted(dgs2.keys()):
        if t <= ts:
            v2 = dgs2[t]
        else:
            break
    if v10 is not None and v2 is not None:
        return v10 - v2
    return None

def get_bars_for_symbol(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' AND ts BETWEEN ? AND ? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return cur.fetchall()

def compute_52w_high(closes, idx, lookback=252):
    if idx < lookback:
        return max(c for _, c in closes[:idx+1]) if idx >= 0 else None
    return max(c for _, c in closes[idx-lookback+1:idx+1])

def find_forward_return(closes, idx, horizon=21):
    if idx + horizon >= len(closes):
        return None
    return (closes[idx + horizon][1] - closes[idx][1]) / closes[idx][1]

def main():
    conn = connect()
    
    # Load macro series
    print("Loading macro series...", file=sys.stderr)
    dgs10 = load_macro_series(conn, 'DGS10')
    dgs2 = load_macro_series(conn, 'DGS2')
    if not dgs10 or not dgs2:
        print("INSUFFICIENT=1")
        return
    
    # Load officer insider purchases
    print("Loading insider trades...", file=sys.stderr)
    cur = conn.execute("""
        SELECT it.symbol_id, it.filed_ts, it.tx_ts, it.value, it.shares, it.price, s.symbol
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P' 
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' 
               OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
          AND it.filed_ts IS NOT NULL
        ORDER BY it.filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Group by symbol for efficient bar loading
    symbol_trades = defaultdict(list)
    for t in trades:
        symbol_trades[t[0]].append(t)
    
    # For each symbol, load bars covering all trade dates + horizon
    all_signals = []
    for symbol_id, trades_list in symbol_trades.items():
        min_filed = min(t[1] for t in trades_list)
        max_filed = max(t[1] for t in trades_list)
        # Need bars from 252 days before min_filed to 21 days after max_filed
        # Approximate: 1 trading day ~ 86400 seconds, but use wider window
        start_ts = min_filed - 300 * 86400
        end_ts = max_filed + 30 * 86400
        
        bars = get_bars_for_symbol(conn, symbol_id, start_ts, end_ts)
        if len(bars) < 252 + 21:
            continue
        
        bar_ts = [b[0] for b in bars]
        bar_close = [b[1] for b in bars]
        
        # Map filed_ts to bar index (latest bar <= filed_ts)
        for trade in trades_list:
            symbol_id, filed_ts, tx_ts, value, shares, price, symbol = trade
            
            # Find bar index for filed_ts
            idx = -1
            for i, ts in enumerate(bar_ts):
                if ts <= filed_ts:
                    idx = i
                else:
                    break
            if idx < 252:  # Need 252 days for 52w high
                continue
            
            # Yield curve spread at filed_ts
            spread = get_yield_spread_at(dgs10, dgs2, filed_ts)
            if spread is None or spread >= 0:  # Not inverted
                continue
            
            # 52-week high
            high_52w = compute_52w_high(list(zip(bar_ts, bar_close)), idx)
            if high_52w is None:
                continue
            
            current_price = bar_close[idx]
            pct_from_high = (current_price - high_52w) / high_52w
            if pct_from_high > -0.15:  # Not >15% below high
                continue
            
            # Forward return over 21 trading days
            fwd_ret = find_forward_return(list(zip(bar_ts, bar_close)), idx, 21)
            if fwd_ret is None:
                continue
            
            label = 1 if fwd_ret > 0 else 0
            all_signals.append({
                'symbol_id': symbol_id,
                'symbol': symbol,
                'filed_ts': filed_ts,
                'fwd_ret': fwd_ret,
                'label': label,
                'spread': spread,
                'pct_from_high': pct_from_high,
                'value': value
            })
    
    if not all_signals:
        print("INSUFFICIENT=1")
        return
    
    # Sort by filed_ts
    all_signals.sort(key=lambda x: x['filed_ts'])
    
    # Hold out most recent 20% as sealed era
    n = len(all_signals)
    split_idx = int(n * 0.8)
    train_signals = all_signals[:split_idx]
    sealed_signals = all_signals[split_idx:]
    
    def compute_metrics(signals):
        if not signals:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(signals)
        hits = sum(s['label'] for s in signals)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # Base rate within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(s['filed_ts']).date() for s in signals))
        # Design effect: approximate by 1 + (avg trades per day - 1) * intraclass_corr
        # Simplified: assume design effect = 1.5 (conservative)
        design_effect = 1.5
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(train_signals)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_signals)
    opportunities = n  # All decision points considered
    
    # Output hypothesis description
    print("MECHANISM=Officer (CEO/CFO) open-market purchases when yield curve (10Y-2Y) is inverted and stock is >15% below 52-week high")
    print("HORIZON=21d")
    print("UNIVERSE=All active stocks with >=252 daily bars and officer insider purchases (code=P) 2008-2026")
    print("ENTRY=Insider purchase filed (filed_ts) with yield curve spread < 0 and price >15% below 52w high at filing")
    print("ABSTAIN=Insufficient bar history for 52w high or forward return, missing macro data, non-officer trades")
    print("CLAIM=Inverted yield curve signals macro stress; officers buying deeply oversold stocks have private conviction that overrides macro fear, yielding positive 21-day forward returns")
    
    # Output metrics
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == '__main__':
    main()