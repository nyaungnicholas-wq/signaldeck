# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 759
# cycle_index: 29
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

OFFICER_KEYWORDS = ('CEO', 'CFO', 'PRESIDENT', 'OFFICER', 'CHIEF', 'EXECUTIVE', 'VP', 'VICE PRESIDENT', 'TREASURER', 'CONTROLLER', 'SECRETARY', 'DIRECTOR')
MIN_PURCHASE_VALUE = 10000
MAX_DISCLOSURE_DELAY_DAYS = 3
VIX_SERIES = 'VIXCLS'
VIX_MA_WINDOW = 200
STOCK_VOL_WINDOW = 20
STOCK_VOL_MEDIAN_WINDOW = 252
FORWARD_HORIZON_DAYS = 21
MIN_BARS_FOR_VOL = 200
SEALED_FRACTION = 0.2
MIN_ISSUED = 30

def is_officer(title: str) -> bool:
    if not title:
        return False
    t = title.upper()
    return any(kw in t for kw in OFFICER_KEYWORDS)

def ts_to_date(ts: int) -> datetime:
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d: datetime) -> int:
    return int(datetime(d.year, d.month, d.day).timestamp())

def compute_rolling_ma(values, window):
    """Simple moving average, returns list aligned with input (None for first window-1)."""
    n = len(values)
    out = [None] * n
    s = 0.0
    for i, v in enumerate(values):
        s += v
        if i >= window:
            s -= values[i - window]
        if i >= window - 1:
            out[i] = s / window
    return out

def compute_rolling_std(values, window):
    """Rolling standard deviation of values (not returns). Returns list aligned with input."""
    n = len(values)
    out = [None] * n
    for i in range(window - 1, n):
        window_vals = values[i - window + 1:i + 1]
        mean = sum(window_vals) / window
        var = sum((x - mean) ** 2 for x in window_vals) / window
        out[i] = var ** 0.5
    return out

