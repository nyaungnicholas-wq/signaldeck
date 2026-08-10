# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 400
# cycle_index: 68
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def business_days_between(start_ts, end_ts):
    """Count business days between two timestamps (inclusive of start, exclusive of end)."""
    start = datetime.utcfromtimestamp(start_ts).date()
    end = datetime.utcfromtimestamp(end_ts).date()
    count = 0
    current = start
    while current < end:
        if current.weekday() < 5:
            count += 1
        current += timedelta(days=1)
    return count

def add_business_days(start_ts, n_days):
    """Add n business days to a timestamp."""
    current = datetime.utcfromtimestamp(start_ts).date()
    added = 0
    while added < n_days:
        current += timedelta(days=1)
        if current.weekday() < 5:
            added += 1
    return int(datetime.combine(current, datetime.min.time()).timestamp())

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get SPY symbol_id
    cur.execute("SELECT id FROM symbols WHERE symbol = 'SPY' AND market = 'stocks'")
    spy_row = cur.fetchone()
    if not spy_row:
        print("INSUFFICIENT=1")
        return
    spy_id = spy_row['id']

    # Get SPY daily bars (1d)
    cur.execute("SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts", (spy_id,))
    spy_bars = cur.fetchall()
    if len(spy_bars) < 1008:
        print("INSUFFICIENT=1")
        return
    spy_ts = [r['ts'] for r in spy_bars]
    spy_close = [r['close'] for r in spy_bars]
    spy_returns = []
    for i in range(1, len(spy_close)):
        if spy_close[i-1] > 0:
            spy_returns.append((spy_close[i] - spy_close[i-1]) / spy_close[i-1])
        else:
            spy_returns.append(0.0)

    # Get all symbols with daily bars
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol 
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id
        WHERE s.market = 'stocks' AND b.tf = '1d' AND s.active = 1
    """)
    symbols = cur.fetchall()
    if not symbols:
        print("INSUFFICIENT=1")
        return

    # Get fundamentals: SharesOutstanding and EPS
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('SharesOutstanding', 'EPS')
    """)
    fund_rows = cur.fetchall()
    fundamentals = defaultdict(lambda: defaultdict(list))
    for r in fund_rows:
        fundamentals[r['symbol_id']][r['metric']].append({
            'value': r['value'],
            'as_of': r['as_of'],
            'fetched_at': r['fetched_at']
        })

    # Get macro series for 10Y-2Y spread (FRED: T10Y2Y)
    cur.execute("SELECT ts, value FROM macro_series WHERE series = 'T10Y2Y' ORDER BY ts")
    macro_rows = cur.fetchall()
    term_spread = {r['ts']: r['value'] for r in macro_rows}

    # Get insider trades (code P = purchase)
    cur.execute("""
        SELECT symbol_id, insider, tx_ts, filed_ts, shares, value
        FROM insider_trades
        WHERE code = 'P'
    """)
    insider_trades = cur.fetchall()

    # Get institutional holdings (13F)
    cur.execute("""
        SELECT symbol_id, period, shares, value
        FROM inst_holdings
        ORDER BY symbol_id, period
    """)
    inst_rows = cur.fetchall()
    inst_holdings = defaultdict(list)
    for r in inst_rows:
        inst_holdings[r['symbol_id']].append({
            'period': r['period'],
            'shares': r['shares'],
            'value': r['value']
        })

    # Get prediction outcomes for 21-day horizon labels
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
    """)
    label_rows = cur.fetchall()
    labels = defaultdict(dict)
    for r in label_rows:
        labels[r['symbol_id']][r['ts']] = {'up': r['up'], 'fwd_return': r['fwd_return']}

    # Get daily bars for all symbols (need for correlations, volume, price)
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    all_bars = cur.fetchall()
    symbol_bars = defaultdict(list)
    for r in all_bars:
        symbol_bars[r['symbol_id']].append({'ts': r['ts'], 'close': r['close'], 'volume': r['volume']})

    # Precompute SPY returns aligned by index
    spy_ret_by_ts = {spy_ts[i+1]: spy_returns[i] for i in range(len(spy_returns))}

    # Universe filtering and correlation calculation
    universe_symbols = []
    for sym in symbols:
        sym_id = sym['id']
        bars = symbol_bars.get(sym_id, [])
        if len(bars) < 1008:
            continue

        # Check market cap > $500M (need latest price * SharesOutstanding)
        # Get latest SharesOutstanding (using fetched_at as knowable time)
        so_list = fundamentals[sym_id].get('SharesOutstanding', [])
        if not so_list:
            continue
        # Use most recent fetched_at
        latest_so = max(so_list, key=lambda x: x['fetched_at'])
        shares_out = latest_so['value']
        if shares_out <= 0:
            continue
        latest_price = bars[-1]['close']
        market_cap = latest_price * shares_out
        if market_cap <= 500_000_000:
            continue

        # Check average daily dollar volume > $1M (use last 252 days)
        recent_bars = bars[-252:] if len(bars) >= 252 else bars
        dollar_vols = [b['close'] * b['volume'] for b in recent_bars if b['close'] > 0 and b['volume'] > 0]
        if not dollar_vols:
            continue
        avg_dollar_vol = sum(dollar_vols) / len(dollar_vols)
        if avg_dollar_vol <= 1_000_000:
            continue

        # Check SPY reference exists (non-missing) - need overlapping dates
        sym_ts = [b['ts'] for b in bars]
        common_ts = set(sym_ts) & set(spy_ts)
        if len(common_ts) < 252:
            continue

        universe_symbols.append(sym_id)

    if not universe_symbols:
        print("INSUFFICIENT=1")
        return

    # Calculate 252-day rolling correlation with SPY for each universe symbol
    # and 3-year (756-day) median correlation
    symbol_correlations = {}
    for sym_id in universe_symbols:
        bars = symbol_bars[sym_id]
        sym_ts = [b['ts'] for b in bars]
        sym_close = [b['close'] for b in bars]
        
        # Align with SPY
        aligned = []
        for i, ts in enumerate(sym_ts):
            if ts in spy_ret_by_ts and i > 0:
                sym_ret = (sym_close[i] - sym_close[i-1]) / sym_close[i-1] if sym_close[i-1] > 0 else 0
                aligned.append((ts, sym_ret, spy_ret_by_ts[ts]))
        
        if len(aligned) < 756:
            continue
        
        # Calculate rolling 252-day correlations
        correlations = []
        for i in range(252, len(aligned)):
            window = aligned[i-252:i]
            sym_rets = [w[1] for w in window]
            spy_rets = [w[2] for w in window]
            
            n = len(sym_rets)
            if n < 2:
                correlations.append((aligned[i][0], None))
                continue
            
            mean_sym = sum(sym_rets) / n
            mean_spy = sum(spy_rets) / n
            
            cov = sum((sym_rets[j] - mean_sym) * (spy_rets[j] - mean_spy) for j in range(n))
            var_sym = sum((x - mean_sym) ** 2 for x in sym_rets)
            var_spy = sum((x - mean_spy) ** 2 for x in spy_rets)
            
            if var_sym > 0 and var_spy > 0:
                corr = cov / (var_sym ** 0.5 * var_spy ** 0.5)
                correlations.append((aligned[i][0], corr))
            else:
                correlations.append((aligned[i][0], None))
        
        # Calculate 3-year (756-day) median correlation up to each point
        valid_corrs = [c for _, c in correlations if c is not None]
        if len(valid_corrs) < 756:
            continue
        
        symbol_correlations[sym_id] = correlations

    # Process insider trades: group by symbol and filed_ts window
    # Need trades where filed_ts within 5 business days of tx_ts
    valid_insider_events = defaultdict(list)  # symbol_id -> list of (filed_ts, insider)
    for trade in insider_trades:
        sym_id = trade['symbol_id']
        if sym_id not in symbol_correlations:
            continue
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']
        if business_days_between(tx_ts, filed_ts) <= 5:
            valid_insider_events[sym_id].append({
                'filed_ts': filed_ts,
                'insider': trade['insider'],
                'shares': trade['shares'],
                'value': trade['value']
            })

    # For each symbol, find 20-day disclosure windows with >=2 distinct insiders
    entry_candidates = []  # (symbol_id, decision_ts, filed_ts_window_start)
    for sym_id, trades in valid_insider_events.items():
        trades.sort(key=lambda x: x['filed_ts'])
        # Sliding window of 20 calendar days (not business days per spec)
        for i in range(len(trades)):
            window_start = trades[i]['filed_ts']
            window_end = window_start + 20 * 86400
            window_trades = [t for t in trades if window_start <= t['filed_ts'] < window_end]
            distinct_insiders = set(t['insider'] for t in window_trades)
            if len(distinct_insiders) >= 2:
                # Decision timestamp is the last filed_ts in window (or window_end?)
                # Use the latest filed_ts in window as decision time
                decision_ts = max(t['filed_ts'] for t in window_trades)
                entry_candidates.append((sym_id, decision_ts, window_start))

    # Now filter entry candidates by all conditions
    issued_calls = []  # (symbol_id, decision_ts, label_up)
    
    for sym_id, decision_ts, window_start in entry_candidates:
        # 1. Check correlation condition at decision_ts
        correlations = symbol_correlations[sym_id]
        # Find correlation at or just before decision_ts
        corr_at_decision = None
        median_3yr = None
        for i, (ts, corr) in enumerate(correlations):
            if ts <= decision_ts and corr is not None:
                corr_at_decision = corr
                # Calculate 3-year median up to this point (756 trading days ~ 3 years)
                # Look back 756 correlation points
                start_idx = max(0, i - 756)
                recent_corrs = [c for _, c in correlations[start_idx:i+1] if c is not None]
                if len(recent_corrs) >= 252:  # Need at least 1 year for median
                    median_3yr = sorted(recent_corrs)[len(recent_corrs) // 2]
                break
        
        if corr_at_decision is None or median_3yr is None:
            continue
        if corr_at_decision > median_3yr - 0.2:  # fallen > 0.2 from median
            continue

        # 2. Check trailing 4-quarter EPS growth > 10%
        eps_list = fundamentals[sym_id].get('EPS', [])
        # Need EPS values with fetched_at <= decision_ts (as-of discipline)
        available_eps = [e for e in eps_list if e['fetched_at'] <= decision_ts]
        if len(available_eps) < 4:
            continue
        # Sort by as_of (period end), take most recent 4 quarters
        available_eps.sort(key=lambda x: x['as_of'], reverse=True)
        recent_4q = available_eps[:4]
        if len(recent_4q) < 4:
            continue
        # Check growth: oldest vs newest (4 quarters ago vs now)
        oldest = recent_4q[-1]['value']
        newest = recent_4q[0]['value']
        if oldest <= 0:
            continue
        eps_growth = (newest - oldest) / abs(oldest)
        if eps_growth <= 0.10:
            continue

        # 3. Check 10Y-2Y term spread positive at decision_ts
        # Find latest term spread with ts <= decision_ts
        spread_val = None
        for ts, val in sorted(term_spread.items()):
            if ts <= decision_ts:
                spread_val = val
            else:
                break
        if spread_val is None or spread_val <= 0:
            continue

        # 4. Check prior-quarter 13F institutional ownership didn't increase >5%
        # Need holdings with period <= decision_ts - 45 days (lag for filing delay)
        lagged_ts = decision_ts - 45 * 86400
        holdings = inst_holdings.get(sym_id, [])
        # Find two consecutive quarters before lagged_ts
        prior_holdings = [h for h in holdings if h['period'] <= lagged_ts]
        if len(prior_holdings) < 2:
            continue
        prior_holdings.sort(key=lambda x: x['period'], reverse=True)
        latest_q = prior_holdings[0]
        prev_q = prior_holdings[1]
        if prev_q['value'] <= 0:
            continue
        ownership_change = (latest_q['value'] - prev_q['value']) / prev_q['value']
        if ownership_change > 0.05:
            continue

        # 5. Check 21-day forward label exists
        label = labels[sym_id].get(decision_ts)
        if label is None:
            continue
        
        issued_calls.append((sym_id, decision_ts, label['up']))

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    # Hold out most recent 20% as sealed era
    issued_calls.sort(key=lambda x: x[1])
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    main_calls = issued_calls[:-n_sealed]
    sealed_calls = issued_calls[-n_sealed:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(1 for _, _, up in calls if up == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class (up=1) within issued
        
        # Distinct UTC days among issued calls
        distinct_days = len(set(datetime.utcfromtimestamp(ts).date() for _, ts, _ in calls))
        
        # Design effect: cluster by day, compute effective N
        day_counts = defaultdict(int)
        for _, ts, _ in calls:
            day = datetime.utcfromtimestamp(ts).date()
            day_counts[day] += 1
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Simplified: use Kish's effective sample size: n_eff = (sum w)^2 / sum(w^2)
        # where w = 1 for each call, but clustered
        # Actually: effective_n = n / (1 + (m-1)*rho) where m=avg cluster size
        # Conservative: assume ICC=0.5, m = avg calls per day
        if day_counts:
            avg_cluster = sum(day_counts.values()) / len(day_counts)
            icc = 0.5  # conservative estimate for financial returns
            design_effect = 1 + (avg_cluster - 1) * icc
            effective_n = issued / design_effect
        else:
            effective_n = issued * 0.5
        
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    # Opportunities: decision points considered (entry candidates that passed universe and data availability)
    # This is the number of entry_candidates that had all data available for evaluation
    opportunities = len(entry_candidates)

    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_precision:.6f}")
    print(f"BASE_RATE={main_base_rate:.6f}")
    print(f"DISTINCT_DAYS={main_distinct_days}")
    print(f"EFFECTIVE_N={main_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()