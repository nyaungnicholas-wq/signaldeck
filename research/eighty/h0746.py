# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 745
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def utc_day(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    con = connect_ro()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Load CEO/CFO open-market purchases (code='P')
    cur.execute("""
        SELECT symbol_id, insider, title, shares, price, shares*price as value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (LOWER(title) LIKE '%ceo%' OR LOWER(title) LIKE '%chief executive%' 
               OR LOWER(title) LIKE '%cfo%' OR LOWER(title) LIKE '%chief financial%')
        ORDER BY tx_ts
    """)
    purchases = [dict(r) for r in cur.fetchall()]
    if not purchases:
        print("INSUFFICIENT=1")
        return 0

    # 2. Get unique symbols involved
    symbols = sorted({p['symbol_id'] for p in purchases})
    sym_placeholders = ','.join('?' for _ in symbols)

    # 3. Load daily bars for these symbols (tf='1d')
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({sym_placeholders})
        ORDER BY symbol_id, ts
    """, symbols)
    bars_by_sym = defaultdict(list)
    for r in cur.fetchall():
        bars_by_sym[r['symbol_id']].append((r['ts'], r['close'], r['volume']))

    # 4. Load sentiment_features (news volume n_all) for these symbols
    cur.execute(f"""
        SELECT symbol_id, day, n_all
        FROM sentiment_features
        WHERE symbol_id IN ({sym_placeholders})
        ORDER BY symbol_id, day
    """, symbols)
    news_by_sym = defaultdict(list)
    for r in cur.fetchall():
        # day is 'YYYY-MM-DD'
        news_by_sym[r['symbol_id']].append((r['day'], r['n_all']))

    # Precompute rolling features for each symbol
    # For bars: 126-session trailing return, 126-session avg volume, 21-session forward return
    # For news: 63-session trailing avg news volume, and daily cross-sectional rank

    # Build date-indexed structures for fast lookup
    bar_idx = {}
    for sym, rows in bars_by_sym.items():
        # rows sorted by ts
        ts_list = [r[0] for r in rows]
        close_list = [r[1] for r in rows]
        vol_list = [r[2] for r in rows]
        bar_idx[sym] = {
            'ts': ts_list,
            'close': close_list,
            'vol': vol_list,
            'ts_to_idx': {ts: i for i, ts in enumerate(ts_list)}
        }

    news_idx = {}
    for sym, rows in news_by_sym.items():
        # rows sorted by day
        day_list = [r[0] for r in rows]
        nall_list = [r[1] for r in rows]
        news_idx[sym] = {
            'day': day_list,
            'nall': nall_list,
            'day_to_idx': {day: i for i, day in enumerate(day_list)}
        }

    # For cross-sectional news volume rank, we need all symbols' news volume per day
    # Build day -> list of (sym, n_all)
    day_to_news = defaultdict(list)
    for sym, data in news_idx.items():
        for day, nall in zip(data['day'], data['nall']):
            day_to_news[day].append((sym, nall))

    # Compute daily cross-sectional quartile threshold (bottom 25%)
    day_news_q25 = {}
    for day, lst in day_to_news.items():
        if len(lst) >= 4:
            vals = sorted([v for _, v in lst])
            qidx = max(0, len(vals) // 4 - 1)
            day_news_q25[day] = vals[qidx]
        else:
            day_news_q25[day] = None

    # 5. Evaluate each purchase
    calls = []  # (tx_ts, symbol_id, forward_return, hit)
    insider_hist = defaultdict(list)  # insider -> list of prior purchase values

    for p in purchases:
        sym = p['symbol_id']
        tx_ts = p['tx_ts']
        insider = p['insider']
        value = p['value']

        # Personal historical conviction: top quartile of this insider's prior purchases
        prior_vals = insider_hist[insider]
        if len(prior_vals) < 4:
            # Not enough history to define quartile
            insider_hist[insider].append(value)
            continue
        prior_sorted = sorted(prior_vals)
        q75 = prior_sorted[len(prior_sorted) * 3 // 4]
        if value < q75:
            insider_hist[insider].append(value)
            continue

        # Bar data availability
        if sym not in bar_idx:
            insider_hist[insider].append(value)
            continue
        b = bar_idx[sym]
        if tx_ts not in b['ts_to_idx']:
            insider_hist[insider].append(value)
            continue
        idx = b['ts_to_idx'][tx_ts]

        # Need 126 sessions before for trailing stats
        if idx < 126:
            insider_hist[insider].append(value)
            continue

        # 126-session trailing return
        close_now = b['close'][idx]
        close_126 = b['close'][idx - 126]
        ret_126 = close_now / close_126 - 1.0
        if ret_126 >= -0.10:  # not down >10%
            insider_hist[insider].append(value)
            continue

        # 126-session avg volume
        vol_126 = b['vol'][idx - 126:idx]
        avg_vol_126 = sum(vol_126) / len(vol_126)
        vol_today = b['vol'][idx]
        if vol_today >= 0.8 * avg_vol_126:  # not low volume today
            insider_hist[insider].append(value)
            continue

        # News volume: need 63 sessions before, and cross-sectional rank bottom quartile
        trade_day = utc_day(tx_ts).isoformat()
        if sym not in news_idx:
            insider_hist[insider].append(value)
            continue
        n = news_idx[sym]
        if trade_day not in n['day_to_idx']:
            insider_hist[insider].append(value)
            continue
        nidx = n['day_to_idx'][trade_day]
        if nidx < 63:
            insider_hist[insider].append(value)
            continue
        nall_63 = n['nall'][nidx - 63:nidx]
        avg_nall_63 = sum(nall_63) / len(nall_63)
        q25 = day_news_q25.get(trade_day)
        if q25 is None or avg_nall_63 > q25:
            insider_hist[insider].append(value)
            continue

        # Forward 21-session return
        if idx + 21 >= len(b['close']):
            insider_hist[insider].append(value)
            continue
        close_fwd = b['close'][idx + 21]
        fwd_ret = close_fwd / close_now - 1.0
        hit = 1 if fwd_ret > 0 else 0

        calls.append((tx_ts, sym, fwd_ret, hit))
        insider_hist[insider].append(value)

    if not calls:
        print("INSUFFICIENT=1")
        return 0

    # 6. Deduplicate by (symbol, UTC day) - one observation per symbol-day
    calls.sort(key=lambda x: x[0])
    seen = set()
    deduped = []
    for tx_ts, sym, fwd_ret, hit in calls:
        day = utc_day(tx_ts)
        key = (sym, day)
        if key in seen:
            continue
        seen.add(key)
        deduped.append((tx_ts, sym, fwd_ret, hit))

    # 7. Split sealed era: most recent 20% of calls by time
    n = len(deduped)
    split_idx = int(n * 0.8)
    if split_idx == n:
        split_idx = n - 1
    main_calls = deduped[:split_idx]
    sealed_calls = deduped[split_idx:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c[3] for c in call_list)
        precision = hits / issued
        base_rate = precision  # within issued subset, predicted class is "up"
        distinct_days = len({utc_day(c[0]) for c in call_list})
        # Design effect: 1 + 2*sum autocorr of hit series (lag 1 to 10)
        hit_series = [c[3] for c in call_list]
        mean_hit = sum(hit_series) / issued
        var_hit = mean_hit * (1 - mean_hit)
        if var_hit == 0:
            deff = 1.0
        else:
            acf_sum = 0.0
            for lag in range(1, min(11, issued)):
                cov = sum((hit_series[i] - mean_hit) * (hit_series[i+lag] - mean_hit) for i in range(issued - lag)) / (issued - lag)
                acf_sum += cov / var_hit
            deff = 1 + 2 * acf_sum
            if deff < 1.0:
                deff = 1.0
        effective_n = issued / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    # 8. Output required lines
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(purchases)}")  # decision points considered (before filters)
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())