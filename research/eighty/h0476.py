# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 475
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all officer open-market purchases (code='P')
    officer_titles = ('%CEO%', '%CFO%', '%COO%', '%PRESIDENT%', '%CHIEF EXECUTIVE%', '%CHIEF FINANCIAL%', '%CHIEF OPERATING%')
    title_cond = ' OR '.join(['title LIKE ?'] * len(officer_titles))
    cur.execute(f'''
        SELECT symbol_id, filed_ts, accession
        FROM insider_trades
        WHERE code = 'P' AND ({title_cond})
        ORDER BY symbol_id, filed_ts
    ''', officer_titles)
    insider_rows = cur.fetchall()
    if not insider_rows:
        print('INSUFFICIENT=1')
        return

    # Group by symbol_id and date (UTC day from filed_ts) -> decision points
    # Use latest filed_ts on each day as decision timestamp
    decisions_by_sym = defaultdict(dict)  # symbol_id -> {date_str: max_filed_ts}
    for row in insider_rows:
        sym = row['symbol_id']
        filed_ts = row['filed_ts']
        dt = datetime.utcfromtimestamp(filed_ts)
        date_str = dt.strftime('%Y-%m-%d')
        if date_str not in decisions_by_sym[sym] or filed_ts > decisions_by_sym[sym][date_str]:
            decisions_by_sym[sym][date_str] = filed_ts

    decision_points = []  # (symbol_id, decision_ts, date_str)
    for sym, dates in decisions_by_sym.items():
        for date_str, ts in dates.items():
            decision_points.append((sym, ts, date_str))
    decision_points.sort(key=lambda x: x[1])

    if not decision_points:
        print('INSUFFICIENT=1')
        return

    # 2. Get all 1d bars for symbols in decision_points (need price, volume, returns)
    sym_ids = list(decisions_by_sym.keys())
    placeholders = ','.join('?' * len(sym_ids))
    cur.execute(f'''
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    ''', sym_ids)
    bars_rows = cur.fetchall()

    # Organize bars by symbol
    bars_by_sym = defaultdict(list)
    for row in bars_rows:
        bars_by_sym[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

    # 3. Compute market 63-day return series (equal-weight avg of symbol 63-day returns)
    # First compute per-symbol 63-day returns at each bar
    sym_returns_63 = defaultdict(dict)  # symbol_id -> {ts: ret_63}
    for sym, bars in bars_by_sym.items():
        if len(bars) < 64:
            continue
        closes = [b[1] for b in bars]
        tss = [b[0] for b in bars]
        for i in range(63, len(bars)):
            ret = closes[i] / closes[i-63] - 1
            sym_returns_63[sym][tss[i]] = ret

    # Compute market 63-day return at each ts (cross-sectional mean)
    market_ret_63 = {}
    all_tss = set()
    for sym_rets in sym_returns_63.values():
        all_tss.update(sym_rets.keys())
    for ts in sorted(all_tss):
        rets = [sym_returns_63[sym].get(ts) for sym in sym_returns_63 if ts in sym_returns_63[sym]]
        if rets:
            market_ret_63[ts] = sum(rets) / len(rets)

    # 4. Get fundamentals for Revenue (metric='Revenues')
    cur.execute(f'''
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'Revenues' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, fetched_at
    ''', sym_ids)
    fund_rows = cur.fetchall()

    # Organize fundamentals by symbol, sorted by fetched_at
    fund_by_sym = defaultdict(list)
    for row in fund_rows:
        fund_by_sym[row['symbol_id']].append((row['fetched_at'], row['as_of'], row['value']))

    # 5. Get prediction_outcomes for 63-day horizon
    # Try common horizon labels
    horizons_to_try = ['63d', '3m', 'quarter', '63', '90d']
    pred_outcomes = {}
    for horiz in horizons_to_try:
        cur.execute(f'''
            SELECT symbol_id, ts, up, fwd_return
            FROM prediction_outcomes
            WHERE horizon = ?
        ''', (horiz,))
        rows = cur.fetchall()
        if rows:
            for row in rows:
                key = (row['symbol_id'], row['ts'])
                pred_outcomes[key] = (row['up'], row['fwd_return'])
            print(f'Using horizon: {horiz} with {len(rows)} labels', file=sys.stderr)
            break
    if not pred_outcomes:
        print('INSUFFICIENT=1')
        return

    # 6. Process each decision point
    opportunities = []  # (symbol_id, decision_ts, date_str, issued, hit)
    for sym, decision_ts, date_str in decision_points:
        bars = bars_by_sym.get(sym, [])
        if not bars:
            continue

        # Find bar at or before decision_ts
        bar_idx = -1
        for i, (ts, _, _) in enumerate(bars):
            if ts <= decision_ts:
                bar_idx = i
            else:
                break
        if bar_idx < 63:  # need 63 prior bars for return
            continue
        if bar_idx < 20:  # need 20 prior bars for avg volume
            continue

        # Stock price at decision
        price = bars[bar_idx][1]
        if price < 5:
            continue

        # Avg dollar volume prior 20 days
        dollar_vols = [bars[bar_idx-j][1] * bars[bar_idx-j][2] for j in range(1, 21)]
        avg_dollar_vol = sum(dollar_vols) / 20
        if avg_dollar_vol < 1_000_000:
            continue

        # Stock 63-day return ending at decision_ts (use bar at bar_idx)
        stock_ret_63 = sym_returns_63[sym].get(bars[bar_idx][0])
        if stock_ret_63 is None:
            continue

        # Market 63-day return at same ts
        market_ret = market_ret_63.get(bars[bar_idx][0])
        if market_ret is None:
            continue

        # Price underperformance: stock_ret_63 < market_ret - 0.10
        if not (stock_ret_63 < market_ret - 0.10):
            continue

        # Revenue acceleration
        funds = fund_by_sym.get(sym, [])
        # Filter fetched_at <= decision_ts
        avail_funds = [(as_of, val) for fet, as_of, val in funds if fet <= decision_ts]
        if len(avail_funds) < 5:  # need at least 5 quarters
            continue
        # Sort by as_of (period end)
        avail_funds.sort(key=lambda x: x[0])
        revenues = [v for _, v in avail_funds]
        # Trailing 4Q sum vs prior 4Q sum
        if len(revenues) < 8:
            continue
        trailing_4q = sum(revenues[-4:])
        prior_4q = sum(revenues[-8:-4])
        prev_trailing_4q = sum(revenues[-5:-1])  # one quarter shifted
        prev_prior_4q = sum(revenues[-9:-5])
        if prior_4q == 0 or prev_prior_4q == 0:
            continue
        growth_current = trailing_4q / prior_4q - 1
        growth_prev = prev_trailing_4q / prev_prior_4q - 1
        if not (growth_current > growth_prev):  # acceleration
            continue

        # Fundamental data stale check: latest fetched_at within 90 days
        latest_fetched = max(fet for fet, _, _ in funds if fet <= decision_ts)
        if decision_ts - latest_fetched > 90 * 86400:
            continue

        # All entry conditions met -> issued call
        # Get label
        label_key = (sym, decision_ts)
        # prediction_outcomes ts might not match exactly; find closest?
        # For now exact match
        label = pred_outcomes.get(label_key)
        if label is None:
            # Try to find closest ts within same day
            found = False
            for (psym, pts), (up, fwd) in pred_outcomes.items():
                if psym == sym and abs(pts - decision_ts) < 86400:
                    label = (up, fwd)
                    found = True
                    break
            if not found:
                continue

        up, fwd = label
        hit = 1 if up == 1 else 0
        opportunities.append((sym, decision_ts, date_str, 1, hit))

    if not opportunities:
        print('INSUFFICIENT=1')
        return

    # 7. Split by time: most recent 20% sealed
    opportunities.sort(key=lambda x: x[1])
    n_total = len(opportunities)
    n_sealed = max(1, int(n_total * 0.2))
    train_ops = opportunities[:-n_sealed]
    sealed_ops = opportunities[-n_sealed:]

    def compute_metrics(ops):
        issued = sum(1 for o in ops if o[3])
        hits = sum(o[4] for o in ops if o[3])
        if issued == 0:
            return 0, 0, 0.0, 0.0, 0, 0.0
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(o[2] for o in ops if o[3]))
        # Design effect: Kish effective sample size
        day_counts = defaultdict(int)
        for o in ops:
            if o[3]:
                day_counts[o[2]] += 1
        sum_sq = sum(c*c for c in day_counts.values())
        eff_n = (issued * issued) / sum_sq if sum_sq > 0 else 0
        return issued, hits, precision, base_rate, distinct_days, eff_n

    train_issued, train_hits, train_prec, train_br, train_days, train_eff = compute_metrics(train_ops)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_metrics(sealed_ops)

    # Overall metrics (on full sample for reporting)
    all_issued, all_hits, all_prec, all_br, all_days, all_eff = compute_metrics(opportunities)

    # 8. Print required lines
    print(f'ISSUED={all_issued}')
    print(f'OPPORTUNITIES={n_total}')
    print(f'PRECISION={all_prec:.6f}')
    print(f'BASE_RATE={all_br:.6f}')
    print(f'DISTINCT_DAYS={all_days}')
    print(f'EFFECTIVE_N={all_eff:.6f}')
    print(f'SEALED_PRECISION={sealed_prec:.6f}')

if __name__ == '__main__':
    main()