def compute_rolling_median(values, window):
    """Rolling median of values. Returns list aligned with input."""
    n = len(values)
    out = [None] * n
    for i in range(window - 1, n):
        window_vals = sorted(values[i - window + 1:i + 1])
        mid = window // 2
        if window % 2 == 0:
            out[i] = (window_vals[mid - 1] + window_vals[mid]) / 2
        else:
            out[i] = window_vals[mid]
    return out

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Load candidate insider trades (code='P', officer, value>=10K, delay<=3d)
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND value >= ?
          AND filed_ts > tx_ts
          AND (filed_ts - tx_ts) <= ? * 86400
          AND tx_ts >= ?  -- ensure enough bar history for 252-day vol median
    """, (MIN_PURCHASE_VALUE, MAX_DISCLOSURE_DELAY_DAYS, 1564617600))  # 2019-08-01 approx (252 trading days after bars start)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # Filter officers
    officer_trades = [t for t in trades if is_officer(t['title'])]
    if not officer_trades:
        print("INSUFFICIENT=1")
        return

    # 2. Get unique tx_dates for VIX query
    tx_dates = sorted(set(ts_to_date(t['tx_ts']) for t in officer_trades))
    min_tx_date = min(tx_dates)
    max_tx_date = max(tx_dates)

    # Need VIX data from (min_tx_date - VIX_MA_WINDOW trading days) to max_tx_date
    # Approximate: VIX_MA_WINDOW * 1.4 calendar days
    vix_start_date = min_tx_date - timedelta(days=int(VIX_MA_WINDOW * 1.5))
    vix_start_ts = date_to_ts(vix_start_date)
    vix_end_ts = date_to_ts(max_tx_date + timedelta(days=1))

    cur.execute("""
        SELECT ts, value FROM macro_series
        WHERE series = ? AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (VIX_SERIES, vix_start_ts, vix_end_ts))
    vix_rows = cur.fetchall()
    if len(vix_rows) < VIX_MA_WINDOW:
        print("INSUFFICIENT=1")
        return

    vix_ts = [r['ts'] for r in vix_rows]
    vix_vals = [r['value'] for r in vix_rows]
    vix_ma = compute_rolling_ma(vix_vals, VIX_MA_WINDOW)
    vix_map = {ts: (val, ma) for ts, val, ma in zip(vix_ts, vix_vals, vix_ma) if ma is not None}

    # 3. For each trade, check VIX condition at tx_ts
    # Also need stock vol condition - will query bars per symbol
    signals = []  # (symbol_id, filed_ts, filed_date, tx_ts)
    for t in officer_trades:
        tx_ts = t['tx_ts']
        filed_ts = t['filed_ts']
        symbol_id = t['symbol_id']

        # VIX check at tx_ts (find closest VIX ts <= tx_ts)
        vix_key = max((k for k in vix_map if k <= tx_ts), default=None)
        if vix_key is None:
            continue
        vix_val, vix_ma_val = vix_map[vix_key]
        if vix_val >= vix_ma_val:
            continue  # VIX not below MA

        # Stock vol check: need bars for this symbol around tx_ts
        bars_start_ts = tx_ts - STOCK_VOL_MEDIAN_WINDOW * 86400 * 2  # generous calendar buffer
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
            ORDER BY ts
        """, (symbol_id, bars_start_ts, tx_ts))
        bars = cur.fetchall()
        if len(bars) < MIN_BARS_FOR_VOL:
            continue

        bar_ts = [b['ts'] for b in bars]
        bar_close = [b['close'] for b in bars]

        # Compute daily returns
        returns = []
        for i in range(1, len(bar_close)):
            ret = (bar_close[i] - bar_close[i-1]) / bar_close[i-1]
            returns.append(ret)

        if len(returns) < STOCK_VOL_WINDOW:
            continue

        # 20-day realized vol (std of returns)
        vol_20d = compute_rolling_std(returns, STOCK_VOL_WINDOW)
        # Align: vol_20d[i] corresponds to returns[i], which is bar_ts[i+1]
        # We need vol at tx_ts (last bar)
        if vol_20d[-1] is None:
            continue
        current_vol = vol_20d[-1]

        # 252-day median of 20-day vol
        # vol_20d has same length as returns; need median over last STOCK_VOL_MEDIAN_WINDOW vol points
        if len(vol_20d) < STOCK_VOL_MEDIAN_WINDOW:
            continue
        recent_vols = [v for v in vol_20d[-STOCK_VOL_MEDIAN_WINDOW:] if v is not None]
        if len(recent_vols) < STOCK_VOL_MEDIAN_WINDOW // 2:
            continue
        recent_vols.sort()
        mid = len(recent_vols) // 2
        median_vol = (recent_vols[mid - 1] + recent_vols[mid]) / 2 if len(recent_vols) % 2 == 0 else recent_vols[mid]

        if current_vol >= median_vol:
            continue  # vol not compressed

        # All conditions met
        signals.append((symbol_id, filed_ts, ts_to_date(filed_ts), tx_ts))

    if not signals:
        print("INSUFFICIENT=1")
        return

    # 4. Aggregate by (symbol_id, filed_date) - one observation per symbol per day
    signals.sort(key=lambda x: (x[0], x[1]))
    unique_signals = []
    seen = set()
    for sym, fts, fdate, tts in signals:
        key = (sym, fdate)
        if key not in seen:
            seen.add(key)
            unique_signals.append((sym, fts, fdate, tts))

    # 5. Compute forward 21-day returns for each signal
    labeled = []
    for sym, fts, fdate, tts in unique_signals:
        # Get bars from filed_date onward (next trading day)
        cur.execute("""
            SELECT ts, close FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
            ORDER BY ts
            LIMIT ?
        """, (sym, fts, FORWARD_HORIZON_DAYS + 1))
        bars = cur.fetchall()
        if len(bars) < FORWARD_HORIZON_DAYS + 1:
            continue
        entry_close = bars[0]['close']
        exit_close = bars[FORWARD_HORIZON_DAYS]['close']
        fwd_ret = (exit_close - entry_close) / entry_close
        label = 1 if fwd_ret > 0 else 0
        labeled.append((sym, fts, fdate, label))

    if not labeled:
        print("INSUFFICIENT=1")
        return

    # 6. Split by time: most recent 20% as sealed
    labeled.sort(key=lambda x: x[1])  # sort by filed_ts
    n = len(labeled)
    split_idx = int(n * (1 - SEALED_FRACTION))
    if split_idx < MIN_ISSUED or (n - split_idx) < 5:
        print("INSUFFICIENT=1")
        return

    train = labeled[:split_idx]
    sealed = labeled[split_idx:]

    # 7. Compute metrics
    def compute_metrics(data, name):
        if not data:
            return 0, 0, 0, 0
        issued = len(data)
        hits = sum(d[3] for d in data)
        precision = hits / issued
        base_rate = hits / issued  # base rate of predicted class (up=1) within issued
        distinct_days = len(set(d[2] for d in data))
        # Design effect: 1 + (avg_per_day - 1) * 0.5
        avg_per_day = issued / distinct_days if distinct_days > 0 else 1
        design_effect = 1 + (avg_per_day - 1) * 0.5
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_days, train_en = compute_metrics(train, 'train')
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_en = compute_metrics(sealed, 'sealed')

    total_issued = train_issued + sealed_issued
    total_hits = train_hits + sealed_hits
    total_precision = total_hits / total_issued if total_issued else 0
    total_base_rate = total_hits / total_issued if total_issued else 0
    total_distinct_days = len(set(d[2] for d in labeled))
    total_avg_per_day = total_issued / total_distinct_days if total_distinct_days else 1
    total_design_effect = 1 + (total_avg_per_day - 1) * 0.5
    total_effective_n = total_issued / total_design_effect

    # OPPORTUNITIES = number of unique (symbol, filed_date) from officer trades with basic filters
    # (before VIX/vol filters)
    cur.execute("""
        SELECT COUNT(DISTINCT symbol_id, date(filed_ts, 'unixepoch'))
        FROM insider_trades
        WHERE code = 'P'
          AND value >= ?
          AND filed_ts > tx_ts
          AND (filed_ts - tx_ts) <= ? * 86400
          AND tx_ts >= ?
          AND (
            title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%PRESIDENT%'
            OR title LIKE '%OFFICER%' OR title LIKE '%CHIEF%' OR title LIKE '%EXECUTIVE%'
            OR title LIKE '%VP%' OR title LIKE '%VICE PRESIDENT%' OR title LIKE '%TREASURER%'
            OR title LIKE '%CONTROLLER%' OR title LIKE '%SECRETARY%' OR title LIKE '%DIRECTOR%'
          )
    """, (MIN_PURCHASE_VALUE, MAX_DISCLOSURE_DELAY_DAYS, 1564617600))
    opportunities = cur.fetchone()[0] or 0

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={total_precision:.6f}")
    print(f"BASE_RATE={total_base_rate:.6f}")
    print(f"DISTINCT_DAYS={total_distinct_days}")
    print(f"EFFECTIVE_N={total_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    conn.close()

if __name__ == '__main__':
    main()