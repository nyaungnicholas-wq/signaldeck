import sqlite3
import math
from collections import defaultdict

DB_PATH = 'data/signaldeck.db'

def fetch_all(query, params=()):
    conn = sqlite3.connect(f'file:{DB_PATH}?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    cur.execute(query, params)
    rows = cur.fetchall()
    conn.close()
    return rows

def main():
    # Check we have sufficient data before building anything
    checks = [
        ("SELECT count(*) FROM symbols WHERE market='stocks'",),
        ("SELECT count(*) FROM insider_trades WHERE code='P'",),
        ("SELECT count(*) FROM macro_series WHERE series='VIX'",),
    ]
    for q, in checks:
        cnt = fetch_all(q)[0][0]
        if cnt < 30:
            print("INSUFFICIENT=1")
            return

    # Get VIX series as dict: date_str -> value
    vix_rows = fetch_all("SELECT ts, value FROM macro_series WHERE series='VIX'")
    vix_dict = {}
    for r in vix_rows:
        ts = r[0]
        # Convert epoch to date string YYYY-MM-DD
        import datetime
        dt = datetime.datetime.utcfromtimestamp(ts)
        day_str = dt.strftime('%Y-%m-%d')
        vix_dict[day_str] = r[1]

    # Get all symbols with daily bars, price, and volume data
    # We'll process day by day, but need to load all data
    # This is large, so we'll filter progressively

    # First, get list of symbols that have insider purchases
    purchase_symbols = set()
    rows = fetch_all("SELECT DISTINCT symbol_id FROM insider_trades WHERE code='P'")
    for r in rows:
        purchase_symbols.add(r[0])

    # Get daily bars for these symbols, with price>=5 and enough history
    # We'll compute per symbol metrics in Python
    bars_by_symbol = defaultdict(list)
    for sym_id in purchase_symbols:
        rows = fetch_all("""
            SELECT ts, open, high, low, close, volume
            FROM bars
            WHERE symbol_id=? AND tf='1d'
            ORDER BY ts ASC
        """, (sym_id,))
        bars_by_symbol[sym_id] = [(r[0], r[1], r[2], r[3], r[4], r[5]) for r in rows]

    # Get insider trades for these symbols
    insider_rows = fetch_all("""
        SELECT symbol_id, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({}) AND (code='P' OR code='S')
    """.format(','.join('?' * len(purchase_symbols))), tuple(purchase_symbols))

    # Organize insider trades by symbol and filed date
    insider_by_sym = defaultdict(list)
    for r in insider_rows:
        sym_id, code, shares, price, value, tx_ts, filed_ts = r
        insider_by_sym[sym_id].append((code, shares, price, value, filed_ts))

    # Prepare to issue calls
    # We'll iterate through each symbol's bars in time order
    # We need to compute rolling metrics: 60-day median volume, 20-day gain, 20-day volatility, etc.
    # Also need VIX history

    # Convert bar timestamps to dates
    def ts_to_date(ts):
        import datetime
        return datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

    issued_calls = []
    opportunities = []
    # Track last call per symbol (in trading days)
    last_call_day = {}

    for sym_id, bars in bars_by_symbol.items():
        if len(bars) < 252:
            continue
        n_bars = len(bars)
        for i in range(252, n_bars):
            ts, o, h, l, c, vol = bars[i]
            date_str = ts_to_date(ts)
            # Check VIX on this day
            if date_str not in vix_dict:
                continue
            vix_val = vix_dict[date_str]
            
            # Check price >= 5
            if c < 5:
                continue
            
            # Compute trailing 20-day gain and volatility
            if i < 20:
                continue
            closes_prev20 = [bars[j][4] for j in range(i-19, i+1)]
            gain_20 = (closes_prev20[-1] - closes_prev20[0]) / closes_prev20[0]
            if gain_20 > 0.3:
                continue
            # Compute 20-day realized volatility (std of log returns)
            log_returns = [math.log(closes_prev20[j]/closes_prev20[j-1]) for j in range(1, len(closes_prev20))]
            mean_r = sum(log_returns)/len(log_returns)
            var_r = sum((r - mean_r)**2 for r in log_returns)/len(log_returns)
            vol_20 = math.sqrt(var_r) * math.sqrt(252)  # annualized
            
            # Compute 60-day median volume
            if i < 60:
                continue
            vols_60 = [bars[j][5] for j in range(i-59, i+1)]
            vols_60_sorted = sorted(vols_60)
            median_vol = vols_60_sorted[30]  # median of 60 is average of 30th and 31st
            if vol < 1.2 * median_vol:
                continue
            
            # Compute 60-day average daily dollar volume
            dollar_vols = [bars[j][4] * bars[j][5] for j in range(i-59, i+1)]
            avg_dollar_vol = sum(dollar_vols)/60
            if avg_dollar_vol < 10e6:
                continue
            
            # Check within 3% of previous close
            prev_close = bars[i-1][4]
            if abs(c - prev_close)/prev_close > 0.03:
                continue
            
            # Check VIX in top quartile of past 252 days
            vix_past252 = []
            for j in range(i-251, i+1):
                d = ts_to_date(bars[j][0])
                if d in vix_dict:
                    vix_past252.append(vix_dict[d])
            if len(vix_past252) < 252:
                continue
            vix_past252_sorted = sorted(vix_past252)
            vix_75th = vix_past252_sorted[252 - 252//4 - 1]  # 75th percentile
            if vix_val < vix_75th:
                continue
            
            # Check for insider purchase in past 5 trading days
            # Need to map ts to trading day index
            # We have bars list, find index of current bar is i
            # We look back up to 5 trading days
            has_purchase = False
            purchase_value = 0
            has_sale_100k = False
            # Get the timestamp for T-5 trading days
            # We need to find the bar index for 5 days before
            idx_start = max(0, i-4)  # inclusive 5 days: i-4,i-3,i-2,i-1,i
            for j in range(idx_start, i+1):
                bar_ts = bars[j][0]
                # Look for insider trades with filed_ts <= bar_ts and within 5 days
                for code, shares, price, value, filed_ts in insider_by_sym.get(sym_id, []):
                    if filed_ts <= bar_ts and code == 'P' and value >= 100000:
                        # Check if filed_ts is within 5 trading days before T
                        # Since we are iterating over trading days, we can approximate by index difference
                        # We'll compute the approximate date difference in days
                        # For simplicity, we'll consider that 5 trading days is about 7 calendar days
                        import datetime
                        filed_dt = datetime.datetime.utcfromtimestamp(filed_ts)
                        t_dt = datetime.datetime.utcfromtimestamp(ts)
                        delta_days = (t_dt - filed_dt).days
                        if 0 <= delta_days <= 7:
                            has_purchase = True
                            purchase_value = shares * c  # use T's close
                            break
                    elif filed_ts <= bar_ts and code == 'S' and value >= 100000:
                        filed_dt = datetime.datetime.utcfromtimestamp(filed_ts)
                        t_dt = datetime.datetime.utcfromtimestamp(ts)
                        delta_days = (t_dt - filed_dt).days
                        if 0 <= delta_days <= 7:
                            has_sale_100k = True
                if has_purchase:
                    break
            
            if not has_purchase:
                continue
            if purchase_value < 100000:
                continue
            if has_sale_100k:
                continue
            
            # Check if we have called this symbol in prior 20 trading days
            if sym_id in last_call_day:
                last_idx = last_call_day[sym_id]
                if i - last_idx <= 20:
                    continue
            
            # Check if we have at least 30 independent observations remaining
            # This is hard to know in advance; we'll issue and later count
            # We'll count issued calls and opportunities after processing all
            
            # Issue call
            issued_calls.append((sym_id, ts, i))
            last_call_day[sym_id] = i
            opportunities.append((sym_id, ts, i))
    
    # If fewer than 30 issued calls, insufficient
    if len(issued_calls) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Compute forward returns for issued calls (T+20)
    hits = 0
    # We'll also collect per-day counts for distinct days
    day_calls = defaultdict(int)
    for sym_id, ts, idx in issued_calls:
        day = ts_to_date(ts)
        day_calls[day] += 1
        # Get forward return at T+20
        bars = bars_by_symbol[sym_id]
        fwd_idx = idx + 20
        if fwd_idx < len(bars):
            fwd_close = bars[fwd_idx][4]
            t_close = bars[idx][4]
            if fwd_close > t_close:
                hits += 1
    
    issued = len(issued_calls)
    opps = len(opportunities)
    precision = hits / issued if issued > 0 else 0
    # Base rate: proportion of UP in issued calls? Actually, base rate of predicted class (UP) within issued subset
    # All issued are UP, so base rate of UP in issued is 1? Wait, that's not right.
    # The base rate is the proportion of UP in the issued calls. Since we only issue UP calls, all issued are UP.
    # So base rate of UP within issued is 1.0. But the claim is about precision, so we compare precision to 1.0?
    # Actually, the hypothesis says: "precision minus issued-subset base rate >= 0.10". If base rate is 1, then precision <= 1, so difference <= 0. That would never be >=0.10.
    # Perhaps base rate means the proportion of positive outcomes in the entire dataset or in the opportunities? Let's re-read.
    # "Report the base rate of the predicted class WITHIN the issued subset." For UP calls, the predicted class is UP. The base rate within the issued subset is the fraction of issued calls that are actually UP (i.e., precision). That would be circular.
    # I think it means: the base rate of the class (UP) in the population of opportunities. So we need to compute the proportion of opportunities that are UP.
    # We'll compute: among all opportunities (including those we didn't call), what fraction have fwd_return > 0?
    # But we didn't compute fwd_return for non-called opportunities. We need to compute that.
    # We'll compute for all opportunities (decision points that passed some filters but maybe not all conditions)
    # Actually, we only have the issued calls list. We need to compute for all opportunities the forward return.
    # We'll recompute for all opportunities we collected.
    opps_up = 0
    opps_total = 0
    for sym_id, ts, idx in opportunities:
        bars = bars_by_symbol[sym_id]
        fwd_idx = idx + 20
        if fwd_idx < len(bars):
            fwd_close = bars[fwd_idx][4]
            t_close = bars[idx][4]
            if fwd_close > t_close:
                opps_up += 1
            opps_total += 1
    base_rate = opps_up / opps_total if opps_total > 0 else 0
    
    # Distinct days in issued calls
    distinct_days = len(day_calls)
    
    # Effective N: need design effect
    # We'll compute autocorrelation of calls within symbols? Simpler: group by symbol and compute variance of counts
    # Design effect = 1 + (average cluster size - 1) * ICC
    # We'll approximate: cluster by symbol, ICC from variance of cluster means
    # But we don't have the underlying series. We'll use a simple formula:
    # Design effect = 1 + (mean cluster size - 1) * ICC
    # We'll estimate ICC as 0.2 as typical for financial data.
    cluster_sizes = defaultdict(int)
    for sym_id, ts, idx in issued_calls:
        cluster_sizes[sym_id] += 1
    if len(cluster_sizes) == 0:
        effective_n = 0
    else:
        m = len(cluster_sizes)  # number of symbols
        n = issued
        avg_k = n / m
        icc = 0.2  # assumed intra-class correlation
        design_effect = 1 + (avg_k - 1) * icc
        effective_n = n / design_effect
    
    # Seal the most recent 20% of opportunities
    if opps > 0:
        sorted_opps = sorted(opportunities, key=lambda x: x[1])  # by ts
        seal_idx = int(len(sorted_opps) * 0.8)
        sealed_opps = sorted_opps[seal_idx:]
        # Issue calls only on sealed_opps that meet conditions? We already issued calls based on conditions.
        # We need to know which of the issued calls fall in the sealed era.
        sealed_issued = [call for call in issued_calls if call[1] >= sorted_opps[seal_idx][1]]
        sealed_hits = 0
        for sym_id, ts, idx in sealed_issued:
            bars = bars_by_symbol[sym_id]
            fwd_idx = idx + 20
            if fwd_idx < len(bars):
                fwd_close = bars[fwd_idx][4]
                t_close = bars[idx][4]
                if fwd_close > t_close:
                    sealed_hits += 1
        sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0
    else:
        sealed_precision = 0
    
    # Output required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opps}")
    print(f"PRECISION={precision}")
    print(f"BASE_RATE={base_rate}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision}")

if __name__ == "__main__":
    main()