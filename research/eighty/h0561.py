# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 560
# cycle_index: 18
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from bisect import bisect_right
from collections import defaultdict
from datetime import datetime, timezone

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()
    
    cur.execute("SELECT DISTINCT ts FROM bars WHERE tf='1d' ORDER BY ts")
    all_trading_days = [row[0] for row in cur.fetchall()]
    if not all_trading_days:
        print("INSUFFICIENT=1")
        return
    
    day_to_idx = {ts: i for i, ts in enumerate(all_trading_days)}
    
    first_trading_days = []
    seen_months = set()
    for ts in all_trading_days:
        dt = datetime.fromtimestamp(ts, tz=timezone.utc)
        month_key = (dt.year, dt.month)
        if month_key not in seen_months:
            seen_months.add(month_key)
            first_trading_days.append(ts)
    
    signal_exit_map = {}
    for signal_ts in first_trading_days:
        idx = day_to_idx[signal_ts]
        exit_idx = idx + 5
        if exit_idx < len(all_trading_days):
            signal_exit_map[signal_ts] = all_trading_days[exit_idx]
    
    issued_calls = []
    total_opportunities = 0
    
    for signal_ts in first_trading_days:
        if signal_ts not in signal_exit_map:
            continue
        exit_ts = signal_exit_map[signal_ts]
        
        cur.execute("""
            SELECT symbol_id, close
            FROM bars
            WHERE tf='1d' AND ts = ? AND close >= 2
        """, (signal_ts,))
        candidates = cur.fetchall()
        if not candidates:
            continue
        
        total_opportunities += len(candidates)
        symbol_ids = [row['symbol_id'] for row in candidates]
        entry_closes = {row['symbol_id']: row['close'] for row in candidates}
        placeholders = ','.join('?' * len(symbol_ids))
        
        cur.execute(f"""
            SELECT symbol_id, COUNT(*) as cnt
            FROM bars
            WHERE tf='1d' AND symbol_id IN ({placeholders}) AND ts <= ?
            GROUP BY symbol_id
            HAVING cnt >= 252
        """, symbol_ids + [signal_ts])
        history_pass = {row['symbol_id'] for row in cur.fetchall()}
        if not history_pass:
            continue
        
        placeholders2 = ','.join('?' * len(history_pass))
        cur.execute(f"""
            SELECT symbol_id, close, volume, ts,
                   ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts DESC) as rn
            FROM bars
            WHERE tf='1d' AND symbol_id IN ({placeholders2}) AND ts <= ?
        """, list(history_pass) + [signal_ts])
        rows = cur.fetchall()
        
        symbol_dollar_vols = defaultdict(list)
        for row in rows:
            if row['rn'] <= 20:
                symbol_dollar_vols[row['symbol_id']].append(row['close'] * row['volume'])
        
        qualified_symbols = []
        for symbol_id, vols in symbol_dollar_vols.items():
            if len(vols) >= 20:
                sorted_vols = sorted(vols)
                n = len(sorted_vols)
                median = sorted_vols[n//2] if n % 2 == 1 else (sorted_vols[n//2 - 1] + sorted_vols[n//2]) / 2
                if median >= 2_000_000:
                    qualified_symbols.append(symbol_id)
        
        if not qualified_symbols:
            continue
        
        placeholders3 = ','.join('?' * len(qualified_symbols))
        cur.execute(f"""
            SELECT symbol_id, close
            FROM bars
            WHERE tf='1d' AND symbol_id IN ({placeholders3}) AND ts = ?
        """, qualified_symbols + [exit_ts])
        exit_closes = {row['symbol_id']: row['close'] for row in cur.fetchall()}
        
        for symbol_id in qualified_symbols:
            if symbol_id in exit_closes:
                entry_close = entry_closes[symbol_id]
                exit_close = exit_closes[symbol_id]
                fwd_return = (exit_close - entry_close) / entry_close
                up = 1 if fwd_return > 0 else 0
                issued_calls.append({
                    'signal_ts': signal_ts,
                    'symbol_id': symbol_id,
                    'entry_close': entry_close,
                    'exit_close': exit_close,
                    'fwd_return': fwd_return,
                    'up': up
                })
    
    if not issued_calls:
        print("INSUFFICIENT=1")
        return
    
    issued_calls.sort(key=lambda x: x['signal_ts'])
    
    # Split into sealed era (most recent 20%)
    total_issued = len(issued_calls)
    sealed_cutoff = int(total_issued * 0.8)
    if sealed_cutoff == total_issued:
        sealed_cutoff = total_issued - 1
    sealed_start_ts = issued_calls[sealed_cutoff]['signal_ts']
    
    full_calls = []
    sealed_calls = []
    for call in issued_calls:
        if call['signal_ts'] >= sealed_start_ts:
            sealed_calls.append(call)
        else:
            full_calls.append(call)
    
    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        hits = sum(1 for c in calls if c['up'] == 1)
        issued = len(calls)
        precision = hits / issued
        base_rate = hits / issued
        distinct_days = len(set(c['signal_ts'] for c in calls))
        design_effect = issued / distinct_days if distinct_days > 0 else 1
        effective_n = issued / design_effect
        return issued, precision, base_rate, distinct_days, effective_n
    
    full_issued, full_precision, full_base_rate, full_distinct_days, full_effective_n = compute_metrics(full_calls)
    sealed_issued, sealed_precision, sealed_base_rate, sealed_distinct_days, sealed_effective_n = compute_metrics(sealed_calls)
    
    print(f"ISSUED={full_issued}")
    print(f"OPPORTUNITIES={total_opportunities}")
    print(f"PRECISION={full_precision:.4f}")
    print(f"BASE_RATE={full_base_rate:.4f}")
    print(f"DISTINCT_DAYS={full_distinct_days}")
    print(f"EFFECTIVE_N={full_effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

if __name__ == "__main__":
    main()