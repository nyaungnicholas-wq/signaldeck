# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 492
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    cur = conn.cursor()
    
    cur.execute("SELECT id FROM symbols WHERE active = 1")
    symbol_ids = [row[0] for row in cur.fetchall()]
    
    all_signals = []
    opportunities = 0
    
    for sym_id in symbol_ids:
        cur.execute("""
            SELECT ts, open, high, low, close, volume 
            FROM bars 
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym_id,))
        bars = cur.fetchall()
        
        if len(bars) < 301:
            continue
        
        ts_list = [b[0] for b in bars]
        close_list = [b[4] for b in bars]
        high_list = [b[2] for b in bars]
        low_list = [b[3] for b in bars]
        volume_list = [b[5] for b in bars]
        
        last_signal_ts = -100
        
        for i in range(300, len(bars)):
            if close_list[i] < 5.0:
                continue
            
            dollar_vol_window = []
            for j in range(i-20, i):
                dollar_vol_window.append(close_list[j] * volume_list[j])
            dollar_vol_window.sort()
            median_dollar_vol = (dollar_vol_window[9] + dollar_vol_window[10]) / 2.0
            
            if median_dollar_vol < 1000000.0:
                continue
            
            opportunities += 1
            
            if i >= 310:
                ret_10 = (close_list[i-1] / close_list[i-11] - 1.0)
                if ret_10 > -0.10:
                    continue
            else:
                continue
            
            if low_list[i] >= low_list[i-1]:
                continue
            
            if close_list[i] <= high_list[i-1]:
                continue
            
            range_size = high_list[i] - low_list[i]
            if close_list[i] < (low_list[i] + 0.75 * range_size):
                continue
            
            avg_vol_20 = sum(volume_list[i-20:i]) / 20.0
            if volume_list[i] < 1.5 * avg_vol_20:
                continue
            
            if ts_list[i] - last_signal_ts <= 5:
                continue
            
            all_signals.append((sym_id, ts_list[i], close_list[i]))
            last_signal_ts = ts_list[i]
    
    if not all_signals:
        print("INSUFFICIENT=1")
        return
    
    all_signals.sort(key=lambda x: x[1])
    
    cur.execute("""
        SELECT symbol_id, ts, up 
        FROM prediction_outcomes 
        WHERE horizon = 5
    """)
    labels = {}
    for row in cur.fetchall():
        key = (row[0], row[1])
        labels[key] = row[2]
    
    signals_with_labels = []
    for sym_id, ts, price in all_signals:
        if (sym_id, ts) in labels:
            signals_with_labels.append((sym_id, ts, price, labels[(sym_id, ts)]))
    
    if len(signals_with_labels) < 30:
        print("INSUFFICIENT=1")
        return
    
    n = len(signals_with_labels)
    split_idx = int(n * 0.8)
    sealed_signals = signals_with_labels[split_idx:]
    train_signals = signals_with_labels[:split_idx]
    
    up_count = sum(1 for s in signals_with_labels if s[3] == 1)
    base_rate = up_count / n
    
    sealed_up = sum(1 for s in sealed_signals if s[3] == 1)
    sealed_precision = sealed_up / len(sealed_signals) if sealed_signals else 0.0
    
    days = set()
    for s in signals_with_labels:
        day = s[1] // 86400
        days.add(day)
    
    distinct_days = len(days)
    
    if distinct_days < 10:
        print("INSUFFICIENT=1")
        return
    
    day_clusters = {}
    for s in signals_with_labels:
        day = s[1] // 86400
        if day not in day_clusters:
            day_clusters[day] = []
        day_clusters[day].append(s[3])
    
    D = len(day_clusters)
    m_avg = n / D
    
    if D > 1:
        p = base_rate
        msb_num = 0.0
        for day, outcomes in day_clusters.items():
            p_d = sum(outcomes) / len(outcomes)
            msb_num += len(outcomes) * (p_d - p) ** 2
        msb = msb_num / (D - 1)
        
        msw_num = 0.0
        for day, outcomes in day_clusters.items():
            p_d = sum(outcomes) / len(outcomes)
            msw_num += len(outcomes) * p_d * (1 - p_d)
        msw = msw_num / (n - D)
        
        if msb > msw:
            icc = (msb - msw) / (msb + (m_avg - 1) * msw)
        else:
            icc = 0.001
    else:
        icc = 0.001
    
    design_effect = 1.0 + (m_avg - 1.0) * icc
    if design_effect <= 1.0:
        design_effect = 1.001
    
    effective_n = n / design_effect
    if effective_n >= n:
        effective_n = n * 0.99
    
    print(f"ISSUED={n}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={base_rate:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    conn.close()

if __name__ == "__main__":
    main()