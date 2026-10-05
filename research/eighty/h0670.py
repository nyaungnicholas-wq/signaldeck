# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 669
# cycle_index: 25
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

# MECHANISM: When the daily hedged ratio (hedged/n_all from sentiment_features) spikes to the top decile of its 252-day history (unusual linguistic hedging in news coverage) AND the concurrent mean_score is negative, AND a corporate officer (CEO/CFO/COO/President) executes an open-market purchase (code P) within the next 3 trading days, the "uncertain pessimism met with officer conviction" predicts 21-day forward outperformance.
# HORIZON: 21d
# UNIVERSE: symbols with market='stocks', active=1, >=252 daily bars in bars(tf='1d'), with sentiment_features history >=252 days, and insider_trades with officer titles
# ENTRY: On officer trade date (tx_ts) where: (1) hedged_ratio on day-1 or day-2 > 90th percentile of prior 252 days, (2) mean_score on day-1 < 0, (3) insider code='P', title contains CEO/CFO/COO/President
# ABSTAIN: If hedged_ratio data missing for symbol, or fewer than 5 total signals in backtest, or VIXCLS > 45 on entry day from macro_series
# CLAIM: Precision > 55% with base rate < 50% in issued subset, effective N > 25, distinct days > 20

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import deque, Counter

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(epoch):
    return datetime.utcfromtimestamp(epoch).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get VIXCLS series_id from macro_series
    cur.execute("SELECT DISTINCT series FROM macro_series WHERE series LIKE '%VIX%' OR series LIKE '%vix%' LIMIT 5")
    vix_series = [row['series'] for row in cur.fetchall()]
    if not vix_series:
        cur.execute("SELECT DISTINCT series FROM macro_series WHERE series IN ('VIXCLS', 'VIX') LIMIT 1")
        row = cur.fetchone()
        vix_series = [row['series']] if row else []
    
    # 2. Get eligible symbols: stocks, active, >=252 daily bars
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        JOIN (
            SELECT symbol_id, COUNT(*) as cnt
            FROM bars
            WHERE tf = '1d'
            GROUP BY symbol_id
            HAVING cnt >= 252
        ) b ON s.id = b.symbol_id
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    eligible_symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get officers from insider_trades (titles containing CEO/CFO/COO/President)
    placeholders = ','.join('?'*len(eligible_symbols))
    cur.execute(f"""
        SELECT DISTINCT symbol_id, insider, title
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%COO%' OR title LIKE '%PRESIDENT%')
    """, list(eligible_symbols.keys()))
    officer_map = {}
    for row in cur.fetchall():
        officer_map.setdefault(row['symbol_id'], []).append((row['insider'], row['title']))

    # 4. Get sentiment_features data for eligible symbols with >=252 days
    cur.execute(f"""
        SELECT symbol_id, day, n_all, hedged, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        AND n_all > 0
        ORDER BY symbol_id, day
    """, list(eligible_symbols.keys()))
    
    sent_by_symbol = {}
    for row in cur.fetchall():
        sent_by_symbol.setdefault(row['symbol_id'], []).append({
            'day': row['day'],
            'n_all': row['n_all'],
            'hedged': row['hedged'],
            'mean_score': row['mean_score']
        })

    # Filter symbols with >=252 sentiment days
    valid_symbols = {sid for sid, rows in sent_by_symbol.items() if len(rows) >= 252}
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return 0

    # 5. Get insider trades for valid symbols, code=P, officer titles
    placeholders2 = ','.join('?'*len(valid_symbols))
    cur.execute(f"""
        SELECT symbol_id, insider, title, code, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders2})
        AND code = 'P'
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%COO%' OR title LIKE '%PRESIDENT%')
        ORDER BY symbol_id, tx_ts
    """, list(valid_symbols))
    
    insider_by_symbol = {}
    for row in cur.fetchall():
        insider_by_symbol.setdefault(row['symbol_id'], []).append({
            'insider': row['insider'],
            'title': row['title'],
            'tx_ts': row['tx_ts'],
            'filed_ts': row['filed_ts']
        })

    # 6. Get VIXCLS data
    vix_data = {}
    if vix_series:
        cur.execute("""
            SELECT ts, value FROM macro_series WHERE series = ? ORDER BY ts
        """, (vix_series[0],))
        for row in cur.fetchall():
            vix_data[epoch_to_date(row['ts'])] = row['value']

    # 7. Get daily bars for forward returns (tf='1d')
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders2})
        ORDER BY symbol_id, ts
    """, list(valid_symbols))
    
    bars_by_symbol = {}
    for row in cur.fetchall():
        bars_by_symbol.setdefault(row['symbol_id'], []).append({
            'ts': row['ts'],
            'close': row['close'],
            'date': epoch_to_date(row['ts'])
        })

    # 8. Build hedged ratio percentiles for each symbol
    hedged_pctls = {}
    for sid, rows in sent_by_symbol.items():
        if sid not in valid_symbols:
            continue
        hedged_ratios = []
        dates = []
        for r in rows:
            if r['n_all'] > 0:
                hedged_ratios.append(r['hedged'] / r['n_all'])
                dates.append(str_to_date(r['day']))
        if len(hedged_ratios) >= 252:
            hedged_pctls[sid] = list(zip(dates, hedged_ratios))

    # 9. Generate signals
    signals = []  # (symbol_id, entry_date, entry_ts, fwd_return)
    
    for sid in valid_symbols:
        if sid not in hedged_pctls or sid not in insider_by_symbol or sid not in bars_by_symbol:
            continue
        
        hr_data = hedged_pctls[sid]  # list of (date, ratio)
        sent_data = sent_by_symbol[sid]  # list of dicts
        insider_trades = insider_by_symbol[sid]
        bars = bars_by_symbol[sid]
        
        # Build date -> sentiment lookup
        sent_lookup = {}
        for r in sent_data:
            sent_lookup[str_to_date(r['day'])] = r
        
        # Build date -> bar close lookup
        bar_lookup = {}
        bar_dates = []
        for b in bars:
            bar_lookup[b['date']] = b['close']
            bar_dates.append(b['date'])
        bar_dates.sort()
        
        # Rolling 90th percentile of hedged_ratio over 252 days
        hr_dates = [d for d, _ in hr_data]
        hr_values = [v for _, v in hr_data]
        
        for trade in insider_trades:
            tx_date = epoch_to_date(trade['tx_ts'])
            
            # Check VIXCLS < 45 on entry day
            if vix_data and tx_date in vix_data and vix_data[tx_date] > 45:
                continue
            
            # Look back 1-2 trading days for hedged_ratio spike
            # Find prior trading days
            prior_dates = [d for d in bar_dates if d < tx_date]
            if len(prior_dates) < 2:
                continue
            day_1 = prior_dates[-1]
            day_2 = prior_dates[-2] if len(prior_dates) >= 2 else None
            
            # Check hedged_ratio on day_1 or day_2 > 90th pct of prior 252 days
            spike_found = False
            for check_date in [day_1, day_2]:
                if check_date is None:
                    continue
                # Find index in hr_dates
                try:
                    idx = hr_dates.index(check_date)
                except ValueError:
                    continue
                if idx < 252:
                    continue
                window = hr_values[idx-252:idx]
                if len(window) < 252:
                    continue
                pct90 = sorted(window)[int(0.9 * 252)]
                if hr_values[idx] > pct90:
                    spike_found = True
                    break
            
            if not spike_found:
                continue
            
            # Check mean_score < 0 on day_1
            if day_1 not in sent_lookup:
                continue
            if sent_lookup[day_1]['mean_score'] >= 0:
                continue
            
            # Compute 21-day forward return from tx_date (using next bar after tx_date)
            # Entry at close of tx_date (or next bar if tx_date not in bars)
            entry_dates = [d for d in bar_dates if d >= tx_date]
            if not entry_dates:
                continue
            entry_date = entry_dates[0]
            entry_idx = bar_dates.index(entry_date)
            exit_idx = entry_idx + 21
            if exit_idx >= len(bar_dates):
                continue
            exit_date = bar_dates[exit_idx]
            entry_px = bar_lookup[entry_date]
            exit_px = bar_lookup[exit_date]
            fwd_return = (exit_px - entry_px) / entry_px
            
            signals.append({
                'symbol_id': sid,
                'entry_date': entry_date,
                'entry_ts': trade['tx_ts'],
                'fwd_return': fwd_return,
                'up': 1 if fwd_return > 0 else 0
            })

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # 10. Hold out most recent 20% as sealed era
    signals.sort(key=lambda x: x['entry_ts'])
    n_total = len(signals)
    n_sealed = max(1, int(n_total * 0.2))
    train_signals = signals[:-n_sealed]
    sealed_signals = signals[-n_sealed:]

    def compute_metrics(sigs):
        if not sigs:
            return None
        issued = len(sigs)
        hits = sum(1 for s in sigs if s['up'] == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate of positive class in issued
        distinct_days = len(set(s['entry_date'] for s in sigs))
        
        # Design effect: cluster by month
        month_counts = Counter((s['entry_date'].year, s['entry_date'].month) for s in sigs)
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Approximate ICC ~ 0.1 for financial returns
        avg_cluster = sum(month_counts.values()) / len(month_counts) if month_counts else 1
        deff = 1 + (avg_cluster - 1) * 0.1
        effective_n = issued / deff
        
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_metrics = compute_metrics(train_signals)
    sealed_metrics = compute_metrics(sealed_signals)

    if not train_metrics or train_metrics['issued'] < 5:
        print("INSUFFICIENT=1")
        return 0

    # Print results
    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={train_metrics['issued']}")  # Same as issued for this strategy
    print(f"PRECISION={train_metrics['precision']:.4f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.4f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.2f}")
    if sealed_metrics:
        print(f"SEALED_PRECISION={sealed_metrics['precision']:.4f}")
    else:
        print("SEALED_PRECISION=0.0000")

    return 0

if __name__ == '__main__':
    sys.exit(main())