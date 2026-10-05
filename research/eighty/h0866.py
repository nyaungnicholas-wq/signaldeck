# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 865
# cycle_index: 11
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timedelta
from bisect import bisect_right

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def unix_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_unix(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def load_symbols(conn):
    cur = conn.execute("SELECT id, symbol FROM symbols WHERE market='stocks'")
    return {row[0]: row[1] for row in cur.fetchall()}

def load_daily_news_sentiment(conn):
    cur = conn.execute("""
        SELECT symbol_id, ts, score
        FROM news
        WHERE score IS NOT NULL
        ORDER BY symbol_id, ts
    """)
    by_symbol = defaultdict(list)
    for sym_id, ts, score in cur.fetchall():
        d = unix_to_date(ts)
        by_symbol[sym_id].append((d, score))
    daily = {}
    for sym_id, rows in by_symbol.items():
        day_scores = defaultdict(list)
        for d, s in rows:
            day_scores[d].append(s)
        daily[sym_id] = {d: sum(v)/len(v) for d, v in day_scores.items()}
    return daily

def compute_rolling_percentiles(daily_sentiment, window=252):
    percentiles = {}
    for sym_id, day_map in daily_sentiment.items():
        days = sorted(day_map.keys())
        if len(days) < window:
            continue
        values = [day_map[d] for d in days]
        sym_pct = {}
        for i in range(window - 1, len(days)):
            window_vals = values[i - window + 1:i + 1]
            current = values[i]
            rank = sum(1 for v in window_vals if v <= current)
            pct = rank / len(window_vals) * 100
            sym_pct[days[i]] = pct
        percentiles[sym_id] = sym_pct
    return percentiles

def load_officer_purchases(conn):
    cur = conn.execute("""
        SELECT accession, symbol_id, insider, title, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
          AND tx_ts IS NOT NULL
          AND filed_ts IS NOT NULL
        ORDER BY filed_ts
    """)
    purchases = []
    for row in cur.fetchall():
        accession, sym_id, insider, title, tx_ts, filed_ts = row
        purchases.append({
            'accession': accession,
            'symbol_id': sym_id,
            'officer': insider,
            'title': title,
            'trade_date': unix_to_date(tx_ts),
            'file_date': unix_to_date(filed_ts),
            'tx_ts': tx_ts,
            'filed_ts': filed_ts
        })
    return purchases

def load_daily_bars(conn, symbol_ids):
    if not symbol_ids:
        return {}
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    by_symbol = defaultdict(list)
    for sym_id, ts, close in cur.fetchall():
        d = unix_to_date(ts)
        by_symbol[sym_id].append((d, close))
    return by_symbol

def get_forward_return(bars, start_date, horizon=21):
    dates = [d for d, _ in bars]
    closes = [c for _, c in bars]
    idx = bisect_right(dates, start_date)
    if idx >= len(dates):
        return None
    entry_price = closes[idx]
    target_idx = idx + horizon
    if target_idx >= len(closes):
        return None
    exit_price = closes[target_idx]
    return (exit_price - entry_price) / entry_price

def main():
    conn = connect()
    try:
        symbols = load_symbols(conn)
        print("Loaded symbols", file=sys.stderr)
        
        daily_sentiment = load_daily_news_sentiment(conn)
        print("Loaded news sentiment", file=sys.stderr)
        
        sentiment_pct = compute_rolling_percentiles(daily_sentiment, 252)
        print("Computed sentiment percentiles", file=sys.stderr)
        
        purchases = load_officer_purchases(conn)
        print(f"Loaded {len(purchases)} officer purchases", file=sys.stderr)
        
        symbol_ids = set(p['symbol_id'] for p in purchases)
        bars = load_daily_bars(conn, list(symbol_ids))
        print("Loaded daily bars", file=sys.stderr)
        
        sym_purchases = defaultdict(list)
        for p in purchases:
            sym_purchases[p['symbol_id']].append(p)
        
        entries = []
        officer_history = defaultdict(list)
        
        for p in purchases:
            sym_id = p['symbol_id']
            trade_date = p['trade_date']
            officer = p['officer']
            
            if sym_id not in sentiment_pct:
                continue
            if trade_date not in sentiment_pct[sym_id]:
                continue
            pct = sentiment_pct[sym_id][trade_date]
            
            hist = officer_history[officer]
            prior_purchases = [h for h in hist if h['trade_date'] < trade_date]
            if len(prior_purchases) < 3:
                officer_history[officer].append({'trade_date': trade_date, 'pct': pct})
                continue
            
            contrarian_count = sum(1 for h in prior_purchases if h['pct'] <= 20)
            if contrarian_count < 2:
                officer_history[officer].append({'trade_date': trade_date, 'pct': pct})
                continue
            
            contrarian_score = contrarian_count / len(prior_purchases)
            
            if pct <= 15 and contrarian_score >= 0.66:
                fwd_ret = get_forward_return(bars.get(sym_id, []), trade_date, 21)
                if fwd_ret is not None:
                    hit = 1 if fwd_ret > 0 else 0
                    entries.append({
                        'symbol_id': sym_id,
                        'officer': officer,
                        'trade_date': trade_date,
                        'file_date': p['file_date'],
                        'sentiment_pct': pct,
                        'contrarian_score': contrarian_score,
                        'fwd_return': fwd_ret,
                        'hit': hit
                    })
            
            officer_history[officer].append({'trade_date': trade_date, 'pct': pct})
        
        print(f"Found {len(entries)} entry signals", file=sys.stderr)
        
        if not entries:
            print("INSUFFICIENT=1")
            return
        
        entries.sort(key=lambda x: x['trade_date'])
        
        n = len(entries)
        split_idx = int(n * 0.8)
        backtest_entries = entries[:split_idx]
        sealed_entries = entries[split_idx:]
        
        if not backtest_entries:
            print("INSUFFICIENT=1")
            return
        
        backtest_days = set((e['symbol_id'], e['trade_date']) for e in backtest_entries)
        if len(backtest_days) < 30:
            print("INSUFFICIENT=1")
            return
        
        issued_bt = len(backtest_entries)
        hits_bt = sum(e['hit'] for e in backtest_entries)
        precision_bt = hits_bt / issued_bt if issued_bt else 0
        base_rate_bt = hits_bt / issued_bt if issued_bt else 0
        
        distinct_days_bt = len(set(e['trade_date'] for e in backtest_entries))
        
        design_effect = 1.0
        if issued_bt > 1:
            by_day = defaultdict(int)
            for e in backtest_entries:
                by_day[e['trade_date']] += 1
            avg_per_day = sum(by_day.values()) / len(by_day)
            design_effect = avg_per_day
        effective_n = issued_bt / design_effect if design_effect > 0 else issued_bt
        
        sealed_precision = 0
        if sealed_entries:
            issued_sealed = len(sealed_entries)
            hits_sealed = sum(e['hit'] for e in sealed_entries)
            sealed_precision = hits_sealed / issued_sealed if issued_sealed else 0
        
        opportunities = len(purchases)
        
        print(f"ISSUED={issued_bt}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision_bt:.6f}")
        print(f"BASE_RATE={base_rate_bt:.6f}")
        print(f"DISTINCT_DAYS={distinct_days_bt}")
        print(f"EFFECTIVE_N={effective_n:.6f}")
        print(f"SEALED_PRECISION={sealed_precision:.6f}")
        
    finally:
        conn.close()

if __name__ == '__main__':
    main()