import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_quarter_end(date_str):
    """Convert YYYY-MM-DD to quarter end timestamp (unix epoch)."""
    dt = datetime.strptime(date_str, '%Y-%m-%d')
    month = dt.month
    if month <= 3:
        q_end = datetime(dt.year, 3, 31)
    elif month <= 6:
        q_end = datetime(dt.year, 6, 30)
    elif month <= 9:
        q_end = datetime(dt.year, 9, 30)
    else:
        q_end = datetime(dt.year, 12, 31)
    return int(q_end.timestamp())

def get_quarter_start_from_end(q_end_ts):
    dt = datetime.utcfromtimestamp(q_end_ts)
    if dt.month == 3:
        return int(datetime(dt.year, 1, 1).timestamp())
    elif dt.month == 6:
        return int(datetime(dt.year, 4, 1).timestamp())
    elif dt.month == 9:
        return int(datetime(dt.year, 7, 1).timestamp())
    else:
        return int(datetime(dt.year, 10, 1).timestamp())

def prev_quarter_end(q_end_ts):
    dt = datetime.utcfromtimestamp(q_end_ts)
    if dt.month == 3:
        return int(datetime(dt.year - 1, 12, 31).timestamp())
    elif dt.month == 6:
        return int(datetime(dt.year, 3, 31).timestamp())
    elif dt.month == 9:
        return int(datetime(dt.year, 6, 30).timestamp())
    else:
        return int(datetime(dt.year, 9, 30).timestamp())

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load quarterly revenue data with fetched_at for as-of discipline
    cur.execute("""
        SELECT symbol_id, value as revenue, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'Revenues'
        ORDER BY symbol_id, as_of
    """)
    rev_rows = cur.fetchall()
    if not rev_rows:
        print("INSUFFICIENT=1")
        return

    # Group by symbol_id, compute YoY growth per quarter (using fetched_at as knowable date)
    # We need at least 3 quarters of data per symbol to detect 2-quarter acceleration
    rev_by_symbol = {}
    for r in rev_rows:
        sym = r['symbol_id']
        rev_by_symbol.setdefault(sym, []).append({
            'revenue': r['revenue'],
            'as_of': r['as_of'],
            'fetched_at': r['fetched_at']
        })

    # 2. Load insider open-market purchases (code='P') and sales (code='S')
    cur.execute("""
        SELECT symbol_id, code, filed_ts
        FROM insider_trades
        WHERE code IN ('P', 'S')
        ORDER BY symbol_id, filed_ts
    """)
    insider_rows = cur.fetchall()
    insider_by_symbol = {}
    for r in insider_rows:
        insider_by_symbol.setdefault(r['symbol_id'], []).append({
            'code': r['code'],
            'filed_ts': r['filed_ts']
        })

    # 3. Load daily bars for price momentum (1d timeframe)
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bar_rows = cur.fetchall()
    bars_by_symbol = {}
    for r in bar_rows:
        bars_by_symbol.setdefault(r['symbol_id'], []).append({
            'ts': r['ts'],
            'close': r['close']
        })

    # 4. Load prediction outcomes for 21-day horizon (horizon=21)
    cur.execute("""
        SELECT symbol_id, horizon, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21
        ORDER BY symbol_id, ts
    """)
    label_rows = cur.fetchall()
    labels_by_symbol = {}
    for r in label_rows:
        labels_by_symbol.setdefault(r['symbol_id'], []).append({
            'ts': r['ts'],
            'up': r['up'],
            'fwd_return': r['fwd_return']
        })

    # 5. Get all symbols for market return calculation
    cur.execute("SELECT id FROM symbols WHERE active = 1")
    active_symbols = [r['id'] for r in cur.fetchall()]

    # Compute market return series (equal-weight average of all active symbols)
    # For each day, compute average return across symbols
    market_daily = {}
    for sym in active_symbols:
        if sym not in bars_by_symbol:
            continue
        bars = bars_by_symbol[sym]
        for i in range(1, len(bars)):
            day = bars[i]['ts']
            ret = (bars[i]['close'] - bars[i-1]['close']) / bars[i-1]['close']
            market_daily.setdefault(day, []).append(ret)
    market_ret = {day: sum(rets)/len(rets) for day, rets in market_daily.items()}

    # 6. Identify decision points
    # For each symbol, each quarter where we have revenue data:
    # - Compute YoY growth for this quarter and previous quarter
    # - Check if acceleration: current YoY > prev YoY for 2+ consecutive quarters
    # - Check net insider buying in the quarter (filed_ts within quarter, lagged 45 days for safety)
    # - Check 6-month price momentum vs market (negative)
    # - Get 21-day forward label

    decisions = []  # (decision_ts, symbol_id, horizon_ts, up, fwd_return)

    for sym, revs in rev_by_symbol.items():
        if sym not in bars_by_symbol or sym not in labels_by_symbol:
            continue
        if len(revs) < 3:
            continue

        bars = bars_by_symbol[sym]
        labels = labels_by_symbol[sym]
        insiders = insider_by_symbol.get(sym, [])

        # Sort by as_of (quarter end)
        revs_sorted = sorted(revs, key=lambda x: x['as_of'])

        # Compute YoY growth for each quarter (need same quarter previous year)
        # Map as_of to revenue
        rev_by_asof = {r['as_of']: r for r in revs_sorted}

        # For each quarter, find same quarter previous year
        yoy_growth = {}
        for r in revs_sorted:
            as_of = r['as_of']
            dt = datetime.utcfromtimestamp(as_of)
            # Previous year same quarter end
            if dt.month == 3:
                prev_as_of = int(datetime(dt.year - 1, 3, 31).timestamp())
            elif dt.month == 6:
                prev_as_of = int(datetime(dt.year - 1, 6, 30).timestamp())
            elif dt.month == 9:
                prev_as_of = int(datetime(dt.year - 1, 9, 30).timestamp())
            else:
                prev_as_of = int(datetime(dt.year - 1, 12, 31).timestamp())

            if prev_as_of in rev_by_asof:
                prev_rev = rev_by_asof[prev_as_of]['revenue']
                if prev_rev > 0:
                    growth = (r['revenue'] - prev_rev) / prev_rev
                    # Knowable at fetched_at of current quarter
                    yoy_growth[as_of] = {
                        'growth': growth,
                        'fetched_at': r['fetched_at'],
                        'revenue': r['revenue']
                    }

        # Need at least 3 quarters with YoY to detect 2-quarter acceleration
        yoy_quarters = sorted(yoy_growth.keys())
        if len(yoy_quarters) < 3:
            continue

        # Check for acceleration: growth increasing for 2+ consecutive quarters
        # i.e., yoy[q] > yoy[q-1] and yoy[q-1] > yoy[q-2]
        accel_quarters = []
        for i in range(2, len(yoy_quarters)):
            q2 = yoy_quarters[i]
            q1 = yoy_quarters[i-1]
            q0 = yoy_quarters[i-2]
            if (yoy_growth[q2]['growth'] > yoy_growth[q1]['growth'] and
                yoy_growth[q1]['growth'] > yoy_growth[q0]['growth']):
                # Acceleration detected at q2
                # Decision can be made at fetched_at of q2 (when we learn q2 revenue)
                decision_ts = yoy_growth[q2]['fetched_at']
                accel_quarters.append((q2, decision_ts))

        if not accel_quarters:
            continue

        # For each acceleration quarter, check conditions
        for q_end, decision_ts in accel_quarters:
            # 1. Net insider buying in the quarter (filed_ts within quarter, lagged)
            q_start = get_quarter_start_from_end(q_end)
            # Use filed_ts, but ensure we only know 45 days after quarter end for safety? 
            # Actually insider filed_ts is knowable at filed_ts. But quarter end is period.
            # We'll use filed_ts in [q_start, q_end] for trades in that quarter.
            net_insider = 0
            for ins in insiders:
                if q_start <= ins['filed_ts'] <= q_end:
                    if ins['code'] == 'P':
                        net_insider += 1
                    elif ins['code'] == 'S':
                        net_insider -= 1
            if net_insider <= 0:
                continue

            # 2. 6-month price momentum vs market (negative)
            # Find bar at decision_ts (or latest before)
            bar_ts_list = [b['ts'] for b in bars]
            idx = -1
            for i, bts in enumerate(bar_ts_list):
                if bts <= decision_ts:
                    idx = i
                else:
                    break
            if idx < 126:  # need ~126 trading days for 6 months
                continue
            price_now = bars[idx]['close']
            price_6m_ago = bars[idx - 126]['close']
            stock_ret_6m = (price_now - price_6m_ago) / price_6m_ago

            # Market return over same period
            # Find market return for the 6-month window
            market_rets = []
            for j in range(idx - 126 + 1, idx + 1):
                day = bars[j]['ts']
                if day in market_ret:
                    market_rets.append(market_ret[day])
            if len(market_rets) < 60:
                continue
            market_ret_6m = sum(market_rets) / len(market_rets) * 126  # approximate
            excess_ret = stock_ret_6m - market_ret_6m
            if excess_ret >= 0:  # negative momentum vs market
                continue

            # 3. Get 21-day forward label
            # Find label with ts >= decision_ts (next available)
            label = None
            for lbl in labels:
                if lbl['ts'] >= decision_ts:
                    label = lbl
                    break
            if not label:
                continue
            if label['ts'] - decision_ts > 21 * 86400:  # label too far in future
                continue

            decisions.append({
                'decision_ts': decision_ts,
                'symbol_id': sym,
                'horizon_ts': label['ts'],
                'up': label['up'],
                'fwd_return': label['fwd_return']
            })

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # 7. Hold out most recent 20% by decision_ts
    decisions.sort(key=lambda x: x['decision_ts'])
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    main_decisions = decisions[:-n_sealed]
    sealed_decisions = decisions[-n_sealed:]

    def compute_stats(dec_list):
        if not dec_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(dec_list)
        hits = sum(1 for d in dec_list if d['up'] == 1)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(d['decision_ts']).date() for d in dec_list))
        # Design effect: cluster by month
        month_counts = {}
        for d in dec_list:
            dt = datetime.utcfromtimestamp(d['decision_ts'])