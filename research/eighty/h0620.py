# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 619
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date, timedelta
from collections import defaultdict
import math

def business_days_between(start_date, end_date):
    """Count business days from start_date (exclusive) to end_date (inclusive)."""
    if start_date >= end_date:
        return 0
    days = 0
    current = start_date + timedelta(days=1)
    while current <= end_date:
        if current.weekday() < 5:
            days += 1
        current += timedelta(days=1)
    return days

def ts_to_date(ts):
    return datetime.fromtimestamp(ts, tz=__import__('datetime').timezone.utc).date()

def compute_realized_vol(returns, window=252):
    """Compute annualized realized volatility from log returns."""
    if len(returns) < window:
        return None
    recent = returns[-window:]
    mean_ret = sum(recent) / len(recent)
    variance = sum((r - mean_ret) ** 2 for r in recent) / (len(recent) - 1)
    return math.sqrt(variance * 252)

def percentile(values, p):
    """Compute p-th percentile (0-100) of sorted values."""
    if not values:
        return None
    values = sorted(values)
    k = (len(values) - 1) * p / 100
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return values[int(f)]
    return values[int(f)] + (values[int(c)] - values[int(f)]) * (k - f)

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all insider purchases (code='P')
    cur.execute("""
        SELECT accession, symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # Get symbols with daily bars since 2018-07-26 (unix ts for 2018-07-26)
    start_ts = int(datetime(2018, 7, 26, tzinfo=__import__('datetime').timezone.utc).timestamp())
    cur.execute("""
        SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d' AND ts >= ?
    """, (start_ts,))
    symbols_with_bars = {row['symbol_id'] for row in cur.fetchall()}

    # Get symbols with EPS fundamentals
    cur.execute("SELECT DISTINCT symbol_id FROM fundamentals WHERE metric = 'EPS'")
    symbols_with_eps = {row['symbol_id'] for row in cur.fetchall()}

    # Universe: symbols with insider trades, bars, and EPS
    trade_symbols = {t['symbol_id'] for t in trades}
    universe_symbols = trade_symbols & symbols_with_bars & symbols_with_eps

    # Filter trades to universe
    trades = [t for t in trades if t['symbol_id'] in universe_symbols]
    if not trades:
        print("INSUFFICIENT=1")
        return

    # Load daily bars for universe symbols
    placeholders = ','.join('?' * len(universe_symbols))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, tuple(universe_symbols))
    bars_rows = cur.fetchall()

    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    # Compute daily log returns and rolling 252-day vol for each symbol
    vol_by_symbol = {}
    for sym, data in bars_by_symbol.items():
        if len(data) < 253:
            continue
        data.sort()
        closes = [c for _, c in data]
        timestamps = [t for t, _ in data]
        returns = [math.log(closes[i] / closes[i-1]) for i in range(1, len(closes))]
        # rolling vol aligned with timestamps[1:] (return at i uses close[i]/close[i-1])
        vols = []
        for i in range(252, len(returns) + 1):
            vol = compute_realized_vol(returns[i-252:i])
            vols.append((timestamps[i], vol))  # vol as of timestamps[i] (end of day i)
        vol_by_symbol[sym] = vols

    # Load EPS fundamentals
    cur.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EPS' AND symbol_id IN ({})
        ORDER BY symbol_id, as_of
    """.format(placeholders), tuple(universe_symbols))
    eps_rows = cur.fetchall()
    eps_by_symbol = defaultdict(list)
    for row in eps_rows:
        eps_by_symbol[row['symbol_id']].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })

    # Load prediction_outcomes for horizon=21 (try integer and string)
    cur.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon IN (21, '21', '21d') AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(placeholders), tuple(universe_symbols))
    outcome_rows = cur.fetchall()
    outcomes_by_symbol = defaultdict(dict)
    for row in outcome_rows:
        outcomes_by_symbol[row['symbol_id']][row['ts']] = row['up']

    # Process each trade as a decision point
    opportunities = []
    for trade in trades:
        sym = trade['symbol_id']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']
        trade_date = ts_to_date(tx_ts)
        file_date = ts_to_date(filed_ts)

        # Filing delay check
        if business_days_between(trade_date, file_date) > 5:
            continue

        decision_ts = filed_ts
        decision_date = file_date

        # Need volatility at T-1 (day before filing)
        # Find vol for the trading day before decision_date
        vol_data = vol_by_symbol.get(sym, [])
        if not vol_data:
            continue

        # Find vol as of the last trading day before decision_ts
        # vol_data has (ts, vol) where ts is end-of-day timestamp
        target_ts = decision_ts - 86400  # approximately previous day
        vol_at_tminus1 = None
        vol_history = []
        for ts, vol in vol_data:
            if ts <= target_ts:
                vol_at_tminus1 = vol
                vol_history.append(vol)
            else:
                break

        if vol_at_tminus1 is None or len(vol_history) < 1260:  # ~5 years of vol observations
            continue

        # 10th percentile of vol over prior 5 years (excluding current)
        # vol_history includes current vol_at_tminus1 at the end
        prior_vols = vol_history[:-1]  # exclude current
        if len(prior_vols) < 100:
            continue
        p10 = percentile(prior_vols, 10)
        if p10 is None or vol_at_tminus1 > p10:
            continue

        # EPS conditions
        eps_data = eps_by_symbol.get(sym, [])
        # Filter by fetched_at <= decision_ts (as-of discipline)
        available_eps = [e for e in eps_data if e['fetched_at'] <= decision_ts]
        if len(available_eps) < 4:
            continue

        # Sort by as_of descending (most recent first)
        available_eps.sort(key=lambda x: x['as_of'], reverse=True)
        # Need at least 4 quarters: Q0, Q1, Q2, Q3
        if len(available_eps) < 4:
            continue

        q0 = available_eps[0]['value']
        q1 = available_eps[1]['value']
        q2 = available_eps[2]['value']
        q3 = available_eps[3]['value']

        # YoY growth > 0: compare Q0 to Q4 (if available) or Q3 (approx 1 year)
        # Find quarter approximately 1 year before Q0
        q0_asof = available_eps[0]['as_of']
        yoy_q = None
        for e in available_eps[1:]:
            if q0_asof - e['as_of'] >= 300:  # ~10 months in days
                yoy_q = e['value']
                break
        if yoy_q is None or yoy_q == 0:
            continue
        yoy_growth = (q0 - yoy_q) / abs(yoy_q)
        if yoy_growth <= 0:
            continue

        # QoQ acceleration: (Q0-Q1)/|Q1| > (Q1-Q2)/|Q2|
        if q1 == 0 or q2 == 0:
            continue
        qoq_current = (q0 - q1) / abs(q1)
        qoq_prior = (q1 - q2) / abs(q2)
        if qoq_current <= qoq_prior:
            continue

        # All conditions met - issue call
        # Get label from prediction_outcomes at decision_ts (or nearest prior)
        label = None
        sym_outcomes = outcomes_by_symbol.get(sym, {})
        if sym_outcomes:
            # Find outcome with ts <= decision_ts, closest
            candidate_ts = [ts for ts in sym_outcomes if ts <= decision_ts]
            if candidate_ts:
                best_ts = max(candidate_ts)
                label = sym_outcomes[best_ts]

        opportunities.append({
            'symbol_id': sym,
            'decision_ts': decision_ts,
            'decision_date': decision_date,
            'label': label
        })

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Sort by decision_ts
    opportunities.sort(key=lambda x: x['decision_ts'])

    # Split 80/20 by time (most recent 20% sealed)
    split_idx = int(len(opportunities) * 0.8)
    train_opps = opportunities[:split_idx]
    sealed_opps = opportunities[split_idx:]

    # Issued calls are all opportunities (all conditions already met)
    issued_train = [o for o in train_opps if o['label'] is not None]
    issued_sealed = [o for o in sealed_opps if o['label'] is not None]
    all_issued = issued_train + issued_sealed

    if not all_issued:
        print("INSUFFICIENT=1")
        return

    # Metrics
    ISSUED = len(all_issued)
    OPPORTUNITIES = len(opportunities)

    hits = sum(1 for o in all_issued if o['label'] == 1)
    PRECISION = hits / ISSUED if ISSUED > 0 else 0.0

    BASE_RATE = PRECISION  # All calls predict "up", so base rate within issued = precision

    # DISTINCT_DAYS: distinct UTC days among issued calls
    issued_dates = {o['decision_date'] for o in all_issued}
    DISTINCT_DAYS = len(issued_dates)

    # Design effect for EFFECTIVE_N
    # Cluster by UTC week
    clusters = defaultdict(list)
    for o in all_issued:
        # Week key: year-week number
        week_key = o['decision_date'].isocalendar()[:2]  # (year, week)
        clusters[week_key].append(o['label'])

    k = len(clusters)
    N = ISSUED
    if k > 1:
        p = PRECISION
        m_avg = N / k
        # Between-cluster variance
        cluster_props = []
        cluster_sizes = []
        for labels in clusters.values():
            n_i = len(labels)
            p_i = sum(labels) / n_i if n_i > 0 else 0
            cluster_props.append(p_i)
            cluster_sizes.append(n_i)

        # ANOVA estimator for ICC
        MSB = sum(n_i * (p_i - p) ** 2 for n_i, p_i in zip(cluster_sizes, cluster_props)) / (k - 1)
        MSW = sum(n_i * p_i * (1 - p_i) for n_i, p_i in zip(cluster_sizes, cluster_props)) / (N - k)
        if MSB > 0 and MSW > 0:
            rho = (MSB - MSW) / (MSB + (m_avg - 1) * MSW)
            rho = max(0, min(1, rho))
        else:
            rho = 0.1  # conservative minimum
        design_effect = 1 + (m_avg - 1) * rho
        design_effect = max(1.01, design_effect)  # ensure > 1
    else:
        design_effect = 1.01

    EFFECTIVE_N = ISSUED / design_effect

    # Sealed precision
    SEALED_PRECISION = 0.0
    if issued_sealed:
        sealed_hits = sum(1 for o in issued_sealed if o['label'] == 1)
        SEALED_PRECISION = sealed_hits / len(issued_sealed)

    # Print results
    print(f"ISSUED={ISSUED}")
    print(f"OPPORTUNITIES={OPPORTUNITIES}")
    print(f"PRECISION={PRECISION:.6f}")
    print(f"BASE_RATE={BASE_RATE:.6f}")
    print(f"DISTINCT_DAYS={DISTINCT_DAYS}")
    print(f"EFFECTIVE_N={EFFECTIVE_N:.2f}")
    print(f"SEALED_PRECISION={SEALED_PRECISION:.6f}")

if __name__ == "__main__":
    main()