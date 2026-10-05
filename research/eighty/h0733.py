# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 732
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def utc_date(ts):
    """Convert Unix epoch to UTC date string, handling pre-1970 dates on Windows."""
    if ts >= 0:
        return datetime.fromtimestamp(ts, tz=timezone.utc).strftime('%Y-%m-%d')
    # For negative timestamps (pre-1970), use manual calculation
    days = ts // 86400
    if ts % 86400 != 0 and ts < 0:
        days -= 1
    base = datetime(1970, 1, 1, tzinfo=timezone.utc)
    from datetime import timedelta
    target = base + timedelta(days=days)
    return target.strftime('%Y-%m-%d')

def main():
    con = sqlite3.connect(DB_PATH, uri=True)
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Load DGS10 macro series
    cur.execute("SELECT ts, value FROM macro_series WHERE series='DGS10' ORDER BY ts")
    macro_rows = cur.fetchall()
    if not macro_rows:
        print("INSUFFICIENT=1")
        return 0

    macro_dates = []
    macro_vals = []
    for r in macro_rows:
        try:
            macro_dates.append(utc_date(r['ts']))
            macro_vals.append(r['value'])
        except (OSError, ValueError, OverflowError):
            continue

    if len(macro_vals) < 253:
        print("INSUFFICIENT=1")
        return 0

    # 252-day moving average
    ma252 = {}
    window = 252
    for i in range(len(macro_vals)):
        if i >= window:
            ma = sum(macro_vals[i-window:i]) / window
            ma252[macro_dates[i]] = ma
        else:
            ma252[macro_dates[i]] = None

    # 2. Load daily bars (tf='1d') grouped by symbol
    cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf='1d' ORDER BY symbol_id, ts")
    bars_rows = cur.fetchall()
    if not bars_rows:
        print("INSUFFICIENT=1")
        return 0

    bars_by_symbol = defaultdict(list)
    for r in bars_rows:
        bars_by_symbol[r['symbol_id']].append((r['ts'], r['close']))

    # Precompute 252-day return and close lookup for each symbol
    ret252_by_symbol = {}
    closes_by_symbol = {}
    dates_by_symbol = {}
    for sym, series in bars_by_symbol.items():
        dates = [utc_date(ts) for ts, _ in series]
        closes = [c for _, c in series]
        ret252 = {}
        for i in range(window, len(closes)):
            ret = closes[i] / closes[i - window] - 1.0
            ret252[dates[i]] = ret
        ret252_by_symbol[sym] = ret252
        closes_by_symbol[sym] = closes
        dates_by_symbol[sym] = dates

    # 3. Load CEO/CFO open-market purchases
    cur.execute("""
        SELECT symbol_id, filed_ts, title
        FROM insider_trades
        WHERE code='P'
          AND (UPPER(title) LIKE '%CEO%' 
               OR UPPER(title) LIKE '%CFO%' 
               OR UPPER(title) LIKE '%CHIEF EXECUTIVE%' 
               OR UPPER(title) LIKE '%CHIEF FINANCIAL%')
        ORDER BY filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # 4. Evaluate each trade
    events = []  # (symbol_id, date_str, up, fwd_ret)
    for r in trades:
        sym = r['symbol_id']
        fdate = utc_date(r['filed_ts'])

        # Macro condition: DGS10 < MA252 on filing date
        ma = ma252.get(fdate)
        if ma is None:
            continue
        # Find DGS10 value on or before filing date
        dgs10_val = None
        for d, v in zip(macro_dates, macro_vals):
            if d == fdate:
                dgs10_val = v
                break
            elif d > fdate:
                break
        if dgs10_val is None:
            continue
        if dgs10_val >= ma:
            continue

        # Stock condition: 252-day return negative
        ret252 = ret252_by_symbol.get(sym, {}).get(fdate)
        if ret252 is None or ret252 >= 0:
            continue

        # Forward 21-day return from bars
        closes = closes_by_symbol.get(sym, [])
        dates = dates_by_symbol.get(sym, [])
        if fdate not in dates:
            continue
        try:
            idx = dates.index(fdate)
        except ValueError:
            continue
        if idx + 21 >= len(closes):
            continue
        entry_px = closes[idx]
        exit_px = closes[idx + 21]
        fwd_ret = exit_px / entry_px - 1.0
        up = 1 if fwd_ret > 0 else 0
        events.append((sym, fdate, up, fwd_ret))

    if not events:
        print("INSUFFICIENT=1")
        return 0

    # 5. Deduplicate by (symbol, date) - one observation per symbol-day
    seen = set()
    deduped = []
    for sym, d, up, fr in events:
        key = (sym, d)
        if key not in seen:
            seen.add(key)
            deduped.append((sym, d, up, fr))

    # 6. Sort by date, split 80/20 sealed
    deduped.sort(key=lambda x: x[1])
    n = len(deduped)
    split = int(n * 0.8)
    in_sample = deduped[:split]
    sealed = deduped[split:]

    def compute_metrics(evts):
        if not evts:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(evts)
        hits = sum(1 for _, _, up, _ in evts if up)
        precision = hits / issued
        base_rate = hits / issued  # base rate within issued subset
        distinct_days = len(set(d for _, d, _, _ in evts))
        # Design effect from day-level clustering
        day_counts = defaultdict(int)
        for _, d, _, _ in evts:
            day_counts[d] += 1
        cluster_sizes = list(day_counts.values())
        mean_c = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
        design_effect = 1.0
        if len(cluster_sizes) > 1 and mean_c > 0:
            var_c = sum((c - mean_c)**2 for c in cluster_sizes) / len(cluster_sizes)
            cv2 = var_c / (mean_c**2)
            design_effect = 1 + cv2 * (mean_c - 1)
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(in_sample)
    _, _, sealed_precision, _, _, _ = compute_metrics(sealed)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={n}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    return 0

if __name__ == '__main__':
    sys.exit(main())