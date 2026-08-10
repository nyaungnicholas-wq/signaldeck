# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 458
# cycle_index: 49
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
import math
from collections import Counter

def parse_period_to_ts(period):
    """Convert period (date string or unix timestamp) to unix timestamp."""
    if isinstance(period, (int, float)):
        return int(period)
    if isinstance(period, str):
        # Try parsing as date string 'YYYY-MM-DD'
        try:
            return int(datetime.strptime(period[:10], '%Y-%m-%d').timestamp())
        except ValueError:
            # Try as unix timestamp string
            try:
                return int(period)
            except ValueError:
                pass
    return None

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_quarter_end(d):
    if d.month <= 3:
        return date_to_unix(datetime(d.year, 3, 31).date())
    elif d.month <= 6:
        return date_to_unix(datetime(d.year, 6, 30).date())
    elif d.month <= 9:
        return date_to_unix(datetime(d.year, 9, 30).date())
    else:
        return date_to_unix(datetime(d.year, 12, 31).date())

def get_next_quarter_end(d):
    if d.month <= 3:
        return date_to_unix(datetime(d.year, 6, 30).date())
    elif d.month <= 6:
        return date_to_unix(datetime(d.year, 9, 30).date())
    elif d.month <= 9:
        return date_to_unix(datetime(d.year, 12, 31).date())
    else:
        return date_to_unix(datetime(d.year + 1, 3, 31).date())

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Load symbols
    symbols = {}
    for row in cur.execute("SELECT id, symbol, market FROM symbols WHERE active=1"):
        symbols[row['id']] = {'symbol': row['symbol'], 'market': row['market']}

    # Load inst_holdings grouped by symbol_id, period
    holdings_by_symbol = {}
    for row in cur.execute("SELECT symbol_id, period, shares FROM inst_holdings"):
        sid = row['symbol_id']
        if sid not in symbols:
            continue
        period_ts = parse_period_to_ts(row['period'])
        if period_ts is None:
            continue
        holdings_by_symbol.setdefault(sid, []).append((period_ts, row['shares']))

    for sid in holdings_by_symbol:
        holdings_by_symbol[sid].sort(key=lambda x: x[0])

    # Load fundamentals: SharesOutstanding
    so_by_symbol = {}
    for row in cur.execute("SELECT symbol_id, as_of, value, fetched_at FROM fundamentals WHERE metric='SharesOutstanding'"):
        sid = row['symbol_id']
        if sid not in symbols:
            continue
        as_of_ts = parse_period_to_ts(row['as_of'])
        if as_of_ts is None:
            continue
        so_by_symbol.setdefault(sid, []).append((as_of_ts, row['value'], row['fetched_at']))

    for sid in so_by_symbol:
        so_by_symbol[sid].sort(key=lambda x: x[0])

    # Load insider purchases (code='P')
    purchases_by_symbol = {}
    for row in cur.execute("SELECT symbol_id, filed_ts FROM insider_trades WHERE code='P'"):
        sid = row['symbol_id']
        if sid not in symbols:
            continue
        purchases_by_symbol.setdefault(sid, []).append(row['filed_ts'])

    for sid in purchases_by_symbol:
        purchases_by_symbol[sid].sort()

    # Helper: get shares outstanding for symbol at period, known by decision_date
    def get_shares_outstanding(sid, period, decision_date):
        if sid not in so_by_symbol:
            return None
        best = None
        for as_of, value, fetched_at in so_by_symbol[sid]:
            if as_of == period and fetched_at <= decision_date:
                if best is None or fetched_at > best[1]:
                    best = (value, fetched_at)
        return best[0] if best else None

    # Helper: compute avg daily dollar volume over prior 63 trading days
    def check_liquidity(sid, decision_ts):
        start_ts = decision_ts - 90 * 86400
        rows = cur.execute(
            "SELECT close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<? ORDER BY ts DESC LIMIT 63",
            (sid, start_ts, decision_ts)
        ).fetchall()
        if len(rows) < 30:
            return False
        total_dollar_vol = sum(r['close'] * r['volume'] for r in rows)
        avg_dollar_vol = total_dollar_vol / len(rows)
        return avg_dollar_vol > 5_000_000

    # Helper: check market cap > $500M at decision_ts
    def check_market_cap(sid, decision_ts, shares_outstanding):
        if shares_outstanding is None or shares_outstanding <= 0:
            return False
        row = cur.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 1",
            (sid, decision_ts)
        ).fetchone()
        if not row:
            return False
        mcap = row['close'] * shares_outstanding
        return mcap > 500_000_000

    # Helper: get 21-day forward return from prediction_outcomes or bars
    def get_forward_return(sid, decision_ts):
        row = cur.execute(
            "SELECT fwd_return FROM prediction_outcomes WHERE symbol_id=? AND horizon=21 AND ts<=? ORDER BY ts DESC LIMIT 1",
            (sid, decision_ts)
        ).fetchone()
        if row and row['fwd_return'] is not None:
            return row['fwd_return']
        row1 = cur.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 1",
            (sid, decision_ts)
        ).fetchone()
        if not row1:
            return None
        start_close = row1['close']
        end_ts = decision_ts + 35 * 86400
        row2 = cur.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 1",
            (sid, end_ts)
        ).fetchone()
        if not row2 or row2['close'] is None or start_close is None or start_close == 0:
            return None
        return (row2['close'] - start_close) / start_close

    opportunities = []
    issued_calls = []

    for sid, holdings in holdings_by_symbol.items():
        if len(holdings) < 2:
            continue
        if sid not in purchases_by_symbol:
            continue

        for i in range(1, len(holdings)):
            period_T = holdings[i][0]
            period_Tm1 = holdings[i-1][0]

            # Quarter T+1 start/end for insider purchases
            q_Tp1_start = period_T + 86400  # day after quarter end
            q_Tp1_end = get_next_quarter_end(unix_to_date(period_T))

            purchases_in_quarter = [ts for ts in purchases_by_symbol[sid] if q_Tp1_start <= ts < q_Tp1_end]
            if len(purchases_in_quarter) < 2:
                continue

            for idx in range(1, len(purchases_in_quarter)):
                decision_ts = purchases_in_quarter[idx]
                if decision_ts < period_T + 45 * 86400:
                    continue

                so_T = get_shares_outstanding(sid, period_T, decision_ts)
                so_Tm1 = get_shares_outstanding(sid, period_Tm1, decision_ts)
                if so_T is None or so_Tm1 is None or so_T <= 0 or so_Tm1 <= 0:
                    continue

                inst_shares_T = sum(sh for p, sh in holdings if p == period_T)
                inst_shares_Tm1 = sum(sh for p, sh in holdings if p == period_Tm1)

                pct_T = inst_shares_T / so_T
                pct_Tm1 = inst_shares_Tm1 / so_Tm1
                change_pp = (pct_T - pct_Tm1) * 100

                if change_pp >= -5:
                    continue

                if not check_liquidity(sid, decision_ts):
                    continue
                if not check_market_cap(sid, decision_ts, so_T):
                    continue

                fwd_ret = get_forward_return(sid, decision_ts)
                if fwd_ret is None:
                    continue

                hit = 1 if fwd_ret > 0 else 0
                issued_calls.append({
                    'sid': sid,
                    'decision_ts': decision_ts,
                    'hit': hit,
                    'fwd_ret': fwd_ret
                })

                opportunities.append({
                    'sid': sid,
                    'period_T': period_T,
                    'decision_ts': decision_ts,
                    'issued': True,
                    'hit': hit
                })
                break

            opportunities.append({
                'sid': sid,
                'period_T': period_T,
                'decision_ts': None,
                'issued': False,
                'hit': None
            })

    if not issued_calls:
        print("INSUFFICIENT=1")
        return

    issued_calls.sort(key=lambda x: x['decision_ts'])

    n_total = len(issued_calls)
    n_sealed = max(1, int(math.ceil(n_total * 0.2)))
    n_main = n_total - n_sealed

    main_calls = issued_calls[:n_main]
    sealed_calls = issued_calls[n_main:]

    issued_count = len(issued_calls)
    main_issued = len(main_calls)
    sealed_issued = len(sealed_calls)

    main_hits = sum(c['hit'] for c in main_calls)
    sealed_hits = sum(c['hit'] for c in sealed_calls)

    precision = main_hits / main_issued if main_issued > 0 else 0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0

    if main_calls:
        main_start = main_calls[0]['decision_ts']
        main_end = main_calls[-1]['decision_ts']
    else:
        main_start = 0
        main_end = 0

    base_rate_rows = cur.execute(
        "SELECT AVG(CASE WHEN fwd_return>0 THEN 1 ELSE 0 END) as br FROM prediction_outcomes WHERE horizon=21 AND ts>=? AND ts<?",
        (main_start, main_end)
    ).fetchone()
    base_rate = base_rate_rows['br'] if base_rate_rows and base_rate_rows['br'] is not None else 0

    distinct_days = len(set(unix_to_date(c['decision_ts']) for c in issued_calls))

    month_counts = Counter()
    for c in issued_calls:
        d = unix_to_date(c['decision_ts'])
        month_key = (d.year, d.month)
        month_counts[month_key] += 1
    if len(month_counts) > 1:
        avg_cluster = issued_count / len(month_counts)
        design_effect = 1 + (avg_cluster - 1) * 0.2
    else:
        design_effect = 1.5
    if design_effect < 1.01:
        design_effect = 1.01
    effective_n = issued_count / design_effect

    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()