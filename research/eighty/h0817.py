# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 816
# cycle_index: 12
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

def business_days_between(start_ts, end_ts):
    start = datetime.fromtimestamp(start_ts).date()
    end = datetime.fromtimestamp(end_ts).date()
    if start > end:
        return 0
    days = 0
    cur = start
    while cur <= end:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days - 1

def get_trading_dates_bars(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT ts, open, high, low, close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    rows = cur.fetchall()
    return [(r[0], r[1], r[2], r[3], r[4], r[5]) for r in rows]

def get_news_sentiment(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT ts, score FROM news WHERE symbol_id=? AND ts>=? AND ts<=? AND score IS NOT NULL ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    rows = cur.fetchall()
    return [(r[0], r[1]) for r in rows]

def ts_to_date(ts):
    return datetime.fromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    cur = conn.execute("""
        SELECT accession, symbol_id, title, code, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%CHIEF EXECUTIVE%' OR title LIKE '%CHIEF FINANCIAL%')
        ORDER BY filed_ts
    """)
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return

    candidates = []
    for t in trades:
        lag = business_days_between(t['tx_ts'], t['filed_ts'])
        if lag <= 5:
            candidates.append({
                'accession': t['accession'],
                'symbol_id': t['symbol_id'],
                'tx_ts': t['tx_ts'],
                'filed_ts': t['filed_ts'],
                'trade_date': ts_to_date(t['tx_ts']),
                'file_date': ts_to_date(t['filed_ts'])
            })

    if not candidates:
        print("INSUFFICIENT=1")
        return

    by_symbol = defaultdict(list)
    for c in candidates:
        by_symbol[c['symbol_id']].append(c)

    all_decisions = []

    for sym_id, clist in by_symbol.items():
        clist.sort(key=lambda x: x['filed_ts'])
        earliest_trade = min(c['tx_ts'] for c in clist)
        latest_file = max(c['filed_ts'] for c in clist)

        bar_start = earliest_trade - 252 * 86400 * 2
        bar_end = latest_file + 21 * 86400 * 2
        bars = get_trading_dates_bars(conn, sym_id, bar_start, bar_end)
        if len(bars) < 252:
            continue
        bar_by_ts = {b[0]: b for b in bars}
        bar_dates = sorted(bar_by_ts.keys())

        news_start = earliest_trade - 252 * 86400 * 2
        news_end = latest_file
        news = get_news_sentiment(conn, sym_id, news_start, news_end)
        news_by_ts = defaultdict(list)
        for nt, score in news:
            news_by_ts[ts_to_date(nt)].append(score)

        daily_range = {}
        dollar_vol = {}
        close_px = {}
        for ts, o, h, l, c, v in bars:
            d = ts_to_date(ts)
            daily_range[d] = (h - l) / c if c else None
            dollar_vol[d] = c * v if c and v else None
            close_px[d] = c

        range_vals = [v for v in daily_range.values() if v is not None]
        if len(range_vals) < 252:
            continue

        sentiment_daily = {}
        for d, scores in news_by_ts.items():
            sentiment_daily[d] = sum(scores) / len(scores)

        for c in clist:
            td = c['trade_date']
            fd = c['file_date']

            if td not in daily_range or daily_range[td] is None:
                continue

            trailing_dates = [d for d in daily_range if d < td]
            if len(trailing_dates) < 252:
                continue
            trailing_dates.sort()
            trailing_252 = trailing_dates[-252:]
            range_252 = [daily_range[d] for d in trailing_252 if daily_range[d] is not None]
            if len(range_252) < 252:
                continue
            range_252.sort()
            p10 = range_252[int(0.10 * len(range_252))]
            if daily_range[td] > p10:
                continue

            sent_dates = [d for d in sentiment_daily if d <= fd]
            if len(sent_dates) < 20:
                continue
            sent_dates.sort()
            recent_20 = sent_dates[-20:]
            mean_sent_20 = sum(sentiment_daily[d] for d in recent_20) / 20

            sent_trailing = [d for d in sentiment_daily if d < fd]
            if len(sent_trailing) < 252:
                continue
            sent_trailing.sort()
            sent_252 = sent_trailing[-252:]
            sent_vals = [sentiment_daily[d] for d in sent_252]
            sent_vals.sort()
            p33 = sent_vals[int(0.33 * len(sent_vals))]
            if mean_sent_20 > p33:
                continue

            px_dates = [d for d in close_px if d <= td]
            if len(px_dates) < 63:
                continue
            px_dates.sort()
            if len(px_dates) < 64:
                continue
            px_63_ago = close_px[px_dates[-64]]
            px_now = close_px[td]
            if px_63_ago is None or px_now is None or px_63_ago == 0:
                continue
            ret_63 = (px_now - px_63_ago) / px_63_ago
            if ret_63 >= 0:
                continue

            vol_dates = [d for d in dollar_vol if d <= td and dollar_vol[d] is not None]
            if len(vol_dates) < 20:
                continue
            vol_dates.sort()
            recent_20_vol = vol_dates[-20:]
            avg_dvol = sum(dollar_vol[d] for d in recent_20_vol) / 20
            if avg_dvol < 1_000_000:
                continue

            all_decisions.append({
                'symbol_id': sym_id,
                'trade_date': td,
                'file_date': fd,
                'filed_ts': c['filed_ts'],
                'accession': c['accession']
            })

    if not all_decisions:
        print("INSUFFICIENT=1")
        return

    all_decisions.sort(key=lambda x: x['filed_ts'])
    n = len(all_decisions)
    split_idx = int(n * 0.8)
    main_decisions = all_decisions[:split_idx]
    sealed_decisions = all_decisions[split_idx:]

    def evaluate_era(decisions, label_suffix=""):
        issued = []
        for d in decisions:
            fd = d['file_date']
            sym = d['symbol_id']
            bars_fwd = get_trading_dates_bars(conn, sym, d['filed_ts'], d['filed_ts'] + 21 * 86400 * 3)
            if len(bars_fwd) < 22:
                continue
            bars_fwd.sort(key=lambda x: x[0])
            entry_px = bars_fwd[0][4]
            exit_px = bars_fwd[21][4] if len(bars_fwd) > 21 else bars_fwd[-1][4]
            if entry_px is None or exit_px is None or entry_px == 0:
                continue
            fwd_ret = (exit_px - entry_px) / entry_px
            up = 1 if fwd_ret > 0 else 0
            issued.append({
                'symbol_id': sym,
                'file_date': fd,
                'filed_ts': d['filed_ts'],
                'up': up
            })
        if not issued:
            return 0, 0, 0, 0, 0
        hits = sum(c['up'] for c in issued)
        precision = hits / len(issued)
        base_rate = precision
        distinct_days = len(set(c['file_date'] for c in issued))
        day_counts = defaultdict(int)
        for c in issued:
            day_counts[c['file_date']] += 1
        sum_sq = sum(v*v for v in day_counts.values())
        design_effect = sum_sq / len(issued) if len(issued) > 0 else 1
        if design_effect <= 1:
            design_effect = 1.01
        effective_n = len(issued) / design_effect
        return len(issued), hits, precision, base_rate, distinct_days, effective_n

    main_issued, main_hits, main_prec, main_br, main_dd, main_en = evaluate_era(main_decisions)
    sealed_issued, sealed_hits, sealed_prec, _, _, _ = evaluate_era(sealed_decisions)

    if main_issued == 0:
        print("INSUFFICIENT=1")
        return

    print(f"ISSUED={main_issued}")
    print(f"OPPORTUNITIES={n}")
    print(f"PRECISION={main_prec:.6f}")
    print(f"BASE_RATE={main_br:.6f}")
    print(f"DISTINCT_DAYS={main_dd}")
    print(f"EFFECTIVE_N={main_en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == "__main__":
    main()