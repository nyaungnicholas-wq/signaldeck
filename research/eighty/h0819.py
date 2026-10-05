# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 818
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import time
from datetime import datetime, timedelta

def main():
    start_time = time.time()
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Check fundamentals EPS data availability - critical path
    cur.execute("""
        SELECT COUNT(*) as cnt, MIN(fetched_at) as min_fetched, MAX(fetched_at) as max_fetched,
               COUNT(DISTINCT symbol_id) as syms
        FROM fundamentals
        WHERE metric = 'EPS' AND as_of != 0
    """)
    eps_info = cur.fetchone()
    print(f"EPS rows: {eps_info['cnt']}, symbols: {eps_info['syms']}, fetched range: {eps_info['min_fetched']}..{eps_info['max_fetched']}", file=sys.stderr)

    if eps_info['cnt'] < 100:
        print("INSUFFICIENT=1")
        return 0

    # 2. Get universe: symbols with daily bars 2018-07+, avg dollar vol > $5M over trailing 252d, 3yr insider history
    # First, get symbols with sufficient bar history
    cur.execute("""
        SELECT s.id, s.symbol,
               MIN(CASE WHEN b.tf='1d' THEN b.ts END) as first_bar,
               MAX(CASE WHEN b.tf='1d' THEN b.ts END) as last_bar,
               COUNT(CASE WHEN b.tf='1d' THEN 1 END) as bar_count
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        GROUP BY s.id, s.symbol
        HAVING bar_count >= 252
    """)
    symbols = cur.fetchall()
    print(f"Symbols with 252+ daily bars: {len(symbols)}", file=sys.stderr)

    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = [str(s['id']) for s in symbols]
    sym_placeholders = ','.join('?' * len(symbol_ids))

    # 3. Get officer (CEO/CFO) open-market purchases (code='P') with tx_ts
    # Title filtering: insider_trades.title contains 'CEO' or 'CFO'
    cur.execute(f"""
        SELECT it.symbol_id, it.tx_ts, it.filed_ts, it.shares, it.price, it.value, it.title, it.code
        FROM insider_trades it
        WHERE it.symbol_id IN ({sym_placeholders})
          AND it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
        ORDER BY it.symbol_id, it.tx_ts
    """, symbol_ids)
    officer_trades = cur.fetchall()
    print(f"Officer open-market purchases: {len(officer_trades)}", file=sys.stderr)

    if not officer_trades:
        print("INSUFFICIENT=1")
        return 0

    # 4. For each trade, we need to check entry conditions at tx_ts (trade date T)
    # Conditions:
    # (1) Gap down >3%: open_T < 0.97 * close_{T-1}
    # (2) Volume_T > 1.5 * avg_volume_{T-21:T-1}
    # (3) Close_T > low_T + 0.75 * (high_T - low_T)  [reversal: close in upper 25% of range]
    # (4) Quarterly EPS growth increased for last 2+ consecutive quarters (latest at T)
    # Abstain if:
    # - Officer trailing 12M net open-market flow (code='P' buys minus code='S' sells) for symbol < 0 at T
    # - Day T news sentiment in top quartile (historical up to T)
    # - Fewer than 3 prior officer open-market purchases in trailing 3 years at T

    # Pre-load daily bars for all relevant symbols into memory for fast lookup
    # We need bars for trade dates and surrounding windows
    trade_dates = set()
    for t in officer_trades:
        # Convert tx_ts to date string for bar lookup
        dt = datetime.utcfromtimestamp(t['tx_ts']).date()
        trade_dates.add((t['symbol_id'], dt.isoformat()))

    # Get date range needed: min trade date - 252 days to max trade date + 21 days
    min_tx = min(t['tx_ts'] for t in officer_trades)
    max_tx = max(t['tx_ts'] for t in officer_trades)
    min_date = datetime.utcfromtimestamp(min_tx).date() - timedelta(days=400)  # buffer for 252 trading days
    max_date = datetime.utcfromtimestamp(max_tx).date() + timedelta(days=60)   # buffer for 21 trading days

    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d'
          AND symbol_id IN ({sym_placeholders})
          AND date(ts, 'unixepoch') BETWEEN ? AND ?
    """, symbol_ids + [min_date.isoformat(), max_date.isoformat()])
    bars_rows = cur.fetchall()
    print(f"Loaded {len(bars_rows)} daily bars", file=sys.stderr)

    # Organize bars by symbol_id -> list of (date_str, open, high, low, close, volume, ts)
    bars_by_sym = {}
    for b in bars_rows:
        sid = b['symbol_id']
        dt = datetime.utcfromtimestamp(b['ts']).date().isoformat()
        bars_by_sym.setdefault(sid, []).append((dt, b['open'], b['high'], b['low'], b['close'], b['volume'], b['ts']))

    # Sort each symbol's bars by date
    for sid in bars_by_sym:
        bars_by_sym[sid].sort(key=lambda x: x[0])

    # Create date->index mapping for each symbol for fast lookups
    bar_idx_by_sym = {}
    for sid, bars in bars_by_sym.items():
        bar_idx_by_sym[sid] = {bar[0]: i for i, bar in enumerate(bars)}

    # 5. Load news sentiment for top quartile check
    cur.execute(f"""
        SELECT symbol_id, ts, sentiment, score
        FROM news
        WHERE symbol_id IN ({sym_placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    news_rows = cur.fetchall()
    print(f"Loaded {len(news_rows)} news rows", file=sys.stderr)

    news_by_sym = {}
    for n in news_rows:
        sid = n['symbol_id']
        dt = datetime.utcfromtimestamp(n['ts']).date().isoformat()
        news_by_sym.setdefault(sid, []).append((dt, n['sentiment'], n['score']))

    # 6. Load fundamentals EPS for growth check
    cur.execute(f"""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EPS' AND as_of != 0 AND symbol_id IN ({sym_placeholders})
        ORDER BY symbol_id, fetched_at
    """, symbol_ids)
    eps_rows = cur.fetchall()
    print(f"Loaded {len(eps_rows)} EPS rows", file=sys.stderr)

    eps_by_sym = {}
    for e in eps_rows:
        sid = e['symbol_id']
        eps_by_sym.setdefault(sid, []).append((e['fetched_at'], e['as_of'], e['value']))

    for sid in eps_by_sym:
        eps_by_sym[sid].sort(key=lambda x: x[0])  # sort by fetched_at

    # 7. Load all insider trades for each symbol for flow and prior purchase counts
    cur.execute(f"""
        SELECT symbol_id, tx_ts, code, shares, price, value, title
        FROM insider_trades
        WHERE symbol_id IN ({sym_placeholders})
        ORDER BY symbol_id, tx_ts
    """, symbol_ids)
    all_insider = cur.fetchall()
    print(f"Loaded {len(all_insider)} total insider trades", file=sys.stderr)

    insider_by_sym = {}
    for it in all_insider:
        sid = it['symbol_id']
        insider_by_sym.setdefault(sid, []).append(it)

    # 8. Evaluate each officer trade
    opportunities = 0
    issued_calls = []  # list of (symbol_id, tx_ts, hit, sealed)

    for trade in officer_trades:
        opportunities += 1
        sid = trade['symbol_id']
        tx_ts = trade['tx_ts']
        trade_date = datetime.utcfromtimestamp(tx_ts).date().isoformat()

        # Check universe constraints at trade date:
        # - Symbol has 252-session avg dollar vol > $5M trailing up to T-1
        # - Symbol has 3 years insider history before T
        bars = bars_by_sym.get(sid, [])
        if not bars:
            continue
        idx = bar_idx_by_sym[sid].get(trade_date)
        if idx is None:
            continue  # no bar for trade date
        if idx < 252:  # need 252 prior bars for avg volume
            continue

        # Avg daily dollar volume over trailing 252 sessions (T-252 to T-1)
        dollar_vols = [bars[i][3] * bars[i][5] for i in range(idx-252, idx)]  # close * volume
        avg_dollar_vol = sum(dollar_vols) / 252
        if avg_dollar_vol <= 5_000_000:
            continue

        # 3 years insider history before T
        insider_trades_sym = insider_by_sym.get(sid, [])
        early_trades = [it for it in insider_trades_sym if it['tx_ts'] < tx_ts]
        if not early_trades:
            continue
        first_insider_date = datetime.utcfromtimestamp(early_trades[0]['tx_ts']).date()
        trade_date_dt = datetime.utcfromtimestamp(tx_ts).date()
        if (trade_date_dt - first_insider_date).days < 3 * 365:
            continue

        # Entry condition (1): Gap down >3%
        if idx == 0:
            continue
        prev_close = bars[idx-1][3]
        curr_open = bars[idx][1]
        if curr_open >= 0.97 * prev_close:
            continue

        # Entry condition (2): Volume_T > 1.5 * avg_volume_{T-21:T-1}
        if idx < 21:
            continue
        vols = [bars[i][5] for i in range(idx-21, idx)]
        avg_vol = sum(vols) / 21
        curr_vol = bars[idx][5]
        if curr_vol <= 1.5 * avg_vol:
            continue

        # Entry condition (3): Close in upper 25% of range
        curr_high = bars[idx][2]
        curr_low = bars[idx][4]
        curr_close = bars[idx][3]
        if curr_high == curr_low:
            continue
        if curr_close <= curr_low + 0.75 * (curr_high - curr_low):
            continue

        # Entry condition (4): EPS growth increased for last 2+ consecutive quarters
        eps_data = eps_by_sym.get(sid, [])
        # Get EPS entries with fetched_at <= tx_ts
        available_eps = [(as_of, val) for fetched, as_of, val in eps_data if fetched <= tx_ts]
        # Group by as_of (quarter), take latest fetched for each quarter
        quarter_eps = {}
        for as_of, val in available_eps:
            if as_of not in quarter_eps or val is not None:
                quarter_eps[as_of] = val
        if len(quarter_eps) < 3:
            continue
        sorted_quarters = sorted(quarter_eps.items())  # by as_of
        # Need at least 3 quarters to have 2 growth comparisons
        growth_rates = []
        for i in range(1, len(sorted_quarters)):
            prev_val = sorted_quarters[i-1][1]
            curr_val = sorted_quarters[i][1]
            if prev_val and prev_val != 0:
                growth_rates.append((curr_val - prev_val) / abs(prev_val))
        if len(growth_rates) < 2:
            continue
        # Last 2+ growth rates must be positive (increasing)
        if not (growth_rates[-1] > 0 and growth_rates[-2] > 0):
            continue

        # Abstention checks
        # A1: Officer trailing 12M net open-market flow < 0
        officer_trades_12m = [it for it in early_trades 
                              if (it['title'].find('CEO') >= 0 or it['title'].find('CFO') >= 0 or 
                                  it['title'].find('Chief Executive') >= 0 or it['title'].find('Chief Financial') >= 0)
                              and tx_ts - it['tx_ts'] <= 365 * 86400]
        net_flow = sum(it['shares'] * it['price'] if it['code'] == 'P' else -it['shares'] * it['price'] 
                       for it in officer_trades_12m)
        if net_flow < 0:
            continue

        # A2: Day T news sentiment in top quartile (historical up to T)
        news_sym = news_by_sym.get(sid, [])
        historical_scores = [score for dt, sent, score in news_sym if dt <= trade_date]
        if historical_scores:
            today_scores = [score for dt, sent, score in news_sym if dt == trade_date]
            if today_scores:
                today_avg = sum(today_scores) / len(today_scores)
                sorted_hist = sorted(historical_scores)
                q75_idx = int(len(sorted_hist) * 0.75)
                if q75_idx < len(sorted_hist):
                    q75 = sorted_hist[q75_idx]
                    if today_avg >= q75:
                        continue

        # A3: Fewer than 3 prior officer open-market purchases in trailing 3 years
        prior_officer_buys = [it for it in early_trades
                              if (it['title'].find('CEO') >= 0 or it['title'].find('CFO') >= 0 or
                                  it['title'].find('Chief Executive') >= 0 or it['title'].find('Chief Financial') >= 0)
                              and it['code'] == 'P'
                              and tx_ts - it['tx_ts'] <= 3 * 365 * 86400]
        if len(prior_officer_buys) < 3:
            continue

        # All conditions met - issue call
        # Compute 21-trading-day forward return
        if idx + 21 >= len(bars):
            continue  # not enough future bars
        future_close = bars[idx + 21][3]
        fwd_return = (future_close - curr_close) / curr_close
        hit = 1 if fwd_return > 0 else 0

        issued_calls.append((sid, tx_ts, hit, trade_date))

    print(f"Opportunities considered: {opportunities}", file=sys.stderr)
    print(f"Issued calls: {len(issued_calls)}", file=sys.stderr)

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 9. Split into sealed era (most recent 20% by time)
    issued_calls.sort(key=lambda x: x[1])  # sort by tx_ts
    split_idx = int(len(issued_calls) * 0.8)
    main_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]

    # 10. Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c[2] for c in calls)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up) within issued subset
        distinct_days = len(set(c[3] for c in calls))
        # Design effect: cluster by symbol and time
        # Simple approximation: group by (symbol, week), count clusters
        clusters = set()
        for sid, tx_ts, hit, dt in calls:
            week = datetime.utcfromtimestamp(tx_ts).isocalendar()[:2]  # (year, week)
            clusters.add((sid, week))
        design_effect = issued / len(clusters) if clusters else 1
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_eff_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    # Total issued across both eras for reporting
    total_issued = main_issued + sealed_issued
    total_hits = main_hits + sealed_hits
    total_precision = total_hits / total_issued if total_issued else 0
    total_base_rate = total_precision  # base rate within issued subset
    total_distinct_days = len(set(c[3] for c in issued_calls))
    # Effective N for total
    clusters = set()
    for sid, tx_ts, hit, dt in issued_calls:
        week = datetime.utcfromtimestamp(tx_ts).isocalendar()[:2]
        clusters.add((sid, week))
    design_effect = total_issued / len(clusters) if clusters else 1
    total_eff_n = total_issued / design_effect if design_effect > 0 else total_issued

    # 11. Print required lines
    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base_rate:.6f}")
    print(f"DISTINCT_DAYS={total_distinct_days}")
    print(f"EFFECTIVE_N={total_eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())