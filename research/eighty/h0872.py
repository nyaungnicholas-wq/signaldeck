# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 871
# cycle_index: 17
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Build equal-weight market daily returns from bars (tf='1d')
    cur.execute("""
        WITH daily AS (
            SELECT symbol_id, date(ts, 'unixepoch') as d, close,
                   LAG(close) OVER (PARTITION BY symbol_id ORDER BY ts) as prev_close
            FROM bars WHERE tf='1d'
        ), rets AS (
            SELECT d, AVG(close / prev_close - 1.0) as mkt_ret
            FROM daily WHERE prev_close IS NOT NULL
            GROUP BY d
        )
        SELECT d, mkt_ret FROM rets ORDER BY d
    """)
    mkt_rows = cur.fetchall()
    if not mkt_rows:
        print("INSUFFICIENT=1")
        return
    mkt_rets = {row['d']: row['mkt_ret'] for row in mkt_rows}
    mkt_dates = sorted(mkt_rets.keys())

    # 2. Get EntityPublicFloat quarterly per symbol, compute 4-quarter decline
    cur.execute("""
        SELECT symbol_id, as_of, value, fetched_at
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat' AND as_of > 0
        ORDER BY symbol_id, as_of
    """)
    float_rows = cur.fetchall()
    float_by_sym = {}
    for r in float_rows:
        float_by_sym.setdefault(r['symbol_id'], []).append(r)

    four_q_decline = set()
    for sym, rows in float_by_sym.items():
        if len(rows) >= 4:
            for i in range(3, len(rows)):
                v0, v1, v2, v3 = rows[i-3]['value'], rows[i-2]['value'], rows[i-1]['value'], rows[i]['value']
                if v0 > v1 > v2 > v3:
                    four_q_decline.add((sym, rows[i]['as_of'], rows[i]['fetched_at']))

    # 3. Get FRED T10Y2Y monthly, compute 3-month rise
    cur.execute("""
        SELECT ts, value FROM macro_series
        WHERE series = 'T10Y2Y' ORDER BY ts
    """)
    fred_rows = cur.fetchall()
    if not fred_rows:
        print("INSUFFICIENT=1")
        return
    # Convert to month-end values
    monthly = {}
    for r in fred_rows:
        dt = datetime.utcfromtimestamp(r['ts'])
        key = (dt.year, dt.month)
        if key not in monthly or r['ts'] > monthly[key][0]:
            monthly[key] = (r['ts'], r['value'])
    sorted_months = sorted(monthly.items())
    three_month_rise = set()
    for i in range(3, len(sorted_months)):
        m0, m1, m2, m3 = sorted_months[i-3][1][1], sorted_months[i-2][1][1], sorted_months[i-1][1][1], sorted_months[i][1][1]
        if m0 < m1 < m2 < m3:
            three_month_rise.add(sorted_months[i][0])  # month key (year, month)

    # 4. Get insider trades: CEO/CFO, code='P', group by symbol, trade_date
    cur.execute("""
        SELECT symbol_id, tx_ts, title
        FROM insider_trades
        WHERE code = 'P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        ORDER BY symbol_id, tx_ts
    """)
    insider_rows = cur.fetchall()
    signals = []
    for r in insider_rows:
        tx_date = datetime.utcfromtimestamp(r['tx_ts']).date()
        signals.append((r['symbol_id'], tx_date, r['title']))

    # Group by symbol, trade_date
    from collections import defaultdict
    trades_by_sym_date = defaultdict(list)
    for sym, d, title in signals:
        trades_by_sym_date[(sym, d)].append(title)

    # 5. Generate candidate signals meeting all conditions
    candidate_signals = []
    for (sym, trade_date), titles in trades_by_sym_date.items():
        if len(set(titles)) < 2:
            continue
        # Check 4-quarter float decline: find latest quarter with as_of <= trade_date
        applicable_float = None
        for sym_f, as_of, fetched_at in four_q_decline:
            if sym_f == sym and as_of <= int(trade_date.strftime('%s')):
                if applicable_float is None or as_of > applicable_float[1]:
                    applicable_float = (fetched_at, as_of)
        if not applicable_float:
            continue
        fetched_at, as_of = applicable_float
        # Check trade_date >= 10 sessions after fetched_at
        fetch_date = datetime.utcfromtimestamp(fetched_at).date()
        # Count trading days between fetch_date and trade_date (approximate with calendar days * 5/7)
        if (trade_date - fetch_date).days < 14:  # ~10 trading days
            continue
        # Check yield curve steepening: trade_date month in three_month_rise
        trade_month = (trade_date.year, trade_date.month)
        if trade_month not in three_month_rise:
            continue
        # Check 252-session underperformance vs equal-weight market
        # Need 252 trading days prior to trade_date
        trade_str = trade_date.isoformat()
        idx = mkt_dates.index(trade_str) if trade_str in mkt_dates else -1
        if idx < 252:
            continue
        # Get symbol's 252-day return
        cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf='1d' AND date(ts, 'unixepoch') <= ?
            ORDER BY ts DESC LIMIT 253
        """, (sym, trade_str))
        bars = cur.fetchall()
        if len(bars) < 253:
            continue
        sym_ret = bars[0]['close'] / bars[-1]['close'] - 1.0
        # Market return over same period
        mkt_ret_252 = 1.0
        for d in mkt_dates[idx-251:idx+1]:
            mkt_ret_252 *= (1.0 + mkt_rets[d])
        mkt_ret_252 -= 1.0
        if sym_ret - mkt_ret_252 > -0.20:  # underperformance <= -20%
            continue
        candidate_signals.append((sym, trade_date))

    if len(candidate_signals) < 25:
        print("INSUFFICIENT=1")
        return

    # 6. Compute forward returns (63 trading days) from next day's close
    results = []
    for sym, trade_date in candidate_signals:
        trade_str = trade_date.isoformat()
        # Find next trading day after trade_date
        cur.execute("""
            SELECT date(ts, 'unixepoch') as d, close FROM bars
            WHERE symbol_id = ? AND tf='1d' AND date(ts, 'unixepoch') > ?
            ORDER BY ts LIMIT 64
        """, (sym, trade_str))
        fwd_bars = cur.fetchall()
        if len(fwd_bars) < 64:
            continue
        entry_close = fwd_bars[0]['close']
        exit_close = fwd_bars[63]['close']
        fwd_ret = exit_close / entry_close - 1.0
        label = 1 if fwd_ret > 0 else 0
        results.append((trade_date, sym, label))

    if not results:
        print("INSUFFICIENT=1")
        return

    # 7. Split: most recent 20% by decision date as sealed
    results.sort(key=lambda x: x[0])
    split_idx = int(len(results) * 0.8)
    in_sample = results[:split_idx]
    sealed = results[split_idx:]

    def compute_metrics(data, label):
        if not data:
            return
        issued = len(data)
        hits = sum(1 for _, _, l in data if l == 1)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # within issued subset
        distinct_days = len(set(d for d, _, _ in data))
        # Design effect approximation: 1 + (avg cluster size - 1) * rho
        # Assume rho=0.1, cluster by day
        day_counts = defaultdict(int)
        for d, _, _ in data:
            day_counts[d] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        deff = 1 + (avg_cluster - 1) * 0.1
        effective_n = issued / deff
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_OPPORTUNITIES={issued}")  # each signal is an opportunity
        print(f"{label}_PRECISION={precision:.6f}")
        print(f"{label}_BASE_RATE={base_rate:.6f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.2f}")

    compute_metrics(in_sample, "IN")
    compute_metrics(sealed, "SEALED")

    # Overall required lines
    all_issued = len(results)
    all_hits = sum(1 for _, _, l in results if l == 1)
    all_precision = all_hits / all_issued if all_issued else 0.0
    all_base = all_hits / all_issued if all_issued else 0.0
    all_distinct = len(set(d for d, _, _ in results))
    day_counts = defaultdict(int)
    for d, _, _ in results:
        day_counts[d] += 1
    avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
    deff = 1 + (avg_cluster - 1) * 0.1
    all_eff_n = all_issued / deff
    sealed_precision = sum(1 for _, _, l in sealed if l == 1) / len(sealed) if sealed else 0.0

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={all_issued}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base:.6f}")
    print(f"DISTINCT_DAYS={all_distinct}")
    print(f"EFFECTIVE_N={all_eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    conn.close()

if __name__ == '__main__':
    main()