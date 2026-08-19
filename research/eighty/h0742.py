# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 741
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import time
from datetime import datetime, timedelta
from collections import defaultdict
import bisect

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def is_business_day(d):
    return d.weekday() < 5

def add_business_days(start_date, n):
    d = start_date
    count = 0
    while count < n:
        d += timedelta(days=1)
        if is_business_day(d):
            count += 1
    return d

def business_days_between(start, end):
    if start >= end:
        return 0
    count = 0
    d = start
    while d < end:
        d += timedelta(days=1)
        if is_business_day(d):
            count += 1
    return count

def get_trading_dates(conn, start_ts, end_ts):
    cur = conn.execute(
        "SELECT DISTINCT ts FROM bars WHERE tf='1d' AND ts >= ? AND ts <= ? ORDER BY ts",
        (start_ts, end_ts)
    )
    return [ts_to_date(row[0]) for row in cur.fetchall()]

def main():
    start_time = time.time()
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.execute("PRAGMA query_only = ON")
    
    # 1. Get all trading dates from bars (1d) for horizon calculation
    print("Loading trading calendar...", file=sys.stderr)
    cur = conn.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
    min_ts, max_ts = cur.fetchone()
    all_trading_dates = get_trading_dates(conn, min_ts, max_ts)
    trading_date_set = set(all_trading_dates)
    date_to_idx = {d: i for i, d in enumerate(all_trading_dates)}
    
    # 2. Get officer open-market purchases
    print("Loading officer purchases...", file=sys.stderr)
    cur = conn.execute("""
        SELECT accession, symbol_id, insider, title, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
          AND tx_ts IS NOT NULL AND filed_ts IS NOT NULL
        ORDER BY filed_ts
    """)
    purchases = cur.fetchall()
    if not purchases:
        print("INSUFFICIENT=1")
        return
    
    # 3. Load news daily counts per symbol
    print("Loading news counts...", file=sys.stderr)
    cur = conn.execute("""
        SELECT symbol_id, ts FROM news WHERE ts IS NOT NULL ORDER BY symbol_id, ts
    """)
    news_by_symbol = defaultdict(list)
    for symbol_id, ts in cur.fetchall():
        news_by_symbol[symbol_id].append(ts_to_date(ts))
    
    # Precompute daily headline counts per symbol
    news_daily_counts = {}
    for symbol_id, dates in news_by_symbol.items():
        counts = defaultdict(int)
        for d in dates:
            counts[d] += 1
        news_daily_counts[symbol_id] = counts
    
    # 4. Load fundamentals (Revenues, SharesOutstanding) with fetched_at
    print("Loading fundamentals...", file=sys.stderr)
    cur = conn.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('Revenues', 'SharesOutstanding')
          AND fetched_at IS NOT NULL
        ORDER BY symbol_id, metric, fetched_at
    """)
    fund_by_symbol = defaultdict(lambda: defaultdict(list))  # symbol -> metric -> list of (as_of, value, fetched_at)
    for symbol_id, metric, value, as_of, fetched_at in cur.fetchall():
        if as_of == 0:
            continue
        fund_by_symbol[symbol_id][metric].append((as_of, value, fetched_at))
    
    # 5. Load bars for market cap and volatility
    print("Loading daily bars...", file=sys.stderr)
    cur = conn.execute("""
        SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts
    """)
    bars_by_symbol = defaultdict(list)
    for symbol_id, ts, close in cur.fetchall():
        bars_by_symbol[symbol_id].append((ts, close))
    
    # 6. Load all insider trades for sell check and officer median
    print("Loading all insider trades...", file=sys.stderr)
    cur = conn.execute("""
        SELECT symbol_id, insider, code, shares, filed_ts
        FROM insider_trades
        WHERE filed_ts IS NOT NULL
        ORDER BY symbol_id, insider, filed_ts
    """)
    trades_by_symbol = defaultdict(list)
    trades_by_officer = defaultdict(list)  # (symbol_id, insider) -> list
    for symbol_id, insider, code, shares, filed_ts in cur.fetchall():
        trades_by_symbol[symbol_id].append((code, shares, filed_ts))
        trades_by_officer[(symbol_id, insider)].append((code, shares, filed_ts))
    
    # 7. Get symbols with market cap > 1B at some point (we'll check at decision time)
    # Also need symbols with insider history 2018+, news 2012+, fundamentals
    symbols_with_insider_2018 = set()
    cur = conn.execute("""
        SELECT DISTINCT symbol_id FROM insider_trades WHERE tx_ts >= ? 
    """, (date_to_ts(datetime(2018,1,1).date()),))
    for row in cur.fetchall():
        symbols_with_insider_2018.add(row[0])
    
    symbols_with_news_2012 = set()
    cur = conn.execute("""
        SELECT DISTINCT symbol_id FROM news WHERE ts >= ?
    """, (date_to_ts(datetime(2012,1,1).date()),))
    for row in cur.fetchall():
        symbols_with_news_2012.add(row[0])
    
    symbols_with_fund = set(fund_by_symbol.keys())
    
    universe_symbols = symbols_with_insider_2018 & symbols_with_news_2012 & symbols_with_fund
    print(f"Universe symbols: {len(universe_symbols)}", file=sys.stderr)
    
    # 8. Process each purchase
    opportunities = []
    issued_calls = []
    
    for accession, symbol_id, insider, title, shares, price, value, tx_ts, filed_ts in purchases:
        if symbol_id not in universe_symbols:
            continue
        
        decision_date = ts_to_date(filed_ts)
        trade_date = ts_to_date(tx_ts)
        
        # Check disclosure delay > 3 business days
        if business_days_between(trade_date, decision_date) > 3:
            opportunities.append((decision_date, symbol_id, False, 'disclosure_delay'))
            continue
        
        # Check market cap > 1B at decision date
        # Need close price on decision date (or prior trading day) and shares outstanding
        bars = bars_by_symbol.get(symbol_id, [])
        if not bars:
            opportunities.append((decision_date, symbol_id, False, 'no_bars'))
            continue
        
        # Find close on or before decision_date
        decision_ts = date_to_ts(decision_date)
        close_price = None
        for ts, close in reversed(bars):
            if ts <= decision_ts:
                close_price = close
                break
        if close_price is None:
            opportunities.append((decision_date, symbol_id, False, 'no_prior_close'))
            continue
        
        # Get SharesOutstanding as of latest fetched_at <= filed_ts
        so_rows = fund_by_symbol[symbol_id].get('SharesOutstanding', [])
        so_value = None
        for as_of, val, fetched in so_rows:
            if fetched <= filed_ts:
                so_value = val
            else:
                break
        if so_value is None or so_value <= 0:
            opportunities.append((decision_date, symbol_id, False, 'no_shares_outstanding'))
            continue
        
        market_cap = close_price * so_value
        if market_cap <= 1_000_000_000:
            opportunities.append((decision_date, symbol_id, False, 'market_cap'))
            continue
        
        # News volume condition: 20-day avg < 252-day median
        news_counts = news_daily_counts.get(symbol_id, {})
        if not news_counts:
            opportunities.append((decision_date, symbol_id, False, 'no_news'))
            continue
        
        # Get daily counts for 252 trading days before decision_date
        lookback_start_idx = date_to_idx.get(decision_date, 0) - 252
        if lookback_start_idx < 0:
            opportunities.append((decision_date, symbol_id, False, 'insufficient_news_history'))
            continue
        
        daily_counts = []
        for i in range(lookback_start_idx, date_to_idx[decision_date] + 1):
            d = all_trading_dates[i]
            daily_counts.append(news_counts.get(d, 0))
        
        if len(daily_counts) < 252:
            opportunities.append((decision_date, symbol_id, False, 'insufficient_news_history'))
            continue
        
        avg_20 = sum(daily_counts[-20:]) / 20
        median_252 = sorted(daily_counts)[len(daily_counts) // 2]
        
        if avg_20 >= median_252:
            opportunities.append((decision_date, symbol_id, False, 'news_volume'))
            continue
        
        # Fundamentals: Revenue YoY growth positive for last 4 consecutive quarters
        rev_rows = fund_by_symbol[symbol_id].get('Revenues', [])
        # Filter to fetched_at <= filed_ts
        rev_available = [(as_of, val) for as_of, val, fetched in rev_rows if fetched <= filed_ts]
        if len(rev_available) < 5:  # Need 5 quarters for 4 YoY comparisons
            opportunities.append((decision_date, symbol_id, False, 'insufficient_revenue'))
            continue
        
        # Sort by as_of (quarter end)
        rev_available.sort(key=lambda x: x[0])
        # Check last 4 YoY growths
        revenue_ok = True
        for i in range(-4, 0):
            if i - 4 < -len(rev_available):
                revenue_ok = False
                break
            curr_val = rev_available[i][1]
            year_ago_val = rev_available[i-4][1]
            if year_ago_val <= 0 or curr_val <= year_ago_val:
                revenue_ok = False
                break
        if not revenue_ok:
            opportunities.append((decision_date, symbol_id, False, 'revenue_growth'))
            continue
        
        # SharesOutstanding: zero quarters with >2% growth over prior 8 quarters
        so_available = [(as_of, val) for as_of, val, fetched in so_rows if fetched <= filed_ts]
        if len(so_available) < 9:  # Need 9 quarters for 8 QoQ comparisons
            opportunities.append((decision_date, symbol_id, False, 'insufficient_so'))
            continue
        
        so_available.sort(key=lambda x: x[0])
        so_ok = True
        for i in range(-8, 0):
            if i - 1 < -len(so_available):
                so_ok = False
                break
            curr_val = so_available[i][1]
            prev_val = so_available[i-1][1]
            if prev_val > 0 and (curr_val - prev_val) / prev_val > 0.02:
                so_ok = False
                break
        if not so_ok:
            opportunities.append((decision_date, symbol_id, False, 'so_growth'))
            continue
        
        # Abstain: Any insider sold in prior 20 sessions
        symbol_trades = trades_by_symbol[symbol_id]
        sell_found = False
        for code, sh, fts in symbol_trades:
            if code == 'S' and fts <= filed_ts:
                fdate = ts_to_date(fts)
                if business_days_between(fdate, decision_date) <= 20 and fdate < decision_date:
                    sell_found = True
                    break
        if sell_found:
            opportunities.append((decision_date, symbol_id, False, 'insider_sell'))
            continue
        
        # Abstain: Purchase size below officer's 252-session median
        officer_trades = trades_by_officer[(symbol_id, insider)]
        prior_purchases = [sh for code, sh, fts in officer_trades if code == 'P' and fts < filed_ts]
        if len(prior_purchases) > 0:
            median_purchase = sorted(prior_purchases)[len(prior_purchases) // 2]
            if shares < median_purchase:
                opportunities.append((decision_date, symbol_id, False, 'purchase_size'))
                continue
        
        # Abstain: 20-day realized volatility in top cross-sectional quintile
        # Need to compute for all symbols on this decision_date
        # We'll compute this later in batch for efficiency
        opportunities.append((decision_date, symbol_id, True, accession, shares, value, filed_ts))
    
    # Now compute volatility quintile for each decision date
    print("Computing volatility quintiles...", file=sys.stderr)
    # Group opportunities by decision_date
    opps_by_date = defaultdict(list)
    for opp in opportunities:
        if opp[2]:  # passed initial filters
            opps_by_date[opp[0]].append(opp)
    
    # For each date, compute 20-day vol for all symbols with data
    vol_quintile = {}  # (date, symbol_id) -> bool (True if in top quintile)
    for decision_date, opps in opps_by_date.items():
        decision_ts = date_to_ts(decision_date)
        idx = date_to_idx.get(decision_date)
        if idx is None or idx < 20:
            for opp in opps:
                vol_quintile[(decision_date, opp[1])] = False
            continue
        
        # Get 20-day returns for all symbols
        symbol_vols = []
        for symbol_id, bars in bars_by_symbol.items():
            # Find 21 closes up to decision_date
            closes = []
            for ts, close in bars:
                if ts <= decision_ts:
                    closes.append(close)
                else:
                    break
            if len(closes) >= 21:
                rets = []
                for i in range(-20, 0):
                    if closes[i-1] > 0:
                        rets.append((closes[i] - closes[i-1]) / closes[i-1])
                if len(rets) == 20:
                    import math
                    vol = math.sqrt(sum(r*r for r in rets) / 20) * math.sqrt(252)
                    symbol_vols.append((symbol_id, vol))
        
        if not symbol_vols:
            for opp in opps:
                vol_quintile[(decision_date, opp[1])] = False
            continue
        
        symbol_vols.sort(key=lambda x: x[1])
        cutoff_idx = int(len(symbol_vols) * 0.8)
        top_quintile_symbols = set(sid for sid, _ in symbol_vols[cutoff_idx:])
        
        for opp in opps:
            vol_quintile[(decision_date, opp[1])] = opp[1] in top_quintile_symbols
    
    # Apply volatility filter
    final_opportunities = []
    for opp in opportunities:
        if not opp[2]:
            final_opportunities.append((opp[0], opp[1], False))
            continue
        decision_date, symbol_id, _, accession, shares, value, filed_ts = opp
        if vol_quintile.get((decision_date, symbol_id), False):
            final_opportunities.append((decision_date, symbol_id, False))
        else:
            final_opportunities.append((decision_date, symbol_id, True, accession, filed_ts))
    
    # 9. Compute labels (63 trading day forward return)
    print("Computing forward returns...", file=sys.stderr)
    issued = []
    for opp in final_opportunities:
        if not opp[2]:
            continue
        decision_date, symbol_id, _, accession, filed_ts = opp
        idx = date_to_idx.get(decision_date)
        if idx is None or idx + 63 >= len(all_trading_dates):
            continue
        horizon_date = all_trading_dates[idx + 63]
        horizon_ts = date_to_ts(horizon_date)
        
        # Get close at decision and horizon
        bars = bars_by_symbol.get(symbol_id, [])
        close_dec = None
        close_hor = None
        for ts, close in bars:
            if ts <= date_to_ts(decision_date) and close_dec is None:
                close_dec = close
            if ts <= horizon_ts:
                close_hor = close
            else:
                break
        
        if close_dec and close_hor and close_dec > 0:
            fwd_return = (close_hor - close_dec) / close_dec
            hit = 1 if fwd_return > 0 else 0
            issued.append((decision_date, symbol_id, accession, hit, fwd_return, filed_ts))
    
    if not issued:
        print("INSUFFICIENT=1")
        return
    
    # 10. Split into in-sample (80%) and sealed (20%) by decision date
    issued.sort(key=lambda x: x[5])  # sort by filed_ts
    split_idx = int(len(issued) * 0.8)
    in_sample = issued[:split_idx]
    sealed = issued[split_idx:]
    
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        n_issued = len(calls)
        hits = sum(c[3] for c in calls)
        precision = hits / n_issued
        base_rate = hits / n_issued  # base rate of predicted class (up) within issued subset
        distinct_days = len(set(c[0] for c in calls))
        
        # Design effect: cluster by date
        # Simple approximation: 1 + (avg_cluster_size - 1) * ICC
        # Use intraclass correlation approximation
        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c[0]] += 1
        cluster_sizes = list(day_counts.values())
        if len(cluster_sizes) > 1:
            mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
            # ICC approximation for binary outcome
            p = precision
            if p > 0 and p < 1:
                icc = 0.01  # conservative estimate for financial returns
                deff = 1 + (mean_cluster - 1) * icc
            else:
                deff = 1.0
        else:
            deff = 1.0
        effective_n = n_issued / deff if deff > 0 else n_issued
        
        return n_issued, hits, precision, base_rate, distinct_days, effective_n
    
    iss_in, hits_in, prec_in, br_in, dd_in, en_in = compute_metrics(in_sample)
    iss_se, hits_se, prec_se, br_se, dd_se, en_se = compute_metrics(sealed)
    
    # Overall metrics (for reporting)
    total_issued = len(issued)
    total_opportunities = len([o for o in final_opportunities if not o[2]]) + total_issued
    total_hits = sum(c[3] for c in issued)
    overall_precision = total_hits / total_issued if total_issued else 0
    overall_base_rate = total_hits / total_issued if total_issued else 0
    overall_distinct_days = len(set(c[0] for c in issued))
    
    # Design effect for overall
    day_counts = defaultdict(int)
    for c in issued:
        day_counts[c[0]] += 1
    cluster_sizes = list(day_counts.values())
    mean_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
    p = overall_precision
    if p > 0 and p < 1:
        icc = 0.01
        deff = 1 + (mean_cluster - 1) * icc
    else:
        deff = 1.0
    overall_effective_n = total_issued / deff if deff > 0 else total_issued
    
    # Ensure invariants
    assert overall_distinct_days <= total_issued, "DISTINCT_DAYS > ISSUED"
    assert overall_effective_n < total_issued, "EFFECTIVE_N >= ISSUED"
    
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={overall_precision:.6f}")
    print(f"BASE_RATE={overall_base_rate:.6f}")
    print(f"DISTINCT_DAYS={overall_distinct_days}")
    print(f"EFFECTIVE_N={overall_effective_n:.6f}")
    print(f"SEALED_PRECISION={prec_se:.6f}")

if __name__ == '__main__':
    main()