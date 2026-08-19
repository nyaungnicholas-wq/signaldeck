# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 716
# cycle_index: 43
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # MECHANISM: When insiders make open-market purchases during periods of zero 
    # insider selling activity (no open-market sales by any insider at the company 
    # for 90 calendar days), the buying signal is amplified because it reflects 
    # unanimous internal conviction rather than divergent views.
    #
    # HORIZON: 21 trading days (forward return from bars tf='1d')
    # UNIVERSE: Symbols with insider_trades and bars(tf='1d') coverage, >=252 daily bars
    # ENTRY: On filed_ts of open-market purchase (code='P'), if zero sales (code='S') 
    #        for that symbol in prior 90 calendar days (using filed_ts), issue long call
    # ABSTAIN: If any sale in prior 90 days, insufficient bars for 21-day forward return,
    #          or fewer than 5 prior insider trades for context
    # CLAIM: Precision exceeds base rate by >=5pp with >=30 independent observations, >=10 distinct days

    # Get all symbols with sufficient daily bars
    cur.execute("""
        SELECT symbol_id, COUNT(*) as n_bars
        FROM bars
        WHERE tf = '1d'
        GROUP BY symbol_id
        HAVING n_bars >= 252
    """)
    eligible_symbols = {row['symbol_id'] for row in cur.fetchall()}
    if not eligible_symbols:
        print("INSUFFICIENT=1")
        return 0

    placeholders = ','.join('?' * len(eligible_symbols))
    
    # Get all insider trades for eligible symbols, ordered by filed_ts
    cur.execute(f"""
        SELECT symbol_id, insider, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
        ORDER BY filed_ts
    """, list(eligible_symbols))
    all_trades = cur.fetchall()

    # Build per-symbol trade lists
    from collections import defaultdict
    trades_by_symbol = defaultdict(list)
    for t in all_trades:
        trades_by_symbol[t['symbol_id']].append(t)

    # Get daily bars for forward return calculation
    # We need close prices for entry day and entry_day + 21 trading days
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, list(eligible_symbols))
    bars_rows = cur.fetchall()

    # Organize bars by symbol_id -> list of (ts, close)
    bars_by_symbol = defaultdict(list)
    for b in bars_rows:
        bars_by_symbol[b['symbol_id']].append((b['ts'], b['close']))

    # For each symbol, build a map from ts to index for fast lookup
    bars_index = {}
    for sym, blist in bars_by_symbol.items():
        bars_index[sym] = {ts: i for i, (ts, _) in enumerate(blist)}

    # Generate signals
    signals = []  # (filed_ts, symbol_id, entry_ts, fwd_return)
    
    for sym, trades in trades_by_symbol.items():
        if sym not in bars_index:
            continue
        bmap = bars_index[sym]
        blist = bars_by_symbol[sym]
        
        # Track sales for 90-day window
        sales_filed = []  # list of filed_ts for code='S'
        
        for i, t in enumerate(trades):
            filed = t['filed_ts']
            code = t['code']
            
            # Maintain 90-day sales window
            cutoff = filed - 90 * 86400
            sales_filed = [s for s in sales_filed if s >= cutoff]
            
            if code == 'P':
                # Check entry conditions
                if len(sales_filed) == 0 and i >= 5:  # no sales in 90d, at least 5 prior trades context
                    # Find entry bar: first bar with ts >= filed_ts (next trading day)
                    entry_idx = None
                    for idx, (ts, _) in enumerate(blist):
                        if ts >= filed:
                            entry_idx = idx
                            break
                    if entry_idx is None:
                        continue
                    # Need 21 trading days forward
                    if entry_idx + 21 >= len(blist):
                        continue
                    entry_close = blist[entry_idx][1]
                    exit_close = blist[entry_idx + 21][1]
                    fwd_return = (exit_close - entry_close) / entry_close
                    signals.append((filed, sym, blist[entry_idx][0], fwd_return))
            
            elif code == 'S':
                sales_filed.append(filed)

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # Sort by filed_ts
    signals.sort(key=lambda x: x[0])
    
    # Hold out most recent 20% as sealed era
    n_total = len(signals)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    
    train_signals = signals[:n_train]
    sealed_signals = signals[n_train:]

    def compute_metrics(sig_list):
        if not sig_list:
            return None
        issued = len(sig_list)
        hits = sum(1 for _, _, _, r in sig_list if r > 0)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate within issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(f).date() for f, _, _, _ in sig_list))
        # Design effect: cluster by day, effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Simplified: use day-clustering design effect
        day_counts = defaultdict(int)
        for f, _, _, _ in sig_list:
            day_counts[datetime.utcfromtimestamp(f).date()] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        # Conservative rho = 0.5 for same-day correlation
        deff = 1 + (avg_cluster - 1) * 0.5
        effective_n = issued / deff if deff > 0 else issued
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_m = compute_metrics(train_signals)
    sealed_m = compute_metrics(sealed_signals)

    if not train_m or train_m['issued'] < 30 or train_m['distinct_days'] < 10:
        print("INSUFFICIENT=1")
        return 0

    # Output required lines
    print(f"ISSUED={train_m['issued']}")
    print(f"OPPORTUNITIES={n_train}")  # decision points considered in train
    print(f"PRECISION={train_m['precision']:.6f}")
    print(f"BASE_RATE={train_m['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_m['distinct_days']}")
    print(f"EFFECTIVE_N={train_m['effective_n']:.2f}")
    if sealed_m:
        print(f"SEALED_PRECISION={sealed_m['precision']:.6f}")
    else:
        print("SEALED_PRECISION=0.000000")

    return 0

if __name__ == '__main__':
    sys.exit(main())