# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 610
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    """Get trading days (ts) for a symbol in range."""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def get_price(conn, symbol_id, ts):
    """Get close price for symbol at or before ts."""
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 1",
        (symbol_id, ts)
    )
    row = cur.fetchone()
    return row[0] if row else None

def get_returns_series(conn, symbol_id, end_ts, lookback_days):
    """Get 63-day returns for each day in trailing window."""
    # Need trading days, not calendar days
    trading_days = get_trading_days(conn, symbol_id, 0, end_ts)
    if len(trading_days) < lookback_days + 63:
        return []
    
    returns = []
    for i in range(63, len(trading_days) - lookback_days + 1):
        t_curr = trading_days[i]
        t_prev = trading_days[i - 63]
        p_curr = get_price(conn, symbol_id, t_curr)
        p_prev = get_price(conn, symbol_id, t_prev)
        if p_curr and p_prev and p_prev > 0:
            returns.append((t_curr, (p_curr - p_prev) / p_prev))
    return returns

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row
    
    # Get all insider purchases (code='P') with filed_ts
    cur = conn.execute("""
        SELECT symbol_id, insider, filed_ts, tx_ts, shares, price, value
        FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL
        ORDER BY filed_ts
    """)
    purchases = cur.fetchall()
    
    if not purchases:
        print("INSUFFICIENT=1")
        return 0
    
    # Get all prediction outcomes for horizon=21
    cur = conn.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21 AND up IS NOT NULL
    """)
    labels = {(row['symbol_id'], row['ts']): row['up'] for row in cur.fetchall()}
    
    # Get active symbols
    cur = conn.execute("SELECT id FROM symbols WHERE active = 1")
    active_symbols = {row[0] for row in cur.fetchall()}
    
    # For each purchase, check conditions
    signals = []
    
    # Pre-compute 63-day return distributions for efficiency
    # Group purchases by symbol
    from collections import defaultdict
    purchases_by_symbol = defaultdict(list)
    for p in purchases:
        if p['symbol_id'] in active_symbols:
            purchases_by_symbol[p['symbol_id']].append(p)
    
    for symbol_id, sym_purchases in purchases_by_symbol.items():
        # Get all trading days for this symbol
        trading_days = get_trading_days(conn, symbol_id, 0, 2**31-1)
        if len(trading_days) < 126:
            continue
        
        # Pre-compute 63-day returns for all trading days
        prices = {}
        for td in trading_days:
            p = get_price(conn, symbol_id, td)
            if p:
                prices[td] = p
        
        returns_63 = {}
        for i in range(63, len(trading_days)):
            t_curr = trading_days[i]
            t_prev = trading_days[i - 63]
            if t_curr in prices and t_prev in prices and prices[t_prev] > 0:
                returns_63[t_curr] = (prices[t_curr] - prices[t_prev]) / prices[t_prev]
        
        # For each purchase, check conditions
        for p in sym_purchases:
            filed_ts = p['filed_ts']
            insider = p['insider']
            
            # Check: no prior purchase by same insider in 63 calendar days
            prior_cutoff = filed_ts - 63 * 86400
            cur = conn.execute("""
                SELECT 1 FROM insider_trades
                WHERE insider = ? AND code = 'P' AND filed_ts >= ? AND filed_ts < ?
                LIMIT 1
            """, (insider, prior_cutoff, filed_ts))
            if cur.fetchone():
                continue
            
            # Find the trading day at or before filed_ts (prior close)
            prior_td = None
            for td in reversed(trading_days):
                if td <= filed_ts:
                    prior_td = td
                    break
            if not prior_td:
                continue
            
            # Need 63-day return as of prior_td
            if prior_td not in returns_63:
                continue
            ret_63 = returns_63[prior_td]
            
            # Need trailing 252-day distribution of 63-day returns ending at prior_td
            # Find index of prior_td in trading_days
            try:
                idx = trading_days.index(prior_td)
            except ValueError:
                continue
            
            if idx < 252:
                continue
            
            # Get 63-day returns for the 252 days ending at prior_td
            dist_returns = []
            for j in range(idx - 251, idx + 1):
                td = trading_days[j]
                if td in returns_63:
                    dist_returns.append(returns_63[td])
            
            if len(dist_returns) < 126:
                continue
            
            # Check if current return is in bottom quartile
            sorted_rets = sorted(dist_returns)
            q1_idx = len(sorted_rets) // 4
            q1_threshold = sorted_rets[q1_idx]
            
            if ret_63 > q1_threshold:
                continue
            
            # Check label exists
            label_key = (symbol_id, filed_ts)
            if label_key not in labels:
                # Try to find closest label timestamp (within same day)
                # prediction_outcomes ts is likely at market close
                found = False
                for offset in range(0, 86400, 3600):
                    for direction in [1, -1]:
                        test_ts = filed_ts + direction * offset
                        if (symbol_id, test_ts) in labels:
                            label_key = (symbol_id, test_ts)
                            found = True
                            break
                    if found:
                        break
                if not found:
                    continue
            
            up = labels[label_key]
            signals.append({
                'symbol_id': symbol_id,
                'filed_ts': filed_ts,
                'insider': insider,
                'ret_63': ret_63,
                'up': up,
                'day': datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
            })
    
    if not signals:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by time
    signals.sort(key=lambda x: x['filed_ts'])
    
    # Split: most recent 20% sealed
    n_total = len(signals)
    n_sealed = max(1, n_total // 5)
    train_signals = signals[:-n_sealed]
    sealed_signals = signals[-n_sealed:]
    
    def compute_metrics(sig_list):
        if not sig_list:
            return 0, 0, 0, 0
        issued = len(sig_list)
        hits = sum(1 for s in sig_list if s['up'] == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = precision  # base rate within issued subset
        distinct_days = len(set(s['day'] for s in sig_list))
        return issued, hits, precision, base_rate, distinct_days
    
    train_issued, train_hits, train_precision, train_base_rate, train_distinct_days = compute_metrics(train_signals)
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_distinct_days = compute_metrics(sealed_signals)
    
    # Design effect (given as 6.99 in context)
    DESIGN_EFFECT = 6.99
    effective_n = train_issued / DESIGN_EFFECT if train_issued > 0 else 0
    
    # Opportunities: total decision points considered (all insider purchases meeting basic criteria)
    # For simplicity, use total purchases with code='P' and filed_ts in active symbols
    cur = conn.execute("""
        SELECT COUNT(*) FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL AND symbol_id IN (SELECT id FROM symbols WHERE active=1)
    """)
    opportunities = cur.fetchone()[0]
    
    print(f"ISSUED={train_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={train_precision:.6f}")
    print(f"BASE_RATE={train_base_rate:.6f}")
    print(f"DISTINCT_DAYS={train_distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    # Invariants check
    if train_distinct_days > train_issued:
        print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
        return 1
    if effective_n >= train_issued and train_issued > 0:
        print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)
        return 1
    
    return 0

if __name__ == '__main__':
    sys.exit(main())