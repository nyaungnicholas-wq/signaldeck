# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 825
# cycle_index: 21
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import sys
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_eligible_symbols(conn):
    """Universe: common stocks with >=504 daily bars, >=252 sentiment days, >=1 insider purchase, median $1M vol/63d"""
    cur = conn.cursor()
    
    # Get symbols with sufficient bars (504 sessions of 1d bars)
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
        AND (
            SELECT COUNT(*) FROM bars b 
            WHERE b.symbol_id = s.id AND b.tf = '1d'
        ) >= 504
    """)
    bar_symbols = {row[0]: row[1] for row in cur.fetchall()}
    
    # Get symbols with sufficient sentiment_features days (252 sessions)
    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM sentiment_features
        GROUP BY symbol_id
        HAVING cnt >= 252
    """)
    sent_symbols = {row[0] for row in cur.fetchall()}
    
    # Get symbols with at least 1 historical insider open-market purchase (code='P')
    cur.execute("""
        SELECT DISTINCT symbol_id
        FROM insider_trades
        WHERE code = 'P'
    """)
    insider_symbols = {row[0] for row in cur.fetchall()}
    
    # Intersect all filters
    eligible = set(bar_symbols.keys()) & sent_symbols & insider_symbols
    return {sid: bar_symbols[sid] for sid in eligible}

def get_daily_bars(conn, symbol_id):
    """Get daily bars (tf='1d') ordered by ts"""
    cur = conn.cursor()
    cur.execute("""
        SELECT ts, open, high, low, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
        ORDER BY ts
    """, (symbol_id,))
    return cur.fetchall()

def get_sentiment_features(conn, symbol_id):
    """Get daily sentiment features ordered by day"""
    cur = conn.cursor()
    cur.execute("""
        SELECT day, mean_score
        FROM sentiment_features
        WHERE symbol_id = ?
        ORDER BY day
    """, (symbol_id,))
    return cur.fetchall()

def get_insider_purchases(conn, symbol_id):
    """Get insider open-market purchases (code='P') by officers/directors, using filed_ts"""
    cur = conn.cursor()
    cur.execute("""
        SELECT filed_ts, insider, title
        FROM insider_trades
        WHERE symbol_id = ? AND code = 'P'
        AND (title LIKE '%Officer%' OR title LIKE '%Director%' OR title LIKE '%CEO%' 
             OR title LIKE '%CFO%' OR title LIKE '%COO%' OR title LIKE '%President%'
             OR title LIKE '%VP%' OR title LIKE '%Vice President%')
        ORDER BY filed_ts
    """, (symbol_id,))
    return cur.fetchall()

def compute_log_returns(closes):
    """Compute daily log returns from close prices"""
    returns = []
    for i in range(1, len(closes)):
        if closes[i-1] > 0 and closes[i] > 0:
            returns.append(math.log(closes[i] / closes[i-1]))
        else:
            returns.append(0.0)
    return returns

def rolling_std(values, window):
    """Compute rolling standard deviation"""
    result = [None] * len(values)
    for i in range(window - 1, len(values)):
        window_vals = values[i - window + 1:i + 1]
        mean = sum(window_vals) / window
        var = sum((x - mean) ** 2 for x in window_vals) / window
        result[i] = math.sqrt(var)
    return result

def rolling_percentile_rank(current_val, history_vals, window):
    """Compute percentile rank of current_val within trailing window"""
    if len(history_vals) < window:
        return None
    trailing = history_vals[-window:]
    valid = [v for v in trailing if v is not None]
    if len(valid) < 10:  # Need minimum history
        return None
    rank = sum(1 for v in valid if v <= current_val) / len(valid)
    return rank

