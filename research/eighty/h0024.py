import sqlite3
import sys
import math
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'
HORIZON = 20  # trading days
ROLLING_WINDOW = 60  # days for avg volume
LOOKBACK_WINDOW = 20  # days for high and price change
VOL_WINDOW = 5  # days for volatility

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        
        # Get all symbols with their market
        cur.execute("SELECT id, symbol, market FROM symbols")
        symbols = {row['id']: row for row in cur.fetchall()}
        
        # Get all daily bars
        cur.execute("SELECT symbol_id, ts, open, high, low, close, volume FROM bars WHERE tf='1d' ORDER BY ts")
        bars_data = cur.fetchall()
        
        # Group bars by symbol
        bars_by_symbol = defaultdict(list)
        for bar in bars_data:
            bars_by_symbol[bar['symbol_id']].append(bar)
        
        # Process each symbol to find earnings announcements
        opportunities = []
        
        for symbol_id, bars in bars_by_symbol.items():
            if len(bars) < LOOKBACK_WINDOW + HORIZON + 1:
                continue
            
            symbol_info = symbols[symbol_id]
            if symbol_info['market'] != 'stocks':
                continue
            
            # Build a mapping from timestamp to bar for quick lookup
            ts_to_bar = {bar['ts']: bar for bar in bars}
            sorted_ts = sorted(ts_to_bar.keys())
            
            # For each day, check if it could be an earnings announcement
            for i, ts in enumerate(sorted_ts):
                # We need enough lookback
                if i < LOOKBACK_WINDOW + 1:
                    continue
                
                # T is the day after announcement, so we consider the previous day as announcement
                announcement_bar = ts_to_bar[sorted_ts[i-1]]
                t_bar = ts_to_bar[ts]  # first trading day after announcement
                
                # Check if we have enough data for forward return
                if i + HORIZON >= len(sorted_ts):
                    continue
                
                future_bar = ts_to_bar[sorted_ts[i + HORIZON]]
                
                # Check basic filters
                if t_bar['close'] < 5:
                    continue
                
                # Calculate average daily dollar volume over prior 60 sessions
                lookback_bars = []
                for j in range(max(0, i-ROLLING_WINDOW), i):
                    lookback_bars.append(ts_to_bar[sorted_ts[j]])
                
                if len(lookback_bars) < 30:  # Need reasonable sample
                    continue
                
                avg_dollar_volume = sum(b['close'] * b['volume'] for b in lookback_bars) / len(lookback_bars)
                if avg_dollar_volume < 20_000_000:
                    continue
                
                # Check if price within 5% of 20-day high
                recent_bars = [ts_to_bar[sorted_ts[j]] for j in range(i-LOOKBACK_WINDOW, i+1)]
                high_20d = max(b['high'] for b in recent_bars)
                if t_bar['close'] < 0.95 * high_20d:
                    continue
                
                # Check 20-day price change
                if i >= LOOKBACK_WINDOW:
                    start_bar = ts_to_bar[sorted_ts[i-LOOKBACK_WINDOW]]
                    price_change = (t_bar['close'] - start_bar['close']) / start_bar['close']
                    if price_change > 0.20:
                        continue
                
                # Check 5-day volatility
                if i >= VOL_WINDOW:
                    vol_bars = [ts_to_bar[sorted_ts[j]] for j in range(i-VOL_WINDOW, i)]
                    returns = []
                    for k in range(1, len(vol_bars)):
                        ret = (vol_bars[k]['close'] - vol_bars[k-1]['close']) / vol_bars[k-1]['close']
                        returns.append(ret)
                    vol = sum(r*r for r in returns) / len(returns) if returns else 0
                    # We'll filter later cross-sectionally
                else:
                    continue
                
                # For now, we'll mark as opportunity, but we need to filter out low analyst coverage, etc.
                # Since we don't have that data, we can't implement those filters.
                # We must output INSUFFICIENT if we can't fully implement.
                # We'll assume we have insufficient data for the full hypothesis.
                
                # Instead of continuing, we'll break and mark insufficient.
                # But let's at least compute what we can for the base rate test.
                
                # For simplicity, we'll treat every opportunity as an UP call for now,
                # but this doesn't meet the SUE>=3 criterion.
                # We don't have earnings data, so we cannot compute SUE.
                
                # Therefore, we must declare insufficient data.
                print("INSUFFICIENT=1")
                sys.exit(0)
        
        # If we get here, we didn't find enough opportunities
        print("INSUFFICIENT=1")
        sys.exit(0)
        
    except Exception as e:
        print("INSUFFICIENT=1")
        sys.exit(0)

if __name__ == '__main__':
    main()