# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 852
# cycle_index: 14
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone
from collections import defaultdict

def unix_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).strftime('%Y-%m-%d')

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get officer (CEO/CFO) open-market purchases with sufficient history
    cur.execute("""
        SELECT it.symbol_id, it.insider, it.title, it.code, it.shares, it.price,
               it.tx_ts, it.filed_ts, s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' 
               OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
          AND it.tx_ts < it.filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # 2. Get distinct symbols and date range for bars/news loading
    symbol_ids = list(set(t['symbol_id'] for t in trades))
    min_tx_ts = min(t['tx_ts'] for t in trades)
    max_filed_ts = max(t['filed_ts'] for t in trades)

    # 3. Load daily bars for relevant symbols (tf='1d')
    # Need bars from 252 days before min_tx_ts to 21 days after max_filed_ts
    cur.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d'
          AND symbol_id IN ({})
          AND ts >= ? AND ts <= ?
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbol_ids))), symbol_ids + [min_tx_ts - 252*86400, max_filed_ts + 21*86400])
    
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append({
            'ts': row['ts'], 'open': row['open'], 'high': row['high'],
            'low': row['low'], 'close': row['close'], 'volume': row['volume']
        })

    # 4. Load news counts per symbol per date
    cur.execute("""
        SELECT symbol_id, date(ts, 'unixepoch') as day, COUNT(*) as cnt
        FROM news
        WHERE symbol_id IN ({})
          AND ts >= ? AND ts <= ?
        GROUP BY symbol_id, day
    """.format(','.join('?'*len(symbol_ids))), symbol_ids + [min_tx_ts - 86400, max_filed_ts + 86400])
    
    news_counts = defaultdict(dict)
    for row in cur.fetchall():
        news_counts[row['symbol_id']][row['day']] = row['cnt']

    # 5. Load all insider trades for personal history (sales check)
    cur.execute("""
        SELECT insider, symbol_id, code, tx_ts
        FROM insider_trades
        WHERE insider IN ({})
          AND tx_ts >= ? AND tx_ts <= ?
    """.format(','.join('?'*len(set(t['insider'] for t in trades)))),
        list(set(t['insider'] for t in trades)) + [min_tx_ts - 252*86400, max_filed_ts])
    
    insider_history = defaultdict(list)
    for row in cur.fetchall():
        insider_history[row['insider']].append({
            'symbol_id': row['symbol_id'], 'code': row['code'], 'tx_ts': row['tx_ts']
        })

    # 6. Evaluate each trade
    calls = []
    for t in trades:
        sym_id = t['symbol_id']
        insider = t['insider']
        tx_ts = t['tx_ts']
        filed_ts = t['filed_ts']
        bars = bars_by_symbol.get(sym_id, [])
        if len(bars) < 252 + 21:
            continue

        # Find bar index for tx_ts (trade date) - latest bar <= tx_ts
        tx_idx = -1
        for i, b in enumerate(bars):
            if b['ts'] <= tx_ts:
                tx_idx = i
            else:
                break
        if tx_idx < 252:  # need 252 prior bars for volatility history
            continue

        # Compute 20-day realized volatility at tx_idx (using close prices)
        # Volatility = std of 20 daily log returns
        if tx_idx < 20:
            continue
        rets = []
        for i in range(tx_idx - 19, tx_idx + 1):
            if i > 0:
                rets.append((bars[i]['close'] - bars[i-1]['close']) / bars[i-1]['close'])
        if len(rets) < 20:
            continue
        import math
        mean_ret = sum(rets) / len(rets)
        vol_20 = math.sqrt(sum((r - mean_ret)**2 for r in rets) / len(rets))

        # Compute 252-day volatility history (rolling 20-day vols)
        vol_history = []
        for i in range(20, tx_idx + 1):
            h_rets = []
            for j in range(i - 19, i + 1):
                if j > 0:
                    h_rets.append((bars[j]['close'] - bars[j-1]['close']) / bars[j-1]['close'])
            if len(h_rets) == 20:
                h_mean = sum(h_rets) / 20
                h_vol = math.sqrt(sum((r - h_mean)**2 for r in h_rets) / 20)
                vol_history.append(h_vol)
        if len(vol_history) < 252:
            continue
        vol_history.sort()
        vol_threshold = vol_history[int(len(vol_history) * 0.1)]  # bottom decile
        if vol_20 > vol_threshold:
            continue

        # Check zero news on trade date
        tx_date = unix_to_date(tx_ts)
        if news_counts[sym_id].get(tx_date, 0) > 0:
            continue

        # Check officer has no sales in prior 252 sessions (trading days)
        # Approximate: 252 trading days ~ 365 calendar days
        prior_cutoff = tx_ts - 365*86400
        has_sale = False
        for h in insider_history.get(insider, []):
            if h['code'] == 'S' and prior_cutoff <= h['tx_ts'] < tx_ts:
                has_sale = True
                break
        if has_sale:
            continue

        # Find entry bar: first bar after filed_ts
        entry_idx = -1
        for i, b in enumerate(bars):
            if b['ts'] > filed_ts:
                entry_idx = i
                break
        if entry_idx == -1 or entry_idx + 21 >= len(bars):
            continue

        entry_price = bars[entry_idx]['open']
        exit_price = bars[entry_idx + 21]['close']
        fwd_return = (exit_price - entry_price) / entry_price
        hit = 1 if fwd_return > 0 else 0

        calls.append({
            'symbol_id': sym_id,
            'symbol': t['symbol'],
            'insider': insider,
            'decision_ts': filed_ts,
            'decision_date': unix_to_date(filed_ts),
            'entry_ts': bars[entry_idx]['ts'],
            'fwd_return': fwd_return,
            'hit': hit
        })

    if not calls:
        print("INSUFFICIENT=1")
        return

    # 7. Deduplicate by (symbol_id, decision_date) - one observation per symbol-day
    seen = set()
    deduped = []
    for c in calls:
        key = (c['symbol_id'], c['decision_date'])
        if key not in seen:
            seen.add(key)
            deduped.append(c)
    calls = deduped

    # 8. Sort by decision_ts and split 80/20 (sealed = most recent 20%)
    calls.sort(key=lambda x: x['decision_ts'])
    n = len(calls)
    split_idx = int(n * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    # 9. Compute metrics
    def compute_metrics(call_list, label):
        if not call_list:
            return None
        issued = len(call_list)
        hits = sum(c['hit'] for c in call_list)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # base rate within issued subset
        distinct_days = len(set(c['decision_date'] for c in call_list))
        
        # Design effect: variance/mean of weekly call counts
        from collections import Counter
        week_counts = Counter()
        for c in call_list:
            dt = datetime.fromtimestamp(c['decision_ts'], tz=timezone.utc)
            week_key = (dt.year, dt.isocalendar()[1])
            week_counts[week_key] += 1
        counts = list(week_counts.values())
        if len(counts) > 1:
            mean_c = sum(counts) / len(counts)
            var_c = sum((c - mean_c)**2 for c in counts) / len(counts)
            design_effect = var_c / mean_c if mean_c > 0 else 1
            design_effect = max(design_effect, 1.0)
        else:
            design_effect = 1.0
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        return {
            'issued': issued, 'hits': hits, 'precision': precision,
            'base_rate': base_rate, 'distinct_days': distinct_days,
            'effective_n': effective_n, 'design_effect': design_effect
        }

    train_m = compute_metrics(train_calls, 'train')
    sealed_m = compute_metrics(sealed_calls, 'sealed')

    if not train_m or not sealed_m:
        print("INSUFFICIENT=1")
        return

    # 10. Print required lines (using sealed era for final report)
    print(f"ISSUED={sealed_m['issued']}")
    print(f"OPPORTUNITIES={len(trades)}")  # total trades considered
    print(f"PRECISION={sealed_m['precision']:.6f}")
    print(f"BASE_RATE={sealed_m['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={sealed_m['distinct_days']}")
    print(f"EFFECTIVE_N={sealed_m['effective_n']:.2f}")
    print(f"SEALED_PRECISION={sealed_m['precision']:.6f}")

if __name__ == '__main__':
    main()