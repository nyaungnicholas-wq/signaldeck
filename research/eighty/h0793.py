# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 792
# cycle_index: 62
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

def get_trading_days(conn):
    """Get all distinct trading days from bars (tf='1d') as sorted list of (date_str, epoch)."""
    cur = conn.execute("""
        SELECT DISTINCT date(ts, 'unixepoch') as d, ts
        FROM bars WHERE tf='1d'
        ORDER BY ts
    """)
    rows = cur.fetchall()
    return [(r[0], r[1]) for r in rows]

def epoch_to_date(epoch):
    return datetime.utcfromtimestamp(epoch).strftime('%Y-%m-%d')

def date_to_epoch(date_str):
    return int(datetime.strptime(date_str, '%Y-%m-%d').timestamp())

def get_symbols_with_bars_since(conn, since_date):
    """Symbols with at least one daily bar on or after since_date."""
    cur = conn.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN bars b ON b.symbol_id = s.id
        WHERE b.tf='1d' AND date(b.ts, 'unixepoch') >= ?
    """, (since_date,))
    return {r[0]: r[1] for r in cur.fetchall()}

def get_shares_outstanding(conn, symbol_ids):
    """Get latest SharesOutstanding per symbol (fetched_at as of decision time)."""
    # We'll fetch all and filter by fetched_at later per decision
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT symbol_id, value, fetched_at
        FROM fundamentals
        WHERE metric='SharesOutstanding' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, fetched_at
    """, list(symbol_ids))
    rows = cur.fetchall()
    by_symbol = defaultdict(list)
    for sid, val, fetched in rows:
        try:
            by_symbol[sid].append((fetched, float(val)))
        except:
            pass
    return by_symbol

def get_market_cap_at(conn, symbol_id, decision_epoch, shares_data):
    """Get market cap at decision time: close price * shares outstanding."""
    # Get close price on decision date (or prior trading day)
    decision_date = epoch_to_date(decision_epoch)
    cur = conn.execute("""
        SELECT close FROM bars
        WHERE symbol_id=? AND tf='1d' AND date(ts, 'unixepoch') <= ?
        ORDER BY ts DESC LIMIT 1
    """, (symbol_id, decision_date))
    row = cur.fetchone()
    if not row:
        return None
    close = row[0]
    # Get shares outstanding known at or before decision
    shares_list = shares_data.get(symbol_id, [])
    shares = None
    for fetched, val in shares_list:
        if fetched <= decision_epoch:
            shares = val
        else:
            break
    if shares is None or shares <= 0:
        return None
    return close * shares

def get_insider_trades(conn, symbol_ids):
    """Get all insider trades for symbols, filtered to CEO/CFO open-market purchases."""
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code='P'
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY symbol_id, tx_ts
    """, list(symbol_ids))
    return cur.fetchall()

def get_news_dates(conn, symbol_ids):
    """Get set of dates with news per symbol."""
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT DISTINCT symbol_id, date(ts, 'unixepoch') as d
        FROM news
        WHERE symbol_id IN ({placeholders})
    """, list(symbol_ids))
    by_symbol = defaultdict(set)
    for sid, d in cur.fetchall():
        by_symbol[sid].add(d)
    return by_symbol

def get_8k_dates(conn, symbol_ids):
    """Get set of dates with 8-K filings per symbol."""
    placeholders = ','.join('?' * len(symbol_ids))
    cur = conn.execute(f"""
        SELECT DISTINCT symbol_id, date(filed_ts, 'unixepoch') as d
        FROM filings
        WHERE symbol_id IN ({placeholders}) AND form='8-K'
    """, list(symbol_ids))
    by_symbol = defaultdict(set)
    for sid, d in cur.fetchall():
        by_symbol[sid].add(d)
    return by_symbol

def get_bars_for_symbol(conn, symbol_id, start_date=None, end_date=None):
    """Get daily bars for a symbol as list of (date_str, epoch, open, high, low, close, volume)."""
    sql = "SELECT date(ts, 'unixepoch'), ts, open, high, low, close, volume FROM bars WHERE symbol_id=? AND tf='1d'"
    params = [symbol_id]
    if start_date:
        sql += " AND date(ts, 'unixepoch') >= ?"
        params.append(start_date)
    if end_date:
        sql += " AND date(ts, 'unixepoch') <= ?"
        params.append(end_date)
    sql += " ORDER BY ts"
    cur = conn.execute(sql, params)
    return cur.fetchall()

