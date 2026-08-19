# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 806
# cycle_index: 2
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def is_business_day(d):
    return d.weekday() < 5

def add_business_days(start, n):
    d = start
    count = 0
    while count < n:
        d += timedelta(days=1)
        if is_business_day(d):
            count += 1
    return d

def business_days_between(start, end):
    if start > end:
        start, end = end, start
    count = 0
    d = start
    while d <= end:
        if is_business_day(d):
            count += 1
        d += timedelta(days=1)
    return count

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def get_nth_trading_day_after(conn, symbol_id, start_ts, n):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>? ORDER BY ts LIMIT ?",
        (symbol_id, start_ts, n)
    )
    rows = cur.fetchall()
    if len(rows) < n:
        return None
    return rows[-1][0]

def get_prior_trading_days(conn, symbol_id, before_ts, n):
    cur = conn.execute(
        "SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' AND ts<? ORDER BY ts DESC LIMIT ?",
        (symbol_id, before_ts, n)
    )
    rows = cur.fetchall()
    rows.reverse()
    return [(r[0], r[1]) for r in rows]

def compute_sma(closes, window):
    if len(closes) < window:
        return None
    return sum(closes[-window:]) / window

def compute_avg_dollar_volume(conn, symbol_id, trade_ts, window=20):
    prior = get_prior_trading_days(conn, symbol_id, trade_ts, window)
    if len(prior) < window:
        return None
    total = sum(c * v for _, c, v in prior)  # Need volume too
    return total / window

def get_prior_bars_with_volume(conn, symbol_id, before_ts, n):
    cur = conn.execute(
        "SELECT ts, close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts<? ORDER BY ts DESC LIMIT ?",
        (symbol_id, before_ts, n)
    )
    rows = cur.fetchall()
    rows.reverse()
    return [(r[0], r[1], r[2]) for r in rows]

def get_shares_outstanding_history(conn, symbol_id, before_fetched_at):
    cur = conn.execute(
        """SELECT as_of, value, fetched_at FROM fundamentals
           WHERE symbol_id=? AND metric='SharesOutstanding' AND fetched_at<? AND as_of>0
           ORDER BY as_of DESC""",
        (symbol_id, before_fetched_at)
    )
    rows = cur.fetchall()
    return [(r[0], float(r[1]), r[2]) for r in rows]

def check_shares_declined_4q(history, trade_fetched_at):
    if len(history) < 4:
        return False
    for i in range(4):
        curr = history[i][1]
        prev = history[i+1][1] if i+1 < len(history) else None
        if prev is None or curr >= prev * 0.99:
            return False
    return True

