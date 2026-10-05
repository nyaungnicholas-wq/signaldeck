# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 381
# cycle_index: 49
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB, uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Get all insider trades with code P (purchases) and S (sales)
    c.execute("""
        SELECT symbol_id, insider, title, code, shares, price, value,
               tx_ts, filed_ts
        FROM insider_trades
    """)
    trades = c.fetchall()
    
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Separate purchases and sales
    purchases = [t for t in trades if t['code'] == 'P']
    sales = [t for t in trades if t['code'] == 'S']
    
    if not purchases:
        print("INSUFFICIENT=1")
        return
    
    # Get symbols with daily bars
    c.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf='1d'")
    symbols_with_bars = {row['symbol_id'] for row in c.fetchall()}
    
    # Filter purchases to symbols with bars
    purchases = [p for p in purchases if p['symbol_id'] in symbols_with_bars]
    
    if not purchases:
        print("INSUFFICIENT=1")
        return
    
    # Build lookup: for each symbol, list of sale filed timestamps
    sales_by_symbol = defaultdict(list)
    for sale in sales:
        sales_by_symbol[sale['symbol_id']].append(sale['filed_ts'])
    
    # Group purchases by symbol
    purchases_by_symbol = defaultdict(list)
    for p in purchases:
        purchases_by_symbol[p['symbol_id']].append(p)
    
    # Get all daily bars for symbols in purchases
    symbols_needed = set(p['symbol_id'] for p in purchases)
    bars_by_symbol = defaultdict(list)
    for sym_id in symbols_needed:
        c.execute("""
            SELECT ts, high, close, volume 
            FROM bars 
            WHERE symbol_id=? AND tf='1d'
            ORDER BY ts
        """, (sym_id,))
        bars_by_symbol[sym_id] = c.fetchall()
    
    # Precompute for each symbol: trading day timestamps, and for each day:
    # - 252-day high (as of that day)
    # - 20-day average dollar volume (as of that day)
    # We'll store these in dictionaries indexed by (symbol_id, trading_day_ts)
    high_252_by_symday = {}
    avg_vol_by_symday = {}
    
    for sym_id, bars in bars_by_symbol.items():
        n = len(bars)
        if n < 252:
            continue  # Need at least 252 days for 252-day high
        
        # Compute 252-day high for each day
        for i in range(251, n):
            day_ts = bars[i]['ts']
            max_high = max(b['high'] for b in bars[i-251:i+1])
            high_252_by_symday[(sym_id, day_ts)] = max_high
        
        # Compute 20-day average dollar volume for each day
        for i in range(19, n):
            day_ts = bars[i]['ts']
            total_dollar_vol = sum(b['close'] * b['volume'] for b in bars[i-19:i+1])
            avg_dollar_vol = total_dollar_vol / 20
            avg_vol_by_symday[(sym_id, day_ts)] = avg_dollar_vol
    
    # Now process each purchase to generate candidate calls
    all_calls = []  # List of (symbol_id, disclosure_ts, outcome, ...metadata)
    
    for sym_id, sym_purchases in purchases_by_symbol.items():
        if sym_id not in bars_by_symbol:
            continue
        
        # Sort purchases by tx_ts (trade date)
        sym_purchases.sort(key=lambda x: x['tx_ts'])
        
        # For tracking first high-zone purchase in 180 days
        last_high_zone_purchase_ts = None
        
        for purchase in sym_purchases:
            trade_ts = purchase['tx_ts']
            filed_ts = purchase['filed_ts']
            trade_price = purchase['price']
            
            # Find the trading day bar for this trade (ts <= trade_ts)
            bars = bars_by_symbol[sym_id]
            trade_bar_ts = None
            for bar in reversed(bars):
                if bar['ts'] <= trade_ts:
                    trade_bar_ts = bar['ts']
                    break
            
            if trade_bar_ts is None:
                continue
            
            # Check 252-day high condition
            key = (sym_id, trade_bar_ts)
            if key not in high_252_by_symday:
                continue
            
            high_252 = high_252_by_symday[key]
            if trade_price < 0.95 * high_252:  # Not within 5% of high
                continue
            
            # Check first such purchase in prior 180 calendar days
            if last_high_zone_purchase_ts is not None:
                days_since = (trade_ts - last_high_zone_purchase_ts) / (24 * 3600)
                if days_since < 180:
                    continue
            
            # Check abstain conditions at disclosure date (filed_ts)
            # 1. No insider sale in prior 90 calendar days
            recent_sales = [s_ts for s_ts in sales_by_symbol.get(sym_id, []) 
                           if (filed_ts - s_ts) / (24 * 3600) < 90 and s_ts < filed_ts]
            if recent_sales:
                continue
            
            # 2. Average dollar volume >= $5M in 20 days before disclosure
            # Find last trading day on or before filed_ts
            last_trading_day_before_disclosure = None
            for bar in reversed(bars):
                if bar['ts'] <= filed_ts:
                    last_trading_day_before_disclosure = bar['ts']
                    break
            
            if last_trading_day_before_disclosure is None:
                continue
            
            vol_key = (sym_id, last_trading_day_before_disclosure)
            if vol_key not in avg_vol_by_symday:
                continue
            
            avg_vol = avg_vol_by_symday[vol_key]
            if avg_vol < 5_000_000:
                continue
            
            # 3. Not an option exercise/gift/144 (already filtered by code='P')
            # 4. No outstanding call for this symbol
            # We'll track per-symbol last disclosure date of a call
            # For now, assume we process in chronological order, we'll enforce this later
            
            # We have a candidate call. Need to get outcome.
            # Try to get from prediction_outcomes first
            c.execute("""
                SELECT up, fwd_return FROM prediction_outcomes 
                WHERE symbol_id=? AND horizon=21 AND basis_epoch=?
            """, (sym_id, filed_ts))
            outcome_row = c.fetchone()
            
            if outcome_row:
                outcome = outcome_row['up']
            else:
                # Compute from bars: find close price at disclosure date and 21 trading days later
                # Get index of last_trading_day_before_disclosure
                bar_idx = None
                for i, bar in enumerate(bars):
                    if bar['ts'] == last_trading_day_before_disclosure:
                        bar_idx = i
                        break
                
                if bar_idx is None or bar_idx + 21 >= len(bars):
                    continue
                
                close_at_entry = bars[bar_idx]['close']
                close_after = bars[bar_idx + 21]['close']
                outcome = 1 if close_after > close_at_entry else 0
            
            all_calls.append({
                'symbol_id': sym_id,
                'disclosure_ts': filed_ts,
                'outcome': outcome,
                'trade_ts': trade_ts,
                'trade_price': trade_price,
                'high_252': high_252
            })
            
            # Update last high-zone purchase timestamp
            last_high_zone_purchase_ts = trade_ts
    
    # Sort calls by disclosure timestamp
    all_calls.sort(key=lambda x: x['disclosure_ts'])
    
    # Enforce no duplicate calls for same symbol (keep only first)
    final_calls = []
    seen_symbols = set()
    for call in all_calls:
        if call['symbol_id'] not in seen_symbols:
            final_calls.append(call)
            seen_symbols.add(call['symbol_id'])
    
    if not final_calls:
        print("INSUFFICIENT=1")
        return
    
    # Split into train (80%) and sealed (20%)
    n_calls = len(final_calls)
    split_idx = int(0.8 * n_calls)
    train_calls = final_calls[:split_idx]
    sealed_calls = final_calls[split_idx:]
    
    # Compute metrics for train set
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0
        
        hits = sum(c['outcome'] for c in calls)
        precision = hits / len(calls)
        
        # Base rate of up class within issued calls
        base_rate = hits / len(calls)
        
        # Distinct days
        distinct_days = len(set(c['disclosure_ts'] // 86400 for c in calls))
        
        # Design effect: cluster by day
        day_clusters = defaultdict(list)
        for c in calls:
            day_key = c['disclosure_ts'] // 86400
            day_clusters[day_key].append(c['outcome'])
        
        # Compute intra-class correlation (ICC)
        if len(day_clusters) < 2:
            icc = 0
        else:
            # Compute variance of outcomes
            outcomes = [c['outcome'] for c in calls]
            mean_outcome = sum(outcomes) / len(outcomes)
            var_total = sum((o - mean_outcome) ** 2 for o in outcomes) / (len(outcomes) - 1)
            
            # Compute variance of cluster means
            cluster_means = [sum(v)/len(v) for v in day_clusters.values() if len(v) > 0]
            if len(cluster_means) > 1:
                mean_cluster = sum(cluster_means) / len(cluster_means)
                var_between = sum((m - mean_cluster) ** 2 for m in cluster_means) / (len(cluster_means) - 1)
                icc = var_between / var_total if var_total > 0 else 0
            else:
                icc = 0
        
        # Design effect = 1 + avg_cluster_size * icc
        avg_cluster_size = len(calls) / len(day_clusters)
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = len(calls) / design_effect
        
        return len(calls), precision, base_rate, distinct_days, effective_n
    
    train_n, train_precision, train_base_rate, train_distinct_days, train_effective_n = compute_metrics(train_calls)
    sealed_n, sealed_precision, sealed_base_rate, _, _ = compute_metrics(sealed_calls)
    
    # Check if we have enough data
    if train_n < 10 or sealed_n < 10:
        print("INSUFFICIENT=1")
        return
    
    # Print required output
    print(f"ISSUED={train_n}")
    print(f"OPPORTUNITIES={len(purchases)}")
    print(f"PRECISION={train_precision:.4f}")
    print(f"BASE_RATE={train_base_rate:.4f}")
    print(f"DISTINCT_DAYS={train_distinct_days}")
    print(f"EFFECTIVE_N={train_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()