# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 758
# cycle_index: 28
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def compute_realized_vol(closes, window):
    if len(closes) < window + 1:
        return None
    rets = [math.log(closes[i] / closes[i-1]) for i in range(-window, 0)]
    return math.sqrt(sum(r*r for r in rets) / window) * math.sqrt(252)

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get active stock symbols
    cur.execute("SELECT id, symbol FROM symbols WHERE market = 'stocks' AND active = 1")
    symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = list(symbols.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # Fetch daily bars with volume for all symbols
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)

    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

    # Filter symbols with >=252 bars and avg dollar volume >= $1M
    valid_symbols = set()
    for sid, bars in bars_by_symbol.items():
        if len(bars) >= 252:
            recent = bars[-252:]
            avg_dollar_vol = sum(c * v for _, c, v in recent) / len(recent)
            if avg_dollar_vol >= 1_000_000:
                valid_symbols.add(sid)

    if not valid_symbols:
        print("INSUFFICIENT=1")
        return 0

    # Fetch officer open-market purchases (code='P', value>=50000)
    valid_list = list(valid_symbols)
    placeholders = ','.join('?' * len(valid_list))
    cur.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
          AND code = 'P'
          AND value >= 50000
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY tx_ts
    """, valid_list)

    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Build per-symbol bar data for fast lookup
    bars_data = {}
    for sid in valid_symbols:
        bars = bars_by_symbol[sid]
        dates = [ts_to_date(ts) for ts, _, _ in bars]
        closes = [c for _, c, _ in bars]
        volumes = [v for _, _, v in bars]
        ts_list = [ts for ts, _, _ in bars]
        bars_data[sid] = {
            'dates': dates,
            'closes': closes,
            'volumes': volumes,
            'ts_list': ts_list,
            'date_to_idx': {d: i for i, d in enumerate(dates)}
        }

    # Build per-insider historical purchase values for percentile calculation
    insider_purchases = defaultdict(list)
    for trade in trades:
        key = (trade['symbol_id'], trade['insider'])
        insider_purchases[key].append(trade['value'])

    # Compute 90th percentile per insider (using only trades BEFORE current trade)
    # We'll compute dynamically during evaluation

    # Evaluate each trade at its tx_ts (decision time)
    calls = []  # (decision_date, symbol_id, tx_ts, forward_return_21d)
    
    for trade in trades:
        sid = trade['symbol_id']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']
        value = trade['value']
        insider = trade['insider']

        # Condition 1: trade-to-filing delay <= 1 day (86400 seconds)
        if filed_ts - tx_ts > 86400:
            continue

        # Condition 2: purchase value > insider's 90th percentile historical purchase value
        # Use only prior trades for this insider at this symbol
        key = (sid, insider)
        prior_values = [v for v in insider_purchases[key] if v < value]  # strictly prior by value? No, by time.
        # Need to filter by time. Let's get prior trades for this insider.
        # Since trades are ordered by tx_ts, we can track as we go. But simpler: recompute.
        pass

    # Re-evaluate with proper temporal ordering
    # Group trades by (symbol_id, insider) and sort by tx_ts
    trades_by_insider = defaultdict(list)
    for trade in trades:
        key = (trade['symbol_id'], trade['insider'])
        trades_by_insider[key].append(trade)

    for key, insider_trades_list in trades_by_insider.items():
        insider_trades_list.sort(key=lambda t: t['tx_ts'])
        historical_values = []
        for trade in insider_trades_list:
            sid = trade['symbol_id']
            tx_ts = trade['tx_ts']
            filed_ts = trade['filed_ts']
            value = trade['value']

            # Condition 1: delay <= 1 day
            if filed_ts - tx_ts > 86400:
                historical_values.append(value)
                continue

            # Condition 2: need >=5 historical purchases to compute percentile
            if len(historical_values) < 5:
                historical_values.append(value)
                continue

            p90 = sorted(historical_values)[int(0.9 * len(historical_values))]
            if value <= p90:
                historical_values.append(value)
                continue

            # Condition 3: 20-day realized vol < 25th percentile of 252-day vol history
            bd = bars_data.get(sid)
            if not bd:
                historical_values.append(value)
                continue

            decision_date = ts_to_date(tx_ts)
            idx = bd['date_to_idx'].get(decision_date)
            if idx is None or idx < 252:
                historical_values.append(value)
                continue

            # Compute 20-day realized vol ending at decision_date (using bars up to decision_date)
            # Need 21 closes for 20 returns
            recent_closes = bd['closes'][idx-20:idx+1]
            if len(recent_closes) < 21:
                historical_values.append(value)
                continue
            vol_20d = compute_realized_vol(recent_closes, 20)

            # Compute 252-day vol history (rolling 20-day vols) up to decision_date
            vol_history = []
            for i in range(252, idx + 1):
                window_closes = bd['closes'][i-20:i+1]
                if len(window_closes) == 21:
                    v = compute_realized_vol(window_closes, 20)
                    if v is not None:
                        vol_history.append(v)
            
            if len(vol_history) < 50:  # need sufficient history
                historical_values.append(value)
                continue

            vol_25th = sorted(vol_history)[int(0.25 * len(vol_history))]
            if vol_20d >= vol_25th:
                historical_values.append(value)
                continue

            # All conditions met - compute forward 21-day return from bars
            # Need 21 trading days after decision_date
            if idx + 21 >= len(bd['closes']):
                historical_values.append(value)
                continue

            entry_close = bd['closes'][idx]
            exit_close = bd['closes'][idx + 21]
            fwd_return = (exit_close - entry_close) / entry_close

            calls.append((decision_date, sid, tx_ts, fwd_return))
            historical_values.append(value)

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # Sort calls by decision timestamp
    calls.sort(key=lambda x: x[2])

    # Hold out most recent 20% as sealed era
    split_idx = int(len(calls) * 0.8)
    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0, 0, 0
        issued = len(call_list)
        hits = sum(1 for _, _, _, ret in call_list if ret > 0)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate of positive class within issued
        distinct_days = len(set(d for d, _, _, _ in call_list))
        # Design effect: cluster by day, compute variance inflation
        day_counts = defaultdict(int)
        for d, _, _, _ in call_list:
            day_counts[d] += 1
        if len(day_counts) > 1:
            mean_c = issued / len(day_counts)
            var_c = sum((c - mean_c) ** 2 for c in day_counts.values()) / len(day_counts)
            deff = 1 + (var_c / mean_c) if mean_c > 0 else 1
        else:
            deff = 1
        effective_n = issued / deff if deff > 0 else issued
        return issued, precision, base_rate, distinct_days, effective_n

    issued_main, prec_main, base_main, days_main, eff_main = compute_metrics(main_calls)
    issued_sealed, prec_sealed, base_sealed, days_sealed, eff_sealed = compute_metrics(sealed_calls)

    # Total opportunities: count of (symbol, day) where symbol in valid_symbols and day in range
    # For simplicity, count decision points considered = number of officer trades evaluated
    opportunities = len(trades)

    print(f"ISSUED={issued_main + issued_sealed}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={(prec_main * issued_main + prec_sealed * issued_sealed) / (issued_main + issued_sealed) if (issued_main + issued_sealed) > 0 else 0:.6f}")
    print(f"BASE_RATE={(base_main * issued_main + base_sealed * issued_sealed) / (issued_main + issued_sealed) if (issued_main + issued_sealed) > 0 else 0:.6f}")
    print(f"DISTINCT_DAYS={days_main + days_sealed}")
    print(f"EFFECTIVE_N={eff_main + eff_sealed:.2f}")
    print(f"SEALED_PRECISION={prec_sealed:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())