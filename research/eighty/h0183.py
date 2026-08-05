import sqlite3
import sys
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    # 1. Get all insider purchase disclosures with filed_ts
    cur.execute("""
        SELECT symbol_id, insider, tx_ts, filed_ts, shares, price, value, code
        FROM insider_trades
        WHERE code = 'P' AND shares > 0 AND value > 0
        ORDER BY symbol_id, filed_ts, insider
    """)
    trades = cur.fetchall()

    # 2. Group by symbol and find pairs of distinct insiders within 30 calendar days
    symbol_trades = defaultdict(list)
    for t in trades:
        symbol_trades[t['symbol_id']].append(t)

    opportunities = []  # (symbol_id, second_filed_ts, T_date)
    for symbol_id, s_trades in symbol_trades.items():
        n = len(s_trades)
        for i in range(n):
            for j in range(i + 1, n):
                t1 = s_trades[i]
                t2 = s_trades[j]
                if t1['insider'] == t2['insider']:
                    continue
                # Both must be purchases (code='P' already)
                # Check 30-day window between filed_ts
                filed1 = t1['filed_ts']
                filed2 = t2['filed_ts']
                if abs(filed2 - filed1) > 30 * 86400:
                    continue
                # D is the later disclosure date
                if filed1 > filed2:
                    D_ts = filed1
                    T_candidate = t1  # For reference
                else:
                    D_ts = filed2
                    T_candidate = t2
                opportunities.append((symbol_id, D_ts))

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # 3. For each opportunity, find T (first trading day after D)
    # Get all trading days per symbol from bars
    cur.execute("""
        SELECT symbol_id, ts
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    all_trading_days = cur.fetchall()
    symbol_days = defaultdict(list)
    for row in all_trading_days:
        symbol_days[row['symbol_id']].append(row['ts'])

    def first_trading_day_after(symbol_id, D_ts):
        days = symbol_days.get(symbol_id, [])
        # Find first ts > D_ts
        for d in days:
            if d > D_ts:
                return d
        return None

    candidates = []
    for symbol_id, D_ts in opportunities:
        T = first_trading_day_after(symbol_id, D_ts)
        if T is None:
            continue
        candidates.append((symbol_id, D_ts, T))

    if not candidates:
        print("INSUFFICIENT=1")
        return

    # 4. Get symbol metadata and filter universe
    cur.execute("SELECT id, symbol, market, name, delisted_at FROM symbols")
    symbol_meta = {row['id']: row for row in cur.fetchall()}

    filtered = []
    for symbol_id, D_ts, T in candidates:
        meta = symbol_meta.get(symbol_id)
        if not meta:
            continue
        if meta['market'] != 'stocks':
            continue
        # Exclude delisted before T
        if meta['delisted_at'] and meta['delisted_at'] <= T:
            continue
        # Need at least 12 months of price history at T
        days = symbol_days.get(symbol_id, [])
        if not days:
            continue
        first_available = days[0]
        if T - first_available < 365 * 86400:
            continue
        filtered.append((symbol_id, D_ts, T))

    if not filtered:
        print("INSUFFICIENT=1")
        return

    # 5. For each filtered candidate, compute filters and labels
    results = []
    for symbol_id, D_ts, T in filtered:
        # Get bar at T-1 and T
        cur.execute("""
            SELECT ts, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            ORDER BY ts DESC
            LIMIT 2
        """, (symbol_id, T))
        bar_rows = cur.fetchall()
        if len(bar_rows) < 2:
            continue
        bar_T = bar_rows[0]
        bar_T_minus1 = bar_rows[1]
        if bar_T['ts'] != T or bar_T_minus1['ts'] != T - 86400:
            # Not consecutive trading days (weekend/holiday), skip
            continue

        # 5a. Price >= $5 at T
        if bar_T['close'] < 5:
            continue

        # 5b. Average daily dollar volume >= $10M over prior 60 sessions at T
        cur.execute("""
            SELECT AVG(close * volume) as adv
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ? AND ts >= ?
        """, (symbol_id, T, T - 60 * 86400))
        adv_row = cur.fetchone()
        if not adv_row or adv_row['adv'] is None or adv_row['adv'] < 10e6:
            continue

        # 5c. Market cap $1B–$30B at D: approximate using price * shares outstanding at D
        cur.execute("""
            SELECT value
            FROM fundamentals
            WHERE symbol_id = ? AND metric = 'SharesOutstanding' AND fetched_at <= ?
            ORDER BY fetched_at DESC
            LIMIT 1
        """, (symbol_id, D_ts))
        shares_row = cur.fetchone()
        if not shares_row or shares_row['value'] is None:
            continue
        shares_outstanding = shares_row['value']
        # Price at D: use nearest bar before or on D
        cur.execute("""
            SELECT close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            ORDER BY ts DESC
            LIMIT 1
        """, (symbol_id, D_ts))
        price_D_row = cur.fetchone()
        if not price_D_row:
            continue
        market_cap = price_D_row['close'] * shares_outstanding
        if market_cap < 1e9 or market_cap > 30e9:
            continue

        # 5d. Aggregate disclosed purchase value across both insiders >= $500K
        # Retrieve the two trades for this symbol and D
        cur.execute("""
            SELECT insider, value, filed_ts
            FROM insider_trades
            WHERE symbol_id = ? AND code = 'P' AND filed_ts BETWEEN ? AND ?
        """, (symbol_id, D_ts - 30*86400, D_ts + 30*86400))
        window_trades = cur.fetchall()
        # Group by insider, take max value per insider (in case multiple trades)
        insider_values = defaultdict(int)
        for t in window_trades:
            insider_values[t['insider']] = max(insider_values[t['insider']], t['value'])
        if len(insider_values) < 2:
            continue
        agg_value = sum(sorted(insider_values.values(), reverse=True)[:2])
        if agg_value < 500000:
            continue

        # 5e. Entry conditions: T close between -3% and +3% of T-1 close
        pct_change = (bar_T['close'] - bar_T_minus1['close']) / bar_T_minus1['close']
        if not (-0.03 <= pct_change <= 0.03):
            continue
        # T volume above its 60-day median
        cur.execute("""
            SELECT volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ? AND ts >= ?
        """, (symbol_id, T, T - 60 * 86400))
        vol_rows = [r['volume'] for r in cur.fetchall()]
        if not vol_rows:
            continue
        vol_rows.sort()
        median_vol = vol_rows[len(vol_rows)//2]
        if bar_T['volume'] <= median_vol:
            continue

        # 5f. Abstain conditions
        # Any insider transaction on same disclosure date is a sale or 10b5-1 (code S or other non-P on that date)
        cur.execute("""
            SELECT code
            FROM insider_trades
            WHERE symbol_id = ? AND filed_ts = ?
        """, (symbol_id, D_ts))
        other_codes = set(r['code'] for r in cur.fetchall())
        if 'S' in other_codes or any(c not in ('P', 'A', 'F', 'M') for c in other_codes):
            continue

        # 5-day realized volatility at T in top cross-sectional decile
        # Compute volatility for all symbols at T
        # We'll collect later, but for now store
        # We'll need to compute volatility for each symbol at T

        # Stock rose >30% in prior 20 sessions
        cur.execute("""
            SELECT close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ? AND ts >= ?
            ORDER BY ts DESC
        """, (symbol_id, T, T - 20 * 86400))
        price_20 = cur.fetchone()
        if price_20 and bar_T_minus1['close'] > 0:
            ret_20 = (bar_T_minus1['close'] - price_20['close']) / price_20['close']
            if ret_20 > 0.30:
                continue

        # Price < $5 already handled

        # Need to compute label (UP or not) for T+20
        # Find the 20th trading day after T
        days_after_T = [d for d in symbol_days[symbol_id] if d > T]
        if len(days_after_T) < 20:
            continue
        T_plus20 = days_after_T[19]  # 20th day

        # Get close at T+20
        cur.execute("""
            SELECT close
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts = ?
        """, (symbol_id, T_plus20))
        close_T20_row = cur.fetchone()
        if not close_T20_row:
            continue

        # Label: UP if close_T20 > bar_T['close']
        label = 1 if close_T20_row['close'] > bar_T['close'] else 0

        results.append({
            'symbol_id': symbol_id,
            'T': T,
            'label': label,
            'T_close': bar_T['close'],
            'T_volume': bar_T['volume'],
        })

    if len(results) < 30:
        print("INSUFFICIENT=1")
        return

    # 6. Compute cross-sectional volatility at each T to apply abstain condition
    # Group results by T to compute top decile volatility
    T_groups = defaultdict(list)
    for r in results:
        T_groups[r['T']].append(r)

    # For each T, compute 5-day volatility for each symbol
    for T, group in T_groups.items():
        volatilities = []
        for r in group:
            symbol_id = r['symbol_id']
            cur.execute("""
                SELECT close
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts < ? AND ts >= ?
                ORDER BY ts DESC
            """, (symbol_id, T, T - 5 * 86400))
            closes = [row['close'] for row in cur.fetchall()]
            if len(closes) < 2:
                continue
            returns = [(closes[i] - closes[i+1]) / closes[i+1] for i in range(len(closes)-1)]
            vol = (sum(r*r for r in returns) / len(returns)) ** 0.5 if returns else 0
            volatilities.append((r['T'], symbol_id, vol))
        if not volatilities:
            continue
        # Compute 90th percentile
        vol_list = [v[2] for v in volatilities]
        vol_list.sort()
        p90 = vol_list[int(len(vol_list)*0.9)]
        # Remove those with volatility above p90
        to_remove = set()
        for T_val, sym, vol in volatilities:
            if vol > p90:
                to_remove.add(sym)
        # Filter group
        T_groups[T] = [r for r in group if r['symbol_id'] not in to_remove]

    # Rebuild results list
    results = []
    for group in T_groups.values():
        results.extend(group)

    if len(results) < 30:
        print("INSUFFICIENT=1")
        return

    # 7. Split into train/test by time (most recent 20% as sealed era)
    results.sort(key=lambda x: x['T'])
    n = len(results)
    split_idx = int(n * 0.8)
    train = results[:split_idx]
    test = results[split_idx:]

    # 8. Compute statistics
    issued_train = len(train)
    if issued_train == 0:
        print("INSUFFICIENT=1")
        return

    # All opportunities are issued calls (we already applied abstain conditions)
    opportunities_count = len(results)  # total independent observations considered
    issued = issued_train
    hits_train = sum(1 for r in train if r['label'] == 1)
    precision_train = hits_train / issued_train if issued_train > 0 else 0
    base_rate_train = hits_train / issued_train if issued_train > 0 else 0

    distinct_days_train = len(set(r['T'] for r in train))

    # Compute design effect: variance of cluster sizes
    day_counts = defaultdict(int)
    for r in train:
        day_counts[r['T']] += 1
    avg_day_count = issued_train / distinct_days_train if distinct_days_train > 0 else 1
    # Sum of squared cluster sizes
    sum_sq = sum(v*v for v in day_counts.values())
    design_effect = sum_sq / (issued_train**2) * distinct_days_train if issued_train > 0 else 1
    effective_n = issued_train / design_effect if design_effect > 0 else issued_train

    # Sealed era
    issued_test = len(test)
    hits_test = sum(1 for r in test if r['label'] == 1)
    precision_test = hits_test / issued_test if issued_test > 0 else 0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision_train:.4f}")
    print(f"BASE_RATE={base_rate_train:.4f}")
    print(f"DISTINCT_DAYS={distinct_days_train}")
    print(f"EFFECTIVE_N={effective_n:.1f}")
    print(f"SEALED_PRECISION={precision_test:.4f}")

if __name__ == "__main__":
    main()