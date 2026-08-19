# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 710
# cycle_index: 37
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def main():
    con = connect_ro()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Universe: symbols with daily bars from 2018-07, min 252 sessions, avg dollar vol $1M-$500M
    # First get all symbols with daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(*) as n_bars, MIN(ts) as min_ts, MAX(ts) as max_ts,
               AVG(close * volume) as avg_dollar_vol
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING n_bars >= 252
           AND min_ts <= strftime('%s', '2018-07-01')
           AND avg_dollar_vol BETWEEN 1000000 AND 500000000
    """)
    universe = {row['symbol_id']: dict(row) for row in cur.fetchall()}
    if not universe:
        print("INSUFFICIENT=1")
        return 0
    print(f"Universe size: {len(universe)}", file=sys.stderr)

    symbol_ids = list(universe.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # 2. Load all daily bars for universe symbols (tf='1d')
    # We need: ts, open, high, low, close, volume
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
            'volume': row['volume'],
            'dollar_vol': row['close'] * row['volume'],
            'daily_range': (row['high'] - row['low']) / row['close'] if row['close'] > 0 else 0
        })

    # 3. Load insider trades for universe symbols (code='P' for purchases, 'S' for sales)
    cur.execute(f"""
        SELECT accession, symbol_id, insider, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code IN ('P', 'S')
        ORDER BY symbol_id, filed_ts
    """, symbol_ids)
    
    insider_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        insider_by_symbol[row['symbol_id']].append({
            'accession': row['accession'],
            'insider': row['insider'],
            'code': row['code'],
            'shares': row['shares'],
            'price': row['price'],
            'value': row['value'],
            'tx_ts': row['tx_ts'],
            'filed_ts': row['filed_ts'],
            'tx_date': ts_to_date(row['tx_ts']),
            'filed_date': ts_to_date(row['filed_ts'])
        })

    # 4. For each symbol, compute trailing 252-session daily range percentiles
    # and identify eligible entry points
    
    # Build trading day calendar per symbol for business day calculations
    trading_days_by_symbol = {}
    for sym, bars in bars_by_symbol.items():
        trading_days_by_symbol[sym] = sorted(set(b['date'] for b in bars))

    def count_trading_days(sym, start_date, end_date):
        """Count trading days in [start_date, end_date] inclusive"""
        days = trading_days_by_symbol.get(sym, [])
        return sum(1 for d in days if start_date <= d <= end_date)

    def get_nth_trading_day(sym, start_date, n):
        """Get the n-th trading day after start_date (n=1 means next trading day)"""
        days = trading_days_by_symbol.get(sym, [])
        idx = next((i for i, d in enumerate(days) if d > start_date), -1)
        if idx >= 0 and idx + n - 1 < len(days):
            return days[idx + n - 1]
        return None

    # For each symbol, compute rolling 252-day daily range percentiles
    # and 5-day average daily range
    opportunities = []  # list of (symbol_id, decision_date, filed_ts, forward_return, label, issued, abstain_reason)
    
    for sym in symbol_ids:
        bars = bars_by_symbol[sym]
        if len(bars) < 252 + 5 + 21:  # need enough history
            continue
        
        insider_trades = insider_by_symbol.get(sym, [])
        if not insider_trades:
            continue
        
        # Map filed_date -> list of insider trades filed that day
        trades_by_filed_date = defaultdict(list)
        for t in insider_trades:
            trades_by_filed_date[t['filed_date']].append(t)
        
        # Precompute daily ranges and 5-day avg ranges
        daily_ranges = [b['daily_range'] for b in bars]
        closes = [b['close'] for b in bars]
        dates = [b['date'] for b in bars]
        
        # Rolling 5-day average daily range
        avg_range_5 = []
        for i in range(len(bars)):
            if i < 4:
                avg_range_5.append(None)
            else:
                avg_range_5.append(sum(daily_ranges[i-4:i+1]) / 5)
        
        # For each bar (day), compute percentile of avg_range_5 within trailing 252 days
        # Need at least 30 eligible days in trailing 252 for percentile calc (abstain e)
        percentiles = []
        eligible_counts = []
        for i in range(len(bars)):
            if i < 252 + 4:  # need 252 trailing + 5 for avg_range
                percentiles.append(None)
                eligible_counts.append(0)
                continue
            trailing_ranges = [r for r in avg_range_5[i-252:i+1] if r is not None]
            eligible_counts.append(len(trailing_ranges))
            if len(trailing_ranges) < 30:
                percentiles.append(None)
            else:
                current = avg_range_5[i]
                rank = sum(1 for r in trailing_ranges if r <= current)
                pct = rank / len(trailing_ranges)
                percentiles.append(pct)
        
        # Close-to-close returns
        returns = [None] * len(bars)
        for i in range(1, len(bars)):
            if closes[i-1] > 0:
                returns[i] = (closes[i] - closes[i-1]) / closes[i-1]
        
        # Forward 21-day returns (from close on day i to close on day i+21 trading days)
        fwd_returns = [None] * len(bars)
        for i in range(len(bars)):
            target_date = get_nth_trading_day(sym, dates[i], 21)
            if target_date:
                # Find bar for target_date
                j = next((idx for idx, d in enumerate(dates) if d == target_date), -1)
                if j >= 0 and closes[i] > 0:
                    fwd_returns[i] = (closes[j] - closes[i]) / closes[i]
        
        # 21-day forward return volatility rank (abstain d)
        # Compute rolling 252-day std of 21-day forward returns
        fwd_vol_rank = [None] * len(bars)
        for i in range(len(bars)):
            if i < 252 + 21:
                continue
            trailing_fwd = [r for r in fwd_returns[i-252:i+1] if r is not None]
            if len(trailing_fwd) < 30:
                continue
            current_fwd = fwd_returns[i]
            if current_fwd is None:
                continue
            # Rank current_fwd volatility? The hypothesis says "21-day forward return volatility rank"
            # This is ambiguous - could mean rank of the forward return itself, or rank of volatility
            # I'll interpret as rank of the absolute forward return (magnitude) within trailing window
            abs_current = abs(current_fwd)
            rank = sum(1 for r in trailing_fwd if abs(r) <= abs_current)
            fwd_vol_rank[i] = rank / len(trailing_fwd)
        
        # Now process each insider purchase filing day
        for filed_date, trades in trades_by_filed_date.items():
            purchases = [t for t in trades if t['code'] == 'P']
            if not purchases:
                continue
            
            # Decision day is filed_date - find corresponding bar index
            try:
                bar_idx = dates.index(filed_date)
            except ValueError:
                continue  # no bar for this date (weekend/holiday)
            
            # Check entry conditions
            # (1) 5-session avg daily range in top quintile (pct >= 0.8)
            pct = percentiles[bar_idx]
            if pct is None or pct < 0.8:
                continue
            
            # (2) Close-to-close return on day D is negative
            ret = returns[bar_idx]
            if ret is None or ret >= 0:
                continue
            
            # (3) At least 2 distinct insiders purchased in prior 63 sessions
            # Look at filed_date - 63 trading days to filed_date - 1 trading day
            start_window = get_nth_trading_day(sym, filed_date, -63)  # 63 days before
            if not start_window:
                continue
            prior_purchases = []
            for t in insider_trades:
                if t['code'] == 'P' and start_window <= t['filed_date'] < filed_date:
                    prior_purchases.append(t['insider'])
            if len(set(prior_purchases)) < 2:
                continue
            
            # Check abstain conditions
            abstain_reason = None
            
            # (a) Any insider sale disclosed in prior 5 sessions
            sales_window_start = get_nth_trading_day(sym, filed_date, -5)
            if sales_window_start:
                for t in insider_trades:
                    if t['code'] == 'S' and sales_window_start <= t['filed_date'] < filed_date:
                        abstain_reason = 'prior_sale'
                        break
            
            # (b) Disclosure lag > 5 business days
            if not abstain_reason:
                for t in purchases:
                    lag_days = count_trading_days(sym, t['tx_date'], t['filed_date'])
                    if lag_days > 5:
                        abstain_reason = 'disclosure_lag'
                        break
            
            # (c) Forward 21-day label missing
            if not abstain_reason:
                if fwd_returns[bar_idx] is None:
                    abstain_reason = 'missing_label'
            
            # (d) 21-day forward return volatility rank in top decile
            if not abstain_reason:
                vol_rank = fwd_vol_rank[bar_idx]
                if vol_rank is not None and vol_rank >= 0.9:
                    abstain_reason = 'high_vol_rank'
            
            # (e) Fewer than 30 eligible symbol-days in trailing 252-session window
            if not abstain_reason:
                if eligible_counts[bar_idx] < 30:
                    abstain_reason = 'insufficient_history'
            
            issued = 1 if abstain_reason is None else 0
            label = 1 if (fwd_returns[bar_idx] is not None and fwd_returns[bar_idx] > 0) else 0
            
            opportunities.append({
                'symbol_id': sym,
                'decision_date': filed_date,
                'filed_ts': purchases[0]['filed_ts'],  # use first purchase filing ts
                'forward_return': fwd_returns[bar_idx],
                'label': label,
                'issued': issued,
                'abstain_reason': abstain_reason
            })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by decision timestamp
    opportunities.sort(key=lambda x: x['filed_ts'])
    
    # Hold out most recent 20% as sealed era
    n_total = len(opportunities)
    n_sealed = max(1, int(n_total * 0.2))
    train_ops = opportunities[:-n_sealed]
    sealed_ops = opportunities[-n_sealed:]
    
    def compute_metrics(ops):
        issued_ops = [o for o in ops if o['issued']]
        if not issued_ops:
            return {
                'issued': 0,
                'opportunities': len(ops),
                'precision': 0.0,
                'base_rate': 0.0,
                'distinct_days': 0,
                'effective_n': 0.0
            }
        
        issued_count = len(issued_ops)
        hits = sum(o['label'] for o in issued_ops)
        precision = hits / issued_count
        base_rate = sum(o['label'] for o in issued_ops) / issued_count
        distinct_days = len(set(o['decision_date'] for o in issued_ops))
        
        # Design effect: cluster by time. Group by decision_date, compute variance inflation
        # Simple approach: effective_n = issued_count / (1 + (avg_cluster_size - 1) * rho)
        # Use intraclass correlation approximation
        day_counts = defaultdict(int)
        for o in issued_ops:
            day_counts[o['decision_date']] += 1
        cluster_sizes = list(day_counts.values())
        if len(cluster_sizes) > 1:
            mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
            # Estimate ICC from data: variance between clusters / total variance
            # Simplified: design_effect = 1 + (mean_cluster - 1) * ICC
            # Use conservative ICC = 0.1 for financial returns clustering
            icc = 0.1
            design_effect = 1 + (mean_cluster - 1) * icc
        else:
            design_effect = 1.0
        effective_n = issued_count / design_effect
        
        return {
            'issued': issued_count,
            'opportunities': len(ops),
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }
    
    train_metrics = compute_metrics(train_ops)
    sealed_metrics = compute_metrics(sealed_ops)
    
    # Output required lines
    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={train_metrics['opportunities']}")
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())