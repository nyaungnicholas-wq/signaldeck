# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 820
# cycle_index: 16
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def is_officer(title: str) -> bool:
    if not title:
        return False
    t = title.lower()
    return any(kw in t for kw in ('chief executive', 'ceo', 'chief financial', 'cfo', 'president'))

def unix_to_date(ts: int) -> str:
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_unix(date_str: str) -> int:
    return int(datetime.strptime(date_str, '%Y-%m-%d').timestamp())

def get_trading_days(conn, start_ts: int, end_ts: int) -> list:
    """Get list of trading days (unix timestamps at 00:00 UTC) from bars tf='1d'."""
    cur = conn.execute(
        "SELECT DISTINCT ts FROM bars WHERE tf='1d' AND ts >= ? AND ts <= ? ORDER BY ts",
        (start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def get_dollar_volume(conn, symbol_id: int, ts: int) -> float:
    """Get dollar volume (close * volume) for a symbol on a specific day."""
    cur = conn.execute(
        "SELECT close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
        (symbol_id, ts)
    )
    row = cur.fetchone()
    if row and row[0] and row[1]:
        return row[0] * row[1]
    return 0.0

def get_median_dollar_volume_63d(conn, symbol_id: int, as_of_ts: int) -> float:
    """Median dollar volume over 63 trading days before as_of_ts (exclusive)."""
    cur = conn.execute(
        """SELECT close, volume FROM bars 
           WHERE symbol_id=? AND tf='1d' AND ts < ?
           ORDER BY ts DESC LIMIT 63""",
        (symbol_id, as_of_ts)
    )
    vols = [r[0] * r[1] for r in cur.fetchall() if r[0] and r[1]]
    if not vols:
        return 0.0
    vols.sort()
    return vols[len(vols) // 2]

def get_cross_sectional_quartile(conn, as_of_ts: int) -> float:
    """Get the 25th percentile of 63-day median dollar volume across all symbols as of as_of_ts."""
    # Get all symbols with at least 10 days of volume history before as_of_ts
    cur = conn.execute(
        """SELECT symbol_id FROM bars 
           WHERE tf='1d' AND ts < ?
           GROUP BY symbol_id
           HAVING COUNT(*) >= 10""",
        (as_of_ts,)
    )
    symbol_ids = [r[0] for r in cur.fetchall()]
    if not symbol_ids:
        return 0.0
    
    medians = []
    for sid in symbol_ids:
        m = get_median_dollar_volume_63d(conn, sid, as_of_ts)
        if m > 0:
            medians.append(m)
    
    if not medians:
        return 0.0
    medians.sort()
    return medians[len(medians) // 4]  # 25th percentile

def get_forward_return_21d(conn, symbol_id: int, start_ts: int) -> float:
    """Get 21-trading-day forward return from start_ts (inclusive)."""
    # Get the bar at or after start_ts as entry
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts >= ? ORDER BY ts LIMIT 1",
        (symbol_id, start_ts)
    )
    entry = cur.fetchone()
    if not entry:
        return None
    entry_ts, entry_close = entry
    
    # Get the bar 21 trading days later
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts > ? ORDER BY ts LIMIT 21",
        (symbol_id, entry_ts)
    )
    closes = [r[0] for r in cur.fetchall() if r[0]]
    if len(closes) < 21:
        return None
    exit_close = closes[-1]
    return (exit_close - entry_close) / entry_close

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    
    # Get all officer open-market purchases (code='P') with filed_ts
    cur = conn.execute(
        """SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
           FROM insider_trades
           WHERE code='P' AND filed_ts IS NOT NULL
           ORDER BY filed_ts"""
    )
    trades = cur.fetchall()
    
    if not trades:
        print("INSUFFICIENT=1")
        return 0
    
    # Filter for officers
    officer_trades = [t for t in trades if is_officer(t['title'])]
    if not officer_trades:
        print("INSUFFICIENT=1")
        return 0
    
    # Determine date range for cross-sectional quartiles
    filed_dates = sorted(set(t['filed_ts'] for t in officer_trades))
    if len(filed_dates) < 10:
        print("INSUFFICIENT=1")
        return 0
    
    # Precompute cross-sectional 25th percentile for each filed_ts date
    # To speed up, compute for unique dates only
    quartile_cache = {}
    for fd in filed_dates:
        quartile_cache[fd] = get_cross_sectional_quartile(conn, fd)
    
    # Evaluate each trade
    events = []  # (filed_ts, symbol_id, fwd_return, purchase_pct_adv)
    for t in officer_trades:
        filed_ts = t['filed_ts']
        symbol_id = t['symbol_id']
        shares = t['shares']
        price = t['price']
        
        if shares is None or price is None or shares <= 0 or price <= 0:
            continue
        
        purchase_value = shares * price
        
        # Get 63-day median dollar volume for this symbol as of filed_ts
        med_vol = get_median_dollar_volume_63d(conn, symbol_id, filed_ts)
        if med_vol <= 0:
            continue
        
        # Check if in bottom quartile cross-sectionally
        q25 = quartile_cache.get(filed_ts, 0)
        if q25 <= 0 or med_vol > q25:
            continue
        
        # Check if purchase > 1% of median dollar volume
        if purchase_value <= 0.01 * med_vol:
            continue
        
        # Get forward return
        fwd_ret = get_forward_return_21d(conn, symbol_id, filed_ts)
        if fwd_ret is None:
            continue
        
        events.append((filed_ts, symbol_id, fwd_ret, purchase_value / med_vol))
    
    if not events:
        print("INSUFFICIENT=1")
        return 0
    
    # Deduplicate: one observation per (symbol, UTC day)
    # Use filed_ts date (UTC)
    dedup = {}
    for filed_ts, symbol_id, fwd_ret, pct in events:
        day = unix_to_date(filed_ts)
        key = (symbol_id, day)
        if key not in dedup:
            dedup[key] = (filed_ts, symbol_id, fwd_ret, pct)
    
    observations = list(dedup.values())
    observations.sort(key=lambda x: x[0])  # sort by filed_ts
    
    n = len(observations)
    if n < 20:
        print("INSUFFICIENT=1")
        return 0
    
    # Split: most recent 20% as sealed era
    split_idx = int(n * 0.8)
    train_obs = observations[:split_idx]
    sealed_obs = observations[split_idx:]
    
    def compute_metrics(obs_list):
        if not obs_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(obs_list)
        hits = sum(1 for _, _, ret, _ in obs_list if ret > 0)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # base rate of positive class within issued subset
        distinct_days = len(set(unix_to_date(ts) for ts, _, _, _ in obs_list))
        # Design effect: 1 + (avg cluster size - 1) * intra-cluster correlation
        # Approximate: group by day, compute variance inflation
        day_counts = defaultdict(int)
        for ts, _, _, _ in obs_list:
            day_counts[unix_to_date(ts)] += 1
        if day_counts:
            avg_cluster = sum(day_counts.values()) / len(day_counts)
            # Conservative ICC estimate for daily financial returns ~0.1-0.2
            icc = 0.15
            design_effect = 1 + (avg_cluster - 1) * icc
            effective_n = issued / design_effect
        else:
            effective_n = issued * 0.5
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    train_issued, train_hits, train_prec, train_br, train_days, train_en = compute_metrics(train_obs)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_en = compute_metrics(sealed_obs)
    
    # Overall metrics (on full sample for reporting)
    all_issued, all_hits, all_prec, all_br, all_days, all_en = compute_metrics(observations)
    
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={len(officer_trades)}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={all_en:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())