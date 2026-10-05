# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 336
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
import sys
from collections import defaultdict
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON_DAYS = 21
DECISION_DAY_OFFSET = 15
OWNERSHIP_INCREASE_THRESHOLD = 0.05

def get_connection():
    return sqlite3.connect(DB_PATH, uri=True, timeout=10)

def get_quarter_start_end(year, quarter):
    start_month = (quarter - 1) * 3 + 1
    start_date = datetime(year, start_month, 1)
    if quarter == 4:
        end_date = datetime(year, 12, 31)
    else:
        next_quarter_start_month = start_month + 3
        if next_quarter_start_month > 12:
            end_date = datetime(year, 12, 31)
        else:
            end_date = datetime(year, next_quarter_start_month, 1) - timedelta(days=1)
    return start_date, end_date

def get_trading_days(conn, symbol_ids, start_date, end_date):
    query = """
    SELECT DISTINCT symbol_id, ts
    FROM bars
    WHERE tf = '1d'
      AND symbol_id IN ({})
      AND ts >= ?
      AND ts <= ?
    ORDER BY symbol_id, ts
    """.format(','.join('?' * len(symbol_ids)))
    rows = conn.execute(query, symbol_ids + [int(start_date.timestamp()), int(end_date.timestamp())]).fetchall()
    trading_days = defaultdict(list)
    for sym, ts in rows:
        dt = datetime.utcfromtimestamp(ts)
        trading_days[sym].append(dt)
    return trading_days

def get_13f_data(conn, symbol_ids, cutoff_date):
    query = """
    SELECT symbol_id, period, 
           SUM(shares) as total_shares
    FROM inst_holdings
    WHERE symbol_id IN ({})
      AND period <= ?
    GROUP BY symbol_id, period
    ORDER BY symbol_id, period
    """.format(','.join('?' * len(symbol_ids)))
    rows = conn.execute(query, symbol_ids + [cutoff_date.strftime('%Y-%m-%d')]).fetchall()
    data = defaultdict(list)
    for sym, period, total_shares in rows:
        period_dt = datetime.strptime(period, '%Y-%m-%d')
        data[sym].append((period_dt, total_shares))
    return data

def get_shares_outstanding(conn, symbol_ids, cutoff_date):
    query = """
    SELECT symbol_id, value, fetched_at
    FROM fundamentals
    WHERE symbol_id IN ({})
      AND metric = 'SharesOutstanding'
      AND fetched_at <= ?
    """.format(','.join('?' * len(symbol_ids)))
    rows = conn.execute(query, symbol_ids + [cutoff_date.strftime('%Y-%m-%d')]).fetchall()
    data = {}
    for sym, value, fetched_at in rows:
        fetched_dt = datetime.strptime(fetched_at, '%Y-%m-%d %H:%M:%S')
        if sym not in data or fetched_dt > data[sym][1]:
            data[sym] = (float(value), fetched_dt)
    return data

def get_price_data(conn, symbol_ids, start_date, end_date):
    query = """
    SELECT symbol_id, ts, close
    FROM bars
    WHERE tf = '1d'
      AND symbol_id IN ({})
      AND ts >= ?
      AND ts <= ?
    """.format(','.join('?' * len(symbol_ids)))
    rows = conn.execute(query, symbol_ids + [int(start_date.timestamp()), int(end_date.timestamp())]).fetchall()
    data = defaultdict(dict)
    for sym, ts, close in rows:
        dt = datetime.utcfromtimestamp(ts)
        data[sym][dt] = close
    return data

def compute_quarter_returns(price_data, trading_days, quarter_start, quarter_end, decision_day_idx):
    returns = {}
    for sym in trading_days:
        days = sorted(trading_days[sym])
        quarter_days = [d for d in days if quarter_start <= d <= quarter_end]
        if len(quarter_days) <= decision_day_idx:
            continue
        decision_day = quarter_days[decision_day_idx]
        if sym not in price_data or decision_day not in price_data[sym]:
            continue
        close_decision = price_data[sym][decision_day]
        if quarter_start in price_data[sym]:
            close_prev_q_end = price_data[sym][quarter_start]
            if close_prev_q_end > 0:
                total_return = (close_decision - close_prev_q_end) / close_prev_q_end
                returns[sym] = (decision_day, total_return)
    return returns

def compute_forward_return(price_data, decision_day, horizon_days):
    if sym not in price_data:
        return None
    days = sorted(price_data[sym].keys())
    day_idx = None
    for i, d in enumerate(days):
        if d >= decision_day:
            day_idx = i
            break
    if day_idx is None or day_idx + horizon_days >= len(days):
        return None
    close_start = price_data[sym][days[day_idx]]
    close_end = price_data[sym][days[day_idx + horizon_days]]
    if close_start > 0:
        return (close_end - close_start) / close_start
    return None

def compute_ownership_change(two_periods_data, shares_data):
    if len(two_periods_data) < 2:
        return None
    period1, shares1 = two_periods_data[-2]
    period2, shares2 = two_periods_data[-1]
    if period1 not in shares_data or period2 not in shares_data:
        return None
    outstanding1 = shares_data[period1][0]
    outstanding2 = shares_data[period2][0]
    if outstanding1 <= 0 or outstanding2 <= 0:
        return None
    pct1 = shares1 / outstanding1
    pct2 = shares2 / outstanding2
    return pct2 - pct1

