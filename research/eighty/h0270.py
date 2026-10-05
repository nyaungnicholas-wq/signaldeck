import sqlite3
import sys
from collections import defaultdict, deque
from datetime import datetime, timezone
import bisect
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def utc_day(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def load_symbols(conn):
    cur = conn.execute("SELECT id, symbol FROM symbols WHERE active=1")
    return {row[0]: row[1] for row in cur.fetchall()}

def load_bars_1d(conn):
    cur = conn.execute("SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    by_symbol = defaultdict(list)
    for sym_id, ts, close, volume in cur.fetchall():
        by_symbol[sym_id].append((ts, close, volume))
    return by_symbol

def load_macro_series(conn, series_names):
    placeholders = ','.join('?' * len(series_names))
    cur = conn.execute(f"SELECT series, ts, value FROM macro_series WHERE series IN ({placeholders}) ORDER BY series, ts", series_names)
    by_series = defaultdict(list)
    for series, ts, value in cur.fetchall():
        by_series[series].append((ts, value))
    return by_series

def load_labels(conn):
    cur = conn.execute("SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=10")
    labels = {}
    for sym_id, ts, up in cur.fetchall():
        labels[(sym_id, ts)] = 1 if up else 0
    return labels

def get_macro_as_of(macro_data, series, as_of_ts):
    series_data = macro_data.get(series, [])
    if not series_data:
        return None
    lo, hi = 0, len(series_data)
    while lo < hi:
        mid = (lo + hi) // 2
        if series_data[mid][0] <= as_of_ts:
            lo = mid + 1
        else:
            hi = mid
    if lo == 0:
        return None
    return series_data[lo - 1][1]

def compute_daily_returns(closes):
    rets = []
    for i in range(1, len(closes)):
        if closes[i-1] > 0:
            rets.append(closes[i] / closes[i-1] - 1.0)
        else:
            rets.append(0.0)
    return rets

def rolling_std(values, window):
    if len(values) < window:
        return None
    window_vals = values[-window:]
    mean = sum(window_vals) / window
    var = sum((x - mean) ** 2 for x in window_vals) / window
    return math.sqrt(var * 252.0)

def percentile_of(sorted_list, value):
    if not sorted_list:
        return 0.5
    pos = bisect.bisect_left(sorted_list, value)
    return pos / len(sorted_list)

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.execute("PRAGMA query_only = ON")

    symbols = load_symbols(conn)
    bars_by_symbol = load_bars_1d(conn)
    macro = load_macro_series(conn, ['T10Y2Y', 'NAPM'])
    labels = load_labels(conn)

    spread_series = macro.get('T10Y2Y', [])
    ism_series = macro.get('NAPM', [])
    if not spread_series or not ism_series:
        print("INSUFFICIENT=1")
        return

    symbol_states = {}
    for sym_id, bars in bars_by_symbol.items():
        if len(bars) < 313:
            continue
        ts_list = [b[0] for b in bars]
        closes = [b[1] for b in bars]
        volumes = [b[2] for b in bars]
        daily_rets = compute_daily_returns(closes)
        symbol_states[sym_id] = {
            'ts': ts_list,
            'closes': closes,
            'volumes': volumes,
            'daily_rets': daily_rets,
            'sorted_ret60': [],
            'sorted_vol60': [],
            'last_call_idx': -1000,
        }

    all_decisions = []
    for sym_id, state in symbol_states.items():
        ts_list = state['ts']
        closes = state['closes']
        volumes = state['volumes']
        daily_rets = state['daily_rets']
        n = len(ts_list)
        sorted_ret60 = state['sorted_ret60']
        sorted_vol60 = state['sorted_vol60']

        for i in range(312, n - 1):
            T_ts = ts_list[i]
            T_close = closes[i]
            if T_close < 5.0:
                continue

            adv_window = volumes[i-60:i]
            close_window = closes[i-60:i]
            if len(adv_window) < 60:
                continue
            adv = sum(c * v for c, v in zip(close_window, adv_window)) / 60.0
            if adv < 5_000_000.0:
                continue

            ret60 = closes[i] / closes[i-60] - 1.0 if closes[i-60] > 0 else 0.0
            vol60 = rolling_std(daily_rets[i-60:i], 60)
            if vol60 is None:
                continue

            if len(sorted_ret60) < 252 or len(sorted_vol60) < 252:
                hist_ret60 = []
                hist_vol60 = []
                for j in range(i-252, i):
                    if j >= 60:
                        r = closes[j] / closes[j-60] - 1.0 if closes[j-60] > 0 else 0.0
                        hist_ret60.append(r)
                        v = rolling_std(daily_rets[j-60:j], 60)
                        if v is not None:
                            hist_vol60.append(v)
                sorted_ret60[:] = sorted(hist_ret60)
                sorted_vol60[:] = sorted(hist_vol60)
                if len(sorted_ret60) < 252 or len(sorted_vol60) < 252:
                    continue

            pct_ret = percentile_of(sorted_ret60, ret60)
            pct_vol = percentile_of(sorted_vol60, vol60)
            if pct_ret < 0.80 or pct_vol > 0.50:
                pass
            else:
                macro_ts = T_ts - 86400
                spread = get_macro_as_of(macro, 'T10Y2Y', macro_ts)
                ism = get_macro_as_of(macro, 'NAPM', macro_ts)
                if spread is not None and ism is not None and spread > 0 and ism > 50:
                    vol20 = rolling_std(daily_rets[i-20:i], 20)
                    all_decisions.append({
                        'sym_id': sym_id,
                        'ts': T_ts,
                        'day': utc_day(T_ts),
                        'ret60': ret60,
                        'vol60': vol60,
                        'vol20': vol20,
                        'pct_ret': pct_ret,
                        'pct_vol': pct_vol,
                        'spread': spread,
                        'ism': ism,
                        'close': T_close,
                        'adv': adv,
                    })

            new_ret60 = closes[i-1] / closes[i-61] - 1.0 if closes[i-61] > 0 else 0.0
            new_vol60 = rolling_std(daily_rets[i-61:i-1], 60)
            if new_vol60 is not None:
                bisect.insort(sorted_ret60, new_ret60)
                bisect.insort(sorted_vol60, new_vol60)
                if len(sorted_ret60) > 252:
                    old_ret60 = closes[i-253] / closes[i-313] - 1.0 if closes[i-313] > 0 else 0.0
                    old_vol60 = rolling_std(daily_rets[i-313:i-253], 60)
                    if old_vol60 is not None:
                        pos = bisect.bisect_left(sorted_ret60, old_ret60)
                        if pos < len(sorted_ret60) and abs(sorted_ret60[pos] - old_ret60) < 1e-12:
                            sorted_ret60.pop(pos)
                        pos = bisect.bisect_left(sorted_vol60, old_vol60)
                        if pos < len(sorted_vol60) and abs(sorted_vol60[pos] - old_vol60) < 1e-12:
                            sorted_vol60.pop(pos)

    if not all_decisions:
        print("INSUFFICIENT=1")
        return

    all_decisions.sort(key=lambda x: x['ts'])
    days = sorted(set(d['day'] for d in all_decisions))
    split_idx = int(len(days) * 0.8)
    sealed_days = set(days[split_idx:])

    decisions_by_day = defaultdict(list)
    for d in all_decisions:
        decisions_by_day[d['day']].append(d)

    for day, decs in decisions_by_day.items():
        vol20s = [d['vol20'] for d in decs if d['vol20'] is not None]
        if vol20s:
            vol20s.sort()
            threshold = vol20s[int(len(vol20s) * 0.9)]
            for d in decs:
                d['vol20_top_decile'] = d['vol20'] is not None and d['vol20'] > threshold
        else:
            for d in decs:
                d['vol20_top_decile'] = False

    issued = []
    last_call_day = {}
    for d in all_decisions:
        sym_id = d['sym_id']
        day = d['day']
        if d['vol20_top_decile']:
            continue
        if sym_id in last_call_day and (day - last_call_day[sym_id]).days <= 20:
            continue
        label = labels.get((sym_id, d['ts']))
        if label is None:
            continue
        issued.append({
            'sym_id': sym_id,
            'day': day,
            'ts': d['ts'],
            'label': label,
            'sealed': day in sealed_days,
        })
        last_call_day[sym_id] = day

    if len(issued) < 30:
        print("INSUFFICIENT=1")
        return

    total_opportunities = len(all_decisions)
    total_issued = len(issued)
    hits = sum(1 for c in issued if c['label'] == 1)
    precision = hits / total_issued if total_issued else 0.0
    base_rate = hits / total_issued if total_issued else 0.0
    distinct_days = len(set(c['day'] for c in issued))

    sealed_issued = [c for c in issued if c['sealed']]
    sealed_hits = sum(1 for c in sealed_issued if c['label'] == 1)
    sealed_precision = sealed_hits / len(sealed_issued) if sealed_issued else 0.0

    day_counts = defaultdict(int)
    for c in issued:
        day_counts[c['day']] += 1
    if day_counts:
        mean_calls = sum(day_counts.values()) / len(day_counts)
        var_calls = sum((v - mean_calls) ** 2 for v in day_counts.values()) / len(day_counts)
        design_effect = 1.0 + var_calls / mean_calls if mean_calls > 0 else 1.0
    else:
        design_effect = 1.0
    effective_n = total_issued / design_effect if design_effect > 0 else total_issued

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()