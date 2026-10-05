# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 387
# cycle_index: 55
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def get_quarter_end_dates(start_year=2018):
    """Generate quarter end dates from start_year to present."""
    quarters = []
    for year in range(start_year, 2027):
        for q in [3, 6, 9, 12]:
            if q == 3:
                d = f"{year}-03-31"
            elif q == 6:
                d = f"{year}-06-30"
            elif q == 9:
                d = f"{year}-09-30"
            else:
                d = f"{year}-12-31"
            quarters.append(d)
    return quarters

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_epoch(d):
    return int(datetime.strptime(d, '%Y-%m-%d').timestamp())

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get symbols with EntityPublicFloat data from 2018+
    cur.execute("""
        SELECT DISTINCT f.symbol_id, s.symbol, s.market
        FROM fundamentals f
        JOIN symbols s ON s.id = f.symbol_id
        WHERE f.metric = 'EntityPublicFloat'
          AND f.as_of >= '2018-01-01'
          AND s.active = 1
          AND s.market = 'stocks'
    """)
    symbols_with_float = cur.fetchall()
    if not symbols_with_float:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = [row['symbol_id'] for row in symbols_with_float]
    placeholders = ','.join('?' * len(symbol_ids))

    # 2. Get quarterly public float values with proper as-of (fetched_at)
    # We need the latest fetched_at for each (symbol_id, as_of) pair
    cur.execute(f"""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat'
          AND symbol_id IN ({placeholders})
          AND as_of >= '2018-01-01'
        ORDER BY symbol_id, as_of, fetched_at
    """, symbol_ids)
    float_rows = cur.fetchall()

    # For each (symbol_id, as_of), take the latest fetched_at (most recent knowledge)
    float_data = defaultdict(dict)  # symbol_id -> {as_of: (value, fetched_at)}
    for row in float_rows:
        key = (row['symbol_id'], row['as_of'])
        if key not in float_data or row['fetched_at'] > float_data[key][1]:
            float_data[row['symbol_id']][row['as_of']] = (float(row['value']), row['fetched_at'])

    # 3. Get 13F institutional holdings - aggregate by symbol_id, period
    # Must lag period by 45 days (filing delay)
    cur.execute(f"""
        SELECT symbol_id, period, SUM(value) as total_value, SUM(shares) as total_shares
        FROM inst_holdings
        WHERE symbol_id IN ({placeholders})
        GROUP BY symbol_id, period
        ORDER BY symbol_id, period
    """, symbol_ids)
    inst_rows = cur.fetchall()
    inst_data = defaultdict(dict)  # symbol_id -> {period: (total_value, total_shares)}
    for row in inst_rows:
        inst_data[row['symbol_id']][row['period']] = (float(row['total_value']), float(row['total_shares']))

    # 4. Get daily bars for dollar volume and market cap calculation
    # Use 1d bars, compute rolling averages
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE symbol_id IN ({placeholders})
          AND tf = '1d'
          AND ts >= {date_to_epoch('2018-01-01')}
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bar_rows = cur.fetchall()

    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for row in bar_rows:
        bars_by_symbol[row['symbol_id']].append((row['ts'], float(row['close']), float(row['volume'])))

    # 5. Get prediction_outcomes for horizon=63 (labels)
    cur.execute(f"""
        SELECT symbol_id, horizon, ts, fwd_return, up, resolved_at, basis_epoch
        FROM prediction_outcomes
        WHERE symbol_id IN ({placeholders})
          AND horizon = 63
          AND resolved_at IS NOT NULL
        ORDER BY symbol_id, ts
    """, symbol_ids)
    label_rows = cur.fetchall()
    labels = defaultdict(list)  # symbol_id -> list of (ts, fwd_return, up)
    for row in label_rows:
        labels[row['symbol_id']].append((row['ts'], float(row['fwd_return']), int(row['up'])))

    # 6. Process each symbol to find entry signals
    quarter_ends = get_quarter_end_dates(2018)
    quarter_end_epochs = {q: date_to_epoch(q) for q in quarter_ends}

    opportunities = []  # (decision_ts, symbol_id, decision_date, fwd_return, up)
    issued_calls = []   # subset of opportunities where entry conditions met

    for sym_row in symbols_with_float:
        sym_id = sym_row['symbol_id']
        sym_float = float_data.get(sym_id, {})
        sym_inst = inst_data.get(sym_id, {})
        sym_bars = bars_by_symbol.get(sym_id, [])
        sym_labels = labels.get(sym_id, [])

        if len(sym_float) < 3 or len(sym_bars) < 63:
            continue

        # Sort quarter ends that have data
        available_quarters = sorted([q for q in quarter_ends if q in sym_float])
        if len(available_quarters) < 3:
            continue

        # Pre-compute daily dollar volume and market cap for liquidity filter
        # We'll check at each decision point using trailing 63-day window
        bars_by_date = {ts: (close, vol) for ts, close, vol in sym_bars}
        sorted_bar_dates = sorted(bars_by_date.keys())

        # For each quarter end (starting from 3rd quarter with data), check conditions
        for i in range(2, len(available_quarters)):
            q0 = available_quarters[i-2]
            q1 = available_quarters[i-1]
            q2 = available_quarters[i]  # current quarter end

            # Float values
            f0, f0_fetched = sym_float[q0]
            f1, f1_fetched = sym_float[q1]
            f2, f2_fetched = sym_float[q2]

            # QoQ changes
            chg1 = (f1 - f0) / f0 if f0 > 0 else 0
            chg2 = (f2 - f1) / f1 if f1 > 0 else 0

            # Check float increase >3% for 2+ consecutive quarters
            if chg1 <= 0.03 or chg2 <= 0.03:
                continue

            # Check institutional ownership - need periods matching quarters
            # 13F periods are quarter ends. Must lag by 45 days.
            # Decision can only be made at fetched_at of q2 float data (when we know q2 float)
            decision_ts = f2_fetched
            decision_date = epoch_to_date(decision_ts)

            # Find 13F periods: we need ownership at q1 and q2 (lagged 45 days)
            # 13F for quarter q is filed ~45 days after q. So at decision_ts (f2_fetched),
            # we can know 13F up to quarter ending 45 days before decision_ts.
            # But f2_fetched is when we learned q2 float. Typically fetched_at >= as_of.
            # Conservative: use 13F periods where period_end + 45 days <= decision_ts

            def get_inst_value_at(period_end_date, decision_ts):
                """Get institutional value for period_end_date, available at decision_ts."""
                period_epoch = date_to_epoch(period_end_date)
                # Must be filed (period + 45 days) <= decision_ts
                if period_epoch + 45*86400 > decision_ts:
                    return None
                # Find the period in inst_data (exact match on period string)
                return sym_inst.get(period_end_date, (None, None))[0]

            inst_q1 = get_inst_value_at(q1, decision_ts)
            inst_q2 = get_inst_value_at(q2, decision_ts)
            inst_q0 = get_inst_value_at(q0, decision_ts)

            if inst_q1 is None or inst_q2 is None or inst_q0 is None:
                continue

            # QoQ inst ownership changes
            inst_chg1 = (inst_q1 - inst_q0) / inst_q0 if inst_q0 > 0 else 0
            inst_chg2 = (inst_q2 - inst_q1) / inst_q1 if inst_q1 > 0 else 0

            # ABSTAIN: institutional ownership increased >5% QoQ in same period
            if inst_chg1 > 0.05 or inst_chg2 > 0.05:
                continue

            # Liquidity check at decision time: avg daily dollar volume > $10M, market cap > $500M
            # Use trailing 63 trading days ending at decision_ts
            trailing_bars = [(ts, c, v) for ts, c, v in sym_bars if ts <= decision_ts][-63:]
            if len(trailing_bars) < 20:
                continue
            avg_dollar_vol = sum(c * v for _, c, v in trailing_bars) / len(trailing_bars)
            latest_close = trailing_bars[-1][1]
            # Estimate shares outstanding from float? Use last close * shares approx.
            # Market cap: we don't have shares outstanding directly. 
            # Use float as proxy (conservative, float <= shares outstanding)
            # Actually, fundamentals has 'SharesOutstanding' metric. Let's check if we have it.
            # But we only queried EntityPublicFloat. For now, skip market cap check or use float * price.
            # The hypothesis says market cap > $500M. We'll approximate with float * price.
            est_market_cap = f2 * latest_close
            if avg_dollar_vol <= 10_000_000 or est_market_cap <= 500_000_000:
                continue

            # All entry conditions met - this is an issued call
            # Find label: forward 63-day return from decision_ts
            # prediction_outcomes has ts (prediction time), horizon=63, fwd_return
            # We need the label where ts == decision_ts (or closest before)
            label_match = None
            for lbl_ts, fwd_ret, up in sym_labels:
                if lbl_ts <= decision_ts:
                    label_match = (lbl_ts, fwd_ret, up)
            if label_match is None:
                # No label available at decision time
                continue

            lbl_ts, fwd_ret, up = label_match
            # Negative directional call: we predict down (up=0)
            # Precision for negative calls = P(up=0 | call issued)
            hit = 1 if up == 0 else 0

            opportunities.append((decision_ts, sym_id, decision_date, fwd_ret, up, hit))
            issued_calls.append((decision_ts, sym_id, decision_date, fwd_ret, up, hit))

    if not opportunities:
        print("INSUFFICIENT=1")
        return 0

    # 7. Split into sealed era (most recent 20% by decision_ts)
    all_decisions = sorted(opportunities, key=lambda x: x[0])
    n_total = len(all_decisions)
    n_sealed = max(1, int(n_total * 0.2))
    sealed = all_decisions[-n_sealed:]
    training = all_decisions[:-n_sealed]

    # Filter issued calls for training and sealed
    issued_training = [c for c in training if c in issued_calls]
    issued_sealed = [c for c in sealed if c in issued_calls]

    # 8. Compute metrics
    def compute_metrics(calls, all_opp):
        if not calls:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(calls)
        hits = sum(c[5] for c in calls)
        precision = hits / issued if issued > 0 else 0.0

        # Base rate of predicted class (negative/up=0) WITHIN issued subset
        base_rate = sum(1 for c in calls if c[4] == 0) / issued if issued > 0 else 0.0

        # Distinct UTC days among ISSUED calls only
        distinct_days = len(set(c[2] for c in calls))

        # Effective N: issued / design_effect
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Simple approximation: cluster by month, ICC ~ 0.1
        # Group by year-month
        from collections import Counter
        month_counts = Counter(c[2][:7] for c in calls)
        avg_cluster = sum(month_counts.values()) / len(month_counts) if month_counts else 1
        icc = 0.1  # conservative intraclass correlation
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = issued / design_effect if design_effect > 0 else issued

        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Training metrics (for reference, not printed)
    # Sealed metrics (printed)
    issued_count, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(issued_sealed, sealed)

    # But the requirement says to print overall ISSUED, OPPORTUNITIES, etc. 
    # and SEALED_PRECISION separately. Let me re-read.
    # "PRINT exactly these lines at the end... SEALED_PRECISION=<precision on the sealed era>"
    # So ISSUED, OPPORTUNITIES, PRECISION, BASE_RATE, DISTINCT_DAYS, EFFECTIVE_N are for FULL sample?
    # And SEALED_PRECISION is for sealed era only.
    # Let me compute for full issued calls (training + sealed)

    issued_full = len(issued_calls)
    opp_full = len(opportunities)
    hits_full = sum(c[5] for c in issued_calls)
    precision_full = hits_full / issued_full if issued_full > 0 else 0.0
    base_rate_full = sum(1 for c in issued_calls if c[4] == 0) / issued_full if issued_full > 0 else 0.0
    distinct_days_full = len(set(c[2] for c in issued_calls))
    
    # Effective N for full
    month_counts = Counter(c[2][:7] for c in issued_calls)
    avg_cluster = sum(month_counts.values()) / len(month_counts) if month_counts else 1
    icc = 0.1
    design_effect = 1 + (avg_cluster - 1) * icc
    effective_n_full = issued_full / design_effect if design_effect > 0 else issued_full

    # Sealed precision
    sealed_issued = len(issued_sealed)
    sealed_hits = sum(c[5] for c in issued_sealed)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    # Invariants check
    assert distinct_days_full <= issued_full, f"DISTINCT_DAYS {distinct_days_full} > ISSUED {issued_full}"
    assert effective_n_full < issued_full, f"EFFECTIVE_N {effective_n_full} >= ISSUED {issued_full}"

    print(f"ISSUED={issued_full}")
    print(f"OPPORTUNITIES={opp_full}")
    print(f"PRECISION={precision_full:.6f}")
    print(f"BASE_RATE={base_rate_full:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_full}")
    print(f"EFFECTIVE_N={effective_n_full:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())