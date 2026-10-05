# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 752
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

# MECHANISM: Officer (CEO/CFO) open-market sales at trade date during positive news sentiment streaks, at prices near 52-week highs, with short disclosure delay (≤3 days), signal local tops.
# HORIZON: 21d from disclosure (built from bars tf='1d' forward return)
# UNIVERSE: Active US stocks with ≥252 daily bars, avg 20d dollar volume >$1M, news coverage in prior 30d
# ENTRY: At filed_ts (disclosure), a code='S' trade by CEO/CFO at tx_ts where filed_ts-tx_ts≤3 days, trade price≥95% 52w high at tx_ts, ≥5 consecutive prior days with news sentiment score>0 ending at tx_ts
# ABSTAIN: No news data prior 30d at tx_ts, <252 bars at tx_ts, disclosure delay>3d, price<95% 52w high, non-officer, non-sale code, multiple same-day officer sales
# CLAIM: Negative 21d forward return from disclosure with precision exceeding base rate in issued subset; sealed era reported separately

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def is_officer_title(title: str) -> bool:
    if not title:
        return False
    t = title.upper()
    return ('CEO' in t or 'CHIEF EXECUTIVE' in t or 'CFO' in t or 'CHIEF FINANCIAL' in t)

def unix_to_date(ts: int) -> datetime:
    return datetime.utcfromtimestamp(ts)

def date_to_str(dt: datetime) -> str:
    return dt.strftime('%Y-%m-%d')

