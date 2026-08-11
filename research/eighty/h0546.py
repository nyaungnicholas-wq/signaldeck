# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 545
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
        
        # Get all symbols with stocktwits and news sentiment coverage
        c.execute("SELECT DISTINCT symbol_id FROM stocktwits_sentiment")
        st_symbols = set(r[0] for r in c.fetchall())
        
        c.execute("SELECT DISTINCT symbol_id FROM sentiment_features")
        nf_symbols = set(r[0] for r in c.fetchall())
        
        candidate_symbols = st_symbols & nf_symbols
        if not candidate_symbols:
            print("INSUFFICIENT=1")
            return
            
        # Get price data for candidate symbols
        c.execute("""
            SELECT b.symbol_id, b.ts, b.close, b.volume 
            FROM bars b 
            WHERE b.symbol_id IN ({}) 
            AND b.tf = '1d' 
            AND b.ts < ?
            ORDER BY b.symbol_id, b.ts
        """.format(','.join('?' * len(candidate_symbols))), 
                 list(candidate_symbols) + [2**32 - 1])
        
        price_data = defaultdict(list)
        for row in c.fetchall():
            price_data[row[0]].append((row[1], row[2], row[3]))
        
        # Filter to symbols with price > $5 and 20-day avg dollar volume > $1M
        valid_symbols = set()
        symbol_prices = {}
        for sym, data in price_data.items():
            if len(data) < 60:
                continue
            prices = [d[1] for d in data]
            if min(prices) <= 5:
                continue
            dollar_volumes = [d[1] * d[2] for d in data]
            if len(dollar_volumes) >= 20:
                avg_vol = sum(dollar_volumes[-20:]) / 20
                if avg_vol <= 1_000_000:
                    continue
            valid_symbols.add(sym)
            symbol_prices[sym] = data
        
        if not valid_symbols:
            print("INSUFFICIENT=1")
            return
        
        # Get StockTwits sentiment
        c.execute("""
            SELECT symbol_id, ts, total 
            FROM stocktwits_sentiment 
            WHERE symbol_id IN ({})
        """.format(','.join('?' * len(valid_symbols))), list(valid_symbols))
        
        stocktwits = defaultdict(list)
        for row in c.fetchall():
            stocktwits[row[0]].append((row[1], row[2]))
        
        # Get news sentiment
        c.execute("""
            SELECT symbol_id, day, mean_score 
            FROM sentiment_features 
            WHERE symbol_id IN ({})
        """.format(','.join('?' * len(valid_symbols))), list(valid_symbols))
        
        news_sentiment = defaultdict(list)
        for row in c.fetchall():
            day_ts = int(datetime.strptime(row[1], "%Y-%m-%d").timestamp())
            news_sentiment[row[0]].append((day_ts, row[2]))
        
        # Get prediction outcomes for 21-day horizon
        c.execute("""
            SELECT symbol_id, ts, up 
            FROM prediction_outcomes 
            WHERE symbol_id IN ({}) 
            AND horizon = 21
        """.format(','.join('?' * len(valid_symbols))), list(valid_symbols))
        
        outcomes = defaultdict(dict)
        for row in c.fetchall():
            outcomes[row[0]][row[1]] = row[2]
        
        conn.close()
        
        # Find common dates for each symbol
        opportunities = []
        for sym in valid_symbols:
            prices = symbol_prices[sym]
            st_data = stocktwits.get(sym, [])
            nf_data = news_sentiment.get(sym, [])
            
            if not st_data or not nf_data:
                continue
                
            # Create date-based indices
            st_by_day = {}
            for ts, total in st_data:
                day = ts // 86400
                st_by_day[day] = st_by_day.get(day, 0) + total
            
            nf_by_day = {}
            for ts, score in nf_data:
                day = ts // 86400
                nf_by_day[day] = score
            
            price_by_day = {}
            for ts, close, vol in prices:
                day = ts // 86400
                price_by_day[day] = (close, vol, ts)
            
            common_days = sorted(set(st_by_day.keys()) & set(nf_by_day.keys()) & set(price_by_day.keys()))
            
            if len(common_days) < 60:
                continue
                
            for i, day in enumerate(common_days):
                if i < 59:
                    continue
                
                # Check as-of: we can only use data up to decision day
                decision_ts = price_by_day[day][2]
                
                # Get StockTwits 20-day average
                st_window = []
                for d in common_days[max(0, i-19):i+1]:
                    st_window.append(st_by_day[d])
                if len(st_window) < 20:
                    continue
                st_avg = sum(st_window) / len(st_window)
                
                # Get news sentiment moving averages
                nf_window = []
                for d in common_days[max(0, i-19):i+1]:
                    if d in nf_by_day:
                        nf_window.append(nf_by_day[d])
                
                if len(nf_window) < 20:
                    continue
                
                nf_5ma = sum(nf_window[-5:]) / 5
                nf_20ma = sum(nf_window) / 20
                
                # Check if 5-day MA has risen for >=10 consecutive sessions
                rising = True
                if i >= 9:
                    for j in range(i-9, i+1):
                        window = [nf_by_day.get(common_days[k], 0) for k in range(max(0, j-4), j+1)]
                        if len(window) < 5:
                            rising = False
                            break
                        if j > 0:
                            prev_window = [nf_by_day.get(common_days[k], 0) for k in range(max(0, j-5), j)]
                            if len(prev_window) >= 5:
                                curr_ma = sum(window) / 5
                                prev_ma = sum(prev_window) / 5
                                if curr_ma <= prev_ma:
                                    rising = False
                                    break
                else:
                    rising = False
                
                opportunities.append({
                    'symbol_id': sym,
                    'decision_ts': decision_ts,
                    'decision_day': day,
                    'st_avg': st_avg,
                    'nf_5ma': nf_5ma,
                    'nf_20ma': nf_20ma,
                    'rising': rising
                })
        
        if not opportunities:
            print("INSUFFICIENT=1")
            return
        
        # Calculate cross-sectional quintiles for each day
        day_st_avgs = defaultdict(list)
        for opp in opportunities:
            day_st_avgs[opp['decision_day']].append(opp['st_avg'])
        
        quintile_thresholds = {}
        for day, avgs in day_st_avgs.items():
            sorted_avgs = sorted(avgs)
            q20 = sorted_avgs[len(sorted_avgs) // 5]
            quintile_thresholds[day] = q20
        
        # Filter opportunities
        issued = []
        for opp in opportunities:
            day = opp['decision_day']
            threshold = quintile_thresholds[day]
            
            if opp['st_avg'] <= threshold and opp['nf_5ma'] > opp['nf_20ma'] and opp['rising']:
                issued.append(opp)
        
        if not issued:
            print("INSUFFICIENT=1")
            return
        
        # Split into non-sealed and sealed eras
        all_days = sorted(set(opp['decision_day'] for opp in issued))
        split_idx = int(len(all_days) * 0.8)
        sealed_days = set(all_days[split_idx:])
        
        non_sealed = [opp for opp in issued if opp['decision_day'] not in sealed_days]
        sealed = [opp for opp in issued if opp['decision_day'] in sealed_days]
        
        # Calculate metrics for non-sealed era
        if not non_sealed:
            print("INSUFFICIENT=1")
            return
        
        hits = 0
        for opp in non_sealed:
            sym = opp['symbol_id']
            decision_ts = opp['decision_ts']
            if sym in outcomes and decision_ts in outcomes[sym]:
                if outcomes[sym][decision_ts]:
                    hits += 1
        
        issued_count = len(non_sealed)
        opportunities_count = len(opportunities)
        precision = hits / issued_count if issued_count > 0 else 0
        base_rate = hits / issued_count if issued_count > 0 else 0
        distinct_days = len(set(opp['decision_day'] for opp in non_sealed))
        
        # Calculate design effect
        day_counts = defaultdict(int)
        for opp in non_sealed:
            day_counts[opp['decision_day']] += 1
        
        n = issued_count
        sum_sq = sum(count**2 for count in day_counts.values())
        design_effect = (sum_sq / n) / (n / len(day_counts)) if n > 0 and len(day_counts) > 0 else 1
        effective_n = n / design_effect
        
        # Calculate sealed precision
        sealed_hits = 0
        for opp in sealed:
            sym = opp['symbol_id']
            decision_ts = opp['decision_ts']
            if sym in outcomes and decision_ts in outcomes[sym]:
                if outcomes[sym][decision_ts]:
                    sealed_hits += 1
        
        sealed_precision = sealed_hits / len(sealed) if sealed else 0
        
        # Print results
        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities_count}")
        print(f"PRECISION={precision:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.2f}")
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()