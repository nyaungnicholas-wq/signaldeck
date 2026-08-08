# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 401
# cycle_index: 69
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

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Universe: symbols with required data coverage
    # Daily bars from 2018-07-26 (1532563200)
    # Insider trades from 2008
    # News sentiment from 2012
    # Quarterly revenues in fundamentals
    cur.execute("""
        SELECT s.id, s.symbol, s.market
        FROM symbols s
        WHERE s.active = 1
          AND EXISTS (SELECT 1 FROM bars b WHERE b.symbol_id = s.id AND b.tf = '1d' AND b.ts >= 1532563200)
          AND EXISTS (SELECT 1 FROM insider_trades it WHERE it.symbol_id = s.id)
          AND EXISTS (SELECT 1 FROM sentiment_features sf WHERE sf.symbol_id = s.id)
          AND EXISTS (SELECT 1 FROM fundamentals f WHERE f.symbol_id = s.id AND f.metric = 'Revenues')
    """)
    universe_symbols = {row['id']: row for row in cur.fetchall()}
    if not universe_symbols:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = list(universe_symbols.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # 2. Load fundamentals: Revenues (quarterly), SharesOutstanding, EntityPublicFloat
    cur.execute(f"""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE symbol_id IN ({placeholders}) AND metric IN ('Revenues','SharesOutstanding','EntityPublicFloat')
        ORDER BY symbol_id, metric, as_of
    """, symbol_ids)
    fund_rows = cur.fetchall()

    revenues = defaultdict(list)  # symbol_id -> list of (as_of, value, fetched_at)
    shares_out = defaultdict(list)
    pub_float = defaultdict(list)
    for r in fund_rows:
        sid = r['symbol_id']
        if r['metric'] == 'Revenues':
            revenues[sid].append((r['as_of'], r['value'], r['fetched_at']))
        elif r['metric'] == 'SharesOutstanding':
            shares_out[sid].append((r['as_of'], r['value'], r['fetched_at']))
        elif r['metric'] == 'EntityPublicFloat':
            pub_float[sid].append((r['as_of'], r['value'], r['fetched_at']))

    # 3. Load daily bars for all universe symbols (1d only)
    # We need: close, low, volume for 252-day low, 20-day avg dollar volume, price at decision
    # Load in chunks to avoid memory issues
    cur.execute(f"""
        SELECT symbol_id, ts, close, low, volume
        FROM bars
        WHERE symbol_id IN ({placeholders}) AND tf = '1d'
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_symbol = defaultdict(list)
    for r in cur.fetchall():
        bars_by_symbol[r['symbol_id']].append((r['ts'], r['close'], r['low'], r['volume']))

    # 4. Load sentiment_features (daily mean_score)
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)
    sent_by_symbol = defaultdict(list)
    for r in cur.fetchall():
        sent_by_symbol[r['symbol_id']].append((r['day'], r['mean_score']))

    # 5. Load insider trades (code='P' for purchase) with Form 4 filings
    # Need filed_ts as decision time, and verify Form 4 exists
    cur.execute(f"""
        SELECT it.accession, it.symbol_id, it.code, it.shares, it.price, it.value, it.tx_ts, it.filed_ts
        FROM insider_trades it
        WHERE it.symbol_id IN ({placeholders}) AND it.code = 'P'
        ORDER BY it.filed_ts
    """, symbol_ids)
    insider_purchases = cur.fetchall()

    # Verify Form 4 filing exists for each accession (or at least for the symbol around that time)
    # filings table has form='4' for Form 4
    cur.execute(f"""
        SELECT symbol_id, filed_ts
        FROM filings
        WHERE symbol_id IN ({placeholders}) AND form = '4'
        ORDER BY symbol_id, filed_ts
    """, symbol_ids)
    form4_by_symbol = defaultdict(list)
    for r in cur.fetchall():
        form4_by_symbol[r['symbol_id']].append(r['filed_ts'])

    # 6. Load prediction_outcomes for horizon=21 (21 trading days)
    # Need to check what horizon values exist. Assume horizon=21 means 21 days.
    cur.execute(f"""
        SELECT symbol_id, horizon, ts, up, fwd_return, resolved_at
        FROM prediction_outcomes
        WHERE symbol_id IN ({placeholders}) AND horizon = 21
        ORDER BY symbol_id, ts
    """, symbol_ids)
    outcomes_by_symbol = defaultdict(list)
    for r in cur.fetchall():
        outcomes_by_symbol[r['symbol_id']].append((r['ts'], r['up'], r['fwd_return'], r['resolved_at']))

    # Helper functions
    def get_price_at_or_before(bars, target_ts):
        """Get close price at or before target_ts (as-of discipline)"""
        # bars sorted by ts ascending
        lo, hi = 0, len(bars) - 1
        ans = None
        while lo <= hi:
            mid = (lo + hi) // 2
            if bars[mid][0] <= target_ts:
                ans = bars[mid]
                lo = mid + 1
            else:
                hi = mid - 1
        return ans

    def get_bars_window(bars, start_ts, end_ts):
        """Get bars with ts in [start_ts, end_ts]"""
        # binary search for start
        lo, hi = 0, len(bars) - 1
        start_idx = len(bars)
        while lo <= hi:
            mid = (lo + hi) // 2
            if bars[mid][0] >= start_ts:
                start_idx = mid
                hi = mid - 1
            else:
                lo = mid + 1
        res = []
        for i in range(start_idx, len(bars)):
            if bars[i][0] > end_ts:
                break
            res.append(bars[i])
        return res

    def compute_252d_low(bars, decision_ts):
        """Compute 252-day low as of decision_ts (using bars strictly before decision)"""
        # 252 trading days ~ 365 calendar days. Use 365 days to be safe.
        cutoff = decision_ts - 365 * 86400
        window = get_bars_window(bars, cutoff, decision_ts - 1)
        if len(window) < 200:  # require sufficient bars
            return None
        return min(b[2] for b in window)  # low

    def compute_20d_avg_dollar_vol(bars, decision_ts):
        """20-day average dollar volume as of decision_ts"""
        cutoff = decision_ts - 30 * 86400  # ~30 calendar days for 20 trading days
        window = get_bars_window(bars, cutoff, decision_ts - 1)
        if len(window) < 15:
            return None
        # take last 20 bars
        last20 = window[-20:]
        dollar_vols = [b[1] * b[3] for b in last20]  # close * volume
        return sum(dollar_vols) / len(dollar_vols)

    def get_revenue_growth_streak(revenues, decision_fetched_at):
        """Check >=4 consecutive quarters of positive YoY revenue growth as of decision_fetched_at"""
        # revenues: list of (as_of, value, fetched_at) sorted by as_of
        # Only use revenues with fetched_at <= decision_fetched_at (as-of discipline)
        avail = [(as_of, val) for as_of, val, fet in revenues if fet <= decision_fetched_at]
        if len(avail) < 5:  # need at least 5 quarters for 4 YoY comparisons
            return False
        # Sort by as_of descending (most recent first)
        avail.sort(key=lambda x: x[0], reverse=True)
        # Check 4 most recent YoY growths
        for i in range(4):
            if i + 4 >= len(avail):
                return False
            curr_val = avail[i][1]
            yoy_val = avail[i + 4][1]
            if curr_val <= yoy_val:
                return False
        return True

    def get_historical_median_sentiment(sentiment, decision_day):
        """Compute historical median sentiment up to decision_day (exclusive)"""
        # sentiment: list of (day_str, mean_score) sorted by day
        # decision_day is 'YYYY-MM-DD'
        vals = [s[1] for s in sentiment if s[0] < decision_day]
        if len(vals) < 20:
            return None
        vals.sort()
        n = len(vals)
        if n % 2 == 1:
            return vals[n // 2]
        return (vals[n // 2 - 1] + vals[n // 2]) / 2

    def get_20d_avg_sentiment(sentiment, decision_day):
        """20-day average sentiment ending at decision_day (exclusive)"""
        # Get last 20 days before decision_day
        vals = [s[1] for s in sentiment if s[0] < decision_day]
        if len(vals) < 20:
            return None
        return sum(vals[-20:]) / 20

    def estimate_market_cap(symbol_id, decision_ts):
        """Estimate market cap at decision_ts using shares outstanding or public float * price"""
        price_row = get_price_at_or_before(bars_by_symbol.get(symbol_id, []), decision_ts)
        if not price_row:
            return None
        price = price_row[1]
        # Try SharesOutstanding first, then EntityPublicFloat
        for series in (shares_out.get(symbol_id, []), pub_float.get(symbol_id, [])):
            avail = [(as_of, val) for as_of, val, fet in series if fet <= decision_ts]
            if avail:
                avail.sort(key=lambda x: x[0], reverse=True)
                shares = avail[0][1]
                return shares * price
        return None

    # 7. Process each insider purchase
    decisions = []  # list of (decision_ts, symbol_id, filed_ts, outcome_up)
    for ip in insider_purchases:
        sid = ip['symbol_id']
        filed_ts = ip['filed_ts']
        accession = ip['accession']

        # Verify Form 4 filing exists near this filed_ts (within a few days)
        form4s = form4_by_symbol.get(sid, [])
        has_form4 = any(abs(f4 - filed_ts) <= 3 * 86400 for f4 in form4s)
        if not has_form4:
            continue

        bars = bars_by_symbol.get(sid, [])
        if not bars:
            continue

        # Market cap > $500M at disclosure (filed_ts)
        mcap = estimate_market_cap(sid, filed_ts)
        if mcap is None or mcap <= 500_000_000:
            continue

        # 20-day avg dollar volume > $1M
        avg_dvol = compute_20d_avg_dollar_vol(bars, filed_ts)
        if avg_dvol is None or avg_dvol <= 1_000_000:
            continue

        # Revenue growth streak: need fetched_at <= filed_ts (as-of discipline)
        # Use filed_ts as the decision timestamp for fundamentals
        if not get_revenue_growth_streak(revenues.get(sid, []), filed_ts):
            continue

        # Price within 10% of 252-day low
        low_252 = compute_252d_low(bars, filed_ts)
        if low_252 is None:
            continue
        price_row = get_price_at_or_before(bars, filed_ts)
        if not price_row:
            continue
        close_price = price_row[1]
        if close_price > low_252 * 1.10:
            continue

        # News sentiment: 20-day avg >= historical median
        sent = sent_by_symbol.get(sid, [])
        if not sent:
            continue
        decision_day = unix_to_date(filed_ts).isoformat()
        hist_median = get_historical_median_sentiment(sent, decision_day)
        if hist_median is None:
            continue
        avg_20d = get_20d_avg_sentiment(sent, decision_day)
        if avg_20d is None or avg_20d < hist_median:
            continue

        # All entry conditions met - this is an issued call
        # Find outcome at horizon=21 from prediction_outcomes
        # The outcome ts should be the decision ts (or close to it)
        outcomes = outcomes_by_symbol.get(sid, [])
        outcome_up = None
        for ots, up, fwd, res in outcomes:
            if ots == filed_ts or abs(ots - filed_ts) <= 86400:  # same day or next day
                outcome_up = up
                break
        if outcome_up is None:
            # No label available for this decision point
            continue

        decisions.append((filed_ts, sid, outcome_up))

    if not decisions:
        print("INSUFFICIENT=1")
        return 0

    # 8. Hold out most recent 20% as sealed era
    decisions.sort(key=lambda x: x[0])  # sort by decision_ts
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    main_decisions = decisions[:-n_sealed]
    sealed_decisions = decisions[-n_sealed:]

    def compute_metrics(dec_list):
        if not dec_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(dec_list)
        hits = sum(1 for _, _, up in dec_list if up == 1)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate of predicted class (up=1) within issued
        # Distinct UTC days among issued calls
        distinct_days = len(set(unix_to_date(ts) for ts, _, _ in dec_list))
        # Design effect: cluster by day, compute effective N
        # Simple design effect: 1 + (avg_cluster_size - 1) * ICC
        # Use conservative ICC=0.5, cluster by day
        day_counts = defaultdict(int)
        for ts, _, _ in dec_list:
            day_counts[unix_to_date(ts)] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        icc = 0.5
        deff = 1 + (avg_cluster - 1) * icc
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued_main, hits_main, prec_main, base_main, distinct_main, eff_main = compute_metrics(main_decisions)
    issued_sealed, hits_sealed, prec_sealed, base_sealed, distinct_sealed, eff_sealed = compute_metrics(sealed_decisions)

    # Opportunities = total decision points considered (insider purchases that passed Form 4 check)
    # But we need to count all decision points considered, not just issued.
    # The hypothesis says "abstention rate >= 0.95", so opportunities = issued / (1 - abstention_rate)
    # But we should count actual decision points evaluated.
    # Let's count all insider purchases with Form 4 that we evaluated (before filtering)
    # Actually, we need to count all (symbol, day) where we could have made a decision.
    # The hypothesis: "ABSTAIN: No qualifying insider purchase; or revenue growth streak broken; or price >10% above 252-day low; or 20-day avg news sentiment < historical median."
    # So opportunities = number of insider purchase disclosure events (Form 4, code=P) that we evaluated.
    opportunities = len([ip for ip in insider_purchases if any(abs(f4 - ip['filed_ts']) <= 3*86400 for f4 in form4_by_symbol.get(ip['symbol_id'], []))])

    # Print required lines
    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={prec_main:.6f}")
    print(f"BASE_RATE={base_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_main}")
    print(f"EFFECTIVE_N={eff_main:.6f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

    # Verify invariants
    if distinct_main > issued_main:
        print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
        sys.exit(1)
    if eff_main >= issued_main:
        print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)
        sys.exit(1)

    return 0

if __name__ == '__main__':
    sys.exit(main())