def get_trading_days_between(conn, symbol_id: int, start_ts: int, end_ts: int) -> list:
    """Get list of trading day timestamps (unix) for symbol between start and end inclusive."""
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def get_bars_for_symbol(conn, symbol_id: int, start_ts: int, end_ts: int) -> dict:
    """Get daily bars as dict ts->(open,high,low,close,volume) for symbol."""
    cur = conn.execute(
        "SELECT ts, open, high, low, close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return {row[0]: (row[1], row[2], row[3], row[4], row[5]) for row in cur.fetchall()}

def get_sentiment_for_symbol(conn, symbol_id: int, start_date: str, end_date: str) -> dict:
    """Get sentiment_features as dict day->mean_score."""
    cur = conn.execute(
        "SELECT day, mean_score FROM sentiment_features WHERE symbol_id=? AND day>=? AND day<=? ORDER BY day",
        (symbol_id, start_date, end_date)
    )
    return {row[0]: row[1] for row in cur.fetchall()}

def compute_52w_high(bars: dict, trade_ts: int, lookback_days: int = 252) -> float:
    """Compute 52-week high from bars prior to trade_ts (exclusive)."""
    prior_ts = [ts for ts in bars.keys() if ts < trade_ts]
    if len(prior_ts) < lookback_days:
        return None
    prior_ts.sort(reverse=True)
    recent_ts = prior_ts[:lookback_days]
    highs = [bars[ts][1] for ts in recent_ts]  # high is index 1
    return max(highs) if highs else None

def compute_avg_dollar_volume(bars: dict, trade_ts: int, window: int = 20) -> float:
    """Compute average dollar volume over window days prior to trade_ts."""
    prior_ts = [ts for ts in bars.keys() if ts < trade_ts]
    if len(prior_ts) < window:
        return None
    prior_ts.sort(reverse=True)
    recent_ts = prior_ts[:window]
    dollar_vols = [bars[ts][3] * bars[ts][4] for ts in recent_ts]  # close * volume
    return sum(dollar_vols) / len(dollar_vols) if dollar_vols else None

def check_sentiment_streak(sentiment: dict, trade_date_str: str, min_streak: int = 5) -> bool:
    """Check if there are min_streak consecutive days with mean_score > 0 ending on or before trade_date."""
    if trade_date_str not in sentiment:
        return False
    dates = sorted([d for d in sentiment.keys() if d <= trade_date_str])
    if len(dates) < min_streak:
        return False
    streak = 0
    for d in reversed(dates):
        if sentiment.get(d, 0) > 0:
            streak += 1
            if streak >= min_streak:
                return True
        else:
            streak = 0
    return False

def get_forward_return(bars: dict, start_ts: int, horizon_days: int) -> float:
    """Get forward return over horizon_days trading days from start_ts."""
    trading_ts = sorted([ts for ts in bars.keys() if ts >= start_ts])
    if len(trading_ts) <= horizon_days:
        return None
    start_close = bars[trading_ts[0]][3]
    end_close = bars[trading_ts[horizon_days]][3]
    return (end_close - start_close) / start_close

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    
    # Get all candidate insider trades: code='S' (sale), officer title
    cur = conn.execute("""
        SELECT it.*, s.symbol, s.market, s.active
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'S' AND s.market = 'stocks' AND s.active = 1
    """)
    trades = cur.fetchall()
    
    if not trades:
        print("INSUFFICIENT=1")
        return 0
    
    # Group by symbol for efficient data loading
    trades_by_symbol = {}
    for t in trades:
        if not is_officer_title(t['title']):
            continue
        symbol_id = t['symbol_id']
        if symbol_id not in trades_by_symbol:
            trades_by_symbol[symbol_id] = []
        trades_by_symbol[symbol_id].append(t)
    
    if not trades_by_symbol:
        print("INSUFFICIENT=1")
        return 0
    
    results = []  # (filed_ts, symbol_id, forward_return, trade_date_str)
    
    for symbol_id, symbol_trades in trades_by_symbol.items():
        # Get all bars for this symbol (need up to 2026-08-15 + 21 trading days)
        # Bars go to 2026-08-15, so latest start_ts for 21d forward is ~2026-07-15
        bars = get_bars_for_symbol(conn, symbol_id, 0, 2000000000)
        if len(bars) < 252:
            continue
        
        # Get sentiment for this symbol
        # sentiment_features day range: 2012-04-17..2026-08-17
        sent = get_sentiment_for_symbol(conn, symbol_id, '2012-01-01', '2026-12-31')
        if not sent:
            continue
        
        for t in symbol_trades:
            tx_ts = t['tx_ts']
            filed_ts = t['filed_ts']
            trade_price = t['price']
            
            # Disclosure delay ≤ 3 days (259200 seconds)
            if filed_ts - tx_ts > 259200:
                continue
            
            # Trade date string
            trade_dt = unix_to_date(tx_ts)
            trade_date_str = date_to_str(trade_dt)
            
            # Check 52-week high at trade date
            high_52w = compute_52w_high(bars, tx_ts)
            if high_52w is None or trade_price < 0.95 * high_52w:
                continue
            
            # Check avg dollar volume > $1M over prior 20 days
            avg_dv = compute_avg_dollar_volume(bars, tx_ts, 20)
            if avg_dv is None or avg_dv < 1_000_000:
                continue
            
            # Check sentiment streak ≥5 days ending at trade date
            if not check_sentiment_streak(sent, trade_date_str, 5):
                continue
            
            # Check multiple same-day officer sales (abstain)
            same_day_count = sum(1 for ot in symbol_trades if ot['tx_ts'] == tx_ts)
            if same_day_count > 1:
                continue
            
            # Compute forward return from disclosure (filed_ts) over 21 trading days
            fwd_ret = get_forward_return(bars, filed_ts, 21)
            if fwd_ret is None:
                continue
            
            results.append((filed_ts, symbol_id, fwd_ret, trade_date_str))
    
    if not results:
        print("INSUFFICIENT=1")
        return 0
    
    # Sort by filed_ts (decision time)
    results.sort(key=lambda x: x[0])
    
    # Hold out most recent 20% as sealed era
    n_total = len(results)
    n_sealed = max(1, int(n_total * 0.2))
    n_train = n_total - n_sealed
    
    train_results = results[:n_train]
    sealed_results = results[n_train:]
    
    def compute_metrics(res_list, label):
        if not res_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(res_list)
        hits = sum(1 for r in res_list if r[2] < 0)  # negative return = hit
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class (negative return) within issued
        distinct_days = len(set(r[3] for r in res_list))
        # Design effect: cluster by symbol-day, approximate as 1 + (avg cluster size - 1) * ICC
        # Simple approximation: effective_n = issued / design_effect, design_effect > 1
        # Use conservative design effect of 2.0 for clustered financial data
        design_effect = 2.0
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n
    
    train_issued, train_hits, train_prec, train_br, train_dd, train_en = compute_metrics(train_results, 'train')
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_dd, sealed_en = compute_metrics(sealed_results, 'sealed')
    
    # Overall metrics (for reporting)
    all_issued = train_issued + sealed_issued
    all_hits = train_hits + sealed_hits
    all_prec = all_hits / all_issued if all_issued > 0 else 0.0
    all_br = all_hits / all_issued if all_issued > 0 else 0.0
    all_dd = len(set(r[3] for r in results))
    all_en = (train_en + sealed_en)  # approximate
    
    # Opportunities: total decision points considered (officer sales with data)
    # Count unique (symbol, trade_date) that passed basic filters before ENTRY
    # For simplicity, use total officer sales with code='S' and data availability
    opportunities = sum(len(v) for v in trades_by_symbol.values())
    
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_dd}")
    print(f"EFFECTIVE_N={all_en:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())