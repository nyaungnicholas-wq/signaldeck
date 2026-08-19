# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 870
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def get_universe(conn):
    cur = conn.execute("""
        SELECT id, symbol FROM symbols
        WHERE market = 'stocks' AND active = 1 AND delisted_at IS NULL
    """)
    return {row[0]: row[1] for row in cur.fetchall()}

def get_bars(conn, symbol_id, start_date, end_date):
    cur = conn.execute("""
        SELECT ts, close, volume FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (symbol_id, date_to_epoch(start_date), date_to_epoch(end_date)))
    return [(epoch_to_date(r[0]), r[1], r[2]) for r in cur.fetchall()]

def get_insider_purchases(conn, symbol_id, start_date, end_date):
    cur = conn.execute("""
        SELECT filed_ts, tx_ts, shares, price, value, insider, title
        FROM insider_trades
        WHERE symbol_id = ? AND code = 'P' AND filed_ts >= ? AND filed_ts <= ?
        ORDER BY filed_ts
    """, (symbol_id, date_to_epoch(start_date), date_to_epoch(end_date)))
    return [(epoch_to_date(r[0]), epoch_to_date(r[1]), r[2], r[3], r[4], r[5], r[6]) for r in cur.fetchall()]

def get_insider_sales(conn, symbol_id, start_date, end_date):
    cur = conn.execute("""
        SELECT filed_ts FROM insider_trades
        WHERE symbol_id = ? AND code = 'S' AND filed_ts >= ? AND filed_ts <= ?
    """, (symbol_id, date_to_epoch(start_date), date_to_epoch(end_date)))
    return [epoch_to_date(r[0]) for r in cur.fetchall()]

def get_fundamentals_revenue(conn, symbol_id):
    cur = conn.execute("""
        SELECT fetched_at, as_of, value FROM fundamentals
        WHERE symbol_id = ? AND metric = 'Revenues' AND as_of > 0
        ORDER BY fetched_at
    """, (symbol_id,))
    return [(epoch_to_date(r[0]), epoch_to_date(r[1]), r[2]) for r in cur.fetchall()]

def get_fundamentals_shares(conn, symbol_id):
    cur = conn.execute("""
        SELECT fetched_at, as_of, value FROM fundamentals
        WHERE symbol_id = ? AND metric = 'SharesOutstanding' AND as_of > 0
        ORDER BY fetched_at
    """, (symbol_id,))
    return [(epoch_to_date(r[0]), epoch_to_date(r[1]), r[2]) for r in cur.fetchall()]

def get_13f_holdings(conn, symbol_id):
    cur = conn.execute("""
        SELECT period, SUM(shares) as total_shares FROM inst_holdings
        WHERE symbol_id = ?
        GROUP BY period
        ORDER BY period
    """, (symbol_id,))
    return [(str_to_date(r[0]), r[1]) for r in cur.fetchall()]

def get_news_sentiment(conn, symbol_id, start_date, end_date):
    cur = conn.execute("""
        SELECT day, mean_score FROM sentiment_features
        WHERE symbol_id = ? AND day >= ? AND day <= ?
        ORDER BY day
    """, (symbol_id, start_date.isoformat(), end_date.isoformat()))
    return [(str_to_date(r[0]), r[1]) for r in cur.fetchall()]

def compute_revenue_acceleration(rev_data, decision_date):
    quarterly = {}
    for fetched, as_of, val in rev_data:
        if fetched > decision_date:
            break
        if as_of not in quarterly or fetched > quarterly[as_of][0]:
            quarterly[as_of] = (fetched, val)
    sorted_quarters = sorted(quarterly.items())
    if len(sorted_quarters) < 4:
        return False
    growth_rates = []
    for i in range(1, len(sorted_quarters)):
        prev_val = sorted_quarters[i-1][1][1]
        curr_val = sorted_quarters[i][1][1]
        if prev_val > 0:
            growth_rates.append((curr_val - prev_val) / prev_val)
    if len(growth_rates) < 3:
        return False
    return all(growth_rates[i] > growth_rates[i-1] for i in range(1, len(growth_rates)))

def compute_institutional_decline(holdings_data, decision_date):
    lagged = [(period, shares) for period, shares in holdings_data if period + timedelta(days=45) <= decision_date]
    if len(lagged) < 3:
        return False
    return all(lagged[i][1] < lagged[i-1][1] for i in range(1, len(lagged)))

def compute_news_sentiment_avg(news_data, decision_date, window=5):
    relevant = [score for day, score in news_data if day <= decision_date]
    if len(relevant) < window:
        return None
    return sum(relevant[-window:]) / window

def compute_sma(bars, decision_date, window=200):
    relevant = [close for day, close, _ in bars if day <= decision_date]
    if len(relevant) < window:
        return None
    return sum(relevant[-window:]) / window

def get_forward_return(bars, decision_date, horizon=21):
    decision_idx = None
    for i, (day, _, _) in enumerate(bars):
        if day == decision_date:
            decision_idx = i
            break
        if day > decision_date:
            decision_idx = i - 1
            break
    if decision_idx is None or decision_idx < 0:
        return None
    if decision_idx + horizon >= len(bars):
        return None
    entry_price = bars[decision_idx][1]
    exit_price = bars[decision_idx + horizon][1]
    return (exit_price - entry_price) / entry_price

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    return 'CEO' in t or 'CFO' in t or 'CHIEF EXECUTIVE' in t or 'CHIEF FINANCIAL' in t

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    universe = get_universe(conn)
    if not universe:
        print("INSUFFICIENT=1")
        return

    all_decisions = []

    for symbol_id, symbol in universe.items():
        bars = get_bars(conn, symbol_id, datetime(2018, 7, 26).date(), datetime(2026, 8, 16).date())
        if len(bars) < 504:
            continue

        rev_data = get_fundamentals_revenue(conn, symbol_id)
        if len(rev_data) < 12:
            continue

        shares_data = get_fundamentals_shares(conn, symbol_id)
        if not shares_data:
            continue

        holdings = get_13f_holdings(conn, symbol_id)
        if len(holdings) < 8:
            continue

        news_data = get_news_sentiment(conn, symbol_id, datetime(2012, 4, 17).date(), datetime(2026, 8, 16).date())
        if len(news_data) < 20:
            continue

        insider_purchases = get_insider_purchases(conn, symbol_id, datetime(2018, 7, 26).date(), datetime(2026, 8, 16).date())
        if not insider_purchases:
            continue

        insider_sales = get_insider_sales(conn, symbol_id, datetime(2018, 7, 26).date(), datetime(2026, 8, 16).date())
        sales_set = set(insider_sales)

        for filed_date, tx_date, shares, price, value, insider, title in insider_purchases:
            if not is_officer(title):
                continue
            if value < 10000:
                continue
            if any(sale_date >= filed_date - timedelta(days=63) and sale_date < filed_date for sale_date in sales_set):
                continue

            sma200 = compute_sma(bars, filed_date, 200)
            if sma200 is None:
                continue
            current_price = next((close for day, close, _ in bars if day == filed_date), None)
            if current_price is None or current_price <= sma200:
                continue

            if not compute_revenue_acceleration(rev_data, filed_date):
                continue

            if not compute_institutional_decline(holdings, filed_date):
                continue

            news_avg = compute_news_sentiment_avg(news_data, filed_date, 5)
            if news_avg is None or news_avg > 0:
                continue

            vol_window = [vol for day, _, vol in bars if day <= filed_date]
            if len(vol_window) < 25:
                continue
            avg_vol = sum(vol_window[-20:]) / 20
            recent_vol = sum(vol_window[-5:]) / 5
            if recent_vol > 3 * avg_vol:
                continue

            fwd_ret = get_forward_return(bars, filed_date, 21)
            if fwd_ret is None:
                continue

            all_decisions.append({
                'symbol_id': symbol_id,
                'symbol': symbol,
                'date': filed_date,
                'fwd_return': fwd_ret,
                'hit': 1 if fwd_ret > 0 else 0
            })

    if not all_decisions:
        print("INSUFFICIENT=1")
        return

    all_decisions.sort(key=lambda x: x['date'])
    n = len(all_decisions)
    split_idx = int(n * 0.8)
    train = all_decisions[:split_idx]
    test = all_decisions[split_idx:]

    def compute_metrics(decisions, label):
        if not decisions:
            return
        issued = len(decisions)
        hits = sum(d['hit'] for d in decisions)
        precision = hits / issued
        base_rate = hits / issued
        distinct_days = len(set(d['date'] for d in decisions))
        design_effect = issued / distinct_days if distinct_days > 0 else 1
        effective_n = issued / design_effect if design_effect > 0 else 0
        print(f"{label}_ISSUED={issued}")
        print(f"{label}_PRECISION={precision:.4f}")
        print(f"{label}_BASE_RATE={base_rate:.4f}")
        print(f"{label}_DISTINCT_DAYS={distinct_days}")
        print(f"{label}_EFFECTIVE_N={effective_n:.2f}")

    compute_metrics(train, "TRAIN")
    compute_metrics(test, "SEALED")

    print(f"ISSUED={len(test)}")
    print(f"OPPORTUNITIES={n}")
    test_hits = sum(d['hit'] for d in test)
    test_precision = test_hits / len(test) if test else 0
    test_base_rate = test_precision
    test_distinct = len(set(d['date'] for d in test))
    test_design = len(test) / test_distinct if test_distinct > 0 else 1
    test_eff_n = len(test) / test_design if test_design > 0 else 0
    print(f"PRECISION={test_precision:.4f}")
    print(f"BASE_RATE={test_base_rate:.4f}")
    print(f"DISTINCT_DAYS={test_distinct}")
    print(f"EFFECTIVE_N={test_eff_n:.2f}")
    print(f"SEALED_PRECISION={test_precision:.4f}")

if __name__ == '__main__':
    main()