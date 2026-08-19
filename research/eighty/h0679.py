# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 678
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    con = connect()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # 1. Universe: symbols with >=4 quarters revenue data, >=252 days sentiment, >=1 officer trade in prior 252d
    # Check fundamentals revenue data first
    cur.execute("""
        SELECT symbol_id, COUNT(*) as n_qtr
        FROM fundamentals
        WHERE metric = 'Revenues' AND as_of > 0
        GROUP BY symbol_id
        HAVING n_qtr >= 4
    """)
    rev_symbols = {row['symbol_id'] for row in cur.fetchall()}
    if not rev_symbols:
        print("INSUFFICIENT=1")
        return

    # Sentiment history >=252 days
    cur.execute("""
        SELECT symbol_id, COUNT(*) as n_days
        FROM sentiment_features
        GROUP BY symbol_id
        HAVING n_days >= 252
    """)
    sent_symbols = {row['symbol_id'] for row in cur.fetchall()}

    # Officer trades in last 252 days (from max date in insider_trades)
    cur.execute("SELECT MAX(filed_ts) as max_filed FROM insider_trades")
    max_filed = cur.fetchone()['max_filed']
    cutoff_ts = max_filed - 252 * 86400
    cur.execute("""
        SELECT DISTINCT symbol_id
        FROM insider_trades
        WHERE filed_ts >= ? AND (title LIKE '%CEO%' OR title LIKE '%CFO%') AND code = 'P'
    """, (cutoff_ts,))
    officer_symbols = {row['symbol_id'] for row in cur.fetchall()}

    universe = rev_symbols & sent_symbols & officer_symbols
    if not universe:
        print("INSUFFICIENT=1")
        return

    # 2. Load sentiment_features for universe: compute 21-day rolling vol of mean_score
    placeholders = ','.join('?' * len(universe))
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, tuple(universe))
    
    sent_by_sym = defaultdict(list)
    for row in cur.fetchall():
        sent_by_sym[row['symbol_id']].append((str_to_date(row['day']), row['mean_score']))

    # Compute 21-day rolling std for each symbol
    sent_vol = {}  # symbol_id -> list of (date, vol_21d)
    for sym, series in sent_by_sym.items():
        if len(series) < 21:
            continue
        vols = []
        for i in range(20, len(series)):
            window = [v for _, v in series[i-20:i+1]]
            mean = sum(window) / len(window)
            var = sum((x - mean) ** 2 for x in window) / len(window)
            vol = math.sqrt(var)
            vols.append((series[i][0], vol))
        sent_vol[sym] = vols

    # 3. Load fundamentals revenue for universe: get quarterly revenues sorted by as_of
    cur.execute(f"""
        SELECT symbol_id, as_of, value
        FROM fundamentals
        WHERE symbol_id IN ({placeholders}) AND metric = 'Revenues' AND as_of > 0
        ORDER BY symbol_id, as_of
    """, tuple(universe))
    
    rev_by_sym = defaultdict(list)
    for row in cur.fetchall():
        rev_by_sym[row['symbol_id']].append((row['as_of'], row['value']))

    # Compute QoQ growth acceleration: need 3+ consecutive quarters of accelerating growth
    # Growth rate g_t = (rev_t - rev_{t-1}) / rev_{t-1}
    # Acceleration: g_t > g_{t-1} > g_{t-2} (3 consecutive increases)
    rev_accel = {}  # symbol_id -> set of as_of dates where condition holds (at that quarter end)
    for sym, series in rev_by_sym.items():
        if len(series) < 4:
            continue
        growths = []
        for i in range(1, len(series)):
            prev = series[i-1][1]
            curr = series[i][1]
            if prev > 0:
                g = (curr - prev) / prev
            else:
                g = None
            growths.append((series[i][0], g))
        # Check 3 consecutive accelerations
        accel_dates = set()
        for i in range(3, len(growths)):
            g3, g2, g1 = growths[i-3][1], growths[i-2][1], growths[i-1][1]
            if all(v is not None for v in (g3, g2, g1)) and g1 > g2 > g3:
                accel_dates.add(growths[i-1][0])  # acceleration holds as of this quarter end
        if accel_dates:
            rev_accel[sym] = accel_dates

    # 4. Load officer insider trades for universe
    cur.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code = 'P' AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
        ORDER BY symbol_id, insider, filed_ts
    """, tuple(universe))
    
    trades_by_sym = defaultdict(list)
    for row in cur.fetchall():
        trades_by_sym[row['symbol_id']].append(dict(row))

    # 5. Load bars for universe (1d) for volatility and forward returns
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE symbol_id IN ({placeholders}) AND tf = '1d'
        ORDER BY symbol_id, ts
    """, tuple(universe))
    
    bars_by_sym = defaultdict(list)
    for row in cur.fetchall():
        bars_by_sym[row['symbol_id']].append((row['ts'], row['close']))

    # Compute 21-day realized volatility for each symbol (rolling std of daily returns)
    bar_vol = {}  # symbol_id -> list of (ts, vol_21d)
    for sym, series in bars_by_sym.items():
        if len(series) < 22:
            continue
        vols = []
        for i in range(21, len(series)):
            rets = []
            for j in range(i-20, i+1):
                p0 = series[j-1][1]
                p1 = series[j][1]
                if p0 > 0:
                    rets.append(math.log(p1 / p0))
            if len(rets) == 21:
                mean = sum(rets) / 21
                var = sum((r - mean) ** 2 for r in rets) / 21
                vol = math.sqrt(var) * math.sqrt(252)  # annualized
                vols.append((series[i][0], vol))
        bar_vol[sym] = vols

    # 6. For each officer, compute 90th percentile disclosure delay and purchase size history
    officer_stats = {}  # (symbol_id, insider) -> {'delays': [], 'sizes': [], 'delay_p90': , 'size_tercile': }
    for sym, trades in trades_by_sym.items():
        by_insider = defaultdict(list)
        for t in trades:
            by_insider[t['insider']].append(t)
        for insider, tlist in by_insider.items():
            delays = [t['filed_ts'] - t['tx_ts'] for t in tlist]
            sizes = [t['value'] for t in tlist]
            delays.sort()
            sizes.sort()
            p90_idx = max(0, int(0.9 * len(delays)) - 1)
            tercile_idx = max(0, int(2/3 * len(sizes)) - 1)
            officer_stats[(sym, insider)] = {
                'delay_p90': delays[p90_idx] if delays else 0,
                'size_tercile': sizes[tercile_idx] if sizes else 0,
                'delays': delays,
                'sizes': sizes
            }

    # 7. Find entry points
    entries = []  # (decision_ts, symbol_id, filed_ts, entry_price, forward_return)
    
    for sym in universe:
        if sym not in sent_vol or sym not in rev_accel or sym not in trades_by_sym or sym not in bar_vol:
            continue
        
        sent_series = sent_vol[sym]  # (date, vol)
        accel_dates = rev_accel[sym]  # set of as_of (epoch)
        trades = trades_by_sym[sym]
        bars = bars_by_sym[sym]
        bar_v = bar_vol[sym]
        
        # Build lookup for bar_vol by ts
        bar_vol_map = {ts: vol for ts, vol in bar_v}
        bar_close_map = {ts: close for ts, close in bars}
        bar_ts_list = [ts for ts, _ in bars]
        
        # For sentiment vol condition: need to check if over prior 63 days, vol fell from top quartile to bottom quartile
        # Compute historical quartiles for this symbol's 21-day vol
        all_vols = [v for _, v in sent_series]
        if len(all_vols) < 63:
            continue
        all_vols_sorted = sorted(all_vols)
        q1 = all_vols_sorted[len(all_vols_sorted) // 4]
        q3 = all_vols_sorted[3 * len(all_vols_sorted) // 4]
        
        # For bar vol top decile
        all_bar_vols = [v for _, v in bar_v]
        if len(all_bar_vols) < 10:
            continue
        bar_vol_sorted = sorted(all_bar_vols)
        bar_vol_p90 = bar_vol_sorted[9 * len(bar_vol_sorted) // 10]
        
        # Process each trade
        for t in trades:
            filed_ts = t['filed_ts']
            tx_ts = t['tx_ts']
            insider = t['insider']
            value = t['value']
            
            # ABSTAIN: disclosure delay exceeds officer's 90th percentile
            delay = filed_ts - tx_ts
            stats = officer_stats.get((sym, insider))
            if not stats or delay > stats['delay_p90']:
                continue
            
            # ABSTAIN: purchase size not in top tercile of officer's history
            if value < stats['size_tercile']:
                continue
            
            # ABSTAIN: stock's 21-day realized vol in top decile at decision time
            # Find bar_vol at or before filed_ts
            decision_vol = None
            for ts, vol in bar_v:
                if ts <= filed_ts:
                    decision_vol = vol
                else:
                    break
            if decision_vol is not None and decision_vol > bar_vol_p90:
                continue
            
            # Condition (a): sentiment vol fell from top quartile to bottom quartile over prior 63 days
            # Find sentiment vol at filed_ts date
            filed_date = epoch_to_date(filed_ts)
            sent_vol_at = None
            sent_vol_63d_ago = None
            for d, v in sent_series:
                if d <= filed_date:
                    sent_vol_at = v
                if d <= filed_date - timedelta(days=63):
                    sent_vol_63d_ago = v
            if sent_vol_at is None or sent_vol_63d_ago is None:
                continue
            if not (sent_vol_63d_ago >= q3 and sent_vol_at <= q1):
                continue
            
            # Condition (b): revenue growth accelerated for 3+ quarters as of decision time
            # Need latest revenue quarter as_of <= filed_ts
            latest_accel = None
            for as_of in accel_dates:
                if as_of <= filed_ts:
                    if latest_accel is None or as_of > latest_accel:
                        latest_accel = as_of
            if latest_accel is None:
                continue
            
            # All conditions met - this is an issued call
            # Find entry price: next bar close after filed_ts (or at filed_ts if market open)
            entry_price = None
            entry_idx = None
            for i, (ts, close) in enumerate(bars):
                if ts >= filed_ts:
                    entry_price = close
                    entry_idx = i
                    break
            if entry_price is None or entry_idx is None:
                continue
            
            # Compute 21-day forward return (21 trading days)
            if entry_idx + 21 >= len(bars):
                continue
            exit_price = bars[entry_idx + 21][1]
            fwd_return = (exit_price - entry_price) / entry_price
            hit = 1 if fwd_return > 0 else 0
            
            entries.append({
                'filed_ts': filed_ts,
                'symbol_id': sym,
                'hit': hit,
                'date': filed_date
            })

    if not entries:
        print("INSUFFICIENT=1")
        return

    # 8. Split into sealed era (most recent 20% of entries by date)
    entries.sort(key=lambda x: x['filed_ts'])
    n = len(entries)
    split_idx = int(n * 0.8)
    train_entries = entries[:split_idx]
    sealed_entries = entries[split_idx:]

    def compute_metrics(entries_list):
        if not entries_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(entries_list)
        hits = sum(e['hit'] for e in entries_list)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of positive class in issued subset
        distinct_days = len(set(e['date'] for e in entries_list))
        # Design effect: 1 + (avg_cluster_size - 1) * rho, approximate with clustering by day
        # Effective N = issued / design_effect
        # Simple approximation: design_effect = issued / distinct_days (if clustered)
        if distinct_days > 0:
            design_effect = issued / distinct_days
            effective_n = issued / design_effect
        else:
            effective_n = 0.0
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_dd, train_en = compute_metrics(train_entries)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_dd, sealed_en = compute_metrics(sealed_entries)

    # Opportunities: count of decision points considered (officer trades that passed universe filters)
    # This is the number of officer trades we evaluated
    opportunities = 0
    for sym in universe:
        if sym in trades_by_sym:
            opportunities += len(trades_by_sym[sym])

    # Print results for full sample (train + sealed combined)
    all_entries = train_entries + sealed_entries
    issued, hits, prec, br, dd, en = compute_metrics(all_entries)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={prec:.6f}")
    print(f"BASE_RATE={br:.6f}")
    print(f"DISTINCT_DAYS={dd}")
    print(f"EFFECTIVE_N={en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()