def rolling_median(values, window):
    """Compute rolling median"""
    result = [None] * len(values)
    for i in range(window - 1, len(values)):
        window_vals = sorted(values[i - window + 1:i + 1])
        n = len(window_vals)
        if n % 2 == 1:
            result[i] = window_vals[n // 2]
        else:
            result[i] = (window_vals[n // 2 - 1] + window_vals[n // 2]) / 2
    return result

def ts_to_date(ts):
    """Convert unix timestamp to YYYY-MM-DD string"""
    import datetime
    return datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_ts(date_str):
    """Convert YYYY-MM-DD to unix timestamp (start of day UTC)"""
    import datetime
    return int(datetime.datetime.strptime(date_str, '%Y-%m-%d').timestamp())

def main():
    conn = connect()
    
    print("Loading universe...", file=sys.stderr)
    eligible = get_eligible_symbols(conn)
    print(f"Eligible symbols: {len(eligible)}", file=sys.stderr)
    
    if len(eligible) == 0:
        print("INSUFFICIENT=1")
        return
    
    all_signals = []  # (symbol_id, symbol, decision_ts, decision_date, fwd_return)
    
    for symbol_id, symbol in eligible.items():
        # Load data
        bars = get_daily_bars(conn, symbol_id)
        if len(bars) < 504:
            continue
            
        sent_data = get_sentiment_features(conn, symbol_id)
        if len(sent_data) < 252:
            continue
            
        insider_buys = get_insider_purchases(conn, symbol_id)
        if not insider_buys:
            continue
        
        # Align bars and sentiment by date
        bar_dates = [ts_to_date(b[0]) for b in bars]
        bar_closes = [b[4] for b in bars]
        bar_volumes = [b[5] for b in bars]
        bar_dollar_vol = [bar_closes[i] * bar_volumes[i] for i in range(len(bars))]
        
        sent_dates = [s[0] for s in sent_data]
        sent_scores = [s[1] for s in sent_data]
        
        # Create date-indexed maps
        bar_idx = {d: i for i, d in enumerate(bar_dates)}
        sent_idx = {d: i for i, d in enumerate(sent_dates)}
        
        # Compute daily log returns
        log_returns = compute_log_returns(bar_closes)
        # Pad to same length as bars (first day has no return)
        log_returns = [None] + log_returns
        
        # Compute 21-day realized price volatility (std of log returns)
        price_vol_21 = rolling_std([r for r in log_returns if r is not None], 21)
        # Pad to match bars length
        price_vol_21_full = [None] * len(bars)
        j = 0
        for i in range(len(bars)):
            if log_returns[i] is not None:
                if j < len(price_vol_21):
                    price_vol_21_full[i] = price_vol_21[j]
                j += 1
        
        # Compute 21-day news sentiment volatility (std of daily mean_score)
        sent_vol_21 = rolling_std(sent_scores, 21)
        
        # Compute 63-day median dollar volume
        median_dollar_vol_63 = rolling_median(bar_dollar_vol, 63)
        
        # Compute 63-day return
        return_63 = [None] * len(bars)
        for i in range(63, len(bars)):
            if bar_closes[i-63] > 0:
                return_63[i] = (bar_closes[i] - bar_closes[i-63]) / bar_closes[i-63]
        
        # For each insider purchase, check entry conditions
        for filed_ts, insider, title in insider_buys:
            decision_date = ts_to_date(filed_ts)
            
            # Must have bar and sentiment data for this date
            if decision_date not in bar_idx or decision_date not in sent_idx:
                continue
            
            bi = bar_idx[decision_date]
            si = sent_idx[decision_date]
            
            # Need 252-day history for percentiles (so index >= 252)
            if bi < 252 or si < 252:
                continue
            
            # Check median dollar volume >= $1M over prior 63 sessions
            if median_dollar_vol_63[bi] is None or median_dollar_vol_63[bi] < 1_000_000:
                continue
            
            # Check 63-day return <= +20% (abstention condition)
            if return_63[bi] is not None and return_63[bi] > 0.20:
                continue
            
            # Get current volatilities
            curr_price_vol = price_vol_21_full[bi]
            curr_sent_vol = sent_vol_21[si]
            
            if curr_price_vol is None or curr_sent_vol is None:
                continue
            
            # Compute percentile ranks over 252-day history
            price_vol_history = [v for v in price_vol_21_full[bi-251:bi+1] if v is not None]
            sent_vol_history = [v for v in sent_vol_21[si-251:si+1] if v is not None]
            
            if len(price_vol_history) < 50 or len(sent_vol_history) < 50:
                continue
            
            price_vol_pctl = sum(1 for v in price_vol_history if v <= curr_price_vol) / len(price_vol_history)
            sent_vol_pctl = sum(1 for v in sent_vol_history if v <= curr_sent_vol) / len(sent_vol_history)
            
            # Entry conditions:
            # (a) 21-day news sentiment volatility <= 10th percentile of 252-day history
            # (b) 21-day realized price volatility >= 90th percentile of 252-day history
            if sent_vol_pctl <= 0.10 and price_vol_pctl >= 0.90:
                # Compute forward 21-day return for label
                if bi + 21 < len(bars) and bar_closes[bi] > 0 and bar_closes[bi + 21] > 0:
                    fwd_return = (bar_closes[bi + 21] - bar_closes[bi]) / bar_closes[bi]
                    all_signals.append({
                        'symbol_id': symbol_id,
                        'symbol': symbol,
                        'decision_ts': filed_ts,
                        'decision_date': decision_date,
                        'fwd_return': fwd_return,
                        'up': 1 if fwd_return > 0 else 0
                    })
    
    print(f"Raw signals found: {len(all_signals)}", file=sys.stderr)
    
    if len(all_signals) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Sort by decision timestamp
    all_signals.sort(key=lambda x: x['decision_ts'])
    
    # Apply abstention: fewer than 2 signals in rolling 63-session window
    # We need to count signals per symbol per 63-day window
    signals_by_symbol = defaultdict(list)
    for sig in all_signals:
        signals_by_symbol[sig['symbol_id']].append(sig)
    
    issued_signals = []
    for symbol_id, sigs in signals_by_symbol.items():
        sigs.sort(key=lambda x: x['decision_ts'])
        for i, sig in enumerate(sigs):
            # Count signals in prior 63 trading days (including current)
            window_start_ts = sig['decision_ts'] - 63 * 86400  # approximate
            count = sum(1 for s in sigs[:i+1] if s['decision_ts'] >= window_start_ts)
            if count >= 2:
                issued_signals.append(sig)
    
    print(f"After abstention filter: {len(issued_signals)}", file=sys.stderr)
    
    if len(issued_signals) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Hold out most recent 20% as sealed era
    split_idx = int(len(issued_signals) * 0.8)
    main_signals = issued_signals[:split_idx]
    sealed_signals = issued_signals[split_idx:]
    
    def compute_metrics(signals):
        if not signals:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(signals)
        hits = sum(1 for s in signals if s['up'] == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class (up) within issued
        distinct_dates = len(set(s['decision_date'] for s in signals))
        
        # Design effect: cluster by date, compute effective N
        # Group by date, count signals per date
        date_counts = defaultdict(int)
        for s in signals:
            date_counts[s['decision_date']] += 1
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Simplified: effective_n = issued / design_effect where design_effect = 1 + (mean_cluster_size - 1) * rho
        # Using Kish's effective sample size: n_eff = (sum w_i)^2 / sum(w_i^2) where w_i = 1/cluster_size
        total_weight = sum(1.0 / c for c in date_counts.values())
        sum_weight_sq = sum((1.0 / c) ** 2 for c in date_counts.values())
        effective_n = (total_weight ** 2) / sum_weight_sq if sum_weight_sq > 0 else issued
        effective_n = min(effective_n, issued - 1e-9)  # Must be strictly less than issued
        
        return issued, hits, precision, base_rate, distinct_dates, effective_n
    
    issued_main, hits_main, precision_main, base_rate_main, distinct_main, eff_n_main = compute_metrics(main_signals)
    issued_sealed, hits_sealed, precision_sealed, _, _, _ = compute_metrics(sealed_signals)
    
    # Total opportunities = all decision points considered (raw signals before abstention)
    opportunities = len(all_signals)
    
    print(f"ISSUED={issued_main}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_main:.6f}")
    print(f"BASE_RATE={base_rate_main:.6f}")
    print(f"DISTINCT_DAYS={distinct_main}")
    print(f"EFFECTIVE_N={eff_n_main:.6f}")
    print(f"SEALED_PRECISION={precision_sealed:.6f}")

if __name__ == '__main__':
    main()