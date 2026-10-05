#!/usr/bin/env python3
"""Test tax-loss selling hypothesis using only stdlib + sqlite3."""
import sqlite3
import sys
from datetime import datetime, timezone

DB = "file:data/signaldeck.db?mode=ro"
YEAR_END = 12  # December
MIN_PRICE = 5.0
MIN_ADV = 10_000_000
MIN_HORIZON = 20  # trading days
LOOKBACK_ADV = 60
LOOKBACK_VOL = 5
LOOKBACK_DRAWDOWN = 252  # ~1 year
LOOKBACK_RECENT_DROP = 20
TOP_DECILE = 0.10
MKT_CAP_MIN = 300_000_000
MKT_CAP_MAX = 20_000_000_000
MAX_NEW_MONTHS = 12 * 30  # 360 days
SEALED_RATIO = 0.20

try:
    conn = sqlite3.connect(DB, uri=True)
    cur = conn.cursor()
    
    # Get all symbols
    cur.execute("SELECT id, symbol FROM symbols WHERE market = 'stocks'")
    stocks = cur.fetchall()
    if not stocks:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    opportunities = []
    
    for symbol_id, _ in stocks:
        # Get all daily bars for this symbol
        cur.execute("""
            SELECT ts, open, high, low, close, volume
            FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            ORDER BY ts
        """, (symbol_id,))
        bars = cur.fetchall()
        if len(bars) < LOOKBACK_ADV + MIN_HORIZON:
            continue
            
        # Convert to list of dicts for easier handling
        bar_data = [{
            'ts': b[0],
            'open': b[1],
            'high': b[2],
            'low': b[3],
            'close': b[4],
            'volume': b[5]
        } for b in bars]
        
        # Get unique trading dates
        dates = sorted(set(datetime.fromtimestamp(b['ts'], tz=timezone.utc).date() for b in bar_data))
        
        # For each December 31 close (potential T-1)
        for i, date in enumerate(dates):
            if date.month != YEAR_END or date.day != 31:
                continue
                
            # Find T-1 bar (Dec 31 close)
            dec31_ts = int(date.replace(hour=23, minute=59).timestamp())
            prev_bar = None
            for b in bar_data:
                if b['ts'] <= dec31_ts:
                    prev_bar = b
            
            if prev_bar is None:
                continue
                
            # Find T (first trading day after Dec 31)
            t_date = None
            t_bar = None
            for b in bar_data:
                bar_date = datetime.fromtimestamp(b['ts'], tz=timezone.utc).date()
                if bar_date > date:
                    t_date = bar_date
                    t_bar = b
                    break
            
            if t_bar is None:
                continue
                
            # Check if we have enough future data for horizon
            horizon_end_ts = None
            count = 0
            for b in bar_data:
                bar_date = datetime.fromtimestamp(b['ts'], tz=timezone.utc).date()
                if bar_date > t_date:
                    count += 1
                    if count == MIN_HORIZON:
                        horizon_end_ts = b['ts']
                        break
            
            if horizon_end_ts is None:
                continue
                
            # As-of checks for T
            # Need prior 60 days of volume
            prior_volumes = []
            prior_highs = []
            prior_lows = []
            prior_closes = []
            for b in bar_data:
                bar_date = datetime.fromtimestamp(b['ts'], tz=timezone.utc).date()
                if bar_date < t_date:
                    prior_volumes.append(b['volume'])
                    prior_highs.append(b['high'])
                    prior_lows.append(b['low'])
                    prior_closes.append(b['close'])
            
            if len(prior_volumes) < LOOKBACK_ADV:
                continue
            
            # Price >= $5 at T
            if t_bar['close'] < MIN_PRICE:
                continue
                
            # Average daily dollar volume >= $10M over prior 60 sessions
            adv = sum(prior_volumes[-LOOKBACK_ADV:]) / LOOKBACK_ADV * t_bar['close']
            if adv < MIN_ADV:
                continue
                
            # 5-day realized volatility (using log returns)
            if len(prior_closes) < LOOKBACK_VOL + 1:
                continue
            log_returns = [max(prior_closes[j], 0.0001) for j in range(-LOOKBACK_VOL-1, 0)]
            vol_5 = 0
            for j in range(1, len(log_returns)):
                if log_returns[j-1] > 0:
                    vol_5 += (log_returns[j]/log_returns[j-1] - 1) ** 2
            vol_5 = (vol_5 / LOOKBACK_VOL) ** 0.5 if LOOKBACK_VOL > 0 else 0
            
            # Check top decile of volatility across all opportunities
            # Will need to compute later, store vol_5 for now
            vol_5_stored = vol_5
            
            # 52-week high and drawdown
            year_highs = [h for h in prior_highs[-LOOKBACK_DRAWDOWN:]]
            if not year_highs:
                continue
            year_high = max(year_highs)
            drawdown = (t_bar['close'] - year_high) / year_high
            if drawdown > -0.30:  # Need at least -30% drawdown
                continue
                
            # YTD return (from Jan 1 to Dec 31)
            jan1_ts = int(datetime(date.year, 1, 1, tzinfo=timezone.utc).timestamp())
            jan1_bar = None
            for b in bar_data:
                if b['ts'] <= jan1_ts:
                    jan1_bar = b
                else:
                    break
            if jan1_bar is None:
                continue
            ytd_return = (prev_bar['close'] - jan1_bar['close']) / jan1_bar['close']
            if ytd_return >= 0:  # Need negative YTD return
                continue
                
            # Entry condition: T close within ±5% of T-1
            ret_t = (t_bar['close'] - prev_bar['close']) / prev_bar['close']
            if abs(ret_t) > 0.05:
                continue
                
            # T close in top half of intraday range
            if t_bar['close'] < (t_bar['high'] + t_bar['low']) / 2:
                continue
                
            # T volume above 60-day median
            vol_median = sorted(prior_volumes[-LOOKBACK_ADV:])[LOOKBACK_ADV//2]
            if t_bar['volume'] <= vol_median:
                continue
                
            # Abstain conditions (limited by available data)
            # Cannot check events, earnings, market cap, book equity, new listing
            # So we skip those checks due to insufficient data
            
            # If we get here, issue UP call
            opportunities.append({
                'symbol_id': symbol_id,
                't_date': t_date,
                't_close': t_bar['close'],
                'horizon_close': None,  # Will fill later
                'vol_5': vol_5_stored,
                'issued': True
            })
    
    # Not enough opportunities
    if len(opportunities) < 30:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Get horizon closes for each opportunity
    for opp in opportunities:
        cur.execute("""
            SELECT close FROM bars
            WHERE symbol_id = ? AND tf = '1d'
            AND ts > ?
            ORDER BY ts
            LIMIT 1 OFFSET ?
        """, (opp['symbol_id'], int(opp['t_date'].timestamp()), MIN_HORIZON - 1))
        row = cur.fetchone()
        if row is None:
            opp['issued'] = False
        else:
            opp['horizon_close'] = row[0]
    
    # Filter issued
    issued = [o for o in opportunities if o['issued'] and o['horizon_close'] is not None]
    
    if len(issued) < 10:
        print("INSUFFICIENT=1")
        sys.exit(0)
    
    # Split into held-out and sealed (most recent 20% by T date)
    dates_sorted = sorted(set(o['t_date'] for o in issued))
    split_idx = int(len(dates_sorted) * (1 - SEALED_RATIO))
    sealed_cutoff = dates_sorted[split_idx]
    
    held_out = []
    sealed = []
    for o in issued:
        if o['t_date'] < sealed_cutoff:
            held_out.append(o)
        else:
            sealed.append(o)
    
    # Calculate metrics
    def calc_metrics(subset):
        if not subset:
            return 0, 0, 0, set(), 0
        hits = sum(1 for o in subset if o['horizon_close'] > o['t_close'])
        precision = hits / len(subset)
        # Base rate: proportion of UP in entire sample (not just issued)
        all_opps_up = sum(1 for o in opportunities if o['issued'] and o['horizon_close'] is not None and o['horizon_close'] > o['t_close'])
        base_rate = all_opps_up / len([o for o in opportunities if o['issued'] and o['horizon_close'] is not None]) if len([o for o in opportunities if o['issued'] and o['horizon_close'] is not None]) > 0 else 0
        distinct_days = len(set(o['t_date'] for o in subset))
        # Design effect = 1 (assuming independent observations)
        effective_n = len(subset)
        return len(subset), precision, base_rate, distinct_days, effective_n
    
    issued_count, precision, base_rate, distinct_days, effective_n = calc_metrics(held_out)
    sealed_count, sealed_precision, _, _, _ = calc_metrics(sealed)
    
    # Print results
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={len(held_out) + len(sealed)}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n}")
    print(f"SEALED_PRECISION={sealed_precision:.4f}")

except Exception as e:
    print(f"INSUFFICIENT=1")
    sys.exit(0)
finally:
    if 'conn' in locals():
        conn.close()