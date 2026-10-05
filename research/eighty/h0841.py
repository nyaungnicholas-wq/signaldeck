# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 840
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import bisect
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_epoch(dstr):
    return int(datetime.strptime(dstr, '%Y-%m-%d').timestamp())

def is_ceo_cfo(title):
    if not title:
        return False
    t = title.lower()
    return ('chief executive' in t or 'chief financial' in t or
            t.strip() in ('ceo', 'cfo') or
            'ceo' in t.split() or 'cfo' in t.split())

def main():
    con = sqlite3.connect(DB_PATH, uri=True)
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Get CEO/CFO open-market purchases (code='P')
    cur.execute("""
        SELECT symbol_id, filed_ts, tx_ts, shares, price, value, title
        FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL
    """)
    all_trades = cur.fetchall()
    ceo_cfo_trades = [r for r in all_trades if is_ceo_cfo(r['title'])]
    if not ceo_cfo_trades:
        print("INSUFFICIENT=1")
        return

    symbol_ids = sorted(set(r['symbol_id'] for r in ceo_cfo_trades))

    # 2. Get daily bars (tf='1d') for these symbols
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_rows = cur.fetchall()

    # Organize bars by symbol_id
    bars_by_sym = defaultdict(list)
    for r in bars_rows:
        bars_by_sym[r['symbol_id']].append((r['ts'], r['close'], r['volume']))

    # 3. Compute daily returns per symbol and market return
    all_dates = set()
    sym_returns = defaultdict(dict)
    for sym, bars in bars_by_sym.items():
        if len(bars) < 2:
            continue
        prev_close = bars[0][1]
        for ts, close, vol in bars[1:]:
            ret = (close / prev_close) - 1.0
            sym_returns[sym][ts] = ret
            all_dates.add(ts)
            prev_close = close

    all_dates = sorted(all_dates)
    if not all_dates:
        print("INSUFFICIENT=1")
        return

    # Market return = equal-weight average of available symbols each day
    market_ret = {}
    for ts in all_dates:
        rets = [sym_returns[sym].get(ts) for sym in sym_returns if ts in sym_returns[sym]]
        if rets:
            market_ret[ts] = sum(rets) / len(rets)

    # 4. For each symbol, compute 20-day rolling correlation with market
    # and 252-day 10th percentile of correlation
    sym_corr = defaultdict(dict)
    sym_corr_p10 = defaultdict(dict)

    for sym in symbol_ids:
        if sym not in sym_returns:
            continue
        common_dates = sorted(set(sym_returns[sym].keys()) & set(market_ret.keys()))
        if len(common_dates) < 252:
            continue
        window = 20
        for i in range(window, len(common_dates)):
            window_dates = common_dates[i-window:i]
            x = [sym_returns[sym][d] for d in window_dates]
            y = [market_ret[d] for d in window_dates]
            mx = sum(x) / window
            my = sum(y) / window
            num = sum((xi - mx) * (yi - my) for xi, yi in zip(x, y))
            dx = sum((xi - mx) ** 2 for xi in x)
            dy = sum((yi - my) ** 2 for yi in y)
            if dx > 0 and dy > 0:
                corr = num / (dx * dy) ** 0.5
            else:
                corr = 0.0
            ts = common_dates[i-1]
            sym_corr[sym][ts] = corr

        corr_dates = sorted(sym_corr[sym].keys())
        if len(corr_dates) < 252:
            continue
        for i in range(252, len(corr_dates)):
            window_corrs = [sym_corr[sym][d] for d in corr_dates[i-252:i]]
            window_corrs.sort()
            p10 = window_corrs[len(window_corrs) // 10]
            ts = corr_dates[i-1]
            sym_corr_p10[sym][ts] = p10

    # 5. Get sentiment_features (daily mean_score) for symbols
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)
    sent_rows = cur.fetchall()

    sent_by_sym = defaultdict(dict)
    for r in sent_rows:
        day_ts = date_to_epoch(r['day'])
        sent_by_sym[r['symbol_id']][day_ts] = r['mean_score']

    # 6. Get prediction_outcomes for 1d horizon (labels)
    cur.execute(f"""
        SELECT symbol_id, ts, fwd_return, up
        FROM prediction_outcomes
        WHERE horizon = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    label_rows = cur.fetchall()

    labels_by_sym = defaultdict(dict)
    for r in label_rows:
        labels_by_sym[r['symbol_id']][r['ts']] = (r['fwd_return'], r['up'])

    # 7. Build decision points: each CEO/CFO trade's filed_ts is the decision time
    # We need correlation and sentiment as of the day BEFORE filed_ts (to avoid lookahead)
    # Horizon: 21 trading days (~1 month). Use prediction_outcomes 1d horizon but we need 21-day forward return.
    # Since prediction_outcomes only has 1d, 1w horizons, we'll compute 21-day forward return from bars.
    
    # Precompute 21-day forward returns from bars
    fwd21_by_sym = defaultdict(dict)
    for sym, bars in bars_by_sym.items():
        if len(bars) < 22:
            continue
        closes = [b[1] for b in bars]
        tss = [b[0] for b in bars]
        for i in range(len(bars) - 21):
            fwd_ret = (closes[i + 21] / closes[i]) - 1.0
            fwd21_by_sym[sym][tss[i]] = fwd_ret

    # 8. Evaluate each trade
    opportunities = 0
    issued = 0
    hits = 0
    issued_days = set()
    sealed_cutoff_idx = None
    
    trade_decisions = []
    for r in ceo_cfo_trades:
        sym = r['symbol_id']
        filed_ts = r['filed_ts']
        tx_ts = r['tx_ts']
        
        # Decision date = filed_ts date (UTC day)
        decision_day = datetime.utcfromtimestamp(filed_ts).replace(hour=0, minute=0, second=0, microsecond=0)
        decision_ts = int(decision_day.timestamp())
        
        # Need correlation as of previous trading day
        corr_dates = sorted(sym_corr.get(sym, {}).keys())
        if not corr_dates:
            continue
        idx = bisect.bisect_left(corr_dates, decision_ts) - 1
        if idx < 0:
            continue
        corr_ts = corr_dates[idx]
        corr = sym_corr[sym][corr_ts]
        
        # Need p10 as of previous trading day
        p10_dates = sorted(sym_corr_p10.get(sym, {}).keys())
        if not p10_dates:
            continue
        idx = bisect.bisect_left(p10_dates, decision_ts) - 1
        if idx < 0:
            continue
        p10_ts = p10_dates[idx]
        p10 = sym_corr_p10[sym][p10_ts]
        
        # Need sentiment as of previous day
        sent_dates = sorted(sent_by_sym.get(sym, {}).keys())
        if not sent_dates:
            continue
        idx = bisect.bisect_left(sent_dates, decision_ts) - 1
        if idx < 0:
            continue
        sent_ts = sent_dates[idx]
        sent = sent_by_sym[sym][sent_ts]
        
        # Need 21-day forward return
        fwd_dates = sorted(fwd21_by_sym.get(sym, {}).keys())
        if not fwd_dates:
            continue
        idx = bisect.bisect_left(fwd_dates, decision_ts)
        if idx >= len(fwd_dates):
            continue
        fwd_ts = fwd_dates[idx]
        fwd_ret = fwd21_by_sym[sym][fwd_ts]
        
        opportunities += 1
        trade_decisions.append((decision_ts, fwd_ret > 0, corr, p10, sent))
    
    if not trade_decisions:
        print("INSUFFICIENT=1")
        return
    
    # Sort by decision time
    trade_decisions.sort(key=lambda x: x[0])
    
    # Seal most recent 20%
    n = len(trade_decisions)
    seal_idx = int(n * 0.8)
    
    for i, (dec_ts, is_up, corr, p10, sent) in enumerate(trade_decisions):
        # Condition: correlation at or below 10th percentile (decoupled) AND sentiment <= 0 (negative/neutral)
        if corr <= p10 and sent <= 0:
            issued += 1
            issued_days.add(datetime.utcfromtimestamp(dec_ts).strftime('%Y-%m-%d'))
            if is_up:
                hits += 1
    
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    
    # Compute design effect for EFFECTIVE_N
    # Simple approximation: 1 + (avg cluster size - 1) * intraclass correlation
    # Use day-level clustering
    day_counts = defaultdict(int)
    for dec_ts, is_up, corr, p10, sent in trade_decisions:
        if corr <= p10 and sent <= 0:
            day = datetime.utcfromtimestamp(dec_ts).strftime('%Y-%m-%d')
            day_counts[day] += 1
    
    if day_counts:
        avg_cluster = sum(day_counts.values()) / len(day_counts)
        # Conservative ICC estimate for financial returns ~0.1-0.2
        icc = 0.15
        design_effect = 1 + (avg_cluster - 1) * icc
        effective_n = issued / design_effect
    else:
        effective_n = issued * 0.5  # fallback
    
    # Sealed era precision
    sealed_issued = 0
    sealed_hits = 0
    for i, (dec_ts, is_up, corr, p10, sent) in enumerate(trade_decisions):
        if i >= seal_idx:
            if corr <= p10 and sent <= 0:
                sealed_issued += 1
                if is_up:
                    sealed_hits += 1
    
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0
    
    precision = hits / issued
    base_rate = sum(1 for _, is_up, _, _, _ in trade_decisions if is_up) / len(trade_decisions)
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={len(issued_days)}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()