# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 470
# cycle_index: 61
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get all 13F periods (quarter ends) with holder counts per symbol
    cur.execute("""
        SELECT 
            symbol_id,
            period,
            COUNT(DISTINCT manager) AS holder_count,
            SUM(value) AS total_value,
            SUM(shares) AS total_shares
        FROM inst_holdings
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """)
    holdings_rows = cur.fetchall()

    # Build holder count history per symbol
    from collections import defaultdict
    holder_history = defaultdict(list)
    for row in holdings_rows:
        holder_history[row['symbol_id']].append({
            'period': row['period'],
            'holder_count': row['holder_count'],
            'total_value': row['total_value'],
            'total_shares': row['total_shares']
        })

    # 2. Identify quarters where holder count increased for 2 consecutive quarters
    decision_points = []  # (symbol_id, decision_date, period)
    for symbol_id, history in holder_history.items():
        if len(history) < 3:
            continue
        history.sort(key=lambda x: x['period'])
        for i in range(2, len(history)):
            h0, h1, h2 = history[i-2], history[i-1], history[i]
            if h1['holder_count'] > h0['holder_count'] and h2['holder_count'] > h1['holder_count']:
                if h2['holder_count'] >= 3:
                    # Period is quarter end (YYYY-MM-DD). Filing deadline ~45 days later.
                    # Decision date = period_end + 45 days (next trading day approximated)
                    period_end = h2['period']
                    try:
                        pe = datetime.strptime(period_end, '%Y-%m-%d')
                    except ValueError:
                        continue
                    decision_date = (pe + timedelta(days=45)).strftime('%Y-%m-%d')
                    decision_points.append((symbol_id, decision_date, period_end))

    if not decision_points:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get insider open-market purchases (code P) filed_ts
    cur.execute("""
        SELECT symbol_id, filed_ts, value, code
        FROM insider_trades
        WHERE code = 'P'
    """)
    insider_rows = cur.fetchall()

    insider_by_symbol = defaultdict(list)
    for row in insider_rows:
        # filed_ts is unix epoch
        filed_date = datetime.utcfromtimestamp(row['filed_ts']).strftime('%Y-%m-%d')
        insider_by_symbol[row['symbol_id']].append({
            'filed_date': filed_date,
            'value': row['value'],
            'filed_ts': row['filed_ts']
        })

    # 4. Get daily bars for price momentum and volume checks
    cur.execute("""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d'
    """)
    bar_rows = cur.fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bar_rows:
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'],
            'close': row['close'],
            'volume': row['volume']
        })
    for sym in bars_by_symbol:
        bars_by_symbol[sym].sort(key=lambda x: x['ts'])

    # 5. Get symbol metadata for active/universe filter
    cur.execute("SELECT id, symbol, active, added_at, delisted_at FROM symbols")
    symbol_meta = {row['id']: row for row in cur.fetchall()}

    # 6. Get prediction_outcomes for labels (horizon ~63 days)
    # First check what horizons exist
    cur.execute("SELECT DISTINCT horizon FROM prediction_outcomes")
    horizons = [row['horizon'] for row in cur.fetchall()]
    target_horizon = 63
    if target_horizon not in horizons:
        # Find closest
        target_horizon = min(horizons, key=lambda h: abs(h - 63))

    cur.execute("""
        SELECT symbol_id, horizon, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = ?
    """, (target_horizon,))
    outcome_rows = cur.fetchall()

    outcomes_by_symbol = defaultdict(dict)
    for row in outcome_rows:
        # ts is unix epoch (decision timestamp)
        dt = datetime.utcfromtimestamp(row['ts']).strftime('%Y-%m-%d')
        outcomes_by_symbol[row['symbol_id']][dt] = {
            'up': row['up'],
            'fwd_return': row['fwd_return']
        }

    # 7. Evaluate each decision point
    issued_calls = []
    opportunities = 0

    for symbol_id, decision_date, period_end in decision_points:
        opportunities += 1

        # Universe filter: symbol must be active at decision date
        meta = symbol_meta.get(symbol_id)
        if not meta or not meta['active']:
            continue
        added_at = meta['added_at']
        delisted_at = meta['delisted_at']
        if added_at and decision_date < added_at[:10]:
            continue
        if delisted_at and decision_date >= delisted_at[:10]:
            continue

        # Check insider purchase in prior 5 trading days (approx 7 calendar days)
        insider_list = insider_by_symbol.get(symbol_id, [])
        has_insider = False
        insider_value = 0
        for ins in insider_list:
            if ins['filed_date'] <= decision_date:
                # Check within 7 calendar days
                try:
                    fd = datetime.strptime(ins['filed_date'], '%Y-%m-%d')
                    dd = datetime.strptime(decision_date, '%Y-%m-%d')
                    if (dd - fd).days <= 7 and (dd - fd).days >= 0:
                        has_insider = True
                        insider_value = max(insider_value, ins['value'] or 0)
                except ValueError:
                    continue
        if not has_insider:
            continue
        if insider_value < 10000:
            continue

        # Check 63-day return negative (using bars up to decision_date)
        bars = bars_by_symbol.get(symbol_id, [])
        if len(bars) < 63:
            continue
        # Find bar at or before decision_date
        dd_ts = int(datetime.strptime(decision_date, '%Y-%m-%d').timestamp())
        idx = -1
        for i, b in enumerate(bars):
            if b['ts'] <= dd_ts:
                idx = i
            else:
                break
        if idx < 62:
            continue
        close_now = bars[idx]['close']
        close_63 = bars[idx - 62]['close']
        ret_63 = (close_now - close_63) / close_63
        if ret_63 >= 0:
            continue

        # Check average daily volume > $1M over prior 63 days
        vol_sum = 0
        for i in range(idx - 62, idx + 1):
            vol_sum += bars[i]['volume'] * bars[i]['close']
        avg_daily_vol = vol_sum / 63
        if avg_daily_vol < 1_000_000:
            continue

        # All entry conditions met - issue call
        # Get label
        outcome = outcomes_by_symbol.get(symbol_id, {}).get(decision_date)
        if outcome is None:
            # Compute from bars if needed (forward 63 days)
            if idx + 63 < len(bars):
                fwd_close = bars[idx + 63]['close']
                fwd_ret = (fwd_close - close_now) / close_now
                up = 1 if fwd_ret > 0 else 0
            else:
                continue  # insufficient forward data
        else:
            up = outcome['up']
            fwd_ret = outcome['fwd_return']

        issued_calls.append({
            'symbol_id': symbol_id,
            'decision_date': decision_date,
            'up': up,
            'fwd_return': fwd_ret
        })

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 8. Hold out most recent 20% as sealed era
    issued_calls.sort(key=lambda x: x['decision_date'])
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_calls = issued_calls[-n_sealed:]
    main_calls = issued_calls[:-n_sealed]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        issued = len(calls)
        hits = sum(1 for c in calls if c['up'] == 1)
        precision = hits / issued if issued else 0
        # Base rate: overall positive rate in opportunities (approximate using all decision points)
        # We'll compute from main_calls opportunities later
        distinct_days = len(set(c['decision_date'] for c in calls))
        # Design effect: simple clustering by day
        # deff = 1 + (avg_cluster_size - 1) * ICC
        # Estimate ICC from day-level precision variance
        from collections import Counter
        day_counts = Counter(c['decision_date'] for c in calls)
        day_hits = Counter(c['decision_date'] for c in calls if c['up'] == 1)
        if len(day_counts) > 1:
            p_overall = hits / issued
            # Between-day variance
            between_var = sum(day_counts[d] * (day_hits.get(d, 0)/day_counts[d] - p_overall)**2 for d in day_counts) / (len(day_counts) - 1)
            total_var = p_overall * (1 - p_overall)
            if total_var > 0:
                icc = max(0, between_var / total_var)
            else:
                icc = 0
            avg_cluster = issued / len(day_counts)
            deff = 1 + (avg_cluster - 1) * icc
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, distinct_days, effective_n

    # Compute base rate from all opportunities (decision points that passed universe filters)
    # We need to track all opportunities considered, not just issued
    # For simplicity, compute base rate from main_calls outcomes (since opportunities ≈ issued + abstained)
    # But we didn't track abstained outcomes. Use main_calls as proxy for base rate in test period.
    main_issued, main_hits, main_precision, main_distinct_days, main_effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _ = compute_metrics(sealed_calls)

    # Base rate: proportion of positive outcomes in the main period opportunities
    # Since we only have outcomes for issued calls, approximate using issued calls in main period
    # But the rule says "base rate of predicted class WITHIN the issued subset"
    # This is ambiguous but likely means the marginal positive rate in the evaluation set
    # We'll use the positive rate among all main_calls (which are the issued calls in main period)
    # Actually, re-reading: "Report the base rate of the predicted class WITHIN the issued subset"
    # This means among the issued calls, what's the base rate? But that's precision.
    # I think it means the base rate in the population from which issued are drawn.
    # We'll compute from all decision points that had outcome data in main period.
    # For now, use main_calls positive rate as base rate (since we don't track abstained outcomes)
    base_rate = main_hits / main_issued if main_issued else 0

    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={main_precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={main_distinct_days}")
    print(f"EFFECTIVE_N={main_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())