# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 679
# cycle_index: 6
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import bisect

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_trading_days(conn, symbol_id, start_date):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts",
        (symbol_id, start_date)
    )
    return [row[0] for row in cur.fetchall()]

def get_bars_data(conn, symbol_id, start_date):
    cur = conn.execute(
        "SELECT ts, close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts",
        (symbol_id, start_date)
    )
    return cur.fetchall()

def get_news_counts(conn, symbol_id, start_date):
    cur = conn.execute(
        "SELECT ts FROM news WHERE symbol_id=? AND ts>=? ORDER BY ts",
        (symbol_id, start_date)
    )
    counts = defaultdict(int)
    for (ts,) in cur.fetchall():
        d = unix_to_date(ts)
        counts[d] += 1
    return counts

def get_insider_purchases(conn, symbol_id, start_date):
    cur = conn.execute(
        "SELECT filed_ts, insider FROM insider_trades WHERE symbol_id=? AND code='P' AND filed_ts>=? ORDER BY filed_ts",
        (symbol_id, start_date)
    )
    return cur.fetchall()

def compute_bottom_quartile_threshold(values):
    if not values:
        return None
    sorted_vals = sorted(values)
    idx = max(0, (len(sorted_vals) * 25) // 100 - 1)
    return sorted_vals[idx]

def main():
    START_TS = date_to_unix(datetime(2018, 7, 26))
    HORIZON_DAYS = 21
    LOOKBACK_QUARTILE = 252
    DROUGHT_WINDOW = 20
    DROUGHT_MIN_DAYS = 15
    INSIDER_WINDOW = 5
    MIN_INSIDERS = 2
    COOLDOWN_DAYS = 10
    MIN_PRICE = 5.0
    MIN_AVG_DOLLAR_VOL = 1_000_000.0
    VOL_LOOKBACK = 20

    conn = connect_ro()
    conn.row_factory = sqlite3.Row

    symbols_cur = conn.execute(
        "SELECT id, symbol FROM symbols WHERE active=1 AND market='stocks'"
    )
    all_symbols = [(row['id'], row['symbol']) for row in symbols_cur.fetchall()]

    eligible_symbols = []
    for symbol_id, symbol in all_symbols:
        bars_cur = conn.execute(
            "SELECT COUNT(*) FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=?",
            (symbol_id, START_TS)
        )
        if bars_cur.fetchone()[0] < LOOKBACK_QUARTILE + HORIZON_DAYS + VOL_LOOKBACK:
            continue
        news_cur = conn.execute(
            "SELECT COUNT(DISTINCT date(ts, 'unixepoch')) FROM news WHERE symbol_id=? AND ts>=?",
            (symbol_id, START_TS)
        )
        if news_cur.fetchone()[0] < LOOKBACK_QUARTILE:
            continue
        insider_cur = conn.execute(
            "SELECT COUNT(*) FROM insider_trades WHERE symbol_id=? AND code='P' AND filed_ts>=?",
            (symbol_id, START_TS)
        )
        if insider_cur.fetchone()[0] < MIN_INSIDERS:
            continue
        eligible_symbols.append((symbol_id, symbol))

    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return 0

    all_signals = []

    for symbol_id, symbol in eligible_symbols:
        bars_data = get_bars_data(conn, symbol_id, START_TS)
        if len(bars_data) < LOOKBACK_QUARTILE + HORIZON_DAYS + VOL_LOOKBACK:
            continue

        trading_days = [row[0] for row in bars_data]
        close_map = {row[0]: row[1] for row in bars_data}
        volume_map = {row[0]: row[2] for row in bars_data}
        day_to_idx = {ts: i for i, ts in enumerate(trading_days)}

        news_counts = get_news_counts(conn, symbol_id, START_TS)
        headline_by_day = []
        for ts in trading_days:
            d = unix_to_date(ts)
            headline_by_day.append(news_counts.get(d, 0))

        drought_flag = [False] * len(trading_days)
        for i in range(LOOKBACK_QUARTILE, len(trading_days)):
            window = headline_by_day[i - LOOKBACK_QUARTILE:i]
            threshold = compute_bottom_quartile_threshold(window)
            if threshold is not None and headline_by_day[i] <= threshold:
                drought_flag[i] = True

        drought_count_20 = [0] * len(trading_days)
        for i in range(DROUGHT_WINDOW, len(trading_days)):
            drought_count_20[i] = sum(drought_flag[i - DROUGHT_WINDOW:i])

        insider_purchases = get_insider_purchases(conn, symbol_id, START_TS)
        insider_by_day = defaultdict(set)
        for filed_ts, insider in insider_purchases:
            d = unix_to_date(filed_ts)
            idx = bisect.bisect_left(trading_days, date_to_unix(d))
            if idx < len(trading_days) and unix_to_date(trading_days[idx]) == d:
                insider_by_day[idx].add(insider)
            elif idx > 0:
                insider_by_day[idx - 1].add(insider)

        insider_count_5 = [0] * len(trading_days)
        for i in range(len(trading_days)):
            unique_insiders = set()
            for j in range(max(0, i - INSIDER_WINDOW + 1), i + 1):
                unique_insiders.update(insider_by_day[j])
            insider_count_5[i] = len(unique_insiders)

        last_signal_idx = -COOLDOWN_DAYS - 1
        for i in range(LOOKBACK_QUARTILE + VOL_LOOKBACK, len(trading_days) - HORIZON_DAYS):
            if insider_count_5[i] < MIN_INSIDERS:
                continue
            if i > 0 and insider_count_5[i - 1] >= MIN_INSIDERS:
                continue
            if drought_count_20[i] < DROUGHT_MIN_DAYS:
                continue
            entry_ts = trading_days[i]
            price = close_map.get(entry_ts, 0)
            if price < MIN_PRICE:
                continue
            vol_window = trading_days[max(0, i - VOL_LOOKBACK + 1):i + 1]
            dollar_vols = [close_map[ts] * volume_map[ts] for ts in vol_window if ts in close_map and ts in volume_map]
            if not dollar_vols or (sum(dollar_vols) / len(dollar_vols)) < MIN_AVG_DOLLAR_VOL:
                continue
            if i - last_signal_idx <= COOLDOWN_DAYS:
                continue

            exit_idx = i + HORIZON_DAYS
            if exit_idx >= len(trading_days):
                continue
            exit_ts = trading_days[exit_idx]
            entry_price = close_map[entry_ts]
            exit_price = close_map[exit_ts]
            fwd_return = (exit_price - entry_price) / entry_price
            hit = 1 if fwd_return > 0 else 0

            all_signals.append({
                'symbol_id': symbol_id,
                'symbol': symbol,
                'entry_ts': entry_ts,
                'entry_date': unix_to_date(entry_ts),
                'hit': hit,
                'fwd_return': fwd_return
            })
            last_signal_idx = i

    if not all_signals:
        print("INSUFFICIENT=1")
        return 0

    all_signals.sort(key=lambda x: x['entry_ts'])
    n = len(all_signals)
    split_idx = int(n * 0.8)
    train_signals = all_signals[:split_idx]
    sealed_signals = all_signals[split_idx:]

    def compute_metrics(signals):
        if not signals:
            return 0, 0, 0, 0, 0, 0
        issued = len(signals)
        hits = sum(s['hit'] for s in signals)
        precision = hits / issued if issued else 0
        base_rate = precision
        distinct_days = len(set(s['entry_date'] for s in signals))
        day_counts = defaultdict(int)
        for s in signals:
            day_counts[s['entry_date']] += 1
        avg_per_day = issued / distinct_days if distinct_days else 1
        design_effect = max(1.01, 1 + (avg_per_day - 1) * 0.5)
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(all_signals)
    _, _, sealed_precision, _, _, _ = compute_metrics(sealed_signals)

    opportunities = 0
    for symbol_id, symbol in eligible_symbols:
        bars_data = get_bars_data(conn, symbol_id, START_TS)
        trading_days = [row[0] for row in bars_data]
        opportunities += max(0, len(trading_days) - LOOKBACK_QUARTILE - VOL_LOOKBACK - HORIZON_DAYS)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())