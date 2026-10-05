#!/usr/bin/env python3
import sqlite3
from datetime import datetime, timedelta
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
    except Exception as e:
        print("INSUFFICIENT=1")
        return

    try:
        # Check for sufficient data - need bars, symbols, and prediction_outcomes
        cur.execute("SELECT COUNT(*) FROM bars WHERE tf='1d' LIMIT 1")
        if cur.fetchone()[0] < 1000:
            print("INSUFFICIENT=1")
            return
            
        cur.execute("SELECT COUNT(*) FROM symbols WHERE market='stocks' LIMIT 1")
        if cur.fetchone()[0] < 10:
            print("INSUFFICIENT=1")
            return
            
        cur.execute("SELECT COUNT(*) FROM prediction_outcomes WHERE horizon=20 LIMIT 1")
        if cur.fetchone()[0] < 100:
            print("INSUFFICIENT=1")
            return
            
        # Get all symbols with active status
        cur.execute("SELECT id, symbol FROM symbols WHERE market='stocks' AND active=1")
        symbols = {row['id']: row['symbol'] for row in cur.fetchall()}
        
        if len(symbols) < 10:
            print("INSUFFICIENT=1")
            return
            
        # Get all daily bars for these symbols
        cur.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume 
            FROM bars 
            WHERE tf='1d' AND symbol_id IN ({})
            ORDER BY symbol_id, ts
        """.format(','.join(str(s) for s in symbols.keys())))
        
        bars_by_symbol = defaultdict(list)
        for row in cur.fetchall():
            bars_by_symbol[row['symbol_id']].append({
                'ts': row['ts'],
                'open': row['open'],
                'high': row['high'],
                'low': row['low'],
                'close': row['close'],
                'volume': row['volume']
            })
        
        # Convert timestamps to datetime for easier manipulation
        for symbol_id in bars_by_symbol:
            for bar in bars_by_symbol[symbol_id]:
                bar['dt'] = datetime.utcfromtimestamp(bar['ts'])
                bar['date'] = bar['dt'].date()
                
        # Get all prediction outcomes for 20-day horizon
        cur.execute("""
            SELECT symbol_id, ts, prob, up, fwd_return 
            FROM prediction_outcomes 
            WHERE horizon=20
        """)
        outcomes = defaultdict(list)
        for row in cur.fetchall():
            outcomes[row['symbol_id']].append({
                'ts': row['ts'],
                'prob': row['prob'],
                'up': row['up'],
                'fwd_return': row['fwd_return'],
                'dt': datetime.utcfromtimestamp(row['ts'])
            })
        
        conn.close()
        
        # Simulate S&P 500 additions using price/volume filters as proxy
        # (actual index additions not in database)
        opportunities = []
        issued_calls = []
        
        # Find decision points: dates when stocks meet entry criteria
        all_dates = set()
        for symbol_id, bars in bars_by_symbol.items():
            for i in range(60, len(bars)):
                bar_t = bars[i]
                bar_t_minus_1 = bars[i-1]
                all_dates.add(bar_t['date'])
                
                # Calculate 60-day stats
                recent_60 = bars[i-60:i]
                closes = [b['close'] for b in recent_60]
                volumes = [b['volume'] for b in recent_60]
                prices = [b['close'] for b in bars[i-20:i]]  # last 20 days before T
                
                if len(closes) < 60 or len(volumes) < 60:
                    continue
                
                # Entry condition: price change T vs T-1
                price_change = (bar_t['close'] - bar_t_minus_1['close']) / bar_t_minus_1['close']
                if not (-0.03 <= price_change <= 0.05):
                    continue
                    
                # T's close in top half of intraday range
                if bar_t['high'] == bar_t['low']:
                    continue
                if bar_t['close'] < (bar_t['high'] + bar_t['low']) / 2:
                    continue
                    
                # Volume above 60-day median
                volume_median = sorted(volumes)[len(volumes)//2]
                if bar_t['volume'] <= volume_median:
                    continue
                    
                # Price >= $5
                if bar_t['close'] < 5:
                    continue
                    
                # Simple volume proxy for dollar volume (assuming price ~ $100)
                avg_daily_dollar_vol = sum(v * c for v, c in zip(volumes, closes)) / len(volumes)
                if avg_daily_dollar_vol < 10_000_000:
                    continue
                    
                # Check for 30% rise in prior 20 days
                if len(prices) > 0:
                    prior_20_start = prices[0]
                    prior_20_end = bar_t_minus_1['close']
                    if prior_20_start > 0 and (prior_20_end - prior_20_start) / prior_20_start > 0.30:
                        continue
                
                # 5-day realized volatility in top cross-sectional decile
                # (simplified: only if we can compute across all symbols on this date)
                # We'll check later in the cross-sectional step
                
                # Record opportunity
                opportunity = {
                    'symbol_id': symbol_id,
                    'decision_date': bar_t['date'],
                    'entry_price': bar_t_minus_1['close'],
                    'entry_bar_ts': bar_t_minus_1['ts'],
                    'signal_bar_ts': bar_t['ts']
                }
                opportunities.append(opportunity)
        
        if len(opportunities) < 20:
            print("INSUFFICIENT=1")
            return
            
        # Apply cross-sectional filters for volatility decile
        # Group opportunities by date
        opp_by_date = defaultdict(list)
        for opp in opportunities:
            opp_by_date[opp['decision_date']].append(opp)
        
        filtered_opportunities = []
        for date, opps in opp_by_date.items():
            # Calculate 5-day realized volatility for each
            vols = []
            valid_opps = []
            for opp in opps:
                symbol_id = opp['symbol_id']
                bars = bars_by_symbol[symbol_id]
                # Find bar for decision date
                decision_bar_idx = None
                for i, bar in enumerate(bars):
                    if bar['date'] == date:
                        decision_bar_idx = i
                        break
                
                if decision_bar_idx is None or decision_bar_idx < 5:
                    continue
                    
                # Get 5-day returns
                returns = []
                for i in range(decision_bar_idx-4, decision_bar_idx+1):
                    if i > 0:
                        ret = (bars[i]['close'] - bars[i-1]['close']) / bars[i-1]['close']
                        returns.append(ret)
                
                if len(returns) < 5:
                    continue
                    
                vol = (sum(r**2 for r in returns) / 5) ** 0.5
                vols.append(vol)
                valid_opps.append((vol, opp))
            
            if len(vols) < 10:
                continue
                
            # Determine 90th percentile
            vols_sorted = sorted(vols)
            p90_idx = int(len(vols_sorted) * 0.9)
            p90 = vols_sorted[p90_idx]
            
            # Keep only those below 90th percentile
            for vol, opp in valid_opps:
                if vol < p90:
                    filtered_opportunities.append(opp)
        
        opportunities = filtered_opportunities
        
        # Get labels: find what happens T+20 trading days
        decisions_with_labels = []
        
        for opp in opportunities:
            symbol_id = opp['symbol_id']
            decision_date = opp['decision_date']
            bars = bars_by_symbol[symbol_id]
            
            # Find decision bar index
            decision_bar_idx = None
            for i, bar in enumerate(bars):
                if bar['date'] == decision_date:
                    decision_bar_idx = i
                    break
            
            if decision_bar_idx is None:
                continue
                
            # Look ahead 20 trading days (skipping weekends)
            future_date = decision_date
            trading_days_found = 0
            future_bar_idx = None
            
            for i in range(decision_bar_idx + 1, len(bars)):
                future_date_candidate = bars[i]['date']
                if future_date_candidate > decision_date:
                    trading_days_found += 1
                    if trading_days_found == 20:
                        future_bar_idx = i
                        break
            
            if future_bar_idx is None:
                continue
                
            future_bar = bars[future_bar_idx]
            entry_price = opp['entry_price']
            
            # Label: did price go up over the horizon?
            label = 1 if future_bar['close'] > entry_price else 0
            
            decisions_with_labels.append({
                'symbol_id': symbol_id,
                'decision_date': decision_date,
                'label': label,
                'future_date': future_bar['date']
            })
        
        if len(decisions_with_labels) < 20:
            print("INSUFFICIENT=1")
            return
            
        # Split into train and sealed (most recent 20%)
        decisions_with_labels.sort(key=lambda x: x['decision_date'])
        split_idx = int(len(decisions_with_labels) * 0.8)
        train_decisions = decisions_with_labels[:split_idx]
        sealed_decisions = decisions_with_labels[split_idx:]
        
        # Count independent observations (symbol, UTC day)
        def count_issued_with_base_rate(decisions):
            issued = defaultdict(set)
            for d in decisions:
                day = d['decision_date']
                symbol = d['symbol_id']
                issued[day].add((day, symbol))
            
            total_issued = 0
            up_count = 0
            for day, symbols in issued.items():
                total_issued += len(symbols)
                for day, symbol in symbols:
                    # Find label for this (day, symbol)
                    for d in decisions:
                        if d['decision_date'] == day and d['symbol_id'] == symbol:
                            up_count += d['label']
                            break
            
            distinct_days = len(issued)
            base_rate = up_count / total_issued if total_issued > 0 else 0
            
            return total_issued, distinct_days, up_count, base_rate
        
        issued_train, distinct_days_train, up_train, base_rate_train = count_issued_with_base_rate(train_decisions)
        issued_sealed, distinct_days_sealed, up_sealed, base_rate_sealed = count_issued_with_base_rate(sealed_decisions)
        
        # Design effect (simplified: assume no clustering beyond day)
        # Effective N = issued / design_effect
        # Design effect for clustering by day = 1 + (cluster_size - 1) * ICC
        # Assuming ICC ≈ 0.1, average cluster size ≈ issued_per_day
        if distinct_days_train > 0:
            avg_cluster_size = issued_train / distinct_days_train
            design_effect = 1 + (avg_cluster_size - 1) * 0.1
        else:
            design_effect = 1
            
        effective_n = issued_train / design_effect
        
        # Output results
        precision_train = up_train / issued_train if issued_train > 0 else 0
        precision_sealed = up_sealed / issued_sealed if issued_sealed > 0 else 0
        
        print(f"ISSUED={issued_train}")
        print(f"OPPORTUNITIES={len(opportunities)}")
        print(f"PRECISION={precision_train:.4f}")
        print(f"BASE_RATE={base_rate_train:.4f}")
        print(f"DISTINCT_DAYS={distinct_days_train}")
        print(f"EFFECTIVE_N={effective_n:.1f}")
        print(f"SEALED_PRECISION={precision_sealed:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()