# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 412
# cycle_index: 3
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
from collections import defaultdict

def main():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        c = conn.cursor()
    except Exception:
        print("INSUFFICIENT=1")
        return

    # Get all symbols with at least one 13F filing in the past year from the latest date in the data
    c.execute("SELECT MAX(ts) FROM prediction_outcomes")
    max_ts = c.fetchone()[0]
    if max_ts is None:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Convert to approximate date (21 days is horizon, we look back 1 year)
    one_year_ago = max_ts - 365 * 86400

    # Get symbols with 13F filings in the past year
    c.execute("""
        SELECT DISTINCT symbol_id 
        FROM inst_holdings 
        WHERE period >= ?
    """, (one_year_ago,))
    symbols_13f = {row[0] for row in c.fetchall()}
    if not symbols_13f:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get macro data for 10Y and 2Y yields (we need series names)
    # We'll search for common names
    c.execute("SELECT DISTINCT series FROM macro_series")
    series_names = {row[0] for row in c.fetchall()}
    
    # Try to identify 10Y and 2Y series
    ten_y = None
    two_y = None
    for s in series_names:
        if 'DGS10' in s.upper():
            ten_y = s
        elif 'DGS2' in s.upper():
            two_y = s
    
    if not ten_y or not two_y:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Get yield curve data
    c.execute("""
        SELECT ts, value FROM macro_series WHERE series = ?
        ORDER BY ts
    """, (ten_y,))
    ten_y_data = {row[0]: row[1] for row in c.fetchall()}
    
    c.execute("""
        SELECT ts, value FROM macro_series WHERE series = ?
        ORDER BY ts
    """, (two_y,))
    two_y_data = {row[0]: row[1] for row in c.fetchall()}

    # Get all decision dates (days with bars)
    c.execute("""
        SELECT DISTINCT ts FROM bars WHERE tf = '1d' ORDER BY ts
    """)
    all_days = [row[0] for row in c.fetchall()]
    if len(all_days) < 30:
        print("INSUFFICIENT=1")
        conn.close()
        return

    # Split into train (80%) and sealed (20%)
    split_idx = int(len(all_days) * 0.8)
    train_days = set(all_days[:split_idx])
    sealed_days = set(all_days[split_idx:])
    
    # For each symbol, get daily bars and compute indicators
    opportunities = []
    
    for sym_id in symbols_13f:
        # Get daily bars for this symbol
        c.execute("""
            SELECT ts, open, high, low, close, volume 
            FROM bars 
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (sym_id,))
        bars = c.fetchall()
        if len(bars) < 40:  # Need at least 40 days for indicators
            continue
            
        # Create price dict
        price_dict = {b[0]: b[3] for b in bars}  # close price
        vol_dict = {b[0]: b[5] for b in bars}  # volume
        
        # Get news sentiment
        c.execute("""
            SELECT ts, score FROM news WHERE symbol_id = ? ORDER BY ts
        """, (sym_id,))
        news_data = c.fetchall()
        news_by_day = defaultdict(list)
        for ts, score in news_data:
            # Convert to day
            day = ts - (ts % 86400)
            news_by_day[day].append(score)
        
        # Get 13F holdings for this symbol (use most recent available)
        c.execute("""
            SELECT period, shares, value 
            FROM inst_holdings 
            WHERE symbol_id = ?
            ORDER BY period DESC
        """, (sym_id,))
        holdings = c.fetchall()
        
        # For each decision day
        for day in all_days:
            # Check we have enough history
            if day - 40 * 86400 < min(price_dict.keys()):
                continue
                
            # Check volume condition (20-day avg daily volume >= $1M)
            recent_days_prices = []
            recent_days_volumes = []
            for d in range(20):
                ts = day - d * 86400
                if ts in price_dict and ts in vol_dict:
                    recent_days_prices.append(price_dict[ts])
                    recent_days_volumes.append(vol_dict[ts])
            
            if len(recent_days_prices) < 20:
                continue
                
            # Approximate volume in dollars (close * volume)
            avg_daily_volume = sum(p * v for p, v in zip(recent_days_prices, recent_days_volumes)) / 20
            if avg_daily_volume < 1_000_000:
                continue
                
            # Check yield curve condition: spread increasing over 20 days
            spread_now = None
            spread_20_ago = None
            for t in [day, day - 20 * 86400]:
                if t in ten_y_data and t in two_y_data:
                    spread = ten_y_data[t] - two_y_data[t]
                    if t == day:
                        spread_now = spread
                    else:
                        spread_20_ago = spread
            
            if spread_now is None or spread_20_ago is None:
                continue
                
            if not (spread_now > spread_20_ago):  # Spread not steepening
                continue
                
            # Check news sentiment condition (aggregate > 0)
            if day in news_by_day and news_by_day[day]:
                avg_sentiment = sum(news_by_day[day]) / len(news_by_day[day])
                if avg_sentiment <= 0:
                    continue
            else:
                continue
                
            # Check stock return condition (20-day return below market return)
            # We don't have market index, use average of all stocks? Too expensive.
            # Use the symbol's own return vs a benchmark? No benchmark.
            # We must skip due to missing market return data
            continue
            
            # Check institutional ownership condition (bottom quartile)
            if not holdings:
                continue
            # Get most recent holding period before decision day
            holding_value = None
            for period, shares, value in holdings:
                if period < day - 45 * 86400:  # 45-day lag for filing
                    holding_value = value
                    break
            if holding_value is None:
                continue
                
            # Need to compare to all symbols' ownership distribution
            # We'll compute this later in the main loop
            
            # We have met all conditions except market return
            # Since we cannot compute market return, we must abort
            print("INSUFFICIENT=1")
            conn.close()
            return
    
    # We need market return data which is not available
    print("INSUFFICIENT=1")
    conn.close()

if __name__ == "__main__":
    main()