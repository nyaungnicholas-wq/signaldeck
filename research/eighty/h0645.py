# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 644
# cycle_index: 19
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

    # Get all insider sales (code='S' for sale) with filed_ts
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, it.shares, it.price, it.value
        FROM insider_trades it
        WHERE it.code = 'S'
        ORDER BY it.filed_ts
    """)
    insider_sales = cur.fetchall()
    
    if not insider_sales:
        print("INSUFFICIENT=1")
        return 0

    # Get symbols with insider coverage (609 symbols)
    symbol_ids = set(row['symbol_id'] for row in insider_sales)
    
    # Get 1d bars for all relevant symbols to compute realized vol
    # We need bars up to each decision point (filed_ts)
    cur.execute("""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbol_ids))), list(symbol_ids))
    bars = cur.fetchall()
    
    if not bars:
        print("INSUFFICIENT=1")
        return 0

    # Organize bars by symbol
    bars_by_symbol = {}
    for row in bars:
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append((row['ts'], row['close']))

    # Get news sentiment for all relevant symbols
    cur.execute("""
        SELECT symbol_id, ts, score
        FROM news
        WHERE symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbol_ids))), list(symbol_ids))
    news_rows = cur.fetchall()
    
    news_by_symbol = {}
    for row in news_rows:
        sid = row['symbol_id']
        if sid not in news_by_symbol:
            news_by_symbol[sid] = []
        news_by_symbol[sid].append((row['ts'], row['score']))

    # Get labels from prediction_outcomes for horizon=21
    cur.execute("""
        SELECT symbol_id, ts, fwd_return
        FROM prediction_outcomes
        WHERE horizon = 21 AND symbol_id IN ({})
        ORDER BY symbol_id, ts
    """.format(','.join('?'*len(symbol_ids))), list(symbol_ids))
    label_rows = cur.fetchall()
    
    labels_by_symbol = {}
    for row in label_rows:
        sid = row['symbol_id']
        if sid not in labels_by_symbol:
            labels_by_symbol[sid] = {}
        labels_by_symbol[sid][row['ts']] = row['fwd_return']

    # For each insider sale, check conditions at filed_ts
    decisions = []  # (filed_ts, symbol_id, label, issued)
    
    for sale in insider_sales:
        symbol_id = sale['symbol_id']
        filed_ts = sale['filed_ts']
        
        # Need bars for this symbol
        if symbol_id not in bars_by_symbol:
            continue
        sym_bars = bars_by_symbol[symbol_id]
        
        # Find bars up to filed_ts (as-of discipline)
        # filed_ts is unix epoch, bars.ts is unix epoch
        prior_bars = [(ts, close) for ts, close in sym_bars if ts <= filed_ts]
        if len(prior_bars) < 252:  # Need 252-day history for vol range
            decisions.append((filed_ts, symbol_id, None, False))
            continue
        
        # Calculate 20-day realized vol (using log returns)
        # Need at least 21 bars for 20 returns
        if len(prior_bars) < 21:
            decisions.append((filed_ts, symbol_id, None, False))
            continue
            
        recent_20 = prior_bars[-21:]  # 21 prices = 20 returns
        returns = []
        for i in range(1, len(recent_20)):
            r = (recent_20[i][1] - recent_20[i-1][1]) / recent_20[i-1][1]
            returns.append(r)
        
        if len(returns) < 20:
            decisions.append((filed_ts, symbol_id, None, False))
            continue
            
        # 20-day realized vol (std of returns * sqrt(252))
        import math
        mean_ret = sum(returns) / len(returns)
        var = sum((r - mean_ret)**2 for r in returns) / len(returns)
        vol_20d = math.sqrt(var * 252)
        
        # Get 252-day vol history to find bottom decile
        # Need rolling 20-day vols over past 252 days
        vol_history = []
        for i in range(20, min(252, len(prior_bars))):
            window = prior_bars[-i-20:-i] if i > 0 else prior_bars[-20:]
            if len(window) < 21:
                continue
            window_returns = []
            for j in range(1, len(window)):
                r = (window[j][1] - window[j-1][1]) / window[j-1][1]
                window_returns.append(r)
            if len(window_returns) >= 20:
                m = sum(window_returns) / len(window_returns)
                v = sum((r - m)**2 for r in window_returns) / len(window_returns)
                vol_history.append(math.sqrt(v * 252))
        
        if len(vol_history) < 50:  # Need sufficient history
            decisions.append((filed_ts, symbol_id, None, False))
            continue
            
        # Bottom decile threshold
        vol_history.sort()
        bottom_decile_idx = max(0, int(len(vol_history) * 0.1) - 1)
        vol_threshold = vol_history[bottom_decile_idx]
        
        vol_condition = vol_20d <= vol_threshold
        
        # Check 5-day mean news sentiment >= 0 at filed_ts
        news_condition = False
        if symbol_id in news_by_symbol:
            prior_news = [(ts, score) for ts, score in news_by_symbol[symbol_id] if ts <= filed_ts]
            # Get last 5 trading days of news (approximate: 5 calendar days = ~432000 seconds)
            five_days_sec = 5 * 86400
            recent_news = [score for ts, score in prior_news if ts >= filed_ts - five_days_sec]
            if recent_news:
                mean_sentiment = sum(recent_news) / len(recent_news)
                news_condition = mean_sentiment >= 0
        
        # Issue DOWN call if both conditions met
        issued = vol_condition and news_condition
        
        # Get label: fwd_return at filed_ts (or nearest prior)
        label = None
        if symbol_id in labels_by_symbol:
            # Find label at or before filed_ts
            label_ts_candidates = [ts for ts in labels_by_symbol[symbol_id].keys() if ts <= filed_ts]
            if label_ts_candidates:
                label_ts = max(label_ts_candidates)
                label = labels_by_symbol[symbol_id][label_ts]
        
        decisions.append((filed_ts, symbol_id, label, issued))

    # Filter to only decisions with labels
    labeled_decisions = [d for d in decisions if d[2] is not None]
    
    if not labeled_decisions:
        print("INSUFFICIENT=1")
        return 0

    # Sort by time
    labeled_decisions.sort(key=lambda x: x[0])
    
    # Hold out most recent 20% as sealed era
    n_total = len(labeled_decisions)
    n_sealed = max(1, int(n_total * 0.2))
    train_decisions = labeled_decisions[:-n_sealed]
    sealed_decisions = labeled_decisions[-n_sealed:]

    def compute_metrics(decisions_list):
        issued_calls = [d for d in decisions_list if d[3]]
        if not issued_calls:
            return {
                'issued': 0,
                'opportunities': len(decisions_list),
                'precision': 0.0,
                'base_rate': 0.0,
                'distinct_days': 0,
                'effective_n': 0.0
            }
        
        # DOWN call: predict fwd_return < 0
        hits = sum(1 for d in issued_calls if d[2] < 0)
        issued_count = len(issued_calls)
        precision = hits / issued_count
        
        # Base rate of down (fwd_return < 0) within issued subset
        base_rate = hits / issued_count  # Same as precision for binary, but this IS the base rate in issued subset
        
        # Distinct UTC days among issued calls
        issued_days = set()
        for d in issued_calls:
            dt = datetime.utcfromtimestamp(d[0])
            day_key = dt.strftime('%Y-%m-%d')
            issued_days.add(day_key)
        distinct_days = len(issued_days)
        
        # Design effect: cluster by day, compute effective N
        # Count calls per day
        day_counts = {}
        for d in issued_calls:
            dt = datetime.utcfromtimestamp(d[0])
            day_key = dt.strftime('%Y-%m-%d')
            day_counts[day_key] = day_counts.get(day_key, 0) + 1
        
        # Design effect = 1 + (avg_cluster_size - 1) * ICC
        # Simplified: use Kish's effective sample size
        # n_eff = (sum w_i)^2 / sum(w_i^2) where w_i = 1 for each call
        # But clustered: n_eff = n / (1 + (m-1)*rho) where m=avg cluster size
        # Conservative: assume ICC=0.5 for same-day calls
        if len(day_counts) > 1:
            avg_cluster = issued_count / len(day_counts)
            icc = 0.5
            design_effect = 1 + (avg_cluster - 1) * icc
            effective_n = issued_count / design_effect
        else:
            effective_n = 1.0  # All on same day
        
        return {
            'issued': issued_count,
            'opportunities': len(decisions_list),
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_metrics = compute_metrics(train_decisions)
    sealed_metrics = compute_metrics(sealed_decisions)

    # Print required lines
    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={train_metrics['opportunities']}")
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())