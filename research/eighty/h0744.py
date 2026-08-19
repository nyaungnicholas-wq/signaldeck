# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 743
# cycle_index: 13
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date
from collections import defaultdict
import statistics

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.isoformat()

def compute_log_returns(closes):
    rets = []
    for i in range(1, len(closes)):
        if closes[i-1] > 0 and closes[i] > 0:
            rets.append((closes[i] / closes[i-1]) - 1)
        else:
            rets.append(0.0)
    return rets

def rolling_vol(rets, window):
    vols = [None] * len(rets)
    for i in range(window - 1, len(rets)):
        window_rets = rets[i - window + 1:i + 1]
        if len(window_rets) == window:
            mean = sum(window_rets) / window
            var = sum((r - mean) ** 2 for r in window_rets) / window
            vols[i] = var ** 0.5
    return vols

def rolling_percentile(values, window, p):
    result = [None] * len(values)
    for i in range(window - 1, len(values)):
        window_vals = [v for v in values[i - window + 1:i + 1] if v is not None]
        if len(window_vals) >= window * 0.8:
            result[i] = statistics.quantiles(window_vals, n=100)[p - 1]
    return result

def rolling_mean(values, window):
    result = [None] * len(values)
    for i in range(window - 1, len(values)):
        window_vals = [v for v in values[i - window + 1:i + 1] if v is not None]
        if len(window_vals) == window:
            result[i] = sum(window_vals) / window
    return result

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    cur.execute("SELECT symbol_id, filed_ts FROM insider_trades WHERE code = 'P' ORDER BY filed_ts")
    purchases = cur.fetchall()
    if not purchases:
        print("INSUFFICIENT=1")
        return 0

    symbol_purchases = defaultdict(list)
    for row in purchases:
        symbol_purchases[row['symbol_id']].append(row['filed_ts'])

    symbols = list(symbol_purchases.keys())
    placeholders = ','.join('?' * len(symbols))

    cur.execute(f"SELECT symbol_id, ts, close FROM bars WHERE symbol_id IN ({placeholders}) AND tf = '1d' ORDER BY symbol_id, ts", symbols)
    bars_rows = cur.fetchall()

    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    cur.execute(f"SELECT symbol_id, day, mean_score FROM sentiment_features WHERE symbol_id IN ({placeholders}) ORDER BY symbol_id, day", symbols)
    sent_rows = cur.fetchall()

    sent_by_symbol = defaultdict(list)
    for row in sent_rows:
        sent_by_symbol[row['symbol_id']].append((row['day'], row['mean_score']))

    all_opportunities = []
    all_issued = []

    for symbol_id in symbols:
        bars = bars_by_symbol.get(symbol_id, [])
        if len(bars) < 272:
            continue

        bar_dates = [ts_to_date(ts) for ts, _ in bars]
        closes = [c for _, c in bars]
        rets = compute_log_returns(closes)
        vol_20 = rolling_vol(rets, 20)
        vol_p10 = rolling_percentile(vol_20, 252, 10)

        sent_data = sent_by_symbol.get(symbol_id, [])
        sent_dict = {day: score for day, score in sent_data}
        sent_dates_sorted = sorted(sent_dict.keys())
        sent_scores = [sent_dict[d] for d in sent_dates_sorted]
        sent_20_avg = rolling_mean(sent_scores, 20)
        sent_avg_by_date = dict(zip(sent_dates_sorted, sent_20_avg))

        for filed_ts in symbol_purchases[symbol_id]:
            decision_date = ts_to_date(filed_ts)

            bar_idx = None
            for i, bd in enumerate(bar_dates):
                if bd <= decision_date:
                    bar_idx = i
                else:
                    break
            if bar_idx is None or bar_idx < 271:
                continue
            if bar_idx + 21 >= len(closes):
                continue

            v20 = vol_20[bar_idx]
            vp10 = vol_p10[bar_idx]
            if v20 is None or vp10 is None:
                continue

            sent_avg = sent_avg_by_date.get(decision_date)
            if sent_avg is None:
                continue

            all_opportunities.append((decision_date, symbol_id))

            if v20 <= vp10 and sent_avg > 0:
                fwd_ret = (closes[bar_idx + 21] / closes[bar_idx]) - 1
                label = 1 if fwd_ret > 0 else 0
                all_issued.append((decision_date, symbol_id, label))

    if not all_issued:
        print("INSUFFICIENT=1")
        return 0

    all_issued.sort(key=lambda x: x[0])
    n_issued = len(all_issued)
    n_sealed = max(1, n_issued // 5)
    training = all_issued[:-n_sealed]
    sealed = all_issued[-n_sealed:]

    def compute_metrics(calls):
        if not calls:
            return 0.0, 0.0, 0, 0.0
        issued_count = len(calls)
        hits = sum(c[2] for c in calls)
        precision = hits / issued_count
        base_rate = precision
        distinct_days = len(set(c[0] for c in calls))

        daily_hits = defaultdict(lambda: [0, 0])
        for d, _, label in calls:
            daily_hits[d][0] += label
            daily_hits[d][1] += 1
        daily_rates = [h/n for h, n in daily_hits.values() if n > 0]
        if len(daily_rates) > 1:
            mean_rate = sum(daily_rates) / len(daily_rates)
            var_between = sum((r - mean_rate) ** 2 for r in daily_rates) / len(daily_rates)
            var_within = sum(r * (1 - r) / n for (h, n), r in zip(daily_hits.values(), daily_rates) if n > 0) / len(daily_rates) if daily_rates else 0
            if var_between + var_within > 0:
                icc = var_between / (var_between + var_within)
            else:
                icc = 0.0
            avg_cluster = issued_count / len(daily_hits)
            deff = 1 + (avg_cluster - 1) * icc
        else:
            deff = 1.01
        deff = max(deff, 1.01)
        effective_n = issued_count / deff
        return precision, base_rate, distinct_days, effective_n

    train_prec, train_base, train_days, train_eff = compute_metrics(training)
    sealed_prec = sum(c[2] for c in sealed) / len(sealed) if sealed else 0.0

    print(f"ISSUED={len(training)}")
    print(f"OPPORTUNITIES={len(all_opportunities)}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_base:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())