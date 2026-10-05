# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 824
# cycle_index: 20
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get active symbols with sufficient daily bars (>= 504 days = ~2 years)
    cur.execute("""
        SELECT s.id, s.symbol, s.delisted_at
        FROM symbols s
        WHERE s.active = 1 AND s.market = 'stocks'
        AND EXISTS (
            SELECT 1 FROM bars b
            WHERE b.symbol_id = s.id AND b.tf = '1d'
            GROUP BY b.symbol_id
            HAVING COUNT(*) >= 504
        )
    """)
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return 0
    symbol_ids = list(symbols.keys())

    # 2. Get officer insider purchases (code='P', title contains CEO/CFO/Officer)
    # Use filed_ts as decision time (knowable at decision)
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
        AND code = 'P'
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%' OR title LIKE '%Officer%')
        AND filed_ts > 0
    """, symbol_ids)
    insider_trades = []
    for row in cur.fetchall():
        dt = epoch_to_date(row['filed_ts'])
        insider_trades.append({
            'accession': row['accession'],
            'symbol_id': row['symbol_id'],
            'insider': row['insider'],
            'title': row['title'],
            'filed_ts': row['filed_ts'],
            'filed_date': dt,
            'trade_price': row['price']
        })
    if not insider_trades:
        print("INSUFFICIENT=1")
        return 0

    # 3. Load sentiment_features for relevant symbols
    # day is 'YYYY-MM-DD' string, mean_score is daily sentiment
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        AND mean_score IS NOT NULL
    """, symbol_ids)
    sentiment_by_sym = defaultdict(dict)
    for row in cur.fetchall():
        sentiment_by_sym[row['symbol_id']][row['day']] = row['mean_score']

    # 4. Load daily bars for price data (tf='1d')
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE symbol_id IN ({placeholders})
        AND tf = '1d'
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_sym = defaultdict(list)
    for row in cur.fetchall():
        dt = epoch_to_date(row['ts'])
        bars_by_sym[row['symbol_id']].append((dt, row['close']))

    # Precompute 252-day high for each symbol at each date
    # And align sentiment data
    print("Computing features...", file=sys.stderr)
    
    # For each symbol, build time series of close prices and sentiment
    symbol_data = {}
    for sid in symbol_ids:
        bars = bars_by_sym.get(sid, [])
        if len(bars) < 252:
            continue
        dates = [d for d, _ in bars]
        closes = [c for _, c in bars]
        
        # Compute rolling 252-day high
        high_252 = []
        for i in range(len(closes)):
            if i < 251:
                high_252.append(None)
            else:
                high_252.append(max(closes[i-251:i+1]))
        
        # Get sentiment series aligned to dates
        sent = sentiment_by_sym.get(sid, {})
        sent_scores = [sent.get(date_to_str(d), None) for d in dates]
        
        symbol_data[sid] = {
            'dates': dates,
            'closes': closes,
            'high_252': high_252,
            'sentiment': sent_scores
        }

    # 5. Evaluate each insider trade as a decision point
    opportunities = 0
    issued = []
    
    for trade in insider_trades:
        sid = trade['symbol_id']
        if sid not in symbol_data:
            continue
        data = symbol_data[sid]
        filed_date = trade['filed_date']
        
        # Find index of filed_date in bars (decision date)
        try:
            idx = data['dates'].index(filed_date)
        except ValueError:
            # filed_date not a trading day, find next trading day
            idx = None
            for i, d in enumerate(data['dates']):
                if d >= filed_date:
                    idx = i
                    break
            if idx is None:
                continue
        
        opportunities += 1
        
        # ABSTAIN checks
        # a) Need 252-day high
        if data['high_252'][idx] is None:
            continue
        # b) Price >= 15% below 252-day high
        close = data['closes'][idx]
        high = data['high_252'][idx]
        if close > high * 0.85:
            continue
        
        # c) Sentiment: 60-day mean in bottom quartile historically, 10-day mean in top half
        # Need at least 60 days of sentiment history
        if idx < 60:
            continue
        
        # Historical sentiment distribution for this symbol (up to idx-1)
        hist_sent = [s for s in data['sentiment'][:idx] if s is not None]
        if len(hist_sent) < 60:
            continue
        hist_sent_sorted = sorted(hist_sent)
        q1 = hist_sent_sorted[len(hist_sent_sorted) // 4]
        median = hist_sent_sorted[len(hist_sent_sorted) // 2]
        
        # 60-day mean ending idx-1 (day before decision)
        sent_60 = [s for s in data['sentiment'][idx-60:idx] if s is not None]
        if len(sent_60) < 30:  # require at least half coverage
            continue
        mean_60 = sum(sent_60) / len(sent_60)
        if mean_60 > q1:  # not in bottom quartile
            continue
        
        # 10-day mean ending idx-1
        sent_10 = [s for s in data['sentiment'][idx-10:idx] if s is not None]
        if len(sent_10) < 5:
            continue
        mean_10 = sum(sent_10) / len(sent_10)
        if mean_10 < median:  # not in top half
            continue
        
        # d) No other officer purchase in prior 20 days for this symbol
        # Check other trades for same symbol
        recent_officer_buy = False
        for other in insider_trades:
            if other['symbol_id'] == sid and other['accession'] != trade['accession']:
                other_date = other['filed_date']
                if filed_date - timedelta(days=20) <= other_date < filed_date:
                    recent_officer_buy = True
                    break
        if recent_officer_buy:
            continue
        
        # e) Check delisted
        delisted = symbols[sid]['delisted_at']
        if delisted:
            delisted_date = str_to_date(delisted)
            if filed_date >= delisted_date:
                continue
        
        # All entry conditions met - issue call
        # Compute 21-day forward return from NEXT trading day
        if idx + 21 >= len(data['closes']):
            continue  # insufficient future data
        entry_price = data['closes'][idx + 1]  # next day open ~ close
        exit_price = data['closes'][idx + 21]
        fwd_return = (exit_price - entry_price) / entry_price
        up = 1 if fwd_return > 0 else 0
        
        issued.append({
            'symbol_id': sid,
            'symbol': symbols[sid]['symbol'],
            'date': filed_date,
            'up': up,
            'fwd_return': fwd_return
        })

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    # 6. Split into sealed era (most recent 20% of decision dates)
    issued.sort(key=lambda x: x['date'])
    n = len(issued)
    split_idx = int(n * 0.8)
    train = issued[:split_idx]
    sealed = issued[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        issued_count = len(calls)
        hits = sum(c['up'] for c in calls)
        precision = hits / issued_count
        base_rate = hits / issued_count  # base rate within issued subset
        distinct_days = len(set(c['date'] for c in calls))
        # Design effect: 1 + (avg cluster size - 1) * intra-cluster correlation
        # Approximate: group by date, compute variance inflation
        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c['date']] += 1
        if len(day_counts) > 1:
            avg_cluster = issued_count / len(day_counts)
            # Conservative ICC estimate for financial returns ~0.1-0.3
            icc = 0.2
            deff = 1 + (avg_cluster - 1) * icc
            effective_n = issued_count / deff
        else:
            effective_n = 1.0
        return issued_count, precision, base_rate, distinct_days, effective_n

    train_issued, train_prec, train_br, train_days, train_en = compute_metrics(train)
    sealed_issued, sealed_prec, sealed_br, sealed_days, sealed_en = compute_metrics(sealed)

    # Overall metrics (on full sample for reporting)
    all_issued, all_prec, all_br, all_days, all_en = compute_metrics(issued)

    # 7. Output required lines
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={all_en:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())