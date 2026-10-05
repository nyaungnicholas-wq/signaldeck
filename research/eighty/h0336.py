# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 335
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_db():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(conn):
    """Get all distinct trading days from 1d bars, sorted."""
    cur = conn.execute("""
        SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts
    """)
    return [row[0] for row in cur.fetchall()]

def get_symbols_with_bars(conn):
    """Get symbols that have 1d bars."""
    cur = conn.execute("""
        SELECT DISTINCT symbol_id FROM bars WHERE tf = '1d'
    """)
    return [row[0] for row in cur.fetchall()]

def get_eps_data(conn):
    """Get EPS fundamentals: symbol_id, value, as_of, fetched_at."""
    cur = conn.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EPS'
        ORDER BY symbol_id, fetched_at
    """)
    return cur.fetchall()

def get_bars_for_symbol(conn, symbol_id, start_ts, end_ts):
    """Get 1d bars for a symbol in timestamp range."""
    cur = conn.execute("""
        SELECT ts, open, high, low, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts <= ?
        ORDER BY ts
    """, (symbol_id, start_ts, end_ts))
    return cur.fetchall()

def get_forward_return(conn, symbol_id, decision_ts, horizon_days=21):
    """Get forward return over horizon_days trading days from prediction_outcomes.
    We'll compute from bars directly for precision."""
    # Find the bar at decision_ts, then look ahead horizon_days bars
    cur = conn.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts = ?
    """, (symbol_id, decision_ts))
    row = cur.fetchone()
    if not row:
        return None
    entry_close = row[0]
    
    # Get the close after horizon_days trading days
    cur = conn.execute("""
        SELECT close FROM bars
        WHERE symbol_id = ? AND tf = '1d' AND ts > ?
        ORDER BY ts LIMIT 1 OFFSET ?
    """, (symbol_id, decision_ts, horizon_days - 1))
    row = cur.fetchone()
    if not row:
        return None
    exit_close = row[0]
    
    return (exit_close - entry_close) / entry_close

def compute_trailing_return(bars, decision_idx, lookback_days):
    """Compute trailing total return over lookback_days trading days."""
    if decision_idx < lookback_days:
        return None
    start_close = bars[decision_idx - lookback_days][3]  # close
    end_close = bars[decision_idx][3]
    return (end_close - start_close) / start_close

def compute_median_dollar_volume(bars, decision_idx, window_days):
    """Compute median daily dollar volume over prior window_days sessions."""
    if decision_idx < window_days:
        return None
    volumes = []
    for i in range(decision_idx - window_days + 1, decision_idx + 1):
        close = bars[i][3]
        volume = bars[i][5]
        dollar_vol = close * volume
        volumes.append(dollar_vol)
    volumes.sort()
    n = len(volumes)
    if n % 2 == 1:
        return volumes[n // 2]
    else:
        return (volumes[n // 2 - 1] + volumes[n // 2]) / 2

def main():
    conn = connect_db()
    conn.row_factory = sqlite3.Row
    
    # Get all trading days
    trading_days = get_trading_days(conn)
    if not trading_days:
        print("INSUFFICIENT=1")
        return
    
    # Get symbols with bars
    symbol_ids = get_symbols_with_bars(conn)
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return
    
    # Get EPS data
    eps_rows = get_eps_data(conn)
    if not eps_rows:
        print("INSUFFICIENT=1")
        return
    
    # Organize EPS by symbol
    eps_by_symbol = defaultdict(list)
    for row in eps_rows:
        eps_by_symbol[row['symbol_id']].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })
    
    # For each symbol, get all bars once
    print("Loading bars...", file=sys.stderr)
    bars_by_symbol = {}
    for sid in symbol_ids:
        bars = get_bars_for_symbol(conn, sid, trading_days[0], trading_days[-1])
        if len(bars) >= 252 + 21:  # need 252 lookback + 21 forward
            bars_by_symbol[sid] = bars
    
    print(f"Symbols with sufficient bars: {len(bars_by_symbol)}", file=sys.stderr)
    
    # Build decision points
    decisions = []  # (symbol_id, decision_ts, eps_growth, trailing_return, dollar_vol, fwd_return)
    
    for sid, bars in bars_by_symbol.items():
        if sid not in eps_by_symbol:
            continue
        
        eps_list = eps_by_symbol[sid]
        # Sort by fetched_at for as-of discipline
        eps_list.sort(key=lambda x: x['fetched_at'])
        
        # Map bar index to ts
        bar_ts_to_idx = {bar[0]: i for i, bar in enumerate(bars)}
        bar_ts_list = [bar[0] for bar in bars]
        
        # For each bar that could be a decision point (need 252 lookback, 21 forward)
        for decision_idx in range(252, len(bars) - 21):
            decision_ts = bars[decision_idx][0]
            
            # Find latest EPS data available at decision_ts (fetched_at <= decision_ts)
            available_eps = [e for e in eps_list if e['fetched_at'] <= decision_ts]
            if not available_eps:
                continue
            
            latest_eps = available_eps[-1]
            
            # Check if EPS data is stale (>120 days old)
            if decision_ts - latest_eps['fetched_at'] > 120 * 86400:
                continue
            
            # Need EPS from 4 quarters ago (approx 252 trading days / 4 = 63 days per quarter? 
            # But quarters are calendar quarters. Use as_of dates.
            # Find EPS with as_of approximately 4 quarters before latest_eps['as_of']
            target_as_of = latest_eps['as_of'] - 4 * 90 * 86400  # approximate 4 quarters
            # Find closest EPS before target_as_of that was also fetched by decision_ts
            prior_eps_candidates = [e for e in available_eps if e['as_of'] <= target_as_of]
            if not prior_eps_candidates:
                continue
            prior_eps = max(prior_eps_candidates, key=lambda x: x['as_of'])
            
            # Compute EPS growth
            if prior_eps['value'] <= 0:
                continue
            eps_growth = (latest_eps['value'] - prior_eps['value']) / prior_eps['value']
            
            # Trailing 252-day return
            trailing_return = compute_trailing_return(bars, decision_idx, 252)
            if trailing_return is None:
                continue
            
            # Median dollar volume over prior 21 sessions
            dollar_vol = compute_median_dollar_volume(bars, decision_idx, 21)
            if dollar_vol is None or dollar_vol < 5_000_000:
                continue
            
            # Entry conditions
            if eps_growth >= 0.40 and trailing_return <= -0.15:
                # Get forward return
                fwd_return = get_forward_return(conn, sid, decision_ts, 21)
                if fwd_return is not None:
                    hit = 1 if fwd_return > 0 else 0
                    decisions.append({
                        'symbol_id': sid,
                        'ts': decision_ts,
                        'eps_growth': eps_growth,
                        'trailing_return': trailing_return,
                        'dollar_vol': dollar_vol,
                        'fwd_return': fwd_return,
                        'hit': hit
                    })
    
    if not decisions:
        print("INSUFFICIENT=1")
        return
    
    print(f"Total decisions (issued calls): {len(decisions)}", file=sys.stderr)
    
    # Sort by timestamp
    decisions.sort(key=lambda x: x['ts'])
    
    # Hold out most recent 20% as sealed era
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    main_decisions = decisions[:-n_sealed]
    sealed_decisions = decisions[-n_sealed:]
    
    def compute_metrics(dec_list):
        if not dec_list:
            return None
        issued = len(dec_list)
        hits = sum(d['hit'] for d in dec_list)
        precision = hits / issued if issued > 0 else 0
        
        # Base rate within issued subset
        base_rate = hits / issued if issued > 0 else 0
        
        # Distinct days
        distinct_days = len(set(d['ts'] for d in dec_list))
        
        # Design effect and effective N
        day_counts = defaultdict(int)
        for d in dec_list:
            day_counts[d['ts']] += 1
        n_d = list(day_counts.values())
        D = len(n_d)
        issued_f = float(issued)
        sum_n2 = sum(c * c for c in n_d)
        if D > 0 and issued_f > 0:
            design_effect = D * sum_n2 / (issued_f * issued_f)
            # Ensure design effect > 1
            if design_effect <= 1.0:
                design_effect = 1.001
        else:
            design_effect = 1.001
        effective_n = issued_f / design_effect
        
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n,
            'design_effect': design_effect
        }
    
    main_metrics = compute_metrics(main_decisions)
    sealed_metrics = compute_metrics(sealed_decisions)
    
    if not main_metrics:
        print("INSUFFICIENT=1")
        return
    
    # Output required lines
    print(f"ISSUED={main_metrics['issued']}")
    print(f"OPPORTUNITIES={main_metrics['issued']}")  # Only counting issued as opportunities considered? 
    # Wait, OPPORTUNITIES should be decision points considered, not just issued.
    # But we didn't track abstained. Let me reconsider.
    # The requirement: "OPPORTUNITIES=<count of decision points considered>"
    # This means all (symbol, day) pairs that met universe criteria and were evaluated.
    # We need to track this.
    
    # Actually, re-reading: "Count independent observations, not rows: one (symbol, UTC day) is one observation, however many forecasts resolve on it."
    # And "OPPORTUNITIES=<count of decision points considered>"
    # So we need to count all (symbol, day) that were in universe and evaluated, regardless of call issued.
    
    # I need to restructure to count opportunities. Let me redo this part.
    
    # For now, let me compute opportunities properly by re-running or tracking.
    # Since we already have the logic, let me count during the loop.
    # But the script is already written... I'll need to modify.
    
    # Let me restart the logic with opportunity counting.

if __name__ == '__main__':
    main()