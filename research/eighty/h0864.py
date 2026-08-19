# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 863
# cycle_index: 9
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def is_officer(title):
    if not title:
        return False
    t = title.upper()
    return any(k in t for k in ('CEO', 'CFO', 'PRESIDENT', 'CHIEF', 'OFFICER'))

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all officer purchases (code P) with filed_ts
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, it.shares, it.price, it.value, it.title
        FROM insider_trades it
        WHERE it.code = 'P' AND it.filed_ts IS NOT NULL
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # Get symbols with daily bars
    cur.execute("SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'")
    bar_symbols = {r['symbol_id'] for r in cur.fetchall()}

    # Get sentiment_features (day is YYYY-MM-DD string)
    cur.execute("SELECT symbol_id, day, mean_score FROM sentiment_features")
    sent_rows = cur.fetchall()
    sent_map = {}
    for r in sent_rows:
        sent_map.setdefault(r['symbol_id'], {})[r['day']] = r['mean_score']

    # Get daily bars for price returns
    cur.execute("SELECT symbol_id, ts, close FROM bars WHERE tf = '1d' ORDER BY symbol_id, ts")
    bar_rows = cur.fetchall()
    bars_by_sym = {}
    for r in bar_rows:
        bars_by_sym.setdefault(r['symbol_id'], []).append((r['ts'], r['close']))

    # Build 20-day sentiment percentile per symbol (rolling)
    sent_percentile = {}
    for sym, data in sent_map.items():
        days = sorted(data.keys())
        scores = [data[d] for d in days]
        # Compute rolling 20-day percentile rank of mean_score
        for i in range(len(days)):
            if i >= 19:
                window = scores[i-19:i+1]
                rank = sum(1 for v in window if v <= scores[i]) / len(window)
                sent_percentile.setdefault(sym, {})[days[i]] = rank

    # Build 20-day price return per symbol
    price_return_20d = {}
    for sym, bars in bars_by_sym.items():
        if len(bars) < 21:
            continue
        closes = [b[1] for b in bars]
        timestamps = [b[0] for b in bars]
        for i in range(20, len(closes)):
            ret = (closes[i] - closes[i-20]) / closes[i-20]
            day = epoch_to_date(timestamps[i]).isoformat()
            price_return_20d.setdefault(sym, {})[day] = ret

    # Build 63-day forward return from each decision day
    fwd_return_63d = {}
    for sym, bars in bars_by_sym.items():
        if len(bars) < 64:
            continue
        closes = [b[1] for b in bars]
        timestamps = [b[0] for b in bars]
        for i in range(len(closes) - 63):
            ret = (closes[i+63] - closes[i]) / closes[i]
            day = epoch_to_date(timestamps[i]).isoformat()
            fwd_return_63d.setdefault(sym, {})[day] = ret

    # Evaluate each trade
    decisions = []
    for t in trades:
        sym = t['symbol_id']
        if sym not in bar_symbols:
            continue
        filed_day = epoch_to_date(t['filed_ts']).isoformat()
        if not is_officer(t['title']):
            continue
        # Need sentiment percentile, 20-day return, and 63-day forward return
        sp = sent_percentile.get(sym, {}).get(filed_day)
        pr = price_return_20d.get(sym, {}).get(filed_day)
        fr = fwd_return_63d.get(sym, {}).get(filed_day)
        if sp is None or pr is None or fr is None:
            continue
        # Entry: sentiment in bottom 20% AND 20-day price return positive
        if sp <= 0.20 and pr > 0:
            decisions.append((filed_day, sym, fr))

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # Sort by date
    decisions.sort(key=lambda x: x[0])
    n = len(decisions)
    split_idx = int(n * 0.8)
    train_decs = decisions[:split_idx]
    sealed_decs = decisions[split_idx:]

    def compute_metrics(decs):
        if not decs:
            return 0, 0, 0, 0, 0
        issued = len(decs)
        hits = sum(1 for _, _, fr in decs if fr > 0)
        precision = hits / issued
        base_rate = hits / issued  # within issued subset, predicted class is fr>0
        distinct_days = len(set(d for d, _, _ in decs))
        # Design effect: 1 + (avg cluster size - 1) * autocorr
        # Approximate: group by day, compute variance inflation
        day_counts = {}
        for d, _, _ in decs:
            day_counts[d] = day_counts.get(d, 0) + 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        # Conservative autocorr estimate 0.1
        deff = 1 + (avg_cluster - 1) * 0.1
        effective_n = issued / deff
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_days, train_en = compute_metrics(train_decs)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_en = compute_metrics(sealed_decs)

    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={len(trades)}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_br:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_en:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()

# MECHANISM: Officer (CEO/CFO) open-market purchases filed on days when 20-day news sentiment is in bottom quintile but 20-day price return is positive, signaling informed buying during pessimistic coverage with price resilience.
# HORIZON: 63d
# UNIVERSE: Symbols with daily bars, sentiment_features, and officer insider trades 2018-2026
# ENTRY: code=P, officer title, filed_ts day has sentiment_percentile<=0.20 and price_return_20d>0
# ABSTAIN: Missing sentiment, price history, or forward return data
# CLAIM: Precision exceeds base rate in both training and sealed eras with effective N > 30