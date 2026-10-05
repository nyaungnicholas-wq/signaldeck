# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 750
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import time
from datetime import datetime, timezone

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_unix(d):
    return int(datetime.combine(d, datetime.min.time(), tzinfo=timezone.utc).timestamp())

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Officer open-market purchases (code='P') with disclosure lag <= 5 days
    # Identify CEO/CFO from title
    cur.execute("""
        SELECT
            it.symbol_id,
            it.accession,
            it.insider,
            it.title,
            it.code,
            it.shares,
            it.price,
            it.value,
            it.tx_ts,
            it.filed_ts
        FROM insider_trades it
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' 
               OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
          AND it.filed_ts - it.tx_ts <= 5 * 86400
        ORDER BY it.filed_ts
    """)
    officer_trades = cur.fetchall()
    if not officer_trades:
        print("INSUFFICIENT=1")
        return 0

    # 2. Build quarterly fundamentals per symbol per period (as_of)
    # Pivot Revenue, SharesOutstanding, EPS; exclude as_of=0 (CIK sentinel)
    cur.execute("""
        SELECT
            f.symbol_id,
            f.as_of,
            f.fetched_at,
            MAX(CASE WHEN f.metric = 'Revenues' THEN f.value END) AS revenue,
            MAX(CASE WHEN f.metric = 'SharesOutstanding' THEN f.value END) AS shares_out,
            MAX(CASE WHEN f.metric = 'EPS' THEN f.value END) AS eps
        FROM fundamentals f
        WHERE f.as_of > 0
        GROUP BY f.symbol_id, f.as_of, f.fetched_at
        HAVING revenue IS NOT NULL AND shares_out IS NOT NULL AND shares_out > 0
        ORDER BY f.symbol_id, f.as_of, f.fetched_at
    """)
    fund_rows = cur.fetchall()
    if not fund_rows:
        print("INSUFFICIENT=1")
        return 0

    # Organize fundamentals by symbol_id -> list of (as_of, fetched_at, revenue, shares_out, eps)
    from collections import defaultdict
    fund_by_symbol = defaultdict(list)
    for r in fund_rows:
        fund_by_symbol[r['symbol_id']].append({
            'as_of': r['as_of'],
            'fetched_at': r['fetched_at'],
            'revenue': r['revenue'],
            'shares_out': r['shares_out'],
            'eps': r['eps'],
            'rev_per_share': r['revenue'] / r['shares_out']
        })

    # 3. For each symbol, sort by as_of (period), and for each period keep the latest fetched_at <= decision_date
    # We'll do this per trade in the loop below.

    # 4. Get daily bars for 252-day lookback and 21-day forward
    # We'll query bars per symbol as needed (could be heavy). Better to pre-load for symbols in trades.
    trade_symbols = set(t['symbol_id'] for t in officer_trades)
    if not trade_symbols:
        print("INSUFFICIENT=1")
        return 0

    placeholders = ','.join('?' * len(trade_symbols))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(trade_symbols))
    bar_rows = cur.fetchall()

    bars_by_symbol = defaultdict(list)
    for r in bar_rows:
        bars_by_symbol[r['symbol_id']].append((r['ts'], r['close']))

    # 5. Process each officer trade
    calls = []  # each: (decision_ts, symbol_id, forward_return, up)
    for tr in officer_trades:
        sym = tr['symbol_id']
        filed_ts = tr['filed_ts']
        tx_ts = tr['tx_ts']

        # Abstain: another officer purchase same symbol in prior 30 days
        # (we can check in the loop since trades are sorted by filed_ts)
        # We'll do this after collecting all calls for simplicity, or track last trade per symbol.
        pass

    # Let's restructure: iterate trades in order, maintain last_officer_trade per symbol
    last_officer_trade = {}
    for tr in officer_trades:
        sym = tr['symbol_id']
        filed_ts = tr['filed_ts']
        tx_ts = tr['tx_ts']

        # Abstain: prior officer trade within 30 days
        if sym in last_officer_trade and filed_ts - last_officer_trade[sym] < 30 * 86400:
            continue

        # Get fundamentals as of filed_ts (latest fetched_at <= filed_ts for each period)
        sym_funds = fund_by_symbol.get(sym, [])
        if len(sym_funds) < 4:  # need at least 4 quarters for 2+ quarter trends
            last_officer_trade[sym] = filed_ts
            continue

        # For each period (as_of), find the latest fetched_at <= filed_ts
        # Group by as_of, take max fetched_at <= filed_ts
        period_data = {}
        for f in sym_funds:
            if f['fetched_at'] <= filed_ts:
                key = f['as_of']
                if key not in period_data or f['fetched_at'] > period_data[key]['fetched_at']:
                    period_data[key] = f

        if len(period_data) < 4:
            last_officer_trade[sym] = filed_ts
            continue

        # Sort periods by as_of ascending
        sorted_periods = sorted(period_data.values(), key=lambda x: x['as_of'])
        # Need last 3 periods (current + 2 prior) for 2-quarter trends
        # Actually need 3 periods to compute 2 QoQ changes and 2 YoY growth rates
        # Let's use the latest 3 periods: p0 (latest), p1, p2
        p0, p1, p2 = sorted_periods[-1], sorted_periods[-2], sorted_periods[-3]

        # Condition 1: SharesOutstanding declined QoQ for last 2 quarters
        # p0.shares_out < p1.shares_out AND p1.shares_out < p2.shares_out
        if not (p0['shares_out'] < p1['shares_out'] < p2['shares_out']):
            last_officer_trade[sym] = filed_ts
            continue

        # Condition 2: Revenue per share YoY growth rate accelerated for last 2 quarters
        # Need p0, p1, p2, and p3 (4 quarters ago from p0), p4 (4 quarters ago from p1)
        # YoY growth for p0 = p0.rev_per_share / p3.rev_per_share - 1
        # YoY growth for p1 = p1.rev_per_share / p4.rev_per_share - 1
        # Acceleration: growth_p0 > growth_p1
        # Need at least 5 periods total
        if len(sorted_periods) < 5:
            last_officer_trade[sym] = filed_ts
            continue
        p3 = sorted_periods[-4]
        p4 = sorted_periods[-5]

        growth_p0 = p0['rev_per_share'] / p3['rev_per_share'] - 1
        growth_p1 = p1['rev_per_share'] / p4['rev_per_share'] - 1
        if not (growth_p0 > growth_p1):
            last_officer_trade[sym] = filed_ts
            continue

        # Condition 3: 252-day return as of filed_ts - 1 day < 0
        bars = bars_by_symbol.get(sym, [])
        if len(bars) < 253:
            last_officer_trade[sym] = filed_ts
            continue

        # Find bar index for decision date (filed_ts). Bars ts are unix timestamps (midnight UTC?).
        # We need the bar with ts <= filed_ts (date part). Use binary search.
        # filed_ts is a timestamp; get the date component.
        decision_date = unix_to_date(filed_ts)
        decision_ts = date_to_unix(decision_date)

        # Binary search for rightmost bar with ts <= decision_ts
        lo, hi = 0, len(bars) - 1
        idx = -1
        while lo <= hi:
            mid = (lo + hi) // 2
            if bars[mid][0] <= decision_ts:
                idx = mid
                lo = mid + 1
            else:
                hi = mid - 1

        if idx < 252:  # need 252 bars before (including idx as day 0)
            last_officer_trade[sym] = filed_ts
            continue

        close_now = bars[idx][1]
        close_252 = bars[idx - 252][1]
        ret_252 = close_now / close_252 - 1
        if ret_252 >= 0:
            last_officer_trade[sym] = filed_ts
            continue

        # Condition 4: 21-day forward return (label)
        if idx + 21 >= len(bars):
            last_officer_trade[sym] = filed_ts
            continue
        close_future = bars[idx + 21][1]
        fwd_ret = close_future / close_now - 1
        up = 1 if fwd_ret > 0 else 0

        # All conditions met - issue call
        calls.append({
            'decision_ts': filed_ts,
            'symbol_id': sym,
            'fwd_ret': fwd_ret,
            'up': up
        })
        last_officer_trade[sym] = filed_ts

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # 6. Split into sealed (most recent 20%) and training (80%)
    calls.sort(key=lambda x: x['decision_ts'])
    n = len(calls)
    split_idx = int(n * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c['up'] for c in call_list)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(unix_to_date(c['decision_ts']) for c in call_list))
        # Design effect: crude approximation using 1 + (avg calls per day - 1) * 0.5
        # But we need effective_n < issued. Use simple clustering adjustment.
        # Effective N = issued / design_effect, design_effect >= 1
        # For simplicity, use design_effect = 1 + (issued / distinct_days - 1) * 0.5 if distinct_days > 0 else 1
        if distinct_days > 0:
            design_effect = 1 + (issued / distinct_days - 1) * 0.5
        else:
            design_effect = 1
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Opportunities: number of officer trades considered (after basic filters)
    # We didn't track this exactly. Let's approximate as number of officer trades with disclosure lag <=5d
    # Actually we need "count of decision points considered". That's the number of officer trades that passed
    # the disclosure lag filter and had enough fundamental/bar data to evaluate.
    # We'll track opportunities in the loop.
    # Let's redo the loop with opportunity counting.

    # Re-run with opportunity tracking
    last_officer_trade = {}
    opportunities = 0
    calls = []
    for tr in officer_trades:
        sym = tr['symbol_id']
        filed_ts = tr['filed_ts']
        tx_ts = tr['tx_ts']

        # Basic filters already applied in query (code=P, officer, lag<=5d)
        # Opportunity: this trade is a decision point if it has enough data to evaluate all conditions
        # We'll count it as opportunity if it has >=4 fundamental periods and >=253 bars
        sym_funds = fund_by_symbol.get(sym, [])
        bars = bars_by_symbol.get(sym, [])
        if len(sym_funds) >= 4 and len(bars) >= 253:
            opportunities += 1

        # Abstain: prior officer trade within 30 days
        if sym in last_officer_trade and filed_ts - last_officer_trade[sym] < 30 * 86400:
            continue

        if len(sym_funds) < 4 or len(bars) < 253:
            last_officer_trade[sym] = filed_ts
            continue

        period_data = {}
        for f in sym_funds:
            if f['fetched_at'] <= filed_ts:
                key = f['as_of']
                if key not in period_data or f['fetched_at'] > period_data[key]['fetched_at']:
                    period_data[key] = f

        if len(period_data) < 4:
            last_officer_trade[sym] = filed_ts
            continue

        sorted_periods = sorted(period_data.values(), key=lambda x: x['as_of'])
        if len(sorted_periods) < 5:
            last_officer_trade[sym] = filed_ts
            continue

        p0, p1, p2, p3, p4 = sorted_periods[-1], sorted_periods[-2], sorted_periods[-3], sorted_periods[-4], sorted_periods[-5]

        if not (p0['shares_out'] < p1['shares_out'] < p2['shares_out']):
            last_officer_trade[sym] = filed_ts
            continue

        growth_p0 = p0['rev_per_share'] / p3['rev_per_share'] - 1
        growth_p1 = p1['rev_per_share'] / p4['rev_per_share'] - 1
        if not (growth_p0 > growth_p1):
            last_officer_trade[sym] = filed_ts
            continue

        decision_date = unix_to_date(filed_ts)
        decision_ts = date_to_unix(decision_date)

        lo, hi = 0, len(bars) - 1
        idx = -1
        while lo <= hi:
            mid = (lo + hi) // 2
            if bars[mid][0] <= decision_ts:
                idx = mid
                lo = mid + 1
            else:
                hi = mid - 1

        if idx < 252 or idx + 21 >= len(bars):
            last_officer_trade[sym] = filed_ts
            continue

        close_now = bars[idx][1]
        close_252 = bars[idx - 252][1]
        ret_252 = close_now / close_252 - 1
        if ret_252 >= 0:
            last_officer_trade[sym] = filed_ts
            continue

        close_future = bars[idx + 21][1]
        fwd_ret = close_future / close_now - 1
        up = 1 if fwd_ret > 0 else 0

        calls.append({
            'decision_ts': filed_ts,
            'symbol_id': sym,
            'fwd_ret': fwd_ret,
            'up': up
        })
        last_officer_trade[sym] = filed_ts

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    calls.sort(key=lambda x: x['decision_ts'])
    n = len(calls)
    split_idx = int(n * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    issued_tr, hits_tr, prec_tr, base_tr, distinct_tr, eff_tr = compute_metrics(train_calls)
    issued_se, hits_se, prec_se, base_se, distinct_se, eff_se = compute_metrics(sealed_calls)

    # Overall metrics (on all calls) for reporting? The spec says report on the sample with sealed separate.
    # The printed metrics should be for the full sample (or training?) with SEALED_PRECISION separate.
    # Looking at the required output lines, they don't specify train vs test. Probably full sample for main metrics,
    # and SEALED_PRECISION for the held-out 20%.
    # But OPPORTUNITIES should be total decision points considered.
    # Let's compute on full calls for main metrics.
    issued_all, hits_all, prec_all, base_all, distinct_all, eff_all = compute_metrics(calls)

    print(f"ISSUED={issued_all}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={prec_all:.6f}")
    print(f"BASE_RATE={base_all:.6f}")
    print(f"DISTINCT_DAYS={distinct_all}")
    print(f"EFFECTIVE_N={eff_all:.2f}")
    print(f"SEALED_PRECISION={prec_se:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())