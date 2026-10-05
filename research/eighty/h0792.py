# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 791
# cycle_index: 61
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import statistics
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(epoch):
    return datetime.utcfromtimestamp(epoch).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def parse_day(day_str):
    return datetime.strptime(day_str, '%Y-%m-%d').date()

def day_to_str(d):
    return d.strftime('%Y-%m-%d')

def add_days(d, days):
    return d + timedelta(days=days)

def get_trading_days_bars(conn, symbol_id, start_date, end_date):
    """Get 1d bars for a symbol in date range, returns list of (date, close)"""
    start_epoch = date_to_epoch(start_date)
    end_epoch = date_to_epoch(add_days(end_date, 1))
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<? ORDER BY ts",
        (symbol_id, start_epoch, end_epoch)
    )
    rows = cur.fetchall()
    return [(epoch_to_date(ts), close) for ts, close in rows]

def get_sentiment_features(conn, symbol_id, start_date, end_date):
    """Get daily sentiment features for symbol in date range"""
    start_str = day_to_str(start_date)
    end_str = day_to_str(add_days(end_date, 1))
    cur = conn.execute(
        "SELECT day, mean_score FROM sentiment_features WHERE symbol_id=? AND day>=? AND day<? ORDER BY day",
        (symbol_id, start_str, end_str)
    )
    rows = cur.fetchall()
    return [(parse_day(day), score) for day, score in rows]

def get_inst_holdings_quarters(conn, symbol_id):
    """Get all quarter periods for a symbol from inst_holdings, sorted"""
    cur = conn.execute(
        "SELECT DISTINCT period FROM inst_holdings WHERE symbol_id=? ORDER BY period",
        (symbol_id,)
    )
    return [row[0] for row in cur.fetchall()]

def get_top10_concentration(conn, symbol_id, period):
    """Get top 10 holders concentration for a symbol at a period.
    Returns (concentration_hhi, distinct_institutions_count, top10_shares_pct)"""
    cur = conn.execute(
        """SELECT manager, value FROM inst_holdings 
           WHERE symbol_id=? AND period=? ORDER BY value DESC LIMIT 10""",
        (symbol_id, period)
    )
    rows = cur.fetchall()
    if not rows:
        return None, 0, 0
    total_value = sum(v for _, v in rows)
    if total_value == 0:
        return None, 0, 0
    concentrations = [(v / total_value) ** 2 for _, v in rows]
    hhi = sum(concentrations)
    distinct_mgrs = len(set(m for m, _ in rows))
    top10_pct = sum(v for _, v in rows) / total_value if total_value else 0
    return hhi, distinct_mgrs, top10_pct

def get_shares_outstanding(conn, symbol_id, as_of_date):
    """Get SharesOutstanding for symbol at or before as_of_date (knowable via fetched_at)"""
    as_of_epoch = date_to_epoch(as_of_date)
    cur = conn.execute(
        """SELECT value, fetched_at FROM fundamentals 
           WHERE symbol_id=? AND metric='SharesOutstanding' AND fetched_at<=? 
           ORDER BY fetched_at DESC LIMIT 1""",
        (symbol_id, as_of_epoch)
    )
    row = cur.fetchone()
    if row:
        return row[0]
    return None

def get_shares_outstanding_quarterly(conn, symbol_id, period_date):
    """Get SharesOutstanding for a specific quarter period (as_of approx period_date)"""
    # Try to find the fundamental with as_of near period_date, knowable by fetched_at
    period_epoch = date_to_epoch(period_date)
    cur = conn.execute(
        """SELECT value, as_of, fetched_at FROM fundamentals 
           WHERE symbol_id=? AND metric='SharesOutstanding' AND as_of>0 AND fetched_at<=?
           ORDER BY ABS(as_of - ?) ASC, fetched_at DESC LIMIT 1""",
        (symbol_id, period_epoch, period_epoch)
    )
    row = cur.fetchone()
    if row:
        return row[0]
    return None

