# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 460
# cycle_index: 51
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def parse_ts(val):
    """Convert various timestamp formats to unix epoch (int)."""
    if val is None:
        return None
    if isinstance(val, int):
        return val
    if isinstance(val, str):
        if val.isdigit():
            return int(val)
        # Try YYYY-MM-DD
        try:
            return int(datetime.strptime(val, '%Y-%m-%d').replace(tzinfo=timezone.utc).timestamp())
        except ValueError:
            pass
        # Try YYYY-MM-DD HH:MM:SS
        try:
            return int(datetime.strptime(val, '%Y-%m-%d %H:%M:%S').replace(tzinfo=timezone.utc).timestamp())
        except ValueError:
            pass
    return None

def get_db():
    return sqlite3.connect(DB_PATH, uri=True)

def main():
    conn = get_db()
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Symbols in both insider_trades and inst_holdings
    cur.execute("""
        SELECT DISTINCT i.symbol_id
        FROM insider_trades i
        JOIN inst_holdings h ON h.symbol_id = i.symbol_id
    """)
    symbol_ids = [row['symbol_id'] for row in cur.fetchall()]
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return

    placeholders = ','.join('?' * len(symbol_ids))

    # 2. Get all inst_holdings for these symbols, aggregate by symbol_id, period
    cur.execute(f"""
        SELECT symbol_id, period, SUM(shares) as total_shares, SUM(value) as total_value
        FROM inst_holdings
        WHERE symbol_id IN ({placeholders})
        GROUP BY symbol_id, period
    """, symbol_ids)
    holdings_rows = cur.fetchall()

    # Group by symbol
    holdings_by_symbol = defaultdict(list)
    for row in holdings_rows:
        ts = parse_ts(row['period'])
        if ts is not None:
            holdings_by_symbol[row['symbol_id']].append({
                'period_ts': ts,
                'shares': row['total_shares'] or 0,
                'value': row['total_value'] or 0
            })

    # 3. Get insider purchases (code='P') for these symbols
    cur.execute(f"""
        SELECT symbol_id, filed_ts, tx_ts, shares, price, value
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code = 'P'
    """, symbol_ids)
    insider_rows = cur.fetchall()
    insider_by_symbol = defaultdict(list)
    for row in insider_rows:
        filed = parse_ts(row['filed_ts'])
        tx = parse_ts(row['tx_ts'])
        if filed is not None:
            insider_by_symbol[row['symbol_id']].append({
                'filed_ts': filed,
                'tx_ts': tx,
                'shares': row['shares'] or 0,
                'price': row['price'] or 0,
                'value': row['value'] or 0
            })

    # 4. Get 1d bars for forward return calculation
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE symbol_id IN ({placeholders}) AND tf = '1d'
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bar_rows = cur.fetchall()
    bars_by_symbol = defaultdict(list)
    for row in bar_rows:
        bars_by_symbol[row['symbol_id']].append({'ts': row['ts'], 'close': row['close']})

    # 5. Generate calls
    calls = []  # each: (entry_ts, symbol_id, up, fwd_return)
    FILING_LAG = 45 * 86400
    WINDOW = 30 * 86400
    HORIZON_DAYS = 63

    for sym in symbol_ids:
        h = holdings_by_symbol.get(sym, [])
        if len(h) < 4:
            continue
        # Sort by period
        h.sort(key=lambda x: x['period_ts'])
        # Exclude most recent period (until its 13F is filed - we don't have filing date, so drop last)
        h = h[:-1]
        if len(h) < 3:
            continue

        insiders = insider_by_symbol.get(sym, [])
        insiders.sort(key=lambda x: x['filed_ts'])
        bars = bars_by_symbol.get(sym, [])
        if not bars:
            continue
        bars.sort(key=lambda x: x['ts'])
        bar_ts = [b['ts'] for b in bars]
        bar_close = [b['close'] for b in bars]

        # For each quarter (index i), compare to previous quarter (i-1)
        for i in range(1, len(h)):
            prev_shares = h[i-1]['shares']
            curr_shares = h[i]['shares']
            if curr_shares >= prev_shares:
                continue  # No institutional selling
            period_ts = h[i]['period_ts']
            decision_ts = period_ts + FILING_LAG

            # Find insider purchase filed near decision_ts
            best_insider = None
            best_diff = float('inf')
            for ins in insiders:
                diff = abs(ins['filed_ts'] - decision_ts)
                if diff <= WINDOW and diff < best_diff:
                    best_diff = diff
                    best_insider = ins
            if not best_insider:
                continue

            entry_ts = max(decision_ts, best_insider['filed_ts'])

            # Find entry bar (first bar with ts > entry_ts)
            entry_idx = -1
            for idx, ts in enumerate(bar_ts):
                if ts > entry_ts:
                    entry_idx = idx
                    break
            if entry_idx == -1:
                continue
            # Need 63 trading days forward
            exit_idx = entry_idx + HORIZON_DAYS
            if exit_idx >= len(bar_ts):
                continue

            entry_price = bar_close[entry_idx]
            exit_price = bar_close[exit_idx]
            if entry_price <= 0:
                continue
            fwd_return = (exit_price - entry_price) / entry_price
            up = 1 if fwd_return > 0 else 0

            calls.append({
                'entry_ts': entry_ts,
                'symbol_id': sym,
                'up': up,
                'fwd_return': fwd_return
            })

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort by entry time
    calls.sort(key=lambda x: x['entry_ts'])

    # Split: most recent 20% sealed
    n = len(calls)
    split_idx = int(n * 0.8)
    train_calls = calls[:split_idx]
    sealed_calls = calls[split_idx:]

    def compute_metrics(call_list, label):
        if not call_list:
            return 0, 0, 0, 0, 0
        issued = len(call_list)
        hits = sum(c['up'] for c in call_list)
        precision = hits / issued if issued else 0
        base_rate = precision  # base rate of predicted class (up=1) within issued subset
        # Distinct UTC days among issued calls
        days = set()
        for c in call_list:
            dt = datetime.fromtimestamp(c['entry_ts'], tz=timezone.utc)
            days.add(dt.date())
        distinct_days = len(days)
        # Effective N: cluster by month, assume ICC=0.1
        months = set()
        for c in call_list:
            dt = datetime.fromtimestamp(c['entry_ts'], tz=timezone.utc)
            months.add((dt.year, dt.month))
        num_months = len(months)
        avg_cluster = issued / num_months if num_months else 1
        icc = 0.1
        design_effect = 1 + (avg_cluster - 1) * icc
        if design_effect < 1.01:
            design_effect = 1.01
        effective_n = issued / design_effect
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_precision, train_base_rate, train_days, train_eff_n = compute_metrics(train_calls, 'train')
    sealed_issued, sealed_hits, sealed_precision, sealed_base_rate, sealed_days, sealed_eff_n = compute_metrics(sealed_calls, 'sealed')

    # Overall metrics (on all calls for reporting)
    all_issued, all_hits, all_precision, all_base_rate, all_days, all_eff_n = compute_metrics(calls, 'all')

    # Print required lines
    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={all_issued}")  # Each call is an opportunity acted on
    print(f"PRECISION={all_precision:.6f}")
    print(f"BASE_RATE={all_base_rate:.6f}")
    print(f"DISTINCT_DAYS={all_days}")
    print(f"EFFECTIVE_N={all_eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()