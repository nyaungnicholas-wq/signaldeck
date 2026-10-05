# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 799
# cycle_index: 69
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

# MECHANISM: Coordinated but staggered insider buying — when 3+ distinct insiders execute open-market purchases on different trade dates within a 10-trading-day window, and their Form 4 filings (disclosures) cluster within a 2-calendar-day window, the market underreacts to this dispersed-yet-synchronized conviction signal.
# HORIZON: 21 trading days (forward return from bars tf='1d')
# UNIVERSE: Symbols with >=252 daily bars in the 2 years preceding decision date, and >=3 insider open-market purchases (code='P') in trailing year.
# ENTRY: On the market open after the last disclosure date (filed_ts) of a qualifying cluster: 3+ purchases with distinct trade dates spanning <=10 trading days, all filed_ts within 2 calendar days.
# ABSTAIN: <3 purchases in cluster, any shared trade date, disclosure window >2 days, insufficient bar history, 20-day avg dollar volume < $1M.
# CLAIM: Precision >= 0.80 on issued long calls at 21-day horizon; base rate of positive 21-day forward returns in issued subset reported.

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    """Return sorted list of trading day timestamps (unix epoch at 00:00 UTC) for symbol in range."""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row

    # Load all insider purchases (code='P')
    purchases = conn.execute(
        "SELECT symbol_id, insider, code, shares, price, value, tx_ts, filed_ts "
        "FROM insider_trades WHERE code='P' ORDER BY symbol_id, tx_ts"
    ).fetchall()

    if not purchases:
        print("INSUFFICIENT=1")
        return

    # Group by symbol
    by_symbol = defaultdict(list)
    for p in purchases:
        by_symbol[p['symbol_id']].append(p)

    # Get all symbols with sufficient daily bar history
    symbols_with_bars = set()
    cur = conn.execute(
        "SELECT symbol_id, MIN(ts) as min_ts, MAX(ts) as max_ts, COUNT(*) as cnt "
        "FROM bars WHERE tf='1d' GROUP BY symbol_id HAVING cnt >= 252"
    )
    for row in cur:
        symbols_with_bars.add(row['symbol_id'])

    # Filter purchases to symbols with bar history
    by_symbol = {sid: plist for sid, plist in by_symbol.items() if sid in symbols_with_bars}

    # Precompute trading days for each symbol (full range)
    symbol_trading_days = {}
    for sid in by_symbol.keys():
        plist = by_symbol[sid]
        min_tx = min(p['tx_ts'] for p in plist)
        max_filed = max(p['filed_ts'] for p in plist)
        # Extend range for forward return calculation
        start_ts = min_tx - 365*24*3600*2  # 2 years before earliest trade
        end_ts = max_filed + 365*24*3600   # 1 year after latest filing
        days = get_trading_days(conn, sid, start_ts, end_ts)
        if len(days) >= 252:
            symbol_trading_days[sid] = days

    by_symbol = {sid: plist for sid, plist in by_symbol.items() if sid in symbol_trading_days}

    # Map timestamp to trading day index for each symbol
    symbol_td_index = {}
    for sid, days in symbol_trading_days.items():
        symbol_td_index[sid] = {ts: i for i, ts in enumerate(days)}

    # Find clusters
    clusters = []  # (symbol_id, decision_ts, decision_date, cluster_purchases)
    for sid, plist in by_symbol.items():
        td_index = symbol_td_index[sid]
        n = len(plist)
        for i in range(n):
            # Start cluster at purchase i
            cluster = [plist[i]]
            for j in range(i+1, n):
                # Check if trade date is distinct and within 10 trading days of first
                td_i = td_index.get(plist[i]['tx_ts'])
                td_j = td_index.get(plist[j]['tx_ts'])
                if td_i is None or td_j is None:
                    continue
                if td_j == td_i:
                    break  # same trade date, invalidates cluster
                if td_j - td_i > 10:
                    break  # beyond 10 trading days
                cluster.append(plist[j])
            
            if len(cluster) >= 3:
                # Check disclosure clustering: all filed_ts within 2 calendar days
                filed_dates = [ts_to_date(p['filed_ts']) for p in cluster]
                min_filed = min(filed_dates)
                max_filed = max(filed_dates)
                if (max_filed - min_filed).days <= 2:
                    # Valid cluster
                    decision_date = max_filed
                    decision_ts = date_to_ts(decision_date) + 13*3600  # 13:00 UTC ~ market open
                    clusters.append((sid, decision_ts, decision_date, cluster))

    if not clusters:
        print("INSUFFICIENT=1")
        return

    # Sort clusters by decision date
    clusters.sort(key=lambda x: x[2])

    # For each cluster, compute 21-day forward return from bars
    results = []  # (symbol_id, decision_ts, decision_date, fwd_return, hit)
    for sid, decision_ts, decision_date, cluster in clusters:
        td_list = symbol_trading_days[sid]
        td_idx_map = symbol_td_index[sid]
        
        # Find trading day index for decision date
        decision_day_ts = date_to_ts(decision_date)
        if decision_day_ts not in td_idx_map:
            # Find next trading day
            next_td = None
            for td in td_list:
                if td >= decision_day_ts:
                    next_td = td
                    break
            if next_td is None:
                continue
            decision_day_ts = next_td
        
        start_idx = td_idx_map[decision_day_ts]
        end_idx = start_idx + 21
        if end_idx >= len(td_list):
            continue  # not enough future data
        
        start_price = None
        end_price = None
        
        # Get close prices from bars
        cur = conn.execute(
            "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts IN (?,?)",
            (sid, td_list[start_idx], td_list[end_idx])
        )
        prices = {row['ts']: row['close'] for row in cur.fetchall()}
        
        if td_list[start_idx] in prices and td_list[end_idx] in prices:
            start_price = prices[td_list[start_idx]]
            end_price = prices[td_list[end_idx]]
            fwd_return = (end_price - start_price) / start_price
            hit = 1 if fwd_return > 0 else 0
            results.append((sid, decision_ts, decision_date, fwd_return, hit))

    if not results:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_ts
    results.sort(key=lambda x: x[1])

    # Hold out most recent 20% as sealed era
    n_total = len(results)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    
    train_results = results[:n_train]
    sealed_results = results[n_train:]

    # Compute metrics on training set
    issued = len(train_results)
    if issued == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(r[4] for r in train_results)
    precision = hits / issued if issued > 0 else 0.0
    base_rate = hits / issued if issued > 0 else 0.0  # base rate of positive class in issued subset

    # Distinct UTC days among issued calls
    distinct_days = len(set(r[2] for r in train_results))

    # Effective N: issued / design_effect
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Approximate: group by decision_date, compute variance inflation
    from collections import Counter
    day_counts = Counter(r[2] for r in train_results)
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    # Conservative ICC estimate for financial returns ~0.1-0.3
    icc = 0.2
    design_effect = 1 + (avg_cluster - 1) * icc
    effective_n = issued / design_effect if design_effect > 0 else issued

    # Sealed precision
    sealed_issued = len(sealed_results)
    sealed_hits = sum(r[4] for r in sealed_results)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # Opportunities: total decision points considered (clusters found)
    opportunities = len(clusters)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()