# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 773
# cycle_index: 43
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict
import statistics
import bisect

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row

    trades = conn.execute("SELECT * FROM insider_trades").fetchall()

    ceo_cfo_pairs = set()
    for t in trades:
        title = (t['title'] or '').upper()
        if any(kw in title for kw in ['CEO', 'CFO', 'CHIEF EXECUTIVE', 'CHIEF FINANCIAL']):
            ceo_cfo_pairs.add((t['insider'], t['symbol_id']))

    pair_trades = defaultdict(list)
    for t in trades:
        key = (t['insider'], t['symbol_id'])
        if key in ceo_cfo_pairs:
            pair_trades[key].append(t)

    for key in pair_trades:
        pair_trades[key].sort(key=lambda x: x['filed_ts'])

    symbol_ids = list(set(sid for _, sid in ceo_cfo_pairs))
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return 0

    placeholders = ','.join('?' * len(symbol_ids))
    bars = conn.execute(
        f"SELECT symbol_id, ts, close FROM bars WHERE tf='1d' AND symbol_id IN ({placeholders})",
        symbol_ids
    ).fetchall()

    symbol_bars = defaultdict(list)
    for b in bars:
        symbol_bars[b['symbol_id']].append((b['ts'], b['close']))
    for sid in symbol_bars:
        symbol_bars[sid].sort(key=lambda x: x[0])

    symbol_trading_dates = {}
    symbol_date_to_idx = {}
    symbol_bars_by_date = {}
    for sid, bars_list in symbol_bars.items():
        d = {}
        dates = []
        for ts, close in bars_list:
            dt = datetime.fromtimestamp(ts, tz=timezone.utc).date()
            d[dt] = (ts, close)
            dates.append(dt)
        symbol_bars_by_date[sid] = d
        symbol_trading_dates[sid] = dates
        symbol_date_to_idx[sid] = {d: i for i, d in enumerate(dates)}

    sentiment_rows = conn.execute(
        f"SELECT symbol_id, day, mean_score FROM sentiment_features WHERE symbol_id IN ({placeholders})",
        symbol_ids
    ).fetchall()

    symbol_sentiment = defaultdict(list)
    for s in sentiment_rows:
        symbol_sentiment[s['symbol_id']].append((s['day'], s['mean_score']))
    for sid in symbol_sentiment:
        symbol_sentiment[sid].sort(key=lambda x: x[0])

    def get_trading_idx(sid, date):
        dates = symbol_trading_dates.get(sid, [])
        if not dates:
            return None
        idx = bisect.bisect_left(dates, date)
        if idx < len(dates):
            return idx
        return None

    def count_trading_days_between(sid, date1, date2):
        idx1 = get_trading_idx(sid, date1)
        idx2 = get_trading_idx(sid, date2)
        if idx1 is None or idx2 is None:
            return 0
        return abs(idx2 - idx1)

    def get_forward_return(sid, decision_date, horizon=21):
        idx = get_trading_idx(sid, decision_date)
        if idx is None or idx + horizon >= len(symbol_trading_dates[sid]):
            return None
        entry_close = symbol_bars_by_date[sid][symbol_trading_dates[sid][idx]][1]
        exit_date = symbol_trading_dates[sid][idx + horizon]
        exit_close = symbol_bars_by_date[sid][exit_date][1]
        return (exit_close - entry_close) / entry_close

    def get_sentiment_median(sid, disclosure_date_str, window=63):
        sent = symbol_sentiment.get(sid, [])
        if not sent:
            return None
        idx = bisect.bisect_right([d for d, _ in sent], disclosure_date_str)
        if idx == 0:
            return None
        window_vals = [v for _, v in sent[max(0, idx - window):idx]]
        if not window_vals:
            return None
        return statistics.median(window_vals)

    issued_calls = []
    opportunities = 0

    for (insider, sid), trades_list in pair_trades.items():
        if sid not in symbol_trading_dates or not symbol_trading_dates[sid]:
            continue
        if sid not in symbol_sentiment or not symbol_sentiment[sid]:
            continue

        cumulative = 0
        qualified_prior = []

        for i, trade in enumerate(trades_list):
            code = trade['code']
            shares = trade['shares'] or 0
            filed_ts = trade['filed_ts']
            title = (trade['title'] or '').upper()

            if code in ('P', 'A', 'M'):
                cumulative += shares
            elif code in ('S', 'F'):
                cumulative -= shares

            is_ceo_cfo_purchase = (code == 'P' and any(kw in title for kw in ['CEO', 'CFO', 'CHIEF EXECUTIVE', 'CHIEF FINANCIAL']))

            if is_ceo_cfo_purchase:
                opportunities += 1
                decision_date = datetime.fromtimestamp(filed_ts, tz=timezone.utc).date()
                decision_date_str = decision_date.isoformat()

                if decision_date not in symbol_trading_dates[sid]:
                    continue

                idx = get_trading_idx(sid, decision_date)
                if idx is None or idx < 252:
                    continue

                first_trade_date = datetime.fromtimestamp(trades_list[0]['filed_ts'], tz=timezone.utc).date()
                if count_trading_days_between(sid, first_trade_date, decision_date) < 252:
                    continue

                max_prior_cumulative = cumulative
                for j in range(i):
                    prior_trade = trades_list[j]
                    prior_filed = prior_trade['filed_ts']
                    prior_date = datetime.fromtimestamp(prior_filed, tz=timezone.utc).date()
                    if count_trading_days_between(sid, prior_date, decision_date) <= 252:
                        prior_cumulative = 0
                        for k in range(j + 1):
                            pt = trades_list[k]
                            ps = pt['shares'] or 0
                            pc = pt['code']
                            if pc in ('P', 'A', 'M'):
                                prior_cumulative += ps
                            elif pc in ('S', 'F'):
                                prior_cumulative -= ps
                        if prior_cumulative > max_prior_cumulative:
                            max_prior_cumulative = prior_cumulative

                if cumulative <= max_prior_cumulative:
                    continue

                sent_median = get_sentiment_median(sid, decision_date_str, 63)
                if sent_median is None:
                    continue
                disclosure_sent = None
                for d, v in symbol_sentiment[sid]:
                    if d == decision_date_str:
                        disclosure_sent = v
                        break
                if disclosure_sent is None or disclosure_sent >= sent_median:
                    continue

                prior_qualified = 0
                for q_idx in qualified_prior:
                    q_trade = trades_list[q_idx]
                    q_date = datetime.fromtimestamp(q_trade['filed_ts'], tz=timezone.utc).date()
                    if count_trading_days_between(sid, q_date, decision_date) <= 252:
                        prior_qualified += 1
                if prior_qualified < 3:
                    continue

                fwd_ret = get_forward_return(sid, decision_date, 21)
                if fwd_ret is None:
                    continue

                label = 1 if fwd_ret > 0 else 0
                issued_calls.append({
                    'symbol_id': sid,
                    'decision_date': decision_date,
                    'filed_ts': filed_ts,
                    'label': label
                })
                qualified_prior.append(i)

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    issued_calls.sort(key=lambda x: x['decision_date'])
    n = len(issued_calls)
    split_idx = int(n * 0.8)
    train_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]

    def compute_precision(calls):
        if not calls:
            return 0.0
        hits = sum(c['label'] for c in calls)
        return hits / len(calls)

    def compute_base_rate(calls):
        if not calls:
            return 0.0
        return sum(c['label'] for c in calls) / len(calls)

    train_precision = compute_precision(train_calls)
    sealed_precision = compute_precision(sealed_calls)
    base_rate = compute_base_rate(issued_calls)

    distinct_days = len(set(c['decision_date'] for c in issued_calls))

    if distinct_days == 0:
        design_effect = 1.0
    else:
        avg_per_day = n / distinct_days
        design_effect = 1 + (avg_per_day - 1) * 0.5
    effective_n = n / design_effect

    abstention_rate = 1 - (n / opportunities) if opportunities > 0 else 1.0

    print(f"ISSUED={n}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == "__main__":
    sys.exit(main())