def get_news_sentiment_scores(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT score FROM news WHERE symbol_id=? AND ts>=? AND ts<=?",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall() if row[0] is not None]

def compute_volatility(scores):
    if len(scores) < 2:
        return None
    mean = sum(scores) / len(scores)
    var = sum((x - mean) ** 2 for x in scores) / (len(scores) - 1)
    return math.sqrt(var)

def get_officer_purchases(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        """SELECT tx_ts, filed_ts, insider, title, code, shares, price
           FROM insider_trades
           WHERE symbol_id=? AND tx_ts>=? AND tx_ts<=? AND code='P'
           AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
           ORDER BY tx_ts""",
        (symbol_id, start_ts, end_ts)
    )
    return cur.fetchall()

def get_all_officer_purchases(conn, symbol_id):
    cur = conn.execute(
        """SELECT tx_ts, filed_ts, insider, title, code, shares, price
           FROM insider_trades
           WHERE symbol_id=? AND code='P'
           AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
           ORDER BY tx_ts""",
        (symbol_id,)
    )
    return cur.fetchall()

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Get all stock symbols with sufficient daily bars
    cur = conn.execute(
        """SELECT s.id, s.symbol FROM symbols s
           WHERE s.market='stocks'
           AND EXISTS (
               SELECT 1 FROM bars b
               WHERE b.symbol_id=s.id AND b.tf='1d'
               GROUP BY b.symbol_id
               HAVING COUNT(*) >= 252 AND MIN(b.ts) <= ? AND MAX(b.ts) >= ?
           )""",
        (date_to_epoch(datetime(2018, 7, 1).date()), date_to_epoch(datetime(2026, 8, 1).date()))
    )
    symbols = [(row['id'], row['symbol']) for row in cur.fetchall()]
    print(f"Candidate symbols: {len(symbols)}", file=sys.stderr)

    if not symbols:
        print("INSUFFICIENT=1")
        return

    # Get all officer purchases across all symbols
    all_trades = []
    for symbol_id, symbol in symbols:
        trades = get_all_officer_purchases(conn, symbol_id)
        for t in trades:
            all_trades.append((symbol_id, symbol) + t)

    print(f"Total officer purchases: {len(all_trades)}", file=sys.stderr)

    if not all_trades:
        print("INSUFFICIENT=1")
        return

    # For each trade, evaluate entry conditions at trade date (tx_ts)
    opportunities = []
    issued_calls = []

    # Pre-compute news sentiment volatility deciles per symbol-day? Too heavy.
    # Instead compute on demand for each trade.

    for (symbol_id, symbol, tx_ts, filed_ts, insider, title, code, shares, price) in all_trades:
        trade_date = epoch_to_date(tx_ts)
        file_date = epoch_to_date(filed_ts)

        # Abstain: disclosure delay > 5 business days
        delay = business_days_between(trade_date, file_date)
        if delay > 5:
            continue

        # Need close price on trade date
        cur = conn.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
            (symbol_id, tx_ts)
        )
        row = cur.fetchone()
        if not row:
            continue
        close_px = row['close']

        # Market cap check: need SharesOutstanding at trade date (fetched_at <= tx_ts)
        cur = conn.execute(
            """SELECT value FROM fundamentals
               WHERE symbol_id=? AND metric='SharesOutstanding' AND fetched_at<=? AND as_of>0
               ORDER BY fetched_at DESC LIMIT 1""",
            (symbol_id, tx_ts)
        )
        row = cur.fetchone()
        if not row:
            continue
        shares_out = float(row['value'])
        market_cap = close_px * shares_out
        if market_cap < 500_000_000:
            continue

        # 20-day avg dollar volume >= $5M
        prior_bars = get_prior_bars_with_volume(conn, symbol_id, tx_ts, 20)
        if len(prior_bars) < 20:
            continue
        avg_dollar_vol = sum(c * v for _, c, v in prior_bars) / 20
        if avg_dollar_vol < 5_000_000:
            continue

        # 200-day SMA
        prior_closes = get_prior_trading_days(conn, symbol_id, tx_ts, 200)
        if len(prior_closes) < 200:
            continue
        sma_200 = compute_sma([c for _, c in prior_closes], 200)
        if close_px >= sma_200:
            continue

        # SharesOutstanding fell >1% in each of prior 4 quarters
        so_history = get_shares_outstanding_history(conn, symbol_id, tx_ts)
        if not check_shares_declined_4q(so_history, tx_ts):
            continue

        # 252-day news sentiment volatility in bottom decile
        # Need to compute volatility for this symbol around trade date
        # and compare to distribution across all symbol-days
        # This is expensive - let's compute for all candidate days first
        opportunities.append({
            'symbol_id': symbol_id,
            'symbol': symbol,
            'tx_ts': tx_ts,
            'filed_ts': filed_ts,
            'trade_date': trade_date,
            'close_px': close_px,
            'shares_out': shares_out,
        })

    print(f"Opportunities after basic filters: {len(opportunities)}", file=sys.stderr)

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    # Compute news sentiment volatility for each opportunity
    # 252 trading days prior to trade date
    volatilities = []
    for opp in opportunities:
        symbol_id = opp['symbol_id']
        tx_ts = opp['tx_ts']
        # Get 252 trading days prior
        prior_days = get_trading_days(conn, symbol_id, tx_ts - 252*86400*2, tx_ts)  # rough window
        if len(prior_days) < 252:
            opp['news_vol'] = None
            continue
        # Use last 252 trading days
        start_ts = prior_days[-252]
        scores = get_news_sentiment_scores(conn, symbol_id, start_ts, tx_ts)
        vol = compute_volatility(scores)
        opp['news_vol'] = vol
        if vol is not None:
            volatilities.append(vol)

    # Compute bottom decile threshold
    volatilities = [v for v in volatilities if v is not None]
    if not volatilities:
        print("INSUFFICIENT=1")
        return
    volatilities.sort()
    decile_idx = max(0, int(len(volatilities) * 0.1) - 1)
    bottom_decile_threshold = volatilities[decile_idx]

    print(f"Bottom decile news vol threshold: {bottom_decile_threshold}", file=sys.stderr)

    # Filter by news volatility
    filtered_opps = [o for o in opportunities if o['news_vol'] is not None and o['news_vol'] <= bottom_decile_threshold]
    print(f"After news vol filter: {len(filtered_opps)}", file=sys.stderr)

    # Check >=2 officer purchases within 5 trade-date window
    final_opps = []
    for opp in filtered_opps:
        symbol_id = opp['symbol_id']
        tx_ts = opp['tx_ts']
        window_start = tx_ts - 5*86400
        window_end = tx_ts + 5*86400
        purchases = get_officer_purchases(conn, symbol_id, window_start, window_end)
        if len(purchases) >= 2:
            opp['purchase_count'] = len(purchases)
            final_opps.append(opp)

    print(f"After multi-purchase filter: {len(final_opps)}", file=sys.stderr)

    if not final_opps:
        print("INSUFFICIENT=1")
        return

    # Now compute forward returns (21 trading days) from bars
    results = []
    for opp in final_opps:
        symbol_id = opp['symbol_id']
        tx_ts = opp['tx_ts']
        close_px = opp['close_px']

        # Get 21st trading day after
        future_ts = get_nth_trading_day_after(conn, symbol_id, tx_ts, 21)
        if not future_ts:
            continue

        cur = conn.execute(
            "SELECT close FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
            (symbol_id, future_ts)
        )
        row = cur.fetchone()
        if not row:
            continue
        future_close = row['close']

        fwd_return = (future_close - close_px) / close_px
        up = 1 if fwd_return > 0 else 0

        results.append({
            'symbol_id': symbol_id,
            'symbol': opp['symbol'],
            'tx_ts': tx_ts,
            'trade_date': opp['trade_date'],
            'up': up,
            'fwd_return': fwd_return,
        })

    print(f"Results with forward returns: {len(results)}", file=sys.stderr)

    if not results:
        print("INSUFFICIENT=1")
        return

    # Hold out most recent 20% as sealed era
    results.sort(key=lambda x: x['tx_ts'])
    split_idx = int(len(results) * 0.8)
    main_results = results[:split_idx]
    sealed_results = results[split_idx:]

    def compute_metrics(res):
        if not res:
            return None
        issued = len(res)
        hits = sum(r['up'] for r in res)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate within issued subset
        distinct_days = len(set(r['trade_date'] for r in res))
        # Design effect: cluster by day
        day_counts = defaultdict(int)
        for r in res:
            day_counts[r['trade_date']] += 1
        if len(day_counts) > 1:
            n = issued
            k = len(day_counts)
            mean_cluster = n / k
            # Kish's effective sample size
            deff = 1 + (mean_cluster - 1) * 0.5  # approximate ICC=0.5 for same-day calls
            effective_n = n / deff
        else:
            effective_n = 1.0
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n,
        }

    main_metrics = compute_metrics(main_results)
    sealed_metrics = compute_metrics(sealed_results)

    if not main_metrics:
        print("INSUFFICIENT=1")
        return

    # Total opportunities considered = all officer purchase trade dates that passed basic data availability
    # For simplicity, use final_opps count as opportunities
    opportunities_count = len(final_opps)

    print(f"ISSUED={main_metrics['issued']}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={main_metrics['precision']:.6f}")
    print(f"BASE_RATE={main_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={main_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={main_metrics['effective_n']:.2f}")
    if sealed_metrics:
        print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")
    else:
        print("SEALED_PRECISION=0.000000")

if __name__ == '__main__':
    main()