def trading_days_between(trading_days, start_epoch, end_epoch):
    """Count trading days between two epochs (inclusive of start, exclusive of end)."""
    start_date = epoch_to_date(start_epoch)
    end_date = epoch_to_date(end_epoch)
    count = 0
    for d, _ in trading_days:
        if d >= start_date and d < end_date:
            count += 1
    return count

def find_nth_trading_day_after(trading_days, start_epoch, n):
    """Find the epoch of the n-th trading day after start_epoch (1-indexed)."""
    start_date = epoch_to_date(start_epoch)
    found_start = False
    count = 0
    for d, epoch in trading_days:
        if not found_start:
            if d >= start_date:
                found_start = True
                count = 1
                if count == n:
                    return epoch
        else:
            count += 1
            if count == n:
                return epoch
    return None

def compute_officer_track_record(trades, trading_days, conn, min_prior=5):
    """Compute historical 21-day win rate for each officer's prior purchases.
    Returns dict: (symbol_id, officer) -> (win_rate, prior_count) for trades up to each point.
    """
    # Group by (symbol_id, officer)
    by_officer = defaultdict(list)
    for t in trades:
        key = (t[1], t[2])  # symbol_id, insider
        by_officer[key].append(t)
    
    track_records = {}
    for key, officer_trades in by_officer.items():
        officer_trades.sort(key=lambda x: x[8])  # tx_ts
        for i, trade in enumerate(officer_trades):
            # Compute win rate on prior trades (indices < i)
            prior_wins = 0
            prior_total = 0
            for j in range(i):
                prior_trade = officer_trades[j]
                tx_ts = prior_trade[8]
                # Get 21-day forward return from tx_ts (trade date) or filed_ts?
                # Track record should be based on same horizon: 21 trading days from trade date
                fwd_epoch = find_nth_trading_day_after(trading_days, tx_ts, 21)
                if fwd_epoch is None:
                    continue
                # Get close at tx_ts and at fwd_epoch
                symbol_id = prior_trade[1]
                cur = conn.execute("""
                    SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?
                """, (symbol_id, tx_ts))
                row1 = cur.fetchone()
                cur = conn.execute("""
                    SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?
                """, (symbol_id, fwd_epoch))
                row2 = cur.fetchone()
                if row1 and row2 and row1[0] > 0:
                    ret = (row2[0] - row1[0]) / row1[0]
                    if ret > 0:
                        prior_wins += 1
                    prior_total += 1
            if prior_total >= min_prior:
                win_rate = prior_wins / prior_total
                track_records[(key, trade[0])] = (win_rate, prior_total)  # keyed by accession
    return track_records

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    
    # 1. Get trading days
    trading_days = get_trading_days(conn)
    if not trading_days:
        print("INSUFFICIENT=1")
        return
    trading_day_set = {d for d, _ in trading_days}
    
    # 2. Universe: symbols with daily bars since 2018-07-26
    since_date = '2018-07-26'
    symbols = get_symbols_with_bars_since(conn, since_date)
    if not symbols:
        print("INSUFFICIENT=1")
        return
    symbol_ids = list(symbols.keys())
    
    # 3. Get shares outstanding data
    shares_data = get_shares_outstanding(conn, symbol_ids)
    
    # 4. Get insider trades (CEO/CFO purchases)
    insider_trades = get_insider_trades(conn, symbol_ids)
    if not insider_trades:
        print("INSUFFICIENT=1")
        return
    
    # 5. Get news dates
    news_dates = get_news_dates(conn, symbol_ids)
    
    # 6. Get 8-K dates
    filing_8k_dates = get_8k_dates(conn, symbol_ids)
    
    # 7. Compute officer track records
    track_records = compute_officer_track_record(insider_trades, trading_days, conn, min_prior=5)
    
    # 8. Process each trade for entry signals
    opportunities = 0
    issued_calls = []  # (decision_epoch, symbol_id, officer, hit)
    
    # Group trades by symbol for volume analysis
    trades_by_symbol = defaultdict(list)
    for t in insider_trades:
        trades_by_symbol[t[1]].append(t)
    
    for symbol_id, trades in trades_by_symbol.items():
        # Get bars for volume/price analysis
        bars = get_bars_for_symbol(conn, symbol_id)
        if len(bars) < 21:  # need at least 20-day avg + forward
            continue
        
        # Build date -> bar map
        bar_map = {d: (epoch, o, h, l, c, v) for d, epoch, o, h, l, c, v in bars}
        bar_dates = sorted(bar_map.keys())
        
        for trade in trades:
            accession, sid, insider, title, code, shares, price, value, tx_ts, filed_ts = trade
            opportunities += 1
            
            # Check track record
            tr_key = ((sid, insider), accession)
            if tr_key not in track_records:
                continue
            win_rate, prior_count = track_records[tr_key]
            if win_rate < 0.55:
                continue
            
            # Check disclosure delay <= 2 trading days
            tx_date = epoch_to_date(tx_ts)
            filed_date = epoch_to_date(filed_ts)
            delay_days = trading_days_between(trading_days, tx_ts, filed_ts)
            if delay_days > 2:
                continue
            
            # Check forced-selling conditions on tx_date
            if tx_date not in bar_map:
                continue
            tx_epoch, tx_open, tx_high, tx_low, tx_close, tx_vol = bar_map[tx_date]
            
            # Price drop >3%: close/open < 0.97
            if tx_open <= 0 or tx_close / tx_open >= 0.97:
                continue
            
            # Volume > 2x 20-day average (prior 20 trading days)
            tx_idx = bar_dates.index(tx_date)
            if tx_idx < 20:
                continue
            prior_vols = [bar_map[bar_dates[tx_idx - k]][5] for k in range(1, 21)]
            avg_vol = sum(prior_vols) / 20
            if tx_vol <= 2 * avg_vol:
                continue
            
            # Zero news on tx_date
            if tx_date in news_dates.get(sid, set()):
                continue
            
            # No 8-K on tx_date
            if tx_date in filing_8k_dates.get(sid, set()):
                continue
            
            # Market cap >= $500M at decision time (filed_ts)
            mcap = get_market_cap_at(conn, sid, filed_ts, shares_data)
            if mcap is None or mcap < 500_000_000:
                continue
            
            # All conditions met - issue call
            # Label: 21-day forward return from filed_ts (decision time)
            fwd_epoch = find_nth_trading_day_after(trading_days, filed_ts, 21)
            if fwd_epoch is None:
                continue
            
            # Get close at filed_ts (or next trading day if filed_ts not a trading day)
            filed_date = epoch_to_date(filed_ts)
            # Find first trading day >= filed_date
            entry_epoch = None
            for d, e in trading_days:
                if d >= filed_date:
                    entry_epoch = e
                    break
            if entry_epoch is None:
                continue
            
            cur = conn.execute("SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (sid, entry_epoch))
            row1 = cur.fetchone()
            cur = conn.execute("SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (sid, fwd_epoch))
            row2 = cur.fetchone()
            if not row1 or not row2 or row1[0] <= 0:
                continue
            
            ret = (row2[0] - row1[0]) / row1[0]
            hit = 1 if ret > 0 else 0
            
            issued_calls.append((filed_ts, sid, insider, hit, filed_date))
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    # 9. Hold out most recent 20% as sealed era
    issued_calls.sort(key=lambda x: x[0])  # by decision_epoch
    n_total = len(issued_calls)
    n_sealed = max(1, int(n_total * 0.2))
    main_calls = issued_calls[:-n_sealed]
    sealed_calls = issued_calls[-n_sealed:]
    
    # 10. Compute metrics
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c[3] for c in calls)
        precision = hits / issued if issued > 0 else 0
        distinct_days = len(set(c[4] for c in calls))
        # Base rate within issued subset
        base_rate = hits / issued if issued > 0 else 0
        # Design effect: simple clustering by day
        # Count calls per day
        day_counts = defaultdict(int)
        for c in calls:
            day_counts[c[4]] += 1
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Approximate: effective_n = issued / (1 + (avg_cluster_size - 1) * 0.5)
        # Using conservative ICC=0.5 for same-day calls
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        design_effect = 1 + (avg_cluster - 1) * 0.5
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    main_issued, main_hits, main_precision, main_base_rate, main_distinct_days, main_effective_n = compute_metrics(main_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)
    
    # Overall metrics (for reporting)
    all_issued, all_hits, all_precision, all_base_rate, all_distinct_days, all_effective_n = compute_metrics(issued_calls)
    
    # Print required lines
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()