#!/usr/bin/env python3
"""
MECHANISM: Large open-market insider purchases filed promptly (within 2 business days) signal urgent private conviction because insiders accelerate disclosure when information is time-sensitive and positive.
HORIZON: 21 trading days
UNIVERSE: All active common stocks with daily bars, insider trade history, and at least 1 year of trading history, excluding the most recent 20% sealed era.
ENTRY: On the filing date (filed_ts) of an open-market purchase (code P) where (a) disclosure delay <= 2 business days, (b) trade value >= $50,000, (c) trade value >= 5x the insider's median purchase value over the prior 2 years for that symbol, (d) no other open-market purchase by any insider of the same symbol in the prior 20 trading days, (e) the stock's 21-day return prior to filing date is negative, (f) insider title indicates officer role (not director-only), (g) average daily dollar volume over prior 63 days >= $2M.
ABSTAIN: No call if any ENTRY condition fails, or if the filing date falls in the sealed era.
CLAIM: Precision >= 60% for positive forward return (up=1) over the 21-day horizon, with a base rate of ~50-52% in the opportunities.
"""

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def is_officer_title(title: str) -> bool:
    if not title:
        return False
    t = title.lower()
    officer_keywords = [
        'chief', 'ceo', 'cfo', 'coo', 'cto', 'cmo', 'cio', 'cso', 'cpo', 'cro',
        'president', 'vice president', 'vp ', 'v.p.', 'executive', 'officer',
        'treasurer', 'secretary', 'controller', 'principal', 'managing director',
        'general counsel', 'head of', 'division', 'senior vice', 'svp', 'evp'
    ]
    director_only = ['director', 'chairman', 'chair', 'board', 'independent', 'lead director']
    # Must have officer keyword and not be only director keywords
    has_officer = any(kw in t for kw in officer_keywords)
    only_director = all(kw not in t for kw in officer_keywords) and any(kw in t for kw in director_only)
    return has_officer and not only_director

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    """Return sorted list of trading day timestamps (unix epoch at midnight UTC) for symbol in range."""
    cur = conn.execute(
        "SELECT DISTINCT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row

    # 1. Get all open-market purchases (code='P')
    trades = conn.execute("""
        SELECT symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P'
        ORDER BY filed_ts
    """).fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # 2. Get trading days for each symbol to compute business day delays and returns
    # We'll need trading days for each symbol around each trade's tx_ts and filed_ts
    # Precompute trading day indices for each symbol
    symbol_trading_days = {}
    symbol_day_to_idx = {}
    for trade in trades:
        sid = trade['symbol_id']
        if sid not in symbol_trading_days:
            # Get all trading days for this symbol
            days = conn.execute(
                "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts",
                (sid,)
            ).fetchall()
            symbol_trading_days[sid] = [d[0] for d in days]
            symbol_day_to_idx[sid] = {d: i for i, d in enumerate(symbol_trading_days[sid])}

    # 3. For each trade, compute disclosure delay in trading days
    qualified = []
    for trade in trades:
        sid = trade['symbol_id']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']
        value = trade['value']
        insider = trade['insider']
        title = trade['title']

        # Find trading day index for tx_ts and filed_ts (use date part)
        tx_date = ts_to_date(tx_ts)
        filed_date = ts_to_date(filed_ts)
        tx_day_ts = date_to_ts(tx_date)
        filed_day_ts = date_to_ts(filed_date)

        day_to_idx = symbol_day_to_idx.get(sid, {})
        tx_idx = day_to_idx.get(tx_day_ts)
        filed_idx = day_to_idx.get(filed_day_ts)

        if tx_idx is None or filed_idx is None:
            continue  # no trading day match

        delay_tdays = filed_idx - tx_idx
        if delay_tdays < 0 or delay_tdays > 2:
            continue  # not within 2 business days

        # Value filter
        if value < 50000:
            continue

        # Title filter
        if not is_officer_title(title):
            continue

        qualified.append({
            'symbol_id': sid,
            'insider': insider,
            'title': title,
            'value': value,
            'tx_ts': tx_ts,
            'filed_ts': filed_ts,
            'filed_date': filed_date,
            'filed_day_ts': filed_day_ts,
            'filed_idx': filed_idx,
        })

    if not qualified:
        print("INSUFFICIENT=1")
        return 0

    # 4. For each qualified trade, check insider's median purchase value over prior 2 years for same symbol
    # Build insider-symbol purchase history
    insider_symbol_purchases = defaultdict(list)
    for trade in trades:
        if trade['code'] == 'P':
            key = (trade['insider'], trade['symbol_id'])
            insider_symbol_purchases[key].append((trade['tx_ts'], trade['value']))

    # Sort each history by tx_ts
    for key in insider_symbol_purchases:
        insider_symbol_purchases[key].sort(key=lambda x: x[0])

    # 5. Check no other purchase in same symbol in prior 20 trading days
    # Build all purchase filing day indices per symbol
    symbol_purchase_filed_indices = defaultdict(list)
    for trade in trades:
        if trade['code'] == 'P':
            sid = trade['symbol_id']
            filed_date = ts_to_date(trade['filed_ts'])
            filed_day_ts = date_to_ts(filed_date)
            day_to_idx = symbol_day_to_idx.get(sid, {})
            idx = day_to_idx.get(filed_day_ts)
            if idx is not None:
                symbol_purchase_filed_indices[sid].append(idx)
    for sid in symbol_purchase_filed_indices:
        symbol_purchase_filed_indices[sid].sort()

    # 6. Check 21-day return prior to filing date negative, and liquidity
    # We'll compute these per trade
    final_signals = []
    for q in qualified:
        sid = q['symbol_id']
        insider = q['insider']
        filed_idx = q['filed_idx']
        filed_day_ts = q['filed_day_ts']
        value = q['value']

        # Insider median purchase value over prior 2 years (730 calendar days ~ 520 trading days)
        # Use tx_ts for history lookback
        tx_ts = q['tx_ts']
        cutoff_ts = tx_ts - 730 * 86400
        history = insider_symbol_purchases.get((insider, sid), [])
        prior_values = [v for t, v in history if cutoff_ts <= t < tx_ts]
        if not prior_values:
            continue
        prior_values.sort()
        median_val = prior_values[len(prior_values) // 2]
        if value < 5 * median_val:
            continue

        # No other purchase in same symbol in prior 20 trading days (by filing date)
        purchase_indices = symbol_purchase_filed_indices.get(sid, [])
        # Count purchases with index in (filed_idx - 20, filed_idx)
        # Since list is sorted, we can binary search but linear is fine for small lists
        recent_count = sum(1 for idx in purchase_indices if filed_idx - 20 < idx < filed_idx)
        if recent_count > 0:
            continue

        # 21-day return prior to filing date negative
        # Need close at filed_idx - 21 and close at filed_idx
        trading_days = symbol_trading_days[sid]
        if filed_idx < 21:
            continue
        close_now = conn.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (sid, trading_days[filed_idx])
        ).fetchone()
        close_past = conn.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (sid, trading_days[filed_idx - 21])
        ).fetchone()
        if not close_now or not close_past or close_past['close'] <= 0:
            continue
        ret_21 = (close_now['close'] - close_past['close']) / close_past['close']
        if ret_21 >= 0:
            continue

        # Liquidity: avg daily dollar volume over prior 63 days >= $2M
        if filed_idx < 63:
            continue
        vol_sum = 0
        for i in range(filed_idx - 63, filed_idx):
            day_ts = trading_days[i]
            row = conn.execute(
                "SELECT close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?", (sid, day_ts)
            ).fetchone()
            if row and row['close'] and row['volume']:
                vol_sum += row['close'] * row['volume']
        avg_dollar_vol = vol_sum / 63
        if avg_dollar_vol < 2_0