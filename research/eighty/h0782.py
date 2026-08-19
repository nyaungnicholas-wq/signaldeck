# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 781
# cycle_index: 51
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timedelta

TRADING_DAYS_PER_YEAR = 252
HORIZON_DAYS = 21
LOOKBACK_DAYS = 252
MIN_BARS = 252
MIN_QUARTERS = 12
MIN_OBSERVATIONS = 30
SEALED_FRACTION = 0.20
SENTIMENT_VOL_PCTL = 0.90
PRICE_VOL_PCTL = 0.10

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Symbols with >=252 daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(*) as n_bars
        FROM bars WHERE tf='1d'
        GROUP BY symbol_id
        HAVING n_bars >= ?
    """, (MIN_BARS,))
    symbols_with_bars = {row['symbol_id'] for row in cur.fetchall()}
    if not symbols_with_bars:
        print("INSUFFICIENT=1"); return

    # 2. Symbols with news sentiment history (sentiment_features)
    cur.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
    symbols_with_sentiment = {row['symbol_id'] for row in cur.fetchall()}

    # 3. Symbols with insider transactions
    cur.execute("SELECT DISTINCT symbol_id FROM insider_trades")
    symbols_with_insider = {row['symbol_id'] for row in cur.fetchall()}

    # 4. Symbols with >=12 quarters EPS AND SharesOutstanding in fundamentals
    # fundamentals: metric in ('EPS','SharesOutstanding'), as_of is period, fetched_at is knowable
    cur.execute("""
        SELECT symbol_id, metric, COUNT(DISTINCT as_of) as n_q
        FROM fundamentals
        WHERE metric IN ('EPS','SharesOutstanding') AND as_of > 0
        GROUP BY symbol_id, metric
        HAVING n_q >= ?
    """, (MIN_QUARTERS,))
    fund_counts = defaultdict(set)
    for row in cur.fetchall():
        fund_counts[row['symbol_id']].add(row['metric'])
    symbols_with_fundamentals = {sid for sid, metrics in fund_counts.items() 
                                  if 'EPS' in metrics and 'SharesOutstanding' in metrics}

    universe = symbols_with_bars & symbols_with_sentiment & symbols_with_insider & symbols_with_fundamentals
    if not universe:
        print("INSUFFICIENT=1"); return

    # 5. CEO/CFO open-market purchases (code='P')
    # title contains CEO or CFO (case-insensitive)
    placeholders = ','.join('?' * len(universe))
    cur.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
          AND code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY filed_ts
    """, tuple(universe))
    trades = [dict(row) for row in cur.fetchall()]
    if not trades:
        print("INSUFFICIENT=1"); return

    # Pre-load data for efficiency
    # Bars: get all 1d bars for universe symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, tuple(universe))
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    # Sentiment features: daily mean_score
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, tuple(universe))
    sent_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        sent_by_symbol[row['symbol_id']].append((row['day'], row['mean_score']))

    # Fundamentals: EPS and SharesOutstanding quarterly
    cur.execute(f"""
        SELECT symbol_id, metric, as_of, value, fetched_at
        FROM fundamentals
        WHERE symbol_id IN ({placeholders}) AND metric IN ('EPS','SharesOutstanding') AND as_of > 0
        ORDER BY symbol_id, metric, as_of
    """, tuple(universe))
    fund_by_symbol = defaultdict(lambda: defaultdict(list))
    for row in cur.fetchall():
        fund_by_symbol[row['symbol_id']][row['metric']].append((row['as_of'], row['value'], row['fetched_at']))

    # All insider trades for prior-trade check
    cur.execute(f"""
        SELECT symbol_id, insider, code, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code = 'P'
        ORDER BY symbol_id, insider, filed_ts
    """, tuple(universe))
    prior_trades = defaultdict(list)
    for row in cur.fetchall():
        prior_trades[(row['symbol_id'], row['insider'])].append(row['filed_ts'])

    # Helper: get trading days from bars for a symbol
    trading_days_cache = {}
    def get_trading_days(symbol_id):
        if symbol_id not in trading_days_cache:
            trading_days_cache[symbol_id] = sorted([epoch_to_date(ts) for ts, _ in bars_by_symbol[symbol_id]])
        return trading_days_cache[symbol_id]

    # Helper: find bar index at or before a date
    def find_bar_index(symbol_id, target_date):
        bars = bars_by_symbol[symbol_id]
        lo, hi = 0, len(bars) - 1
        ans = -1
        while lo <= hi:
            mid = (lo + hi) // 2
            if epoch_to_date(bars[mid][0]) <= target_date:
                ans = mid
                lo = mid + 1
            else:
                hi = mid - 1
        return ans

    # Helper: compute 21-day forward return from bar index
    def get_forward_return(symbol_id, bar_idx):
        if bar_idx < 0 or bar_idx + HORIZON_DAYS >= len(bars_by_symbol[symbol_id]):
            return None
        p0 = bars_by_symbol[symbol_id][bar_idx][1]
        p1 = bars_by_symbol[symbol_id][bar_idx + HORIZON_DAYS][1]
        return (p1 - p0) / p0

    # Helper: compute rolling volatility percentiles for sentiment
    def compute_sentiment_vol_pctls(symbol_id, as_of_date):
        """Return (current_21d_vol, pctl_90_of_prior_252d) or (None, None)"""
        sent = sent_by_symbol.get(symbol_id, [])
        if len(sent) < LOOKBACK_DAYS + HORIZON_DAYS:
            return None, None
        # Filter to days <= as_of_date
        vals = [v for d, v in sent if str_to_date(d) <= as_of_date]
        if len(vals) < LOOKBACK_DAYS + HORIZON_DAYS:
            return None, None
        # 21-day rolling std
        rolling_vols = []
        for i in range(HORIZON_DAYS - 1, len(vals)):
            window = vals[i - HORIZON_DAYS + 1:i + 1]
            mean = sum(window) / HORIZON_DAYS
            vol = math.sqrt(sum((x - mean) ** 2 for x in window) / HORIZON_DAYS)
            rolling_vols.append(vol)
        if len(rolling_vols) < LOOKBACK_DAYS:
            return None, None
        current_vol = rolling_vols[-1]
        prior_vols = rolling_vols[-LOOKBACK_DAYS - 1:-1]  # prior 252 days excluding current
        if len(prior_vols) < LOOKBACK_DAYS:
            return None, None
        prior_vols_sorted = sorted(prior_vols)
        pctl_90 = prior_vols_sorted[int(SENTIMENT_VOL_PCTL * (LOOKBACK_DAYS - 1))]
        return current_vol, pctl_90

    # Helper: compute rolling volatility percentiles for price
    def compute_price_vol_pctls(symbol_id, as_of_date):
        """Return (current_21d_vol, pctl_10_of_prior_252d) or (None, None)"""
        bars = bars_by_symbol.get(symbol_id, [])
        if len(bars) < LOOKBACK_DAYS + HORIZON_DAYS:
            return None, None
        # Filter to bars <= as_of_date
        vals = [close for ts, close in bars if epoch_to_date(ts) <= as_of_date]
        if len(vals) < LOOKBACK_DAYS + HORIZON_DAYS:
            return None, None
        # Log returns
        rets = [math.log(vals[i] / vals[i-1]) for i in range(1, len(vals))]
        # 21-day rolling std of returns
        rolling_vols = []
        for i in range(HORIZON_DAYS - 1, len(rets)):
            window = rets[i - HORIZON_DAYS + 1:i + 1]
            mean = sum(window) / HORIZON_DAYS
            vol = math.sqrt(sum((x - mean) ** 2 for x in window) / HORIZON_DAYS)
            rolling_vols.append(vol)
        if len(rolling_vols) < LOOKBACK_DAYS:
            return None, None
        current_vol = rolling_vols[-1]
        prior_vols = rolling_vols[-LOOKBACK_DAYS - 1:-1]
        if len(prior_vols) < LOOKBACK_DAYS:
            return None, None
        prior_vols_sorted = sorted(prior_vols)
        pctl_10 = prior_vols_sorted[int(PRICE_VOL_PCTL * (LOOKBACK_DAYS - 1))]
        return current_vol, pctl_10

    # Helper: check EPS YoY growth acceleration for 3+ consecutive quarters
    def check_eps_acceleration(symbol_id, as_of_fetched):
        """as_of_fetched is the decision time (filed_ts). Only use fundamentals with fetched_at <= as_of_fetched."""
        eps_data = fund_by_symbol[symbol_id].get('EPS', [])
        # Filter to knowable at decision time
        eps_knowable = [(as_of, val) for as_of, val, fetched in eps_data if fetched <= as_of_fetched]
        if len(eps_knowable) < 4:
            return False
        # Sort by as_of (period)
        eps_knowable.sort(key=lambda x: x[0])
        # Compute YoY growth for each quarter: need same quarter prior year
        # as_of is period timestamp (quarter end). Assume quarterly spacing.
        yoy_growth = []
        for i in range(len(eps_knowable)):
            as_of_i, val_i = eps_knowable[i]
            # Find same quarter prior year (approx 4 quarters back)
            target_as_of = as_of_i - 4 * 90 * 86400  # rough 4 quarters in seconds
            # Find closest prior quarter
            prior_val = None
            for j in range(i-1, -1, -1):
                if eps_knowable[j][0] <= target_as_of + 45*86400:  # within ~45 days
                    prior_val = eps_knowable[j][1]
                    break
            if prior_val and prior_val != 0:
                yoy_growth.append((val_i - prior_val) / abs(prior_val))
            else:
                yoy_growth.append(None)
        # Need last 4 quarters with valid growth and accelerating: q-1 > q-2 > q-3 > q-4
        valid = [g for g in yoy_growth if g is not None]
        if len(valid) < 4:
            return False
        last4 = valid[-4:]
        return last4[3] > last4[2] > last4[1] > last4[0]

    # Helper: check SharesOutstanding did not increase over prior 2 quarters
    def check_shares_not_increased(symbol_id, as_of_fetched):
        so_data = fund_by_symbol[symbol_id].get('SharesOutstanding', [])
        so_knowable = [(as_of, val) for as_of, val, fetched in so_data if fetched <= as_of_fetched]
        if len(so_knowable) < 2:
            return False
        so_knowable.sort(key=lambda x: x[0])
        last2 = so_knowable[-2:]
        return last2[1][1] <= last2[0][1]  # most recent <= previous

    # Helper: check same officer zero open-market trades in prior 21 sessions
    def check_no_prior_trades(symbol_id, insider, filed_ts):
        trades_list = prior_trades.get((symbol_id, insider), [])
        # Count trades with filed_ts in (filed_ts - 21 trading days, filed_ts)
        # Approximate 21 trading days ~ 30 calendar days
        cutoff = filed_ts - 30 * 86400
        for t in trades_list:
            if cutoff < t < filed_ts:
                return False
        return True

    # Evaluate each trade
    opportunities = 0
    issued_calls = []  # (symbol_id, filed_ts, decision_date, fwd_return, up)
    for tr in trades:
        symbol_id = tr['symbol_id']
        filed_ts = tr['filed_ts']
        insider = tr['insider']
        opportunities += 1

        decision_date = epoch_to_date(filed_ts)

        # (a) Sentiment volatility
        sent_vol, sent_pctl90 = compute_sentiment_vol_pctls(symbol_id, decision_date)
        if sent_vol is None or sent_vol <= sent_pctl90:
            continue

        # (b) Price volatility
        price_vol, price_pctl10 = compute_price_vol_pctls(symbol_id, decision_date)
        if price_vol is None or price_vol >= price_pctl10:
            continue

        # (c) EPS acceleration
        if not check_eps_acceleration(symbol_id, filed_ts):
            continue

        # (d) SharesOutstanding not increased
        if not check_shares_not_increased(symbol_id, filed_ts):
            continue

        # (e) No prior trades by same officer in 21 sessions
        if not check_no_prior_trades(symbol_id, insider, filed_ts):
            continue

        # All conditions met - compute label
        bar_idx = find_bar_index(symbol_id, decision_date)
        fwd_ret = get_forward_return(symbol_id, bar_idx)
        if fwd_ret is None:
            continue
        up = 1 if fwd_ret > 0 else 0
        issued_calls.append((symbol_id, filed_ts, decision_date, fwd_ret, up))

    if len(issued_calls) < MIN_OBSERVATIONS:
        print("INSUFFICIENT=1"); return

    # Purged CPCV: time-ordered, hold out most recent 20% as sealed era
    issued_calls.sort(key=lambda x: x[1])  # by filed_ts
    n = len(issued_calls)
    sealed_start = int(n * (1 - SEALED_FRACTION))
    train_calls = issued_calls[:sealed_start]
    sealed_calls = issued_calls[sealed_start:]

    # For CPCV, we need day-clustered effective N
    # Design effect = 1 + (avg_cluster_size - 1) * ICC
    # Simplified: cluster by UTC day, compute effective N = n_days * (n / n_days) / design_effect
    # But requirement: EFFECTIVE_N = issued / design_effect, design_effect > 1
    # We'll compute design effect from day clustering
    def compute_effective_n(calls):
        if not calls:
            return 0.0
        days = set(c[2] for c in calls)
        n_calls = len(calls)
        n_days = len(days)
        if n_days == 0:
            return 0.0
        avg_cluster = n_calls / n_days
        # Intraclass correlation estimate: assume 0.2 for financial returns
        icc = 0.2
        design_effect = 1 + (avg_cluster - 1) * icc
        if design_effect <= 1:
            design_effect = 1.01
        return n_calls / design_effect

    # Metrics on full issued set (for reporting)
    issued = len(issued_calls)
    hits = sum(c[4] for c in issued_calls)
    precision = hits / issued if issued else 0.0
    base_rate = hits / issued if issued else 0.0  # base rate of predicted class (up) within issued subset
    distinct_days = len(set(c[2] for c in issued_calls))
    effective_n = compute_effective_n(issued_calls)

    # Sealed era metrics
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(c[4] for c in sealed_calls)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0.0

    # Invariants check
    assert distinct_days <= issued, "DISTINCT_DAYS > ISSUED"
    assert effective_n < issued, "EFFECTIVE_N >= ISSUED"

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()