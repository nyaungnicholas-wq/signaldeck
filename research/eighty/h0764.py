# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 763
# cycle_index: 33
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

OFFICER_TITLES = {
    'CEO', 'CFO', 'COO', 'CTO', 'CIO', 'CMO', 'CLO', 'CRO', 'CAO', 'CCO',
    'CHAIRMAN', 'PRESIDENT', 'VICE PRESIDENT', 'VP', 'EXECUTIVE CHAIR',
    'CHIEF EXECUTIVE', 'CHIEF FINANCIAL', 'CHIEF OPERATING', 'CHIEF TECHNOLOGY',
    'CHIEF INFORMATION', 'CHIEF MARKETING', 'CHIEF LEGAL', 'CHIEF RISK',
    'CHIEF ACCOUNTING', 'CHIEF COMPLIANCE', 'GENERAL COUNSEL', 'TREASURER',
    'CONTROLLER', 'PRINCIPAL ACCOUNTING', 'PRINCIPAL FINANCIAL',
    'PRINCIPAL EXECUTIVE', 'PRINCIPAL OPERATING', 'SENIOR VP', 'SVP',
    'EXECUTIVE VP', 'EVP', 'GROUP VP', 'CORPORATE VP', 'DIVISION VP'
}

def is_officer(title: str) -> bool:
    if not title:
        return False
    t = title.upper()
    for ot in OFFICER_TITLES:
        if ot in t:
            return True
    return False

def ts_to_date(ts: int) -> str:
    return datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

def date_to_ts(date_str: str) -> int:
    return int(datetime.strptime(date_str, '%Y-%m-%d').timestamp())

