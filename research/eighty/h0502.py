# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 501
# cycle_index: 31
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 21
SENTIMENT_WINDOW = 10
VOLUME_WINDOW = 20
HIGH_WINDOW = 252
MIN_SENTIMENT_HISTORY = 730
ENTRY_SENTIMENT_MIN = -0.2
ENTRY_SENTIMENT_MAX = 0.0
ENTRY_VOLUME_RATIO = 0.80
ENTRY_PRICE_FROM_HIGH = 0.90
ABSTAIN_SENTIMENT_DAYS_MAX = 5
ABSTAIN_VOLUME_RATIO = 1.0
ABSTAIN_PRICE_FROM_HIGH = 0.95
ABSTAIN_SENTIMENT_VOL_MAX = 0.3
CLAIM = 0.80

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    
    # Get universe: symbols with >=2yr sentiment + fundamentals
    c.execute("""
        SELECT symbol_id FROM sentiment_features
        GROUP BY symbol_id HAVING COUNT(DISTINCT day) >= ?
    """, (MIN_SENTIMENT_HISTORY,))
    sentiment_syms = {r[0] for r in c.fetchall()}
    
    c.execute("SELECT DISTINCT symbol_id FROM fundamentals")
    fundamental_syms = {r[0] for r in c.fetchall()}
    
    universe = sentiment_syms & fundamental_syms
    if not universe:
        print("INSUFFICIENT=1")
        return
    
    # Get all needed data
    syms = tuple(universe)
    
    # Get bars (daily)
    c.execute("""
        SELECT symbol_id, ts, volume, high, close
        FROM bars WHERE tf='1d' AND symbol_id IN ({})
    """.format(','.join('?'*len(syms))), syms)
    bars = defaultdict(list)
    for r in c.fetchall():
        bars[r[0]].append((r[1], r[2], r[3], r[4]))  # ts, volume, high, close
    for s in bars:
        bars[s].sort(key=lambda x: x[0])
    
    # Get sentiment
    c.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features WHERE symbol_id IN ({})
    """.format(','.join('?'*len(syms))), syms)
    sentiment = defaultdict(list)
    for r in c.fetchall():
        sentiment[r[0]].append((r[1], r[2]))  # day, mean_score
    for s in sentiment:
        sentiment[s].sort(key=lambda x: x[0])
    
    # Precompute day indices for each symbol
    day_index = {}
    for s in universe:
        if s not in sentiment or len(sentiment[s]) < SENTIMENT_WINDOW:
            continue
        day_to_idx = {d[0]: i for i, d in enumerate(sentiment[s])}
        day_index[s] = day_to_idx
    
    # Get outcomes (labels)
    c.execute("""
        SELECT symbol_id, basis_epoch, up
        FROM prediction_outcomes WHERE horizon = ? AND symbol_id IN ({})
    """.format(','.join('?'*len(syms))), (HORIZON,) + syms)
    outcomes = {}
    for r in c.fetchall():
        sym, basis, up = r[0], r[1], r[2]
        # Convert basis to date
        basis_date = None
        if basis:
            basis_date = sqlite3.format_date(basis)
        outcomes[(sym, basis_date)] = up
    conn.close()
    
    calls = []
    opportunities = 0
    
    for sym in universe:
        if sym not in sentiment or sym not in bars:
            continue
        sent_data = sentiment[sym]
        bar_data = bars[sym]
        if len(bar_data) < HIGH_WINDOW:
            continue
        
        # Build bar lookup by date
        bar_by_date = {}
        for b in bar_data:
            date = sqlite3.format_date(b[0])
            bar_by_date[date] = b  # (ts, volume, high, close)
        
        sent_dates = [s[0] for s in sent_data]
        
        for i in range(SENTIMENT_WINDOW - 1, len(sent_data)):
            current_day = sent_data[i][0]
            opportunities += 1
            
            # Get recent sentiment scores (last 10 days including current)
            recent_sent = [sent_data[j][1] for j in range(i-SENTIMENT_WINDOW+1, i+1)]
            
            # Abstain conditions
            # 1. Sentiment >0 for >5 of past 10 days
            sent_above_zero = sum(1 for s in recent_sent if s > 0)
            if sent_above_zero > ABSTAIN_SENTIMENT_DAYS_MAX:
                continue
            
            # 2. Sentiment volatility >0.3
            if len(recent_sent) >= 2:
                mean = sum(recent_sent) / len(recent_sent)
                var = sum((s - mean)**2 for s in recent_sent) / len(recent_sent)
                std = math.sqrt(var)
                if std > ABSTAIN_SENTIMENT_VOL_MAX:
                    continue
            
            # Need volume and price data
            if current_day not in bar_by_date:
                continue
            
            current_bar = bar_by_date[current_day]
            current_volume = current_bar[1]
            current_high = current_bar[2]
            current_close = bar_by_date[current_day][3]
            
            # Get past 20 days volume for average (excluding current)
            past_volumes = []
            for j in range(1, VOLUME_WINDOW + 1):
                if i - j >= 0:
                    prev_day = sent_data[i-j][0]
                    if prev_day in bar_by_date:
                        past_volumes.append(bar_by_date[prev_day][1])
            if len(past_volumes) < VOLUME_WINDOW - 5:  # Allow some missing
                continue
            avg_volume = sum(past_volumes) / len(past_volumes)
            
            # Abstain: volume above average
            if current_volume > avg_volume * ABSTAIN_VOLUME_RATIO:
                continue
            
            # Get 52-week high (252 trading days)
            high_prices = []
            for j in range(1, HIGH_WINDOW + 1):
                if i - j >= 0:
                    prev_day = sent_data[i-j][0]
                    if prev_day in bar_by_date:
                        high_prices.append(bar_by_date[prev_day][2])
            if len(high_prices) < HIGH_WINDOW * 0.8:
                continue
            high_52w = max(high_prices)
            
            # Abstain: within 5% of high
            if current_close >= high_52w * ABSTAIN_PRICE_FROM_HIGH:
                continue
            
            # Entry conditions
            # 1. Sentiment rose from below -0.2 to above 0 over past 10 days
            past_sent = [sent_data[j][1] for j in range(i-SENTIMENT_WINDOW+1, i)]
            if not past_sent:
                continue
            min_past_sent = min(past_sent)
            if not (min_past_sent < ENTRY_SENTIMENT_MIN and recent_sent[-1] > ENTRY_SENTIMENT_MAX):
                continue
            
            # 2. Volume below 20-day average by at least 20%
            if current_volume > avg_volume * ENTRY_VOLUME_RATIO:
                continue
            
            # 3. Down at least 10% from 52-week high
            if current_close > high_52w * ENTRY_PRICE_FROM_HIGH:
                continue
            
            # Get outcome
            up = outcomes.get((sym, current_day))
            if up is not None:
                calls.append((current_day, up))
    
    if not calls:
        print("INSUFFICIENT=1")
        return
    
    # Sort by date and split into main/sealed (last 20% as sealed)
    calls.sort(key=lambda x: x[0])
    split_idx = int(len(calls) * 0.8)
    main_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]
    
    # Compute metrics for main set
    issued = len(main_calls)
    if issued == 0:
        print("INSUFFICIENT=1")
        return
    
    hits = sum(1 for _, up in main_calls if up)
    precision = hits / issued
    base_rate = precision  # Same as precision for binary classification
    
    # Distinct days
    distinct_days = len({day for day, _ in main_calls})
    
    # Design effect: cluster by day
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for day, up in main_calls:
        day_counts[day] += 1
        if up:
            day_hits[day] += 1
    
    k = issued / distinct_days  # Average cluster size
    p = precision
    
    # Weighted variance of day-level proportions
    weighted_sq_sum = 0
    total_weight = 0
    for day, n in day_counts.items():
        p_i = day_hits[day] / n
        weighted_sq_sum += n * (p_i - p) ** 2
        total_weight += n
    
    if total_weight > 0 and p * (1 - p) > 0:
        icc = weighted_sq_sum / (total_weight * p * (1 - p))
    else:
        icc = 0
    
    design_effect = 1 + (k - 1) * icc
    effective_n = issued / design_effect if design_effect > 0 else issued
    
    # Sealed precision
    sealed_issued = len(sealed_calls)
    sealed_hits = sum(1 for _, up in sealed_calls if up)
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
    
    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()