# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 878
# cycle_index: 24
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_universe(conn):
    """Symbols with >=252 daily bars, active stocks only."""
    cur = conn.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        JOIN (
            SELECT symbol_id, COUNT(*) as n_bars
            FROM bars
            WHERE tf = '1d'
            GROUP BY symbol_id
            HAVING n_bars >= 252
        ) b ON s.id = b.symbol_id
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    return {row[0]: row[1] for row in cur.fetchall()}

def get_insider_signals(conn, universe_ids):
    """CEO/CFO open-market purchases >$50k with trade date and filed date."""
    placeholders = ','.join('?' * len(universe_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, tx_ts, filed_ts, value, insider, title
        FROM insider_trades
        WHERE symbol_id IN ({placeholders})
          AND code = 'P'
          AND value > 50000
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY tx_ts
    """, list(universe_ids))
    return cur.fetchall()

def get_8k_filings(conn, universe_ids):
    """Form 8-K filing dates per symbol."""
    placeholders = ','.join('?' * len(universe_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, filed_ts
        FROM filings
        WHERE symbol_id IN ({placeholders})
          AND form = '8-K'
        ORDER BY symbol_id, filed_ts
    """, list(universe_ids))
    rows = cur.fetchall()
    by_symbol = {}
    for sym_id, filed_ts in rows:
        by_symbol.setdefault(sym_id, []).append(filed_ts)
    return by_symbol

def get_daily_bars(conn, universe_ids):
    """Daily close prices for vol calculation."""
    placeholders = ','.join('?' * len(universe_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE symbol_id IN ({placeholders})
          AND tf = '1d'
        ORDER BY symbol_id, ts
    """, list(universe_ids))
    rows = cur.fetchall()
    by_symbol = {}
    for sym_id, ts, close in rows:
        by_symbol.setdefault(sym_id, []).append((ts, close))
    return by_symbol

def compute_realized_vol(prices, window=20):
    """Compute 20-day realized volatility from daily closes."""
    if len(prices) < window + 1:
        return []
    vols = []
    for i in range(window, len(prices)):
        rets = []
        for j in range(i - window + 1, i + 1):
            ret = (prices[j][1] - prices[j-1][1]) / prices[j-1][1]
            rets.append(ret)
        mean_ret = sum(rets) / len(rets)
        var = sum((r - mean_ret) ** 2 for r in rets) / len(rets)
        vol = (var ** 0.5) * (252 ** 0.5)  # annualized
        vols.append((prices[i][0], vol))
    return vols

def compute_median_vol(vols, window=252):
    """Compute rolling 252-day median of realized vol."""
    if len(vols) < window:
        return []
    medians = []
    for i in range(window - 1, len(vols)):
        window_vols = [v[1] for v in vols[i - window + 1:i + 1]]
        window_vols.sort()
        median = window_vols[len(window_vols) // 2]
        medians.append((vols[i][0], median))
    return medians

def has_8k_in_window(filing_dates, tx_ts, window_days=60):
    """Check if any 8-K filing in past window_days calendar days before tx_ts."""
    if not filing_dates:
        return False
    cutoff = tx_ts - window_days * 86400
    for fd in filing_dates:
        if cutoff <= fd <= tx_ts:
            return True
    return False

def get_forward_return(bars, tx_ts, horizon_days=21):
    """Get forward return over horizon_days trading days from tx_ts."""
    # Find the bar at or just after tx_ts (decision bar)
    idx = -1
    for i, (ts, _) in enumerate(bars):
        if ts >= tx_ts:
            idx = i
            break
    if idx == -1 or idx + horizon_days >= len(bars):
        return None
    entry_price = bars[idx][1]
    exit_price = bars[idx + horizon_days][1]
    return (exit_price - entry_price) / entry_price

def main():
    conn = connect()
    
    universe = get_universe(conn)
    universe_ids = list(universe.keys())
    print(f"Universe: {len(universe_ids)} symbols", file=sys.stderr)
    
    insider_signals = get_insider_signals(conn, universe_ids)
    print(f"Insider signals: {len(insider_signals)}", file=sys.stderr)
    
    filings_8k = get_8k_filings(conn, universe_ids)
    print(f"8-K filings: {sum(len(v) for v in filings_8k.values())}", file=sys.stderr)
    
    daily_bars = get_daily_bars(conn, universe_ids)
    print(f"Daily bars loaded for {len(daily_bars)} symbols", file=sys.stderr)
    
    # Precompute vol and median vol per symbol
    vol_data = {}
    median_vol_data = {}
    for sym_id, bars in daily_bars.items():
        vols = compute_realized_vol(bars)
        if vols:
            medians = compute_median_vol(vols)
            if medians:
                vol_data[sym_id] = vols
                median_vol_data[sym_id] = medians
    
    print(f"Vol data for {len(vol_data)} symbols", file=sys.stderr)
    
    # Build decision points
    decisions = []  # (tx_ts, sym_id, fwd_ret, issued)
    
    for sym_id, tx_ts, filed_ts, value, insider, title in insider_signals:
        if sym_id not in vol_data or sym_id not in median_vol_data:
            continue
        if sym_id not in daily_bars:
            continue
        
        # Check vol compression: 20-day vol < 252-day median vol at tx_ts
        vols = vol_data[sym_id]
        medians = median_vol_data[sym_id]
        
        # Find vol at tx_ts
        vol_at_tx = None
        for ts, vol in vols:
            if ts >= tx_ts:
                vol_at_tx = vol
                break
        if vol_at_tx is None:
            continue
        
        # Find median at tx_ts
        median_at_tx = None
        for ts, med in medians:
            if ts >= tx_ts:
                median_at_tx = med
                break
        if median_at_tx is None:
            continue
        
        if vol_at_tx >= median_at_tx:
            continue  # not compressed
        
        # Check 8-K quiet period: no 8-K in past 60 calendar days
        filing_dates = filings_8k.get(sym_id, [])
        if has_8k_in_window(filing_dates, tx_ts, 60):
            continue
        
        # Get forward return
        fwd_ret = get_forward_return(daily_bars[sym_id], tx_ts, 21)
        if fwd_ret is None:
            continue
        
        decisions.append((tx_ts, sym_id, fwd_ret))
    
    print(f"Decision points: {len(decisions)}", file=sys.stderr)
    
    if len(decisions) < 30:
        print("INSUFFICIENT=1")
        return
    
    # Sort by time
    decisions.sort(key=lambda x: x[0])
    
    # Hold out most recent 20% as sealed era
    split_idx = int(len(decisions) * 0.8)
    train_decisions = decisions[:split_idx]
    sealed_decisions = decisions[split_idx:]
    
    # Abstention: require at least 5 signals in rolling 63-day window
    issued_train = []
    for i, (tx_ts, sym_id, fwd_ret) in enumerate(train_decisions):
        window_start = tx_ts - 63 * 86400
        count = sum(1 for j in range(max(0, i-100), i+1) 
                    if train_decisions[j][0] >= window_start)
        if count >= 5:
            issued_train.append((tx_ts, sym_id, fwd_ret))
    
    issued_sealed = []
    for i, (tx_ts, sym_id, fwd_ret) in enumerate(sealed_decisions):
        window_start = tx_ts - 63 * 86400
        # Count in full decisions list (train + sealed up to i)
        all_decisions = train_decisions + sealed_decisions[:i+1]
        count = sum(1 for d in all_decisions if d[0] >= window_start)
        if count >= 5:
            issued_sealed.append((tx_ts, sym_id, fwd_ret))
    
    def compute_metrics(issued):
        if not issued:
            return 0, 0, 0, 0, 0
        hits = sum(1 for _, _, ret in issued if ret > 0)
        precision = hits / len(issued)
        base_rate = sum(1 for _, _, ret in issued if ret > 0) / len(issued)  # same as precision for issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(ts).date() for ts, _, _ in issued))
        # Design effect: 1 + (avg cluster size - 1) * intraclass correlation
        # Approximate: group by day, compute variance of daily counts
        from collections import Counter
        day_counts = Counter(datetime.utcfromtimestamp(ts).date() for ts, _, _ in issued)
        if len(day_counts) > 1:
            mean_count = sum(day_counts.values()) / len(day_counts)
            var_count = sum((c - mean_count) ** 2 for c in day_counts.values()) / len(day_counts)
            deff = 1 + (var_count / mean_count) if mean_count > 0 else 1
        else:
            deff = 1.0
        effective_n = len(issued) / deff if deff > 0 else len(issued)
        return len(issued), precision, base_rate, distinct_days, effective_n
    
    issued_count, precision, base_rate, distinct_days, effective_n = compute_metrics(issued_train)
    sealed_issued, sealed_precision, _, _, _ = compute_metrics(issued_sealed)
    
    opportunities = len(decisions)
    
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()