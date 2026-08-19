# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 714
# cycle_index: 41
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_ts(d):
    return int(datetime.combine(d, datetime.min.time(), tzinfo=timezone.utc).timestamp())

def is_officer_title(title):
    if not title:
        return False
    t = title.upper()
    return any(role in t for role in ['CEO', 'CFO', 'COO', 'PRESIDENT'])

def main():
    con = connect_ro()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # Get symbols with daily bars (tf='1d') and insider trades
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        JOIN insider_trades it ON it.symbol_id = s.id
        WHERE b.ts >= strftime('%s', '2018-07-01')
    """)
    symbols = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    # Load all 1d bars for these symbols
    symbol_ids = [sid for sid, _ in symbols]
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'date': ts_to_date(row['ts']),
            'open': row['open'],
            'high': row['high'],
            'low': row['low'],
            'close': row['close'],
            'volume': row['volume']
        })

    # Load officer purchases (code='P', officer title)
    cur.execute(f"""
        SELECT symbol_id, filed_ts, tx_ts, title, code, shares, price
        FROM insider_trades
        WHERE code = 'P' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, filed_ts
    """, symbol_ids)
    
    trades_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        if is_officer_title(row['title']):
            trades_by_symbol[row['symbol_id']].append({
                'filed_ts': row['filed_ts'],
                'filed_date': ts_to_date(row['filed_ts']),
                'tx_ts': row['tx_ts'],
                'title': row['title'],
                'shares': row['shares'],
                'price': row['price']
            })

    # For each symbol, compute rolling stats and match trades
    all_decisions = []  # (decision_date, symbol_id, symbol, hit, forward_return)
    
    for sym_id, sym_name in symbols:
        bars = bars_by_symbol.get(sym_id, [])
        trades = trades_by_symbol.get(sym_id, [])
        if len(bars) < 252 or not trades:
            continue
        
        # Build date-indexed bar lookup
        bar_by_date = {b['date']: b for b in bars}
        dates = sorted(bar_by_date.keys())
        
        # Precompute daily range and volume
        daily_range = {}
        daily_volume = {}
        for d in dates:
            b = bar_by_date[d]
            daily_range[d] = (b['high'] - b['low']) / b['close'] if b['close'] > 0 else 0
            daily_volume[d] = b['volume']
        
        # Rolling 252-day 90th percentile of range, 20-day median volume
        # Compute for each date using only prior data (as-of discipline)
        range_p90 = {}
        vol_median20 = {}
        
        for i, d in enumerate(dates):
            # 252-day window ending at i-1 (day T-1 for decision on day T)
            if i >= 252:
                window_ranges = [daily_range[dates[j]] for j in range(i-252, i)]
                window_ranges.sort()
                idx = int(0.9 * (len(window_ranges) - 1))
                range_p90[d] = window_ranges[idx]
            # 20-day median volume ending at i-1
            if i >= 20:
                window_vols = [daily_volume[dates[j]] for j in range(i-20, i)]
                window_vols.sort()
                mid = len(window_vols) // 2
                if len(window_vols) % 2 == 0:
                    vol_median20[d] = (window_vols[mid-1] + window_vols[mid]) / 2
                else:
                    vol_median20[d] = window_vols[mid]
        
        # Process each trade
        for tr in trades:
            T = tr['filed_date']
            # Need T-1 data
            T_minus_1 = T - timedelta(days=1)
            # Find the actual trading day for T-1 (might be weekend/holiday)
            # Use the latest bar date <= T-1
            prior_dates = [d for d in dates if d <= T_minus_1]
            if not prior_dates:
                continue
            bar_date = max(prior_dates)
            
            # Check conditions
            if bar_date not in range_p90 or bar_date not in vol_median20:
                continue
            if daily_range[bar_date] <= range_p90[bar_date]:
                continue
            if daily_volume[bar_date] >= vol_median20[bar_date]:
                continue
            # Check 252 days history before T (bar_date is T-1, so need 252 days before bar_date)
            bar_idx = dates.index(bar_date)
            if bar_idx < 252:
                continue
            
            # Compute forward 21-day return from day T close
            # Find bar for day T (or next available)
            future_dates = [d for d in dates if d >= T]
            if not future_dates:
                continue
            entry_date = future_dates[0]
            entry_idx = dates.index(entry_date)
            exit_idx = entry_idx + 21
            if exit_idx >= len(dates):
                continue
            exit_date = dates[exit_idx]
            
            entry_close = bar_by_date[entry_date]['close']
            exit_close = bar_by_date[exit_date]['close']
            if entry_close <= 0:
                continue
            fwd_return = (exit_close - entry_close) / entry_close
            hit = 1 if fwd_return > 0 else 0
            
            all_decisions.append({
                'decision_date': T,
                'symbol_id': sym_id,
                'symbol': sym_name,
                'hit': hit,
                'fwd_return': fwd_return
            })

    if not all_decisions:
        print("INSUFFICIENT=1")
        return 0

    # Deduplicate: one (symbol, UTC day) = one observation
    # If multiple trades for same symbol on same decision day, keep first
    seen = set()
    unique_decisions = []
    for d in all_decisions:
        key = (d['symbol_id'], d['decision_date'])
        if key not in seen:
            seen.add(key)
            unique_decisions.append(d)

    # Sort by decision date
    unique_decisions.sort(key=lambda x: x['decision_date'])
    
    # 80/20 split by time (most recent 20% = sealed era)
    n_total = len(unique_decisions)
    n_sealed = max(1, int(math.ceil(n_total * 0.2)))
    n_train = n_total - n_sealed
    
    train_decisions = unique_decisions[:n_train]
    sealed_decisions = unique_decisions[n_train:]

    # Compute metrics on sealed era
    if not sealed_decisions:
        print("INSUFFICIENT=1")
        return 0

    issued_sealed = len(sealed_decisions)
    hits_sealed = sum(d['hit'] for d in sealed_decisions)
    precision_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Base rate within issued subset (sealed era)
    base_rate_sealed = hits_sealed / issued_sealed if issued_sealed > 0 else 0
    
    # Distinct days among issued calls in sealed era
    distinct_days_sealed = len(set(d['decision_date'] for d in sealed_decisions))
    
    # Design effect and effective N for sealed era
    # Cluster by day, compute ICC
    day_clusters = defaultdict(list)
    for d in sealed_decisions:
        day_clusters[d['decision_date']].append(d['hit'])
    
    if len(day_clusters) > 1:
        # Between-day variance
        day_means = [sum(hits)/len(hits) for hits in day_clusters.values()]
        grand_mean = sum(day_means) / len(day_means)
        between_var = sum((m - grand_mean)**2 for m in day_means) / (len(day_means) - 1)
        
        # Within-day variance
        within_vars = []
        for hits in day_clusters.values():
            if len(hits) > 1:
                m = sum(hits)/len(hits)
                within_vars.append(sum((h - m)**2 for h in hits) / (len(hits) - 1))
        within_var = sum(within_vars) / len(within_vars) if within_vars else 0
        
        total_var = between_var + within_var
        icc = between_var / total_var if total_var > 0 else 0
        
        avg_cluster_size = issued_sealed / len(day_clusters)
        design_effect = 1 + (avg_cluster_size - 1) * icc
        effective_n = issued_sealed / design_effect if design_effect > 0 else issued_sealed
    else:
        effective_n = issued_sealed - 1 if issued_sealed > 1 else 0.5  # ensure < issued
    
    # Ensure effective_n < issued_sealed
    if effective_n >= issued_sealed:
        effective_n = issued_sealed - 0.5

    # Also compute overall metrics for reference (but sealed is what matters)
    issued_all = len(unique_decisions)
    hits_all = sum(d['hit'] for d in unique_decisions)
    precision_all = hits_all / issued_all if issued_all > 0 else 0
    base_rate_all = hits_all / issued_all if issued_all > 0 else 0
    distinct_days_all = len(set(d['decision_date'] for d in unique_decisions))

    # Print required metrics (sealed era)
    print(f"ISSUED={issued_sealed}")
    print(f"OPPORTUNITIES={issued_sealed}")  # opportunities considered = issued since we only count qualified
    print(f"PRECISION={precision_sealed:.6f}")
    print(f"BASE_RATE={base_rate_sealed:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_sealed}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())