def get_trading_days(conn, symbol_id: int, start_ts: int, end_ts: int) -> list:
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def get_forward_return(conn, symbol_id: int, decision_ts: int, horizon_days: int) -> float:
    bars = get_trading_days(conn, symbol_id, decision_ts, 2**31-1)
    if len(bars) <= horizon_days:
        return None
    entry_ts = bars[0]
    exit_ts = bars[horizon_days]
    cur = conn.execute(
        "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts IN (?,?)",
        (symbol_id, entry_ts, exit_ts)
    )
    rows = cur.fetchall()
    if len(rows) != 2:
        return None
    entry_px, exit_px = rows[0][0], rows[1][0]
    if entry_px == 0:
        return None
    return (exit_px - entry_px) / entry_px

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.execute("PRAGMA query_only = ON")
    
    # Get all insider purchases with filed_ts (knowable at decision time)
    cur = conn.execute("""
        SELECT symbol_id, insider, title, code, shares, price, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND filed_ts IS NOT NULL AND shares > 0 AND price > 0
        ORDER BY filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return
    
    # Group by symbol_id
    from collections import defaultdict
    trades_by_symbol = defaultdict(list)
    for t in trades:
        trades_by_symbol[t[0]].append(t)
    
    # For each symbol, compute officer buy dominance ratio over rolling 21-day windows
    # using filed_ts as decision time
    HORIZON = 21  # trading days
    LOOKBACK = 252  # trading days for "first time" check
    WINDOW = 21  # trading days for ratio calculation
    
    opportunities = []  # (symbol_id, decision_ts, filed_ts, ratio, is_first_time)
    
    for symbol_id, sym_trades in trades_by_symbol.items():
        # Get trading days for this symbol to map calendar days to trading days
        all_bars = get_trading_days(conn, symbol_id, 0, 2**31-1)
        if len(all_bars) < LOOKBACK + HORIZON + WINDOW:
            continue
        bar_set = set(all_bars)
        ts_to_idx = {ts: i for i, ts in enumerate(all_bars)}
        
        # Process each trade as a potential decision point (at filed_ts)
        for trade in sym_trades:
            _, insider, title, code, shares, price, tx_ts, filed_ts = trade
            if filed_ts not in bar_set:
                # Find next trading day after filed_ts
                next_idx = None
                for i, ts in enumerate(all_bars):
                    if ts >= filed_ts:
                        next_idx = i
                        break
                if next_idx is None:
                    continue
                decision_idx = next_idx
                decision_ts = all_bars[decision_idx]
            else:
                decision_idx = ts_to_idx[filed_ts]
                decision_ts = filed_ts
            
            # Need enough history before decision_idx
            if decision_idx < LOOKBACK:
                continue
            # Need enough future for horizon
            if decision_idx + HORIZON >= len(all_bars):
                continue
            
            # Compute officer buy dominance in prior WINDOW trading days (including decision day)
            window_start_idx = decision_idx - WINDOW + 1
            window_trades = []
            for t in sym_trades:
                _, _, t_title, t_code, t_shares, t_price, t_tx_ts, t_filed_ts = t
                if t_code != 'P' or t_filed_ts is None:
                    continue
                # Find trading day index for this trade's filed_ts
                t_idx = None
                for i, ts in enumerate(all_bars):
                    if ts >= t_filed_ts:
                        t_idx = i
                        break
                if t_idx is None:
                    continue
                if window_start_idx <= t_idx <= decision_idx:
                    window_trades.append((t_title, t_shares * t_price))
            
            if not window_trades:
                continue
            
            total_buy_value = sum(v for _, v in window_trades)
            officer_buy_value = sum(v for title, v in window_trades if is_officer(title))
            
            if total_buy_value == 0:
                continue
            
            ratio = officer_buy_value / total_buy_value
            
            # Check if this is the first time in LOOKBACK days that ratio > 0.5
            is_first_time = True
            if ratio > 0.5:
                # Check prior LOOKBACK days (excluding current window)
                check_end_idx = window_start_idx - 1
                check_start_idx = max(0, check_end_idx - LOOKBACK + 1)
                for check_idx in range(check_start_idx, check_end_idx + 1):
                    check_window_start = check_idx - WINDOW + 1
                    if check_window_start < 0:
                        continue
                    check_window_trades = []
                    for t in sym_trades:
                        _, _, t_title, t_code, t_shares, t_price, t_tx_ts, t_filed_ts = t
                        if t_code != 'P' or t_filed_ts is None:
                            continue
                        t_idx = None
                        for i, ts in enumerate(all_bars):
                            if ts >= t_filed_ts:
                                t_idx = i
                                break
                        if t_idx is None:
                            continue
                        if check_window_start <= t_idx <= check_idx:
                            check_window_trades.append((t_title, t_shares * t_price))
                    
                    if check_window_trades:
                        check_total = sum(v for _, v in check_window_trades)
                        check_officer = sum(v for title, v in check_window_trades if is_officer(title))
                        if check_total > 0 and (check_officer / check_total) > 0.5:
                            is_first_time = False
                            break
            
            opportunities.append((symbol_id, decision_ts, ratio, is_first_time))
    
    if not opportunities:
        print("INSUFFICIENT=1")
        return
    
    # Filter to entry condition: ratio > 0.5 AND is_first_time
    entries = [(sym, dt) for sym, dt, ratio, first in opportunities if ratio > 0.5 and first]
    
    if not entries:
        print("INSUFFICIENT=1")
        return
    
    # Compute forward returns for entries
    results = []
    for symbol_id, decision_ts in entries:
        fwd_ret = get_forward_return(conn, symbol_id, decision_ts, HORIZON)
        if fwd_ret is not None:
            results.append((symbol_id, decision_ts, fwd_ret))
    
    if not results:
        print("INSUFFICIENT=1")
        return
    
    # Determine sealed era (most recent 20% by decision_ts)
    results.sort(key=lambda x: x[1])
    split_idx = int(len(results) * 0.8)
    main_results = results[:split_idx]
    sealed_results = results[split_idx:]
    
    def compute_metrics(res_list):
        if not res_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(res_list)
        hits = sum(1 for _, _, ret in res_list if ret > 0)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate of positive class in issued subset
        distinct_days = len(set(ts_to_date(ts) for _, ts, _ in res_list))
        # Design effect: simple approximation using autocorrelation of returns
        # For clustered calls, effective_n < issued
        # Use 1 + 2*sum(rho_k) approximation; here we use a conservative estimate
        # Group by symbol and date to measure clustering
        from collections import Counter
        day_counts = Counter(ts_to_date(ts) for _, ts, _ in res_list)
        # Design effect approx: 1 + (avg_cluster_size - 1) * intra_cluster_corr
        # Conservative: assume intra-cluster corr = 0.5, avg cluster size = issued/distinct_days
        if distinct_days > 0:
            avg_cluster = issued / distinct_days
            deff = 1 + (avg_cluster - 1) * 0.5
        else:
            deff = 1.0
        effective_n = issued / deff if deff > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(main_results)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_results)
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()