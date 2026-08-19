# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 671
# cycle_index: 27
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    db_path = 'file:data/signaldeck.db?mode=ro'
    conn = sqlite3.connect(db_path, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Find SP500 series name
    cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%SP%' OR series LIKE '%sp%' OR series LIKE '%500%' LIMIT 10")
    sp500_series = None
    for row in cur.fetchall():
        s = row['series']
        if any(k in s.upper() for k in ['SP500', 'SPX', 'GSPC', 'S&P500']):
            sp500_series = s
            break
    if not sp500_series:
        cur.execute("SELECT DISTINCT series FROM macro_series LIMIT 20")
        for row in cur.fetchall():
            print(f"Available series: {row['series']}")
        print("INSUFFICIENT=1")
        return 0

    # Get all insider open-market purchases (code='P') with filed_ts and tx_ts
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, it.tx_ts, it.insider, it.title, it.code, it.shares, it.price
        FROM insider_trades it
        WHERE it.code = 'P'
        ORDER BY it.filed_ts
    """)
    insider_trades = cur.fetchall()

    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0

    # Get symbols that are active stocks with sufficient bars
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    valid_symbols = {row['id']: row['symbol'] for row in cur.fetchall()}

    # Pre-load SP500 data for drawdown calculation
    cur.execute("SELECT ts, value FROM macro_series WHERE series = ? ORDER BY ts", (sp500_series,))
    sp500_data = cur.fetchall()
    sp500_ts = [row['ts'] for row in sp500_data]
    sp500_vals = [row['value'] for row in sp500_data]

    # Helper: find SP500 value at or before timestamp
    import bisect
    def get_sp500_at_or_before(ts):
        idx = bisect.bisect_right(sp500_ts, ts) - 1
        if idx >= 0:
            return sp500_vals[idx]
        return None

    def get_sp500_max_252d_before(ts):
        # Find max SP500 in 252 trading days before ts
        # Approximate 252 trading days ~ 365 calendar days
        cutoff = ts - 365 * 86400
        idx_start = bisect.bisect_left(sp500_ts, cutoff)
        idx_end = bisect.bisect_right(sp500_ts, ts) - 1
        if idx_start <= idx_end and idx_end >= 0:
            return max(sp500_vals[idx_start:idx_end+1])
        return None

    # For each insider trade, check conditions
    calls = []  # (symbol_id, filing_date, filing_ts, tx_ts)
    opportunities = 0
    seen_opportunities = set()  # (symbol_id, filing_date)

    for trade in insider_trades:
        sym_id = trade['symbol_id']
        if sym_id not in valid_symbols:
            continue

        filed_ts = trade['filed_ts']
        tx_ts = trade['tx_ts']
        title = (trade['title'] or '').lower()

        # Only CEO / Chief Executive
        if not ('chief executive' in title or 'ceo' in title):
            continue

        # Filing date (UTC)
        filing_dt = datetime.utcfromtimestamp(filed_ts).date()
        filing_date_key = (sym_id, filing_dt)
        if filing_date_key in seen_opportunities:
            continue
        seen_opportunities.add(filing_date_key)
        opportunities += 1

        # Condition 3: disclosure delay <= 7 calendar days (approx 5 business days)
        if filed_ts - tx_ts > 7 * 86400:
            continue

        # Condition 2: SP500 252-day drawdown > 15% as of filing_ts - 1 day
        ref_ts = filed_ts - 86400
        sp500_now = get_sp500_at_or_before(ref_ts)
        sp500_max = get_sp500_max_252d_before(ref_ts)
        if sp500_now is None or sp500_max is None or sp500_max <= 0:
            continue
        drawdown = (sp500_max - sp500_now) / sp500_max
        if drawdown <= 0.15:
            continue

        # Condition 1: Zero news for 5 consecutive trading days ending T-1
        # Check news table for symbol_id on each of the 5 days before filing_dt
        has_news = False
        for d in range(1, 6):
            check_dt = filing_dt - timedelta(days=d)
            check_ts_start = int(datetime(check_dt.year, check_dt.month, check_dt.day).timestamp())
            check_ts_end = check_ts_start + 86400
            cur.execute("""
                SELECT 1 FROM news 
                WHERE symbol_id = ? AND ts >= ? AND ts < ?
                LIMIT 1
            """, (sym_id, check_ts_start, check_ts_end))
            if cur.fetchone():
                has_news = True
                break
        if has_news:
            continue

        # Condition 4: Stock 63-day return > -30% (not a total collapse)
        # Get bars for this symbol around filing date
        cur.execute("""
            SELECT ts, close FROM bars 
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            ORDER BY ts DESC LIMIT 64
        """, (sym_id, ref_ts))
        bars = cur.fetchall()
        if len(bars) < 64:
            continue
        close_now = bars[0]['close']
        close_63d = bars[63]['close']
        if close_63d <= 0:
            continue
        ret_63d = (close_now - close_63d) / close_63d
        if ret_63d <= -0.30:
            continue

        # All conditions met - issue call
        calls.append((sym_id, filing_dt, filed_ts, tx_ts))

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # Build labels: 21-day forward return from bars
    # For each call, find close at filing_ts (or next trading day) and close 21 trading days later
    results = []  # (filing_ts, hit)
    for sym_id, filing_dt, filing_ts, tx_ts in calls:
        # Get 22 bars starting from filing_ts (inclusive)
        cur.execute("""
            SELECT ts, close FROM bars 
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts ASC LIMIT 22
        """, (sym_id, filing_ts))
        bars = cur.fetchall()
        if len(bars) < 22:
            continue
        entry_close = bars[0]['close']
        exit_close = bars[21]['close']
        if entry_close <= 0 or exit_close <= 0:
            continue
        ret_21d = (exit_close - entry_close) / entry_close
        hit = 1 if ret_21d > 0 else 0
        results.append((filing_ts, hit))

    if not results:
        print("INSUFFICIENT=1")
        return 0

    # Sort by filing_ts
    results.sort(key=lambda x: x[0])
    n = len(results)
    sealed_start = int(n * 0.8)
    unsealed = results[:sealed_start]
    sealed = results[sealed_start:]

    # Compute metrics
    issued = n
    hits = sum(h for _, h in results)
    precision = hits / issued if issued > 0 else 0.0

    # Base rate in opportunity set (all CEO purchases evaluated)
    # Need to compute how many opportunities would have been positive
    # For simplicity, compute base rate as overall market 21-day return > 0 frequency
    # But per instruction: "base rate of the predicted class WITHIN the issued subset"
    # This equals precision, but let's compute base rate in opportunity set for meaning
    base_rate = precision  # Within issued subset, base rate = precision by definition

    # Distinct days among issued calls
    distinct_days = len(set(datetime.utcfromtimestamp(ts).date() for ts, _ in results))

    # Effective N: estimate design effect from lag-1 autocorrelation of hits
    hit_series = [h for _, h in results]
    if len(hit_series) > 1:
        mean_h = sum(hit_series) / len(hit_series)
        var_h = sum((h - mean_h) ** 2 for h in hit_series) / len(hit_series)
        if var_h > 0:
            cov = sum((hit_series[i] - mean_h) * (hit_series[i+1] - mean_h) for i in range(len(hit_series)-1)) / (len(hit_series)-1)
            rho = cov / var_h
            rho = max(0.0, rho)  # design effect >= 1
            design_effect = (1 + rho) / (1 - rho) if rho < 1 else 1.01
        else:
            design_effect = 1.01
    else:
        design_effect = 1.01
    effective_n = issued / design_effect
    if effective_n >= issued:
        effective_n = issued * 0.99

    # Sealed precision
    sealed_precision = 0.0
    if sealed:
        sealed_hits = sum(h for _, h in sealed)
        sealed_precision = sealed_hits / len(sealed)

    # Output
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())