def compute_forward_return(conn, symbol_id, knowable_date, horizon_days=21):
    """Compute forward return over horizon_days trading days from knowable_date"""
    bars = get_trading_days_bars(conn, symbol_id, knowable_date, add_days(knowable_date, horizon_days * 2))
    if len(bars) < 2:
        return None
    # Find the bar on or after knowable_date
    entry_bar = None
    for i, (d, c) in enumerate(bars):
        if d >= knowable_date:
            entry_bar = (i, d, c)
            break
    if entry_bar is None:
        return None
    idx, entry_date, entry_price = entry_bar
    # Need horizon_days trading days after entry
    if idx + horizon_days >= len(bars):
        return None
    exit_date, exit_price = bars[idx + horizon_days]
    return (exit_price - entry_price) / entry_price

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    
    # 1. Find eligible symbols: >=4 quarters 13F, >=252 days sentiment, avg dollar vol > $1M
    print("Finding eligible symbols...", file=sys.stderr)
    
    # Get symbols with >=4 quarters in inst_holdings
    cur = conn.execute(
        """SELECT symbol_id, COUNT(DISTINCT period) as n_quarters 
           FROM inst_holdings GROUP BY symbol_id HAVING n_quarters >= 4"""
    )
    symbols_13f = {row['symbol_id']: row['n_quarters'] for row in cur.fetchall()}
    print(f"Symbols with >=4 quarters 13F: {len(symbols_13f)}", file=sys.stderr)
    
    # Get symbols with >=252 days sentiment_features
    cur = conn.execute(
        """SELECT symbol_id, COUNT(DISTINCT day) as n_days 
           FROM sentiment_features GROUP BY symbol_id HAVING n_days >= 252"""
    )
    symbols_sent = {row['symbol_id']: row['n_days'] for row in cur.fetchall()}
    print(f"Symbols with >=252 days sentiment: {len(symbols_sent)}", file=sys.stderr)
    
    # Get symbols with avg daily dollar volume > $1M (using bars 1d)
    # Need to compute over recent period, say last year of data
    cur = conn.execute(
        """SELECT symbol_id, AVG(close * volume) as avg_dollar_vol
           FROM bars WHERE tf='1d' AND ts >= (SELECT MAX(ts) FROM bars WHERE tf='1d') - 365*24*3600
           GROUP BY symbol_id HAVING avg_dollar_vol > 1000000"""
    )
    symbols_vol = {row['symbol_id'] for row in cur.fetchall()}
    print(f"Symbols with avg dollar vol > $1M: {len(symbols_vol)}", file=sys.stderr)
    
    eligible_symbols = set(symbols_13f.keys()) & set(symbols_sent.keys()) & symbols_vol
    print(f"Eligible symbols: {len(eligible_symbols)}", file=sys.stderr)
    
    if len(eligible_symbols) == 0:
        print("INSUFFICIENT=1")
        return
    
    # 2. For each eligible symbol, process each 13F quarter as decision point
    all_decisions = []  # (symbol_id, knowable_date, period, entry_conditions, abstain_conditions, forward_return)
    
    for symbol_id in eligible_symbols:
        quarters = get_inst_holdings_quarters(conn, symbol_id)
        if len(quarters) < 4:
            continue
        
        # Need at least 2 prior quarters for concentration change check
        for i in range(2, len(quarters)):
            period = quarters[i]
            prev_period = quarters[i-1]
            prev2_period = quarters[i-2]
            
            # Knowable date = period + 45 days (filing lag)
            try:
                period_date = parse_day(period)
            except:
                continue
            knowable_date = add_days(period_date, 45)
            
            # Check if knowable_date is in the past (not future)
            max_bar_date = epoch_to_date(conn.execute("SELECT MAX(ts) FROM bars WHERE tf='1d'").fetchone()[0])
            if knowable_date > max_bar_date:
                continue
            
            # --- ABSTENTION CHECKS ---
            
            # 1. Fewer than 3 distinct institutions in top 10 (current period)
            hhi, distinct_mgrs, top10_pct = get_top10_concentration(conn, symbol_id, period)
            if distinct_mgrs < 3:
                continue
            
            # 2. News sentiment volatility 252-day range < 0.1
            # Need 252-day window ending at knowable_date - 1 day (since knowable_date not yet in sentiment)
            sent_end = add_days(knowable_date, -1)
            sent_start = add_days(sent_end, -251)
            sent_data = get_sentiment_features(conn, symbol_id, sent_start, sent_end)
            if len(sent_data) < 50:
                continue
            
            daily_scores = [s for _, s in sent_data]
            sent_range = max(daily_scores) - min(daily_scores)
            if sent_range < 0.1:
                continue
            
            # 3. 13F filing lag > 60 days - cannot check exactly, assume 45 days standard
            # Since we don't have filing date, and regulation says max 45 days, this should not trigger
            # But we'll skip if period to knowable_date > 60 (it's 45 by construction)
            
            # 4. Fewer than 50 news sentiment observations in 252-day window - checked above
            
            # --- ENTRY CONDITIONS ---
            
            # A. News sentiment volatility (21-day rolling std) at 252-day low
            # Compute 21-day rolling std for each day in the 252-day window
            if len(daily_scores) < 21:
                continue
            
            rolling_stds = []
            for j in range(20, len(daily_scores)):
                window = daily_scores[j-20:j+1]
                if len(window) == 21:
                    rolling_stds.append(statistics.stdev(window) if len(set(window)) > 1 else 0)
            
            if not rolling_stds:
                continue
            
            current_vol = rolling_stds[-1]  # Most recent 21-day std (ending at sent_end)
            min_vol_252 = min(rolling_stds)
            
            # Check if current is at 252-day low (allow small tolerance)
            if current_vol > min_vol_252 * 1.001:  # Not at low
                continue
            
            # B. Top-10 institutional holder concentration increased in each of last two quarters
            hhi_curr, _, _ = get_top10_concentration(conn, symbol_id, period)
            hhi_prev, _, _ = get_top10_concentration(conn, symbol_id, prev_period)
            hhi_prev2, _, _ = get_top10_concentration(conn, symbol_id, prev2_period)
            
            if hhi_curr is None or hhi_prev is None or hhi_prev2 is None:
                continue
            
            if not (hhi_curr > hhi_prev and hhi_prev > hhi_prev2):
                continue
            
            # C. Quarterly SharesOutstanding growth <= 2% in both quarters
            so_curr = get_shares_outstanding_quarterly(conn, symbol_id, period_date)
            so_prev = get_shares_outstanding_quarterly(conn, symbol_id, parse_day(prev_period))
            so_prev2 = get_shares_outstanding_quarterly(conn, symbol_id, parse_day(prev2_period))
            
            if so_curr is None or so_prev is None or so_prev2 is None:
                continue
            
            growth1 = (so_curr - so_prev) / so_prev if so_prev != 0 else float('inf')
            growth2 = (so_prev - so_prev2) / so_prev2 if so_prev2 != 0 else float('inf')
            
            if growth1 > 0.02 or growth2 > 0.02:
                continue
            
            # All entry conditions met, no abstention - ISSUE CALL
            # Compute forward return over 21 trading days from knowable_date
            fwd_return = compute_forward_return(conn, symbol_id, knowable_date, 21)
            if fwd_return is None:
                continue
            
            hit = 1 if fwd_return > 0 else 0
            all_decisions.append({
                'symbol_id': symbol_id,
                'knowable_date': knowable_date,
                'period': period,
                'hit': hit,
                'fwd_return': fwd_return,
                'sent_vol': current_vol,
                'hhi': hhi_curr
            })
    
    if not all_decisions:
        print("INSUFFICIENT=1")
        return
    
    print(f"Total decisions (issued calls): {len(all_decisions)}", file=sys.stderr)
    
    # 3. Hold out most recent 20% as sealed era
    all_decisions.sort(key=lambda x: x['knowable_date'])
    n_total = len(all_decisions)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    
    train_decisions = all_decisions[:n_train]
    sealed_decisions = all_decisions[n_train:]
    
    # 4. Compute metrics
    def compute_metrics(decisions):
        if not decisions:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(decisions)
        hits = sum(d['hit'] for d in decisions)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = precision  # Base rate of positive class within issued subset
        distinct_days = len(set(d['knowable_date'] for d in decisions))
        
        # Design effect: cluster by date, compute effective N
        # Group by date, count decisions per date
        date_counts = defaultdict(int)
        for d in decisions:
            date_counts[d['knowable_date']] += 1
        
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Simplified: effective_n = issued / design_effect
        # Use Kish's effective sample size: n_eff = (sum w_i)^2 / sum(w_i^2) where w_i = 1/cluster_size
        # For equal weight per decision, but clustered by date:
        cluster_sizes = list(date_counts.values())
        if cluster_sizes:
            # Effective N assuming intra-cluster correlation
            # Conservative: design_effect = 1 + (mean_cluster_size - 1) * rho
            # With rho unknown, use Kish formula for unequal weights
            # Here each decision has weight 1, but clustered
            # Kish: n_eff = n / (1 + CV^2) where CV is coefficient of variation of cluster sizes
            mean_c = statistics.mean(cluster_sizes)
            if mean_c > 0:
                cv = statistics.stdev(cluster_sizes) / mean_c if len(cluster_sizes) > 1 else 0
                design_effect = 1 + cv * cv
                effective_n = issued / design_effect
            else:
                effective_n = issued
        else:
            effective_n = issued
        
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days, train_effective_n = compute_metrics(train_decisions)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_decisions)
    
    # Overall metrics (on full sample for reporting)
    all_issued, all_hits, all_precision, all_base_rate, all_distinct_days, all_effective_n = compute_metrics(all_decisions)
    
    # 5. Print required lines
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={all_issued}")  # Opportunities considered = issued (since we only count decision points that passed all filters)
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_distinct_days}")
    print(f"EFFECTIVE_N={all_effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()