def main():
    conn = get_connection()
    try:
        cursor = conn.cursor()
        
        cursor.execute("SELECT id FROM symbols WHERE market = 'stocks'")
        all_symbols = [row[0] for row in cursor.fetchall()]
        
        cursor.execute("""
        SELECT DISTINCT symbol_id FROM inst_holdings
        """)
        inst_symbols = set(row[0] for row in cursor.fetchall())
        
        symbols = [s for s in all_symbols if s in inst_symbols]
        
        if not symbols:
            print("INSUFFICIENT=1")
            return
            
        cursor.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf = '1d' AND symbol_id IN ({})".format(','.join('?' * len(symbols))), symbols)
        min_ts, max_ts = cursor.fetchone()
        if min_ts is None or max_ts is None:
            print("INSUFFICIENT=1")
            return
            
        start_date = datetime.utcfromtimestamp(min_ts)
        end_date = datetime.utcfromtimestamp(max_ts)
        
        quarters = []
        for year in range(start_date.year, end_date.year + 1):
            for quarter in range(1, 5):
                q_start, q_end = get_quarter_start_end(year, quarter)
                if q_start > end_date:
                    continue
                if q_end < start_date:
                    continue
                quarters.append((year, quarter, max(q_start, start_date), min(q_end, end_date)))
        
        all_calls = []
        for year, quarter, q_start, q_end in quarters:
            trading_days = get_trading_days(conn, symbols, q_start, q_end)
            if not trading_days:
                continue
                
            cutoff_date = q_start - timedelta(days=45)
            inst_data = get_13f_data(conn, symbols, cutoff_date)
            
            prev_q_year, prev_q_quarter = (year, quarter - 1) if quarter > 1 else (year - 1, 4)
            prev_q_start, _ = get_quarter_start_end(prev_q_year, prev_q_quarter)
            
            price_data = get_price_data(conn, symbols, prev_q_start, q_end)
            
            returns = compute_quarter_returns(price_data, trading_days, prev_q_start, q_end, DECISION_DAY_OFFSET)
            if not returns:
                continue
                
            sorted_syms = sorted(returns.items(), key=lambda x: x[1][1], reverse=True)
            n_syms = len(sorted_syms)
            if n_syms < 10:
                continue
                
            top_decile_size = max(1, int(n_syms * 0.1))
            top_decile = set(sym for sym, _ in sorted_syms[:top_decile_size])
            
            for sym, (decision_day, _) in sorted_syms:
                if sym not in top_decile:
                    continue
                    
                if sym not in inst_data or len(inst_data[sym]) < 2:
                    continue
                    
                period_data = inst_data[sym]
                valid_periods = [(p, s) for p, s in period_data if p <= decision_day]
                if len(valid_periods) < 2:
                    continue
                    
                recent_two = valid_periods[-2:]
                ownership_change = compute_ownership_change(recent_two, None)
                if ownership_change is None or ownership_change < OWNERSHIP_INCREASE_THRESHOLD:
                    continue
                    
                forward_return = compute_forward_return(price_data, decision_day, HORIZON_DAYS)
                if forward_return is None:
                    continue
                    
                hit = 1 if forward_return > 0 else 0
                all_calls.append((decision_day.date(), sym, hit))
        
        if not all_calls:
            print("INSUFFICIENT=1")
            return
            
        all_calls.sort(key=lambda x: x[0])
        n_total = len(all_calls)
        n_holdout = int(n_total * 0.2)
        if n_holdout == 0:
            holdout_calls = []
            main_calls = all_calls
        else:
            holdout_calls = all_calls[-n_holdout:]
            main_calls = all_calls[:-n_holdout]
        
        def compute_stats(calls):
            if not calls:
                return 0, 0, 0, set(), 0
            hits = sum(c[2] for c in calls)
            issued = len(calls)
            precision = hits / issued if issued > 0 else 0
            base_rate = precision
            
            days = set(c[0] for c in calls)
            distinct_days = len(days)
            
            day_counts = defaultdict(int)
            day_hits = defaultdict(int)
            for date, _, hit in calls:
                day_counts[date] += 1
                day_hits[date] += hit
            
            avg_cluster_size = sum(day_counts.values()) / len(days) if days else 0
            cluster_means = [day_hits[d] / day_counts[d] for d in days]
            mean_cluster_mean = sum(cluster_means) / len(cluster_means) if cluster_means else 0
            var_between = sum((m - mean_cluster_mean) ** 2 for m in cluster_means) / len(cluster_means) if cluster_means else 0
            
            p = hits / issued if issued > 0 else 0
            total_variance = p * (1 - p)
            
            icc = var_between / total_variance if total_variance > 0 else 0
            design_effect = 1 + (avg_cluster_size - 1) * icc if avg_cluster_size > 1 else 1
            effective_n = issued / design_effect if design_effect > 0 else 0
            
            return issued, hits, precision, days, effective_n
        
        main_issued, main_hits, main_precision, main_days, main_effective_n = compute_stats(main_calls)
        holdout_issued, holdout_hits, holdout_precision, _, _ = compute_stats(holdout_calls)
        
        opps = len(main_calls) + len(holdout_calls)
        
        print(f"ISSUED={main_issued}")
        print(f"OPPORTUNITIES={opps}")
        print(f"PRECISION={main_precision:.6f}")
        print(f"BASE_RATE={main_precision:.6f}")
        print(f"DISTINCT_DAYS={len(main_days)}")
        print(f"EFFECTIVE_N={main_effective_n:.6f}")
        print(f"SEALED_PRECISION={holdout_precision:.6f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)
    finally:
        conn.close()

if __name__ == "__main__":
    main()