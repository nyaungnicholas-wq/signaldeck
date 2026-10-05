# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 800
# cycle_index: 70
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def get_bars_for_symbols(conn, symbol_ids, start_ts, end_ts):
    """Fetch daily bars for given symbols in timestamp range."""
    if not symbol_ids:
        return {}
    placeholders = ','.join('?' * len(symbol_ids))
    q = f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders}) AND ts >= ? AND ts <= ?
        ORDER BY symbol_id, ts
    """
    params = list(symbol_ids) + [start_ts, end_ts]
    cur = conn.execute(q, params)
    bars_by_symbol = {}
    for symbol_id, ts, close in cur:
        bars_by_symbol.setdefault(symbol_id, []).append((ts, close))
    return bars_by_symbol

def find_bar_at_or_after(bars, target_ts):
    """Find first bar with ts >= target_ts. bars is sorted list of (ts, close)."""
    for ts, close in bars:
        if ts >= target_ts:
            return ts, close
    return None, None

def find_bar_at_or_before(bars, target_ts):
    """Find last bar with ts <= target_ts."""
    result = None
    for ts, close in bars:
        if ts <= target_ts:
            result = (ts, close)
        else:
            break
    return result

def compute_forward_return(bars, entry_ts, horizon_days):
    """Compute forward return over horizon_days from entry_ts."""
    entry_idx = None
    for i, (ts, close) in enumerate(bars):
        if ts >= entry_ts:
            entry_idx = i
            break
    if entry_idx is None or entry_idx + horizon_days >= len(bars):
        return None
    entry_close = bars[entry_idx][1]
    exit_close = bars[entry_idx + horizon_days][1]
    return (exit_close - entry_close) / entry_close

def main():
    conn = connect_ro()
    conn.row_factory = sqlite3.Row

    # 1. Load qualifying insider trades: code='P', CEO/CFO in title
    insider_q = """
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
          AND tx_ts > 0 AND filed_ts > 0 AND filed_ts >= tx_ts
    """
    trades = conn.execute(insider_q).fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # 2. Get unique symbols and date range for bars
    symbol_ids = list(set(t['symbol_id'] for t in trades))
    min_tx = min(t['tx_ts'] for t in trades)
    max_filed = max(t['filed_ts'] for t in trades)
    # Need bars from min_tx to max_filed + 21 trading days (~30 calendar days)
    # 21 trading days ~ 30 calendar days, add buffer
    end_ts = max_filed + 30 * 86400

    bars_by_symbol = get_bars_for_symbols(conn, symbol_ids, min_tx - 86400, end_ts)
    if not bars_by_symbol:
        print("INSUFFICIENT=1")
        return

    # 3. Compute historical purchase value percentiles per insider
    insider_values = {}
    for t in trades:
        insider_key = (t['symbol_id'], t['insider'])
        insider_values.setdefault(insider_key, []).append(t['value'])

    insider_p75 = {}
    for key, values in insider_values.items():
        if len(values) >= 4:  # need enough history for quartile
            sorted_vals = sorted(values)
            idx = int(len(sorted_vals) * 0.75)
            insider_p75[key] = sorted_vals[idx]

    # 4. Evaluate each trade
    HORIZON_DAYS = 21
    MIN_RETURN_TRADE_TO_FILED = 0.05  # 5%
    results = []  # (symbol_id, decision_date, decision_ts, forward_return, is_issued)

    for t in trades:
        symbol_id = t['symbol_id']
        bars = bars_by_symbol.get(symbol_id)
        if not bars:
            continue

        tx_ts = t['tx_ts']
        filed_ts = t['filed_ts']

        # Price at trade date (bar at or after tx_ts)
        _, px_tx = find_bar_at_or_after(bars, tx_ts)
        # Price at filing date (bar at or after filed_ts)
        _, px_filed = find_bar_at_or_after(bars, filed_ts)

        if px_tx is None or px_filed is None or px_tx <= 0:
            continue

        ret_trade_to_filed = (px_filed - px_tx) / px_tx
        if ret_trade_to_filed < MIN_RETURN_TRADE_TO_FILED:
            continue

        # Check if purchase value is in top quartile for this insider
        insider_key = (symbol_id, t['insider'])
        p75 = insider_p75.get(insider_key)
        if p75 is None or t['value'] < p75:
            continue

        # Forward return from filing date
        fwd_ret = compute_forward_return(bars, filed_ts, HORIZON_DAYS)
        if fwd_ret is None:
            continue

        # Decision date (UTC day of filed_ts)
        decision_dt = datetime.utcfromtimestamp(filed_ts).date()
        decision_date_str = decision_dt.isoformat()

        # This is an opportunity (insider purchase disclosure)
        # It becomes an issued call if all conditions met (which they are here)
        results.append({
            'symbol_id': symbol_id,
            'decision_date': decision_date_str,
            'decision_ts': filed_ts,
            'forward_return': fwd_ret,
            'hit': 1 if fwd_ret > 0 else 0
        })

    if not results:
        print("INSUFFICIENT=1")
        return

    # 5. Deduplicate by (symbol_id, decision_date) - one observation per symbol-day
    # If multiple qualifying trades same symbol-day, keep the one with highest forward_return (or first)
    dedup = {}
    for r in results:
        key = (r['symbol_id'], r['decision_date'])
        if key not in dedup or r['forward_return'] > dedup[key]['forward_return']:
            dedup[key] = r

    observations = list(dedup.values())
    observations.sort(key=lambda x: x['decision_ts'])

    # 6. Split into training (80%) and sealed (20%) by time
    n = len(observations)
    split_idx = int(n * 0.8)
    if split_idx < 10 or n - split_idx < 5:
        print("INSUFFICIENT=1")
        return

    train = observations[:split_idx]
    sealed = observations[split_idx:]

    # 7. Compute metrics on training set
    # OPPORTUNITIES: all symbol-days with at least one insider purchase disclosure (CEO/CFO)
    # We need to count all decision points, not just issued ones.
    # For this, we need all CEO/CFO purchase disclosures per symbol-day.
    opp_q = """
        SELECT symbol_id, date(filed_ts, 'unixepoch') as decision_date, filed_ts
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
          AND filed_ts > 0
        GROUP BY symbol_id, decision_date
    """
    all_opportunities = conn.execute(opp_q).fetchall()

    # Map opportunity to forward return
    opp_returns = {}
    for opp in all_opportunities:
        symbol_id = opp['symbol_id']
        decision_ts = opp['filed_ts']
        bars = bars_by_symbol.get(symbol_id)
        if not bars:
            continue
        fwd_ret = compute_forward_return(bars, decision_ts, HORIZON_DAYS)
        if fwd_ret is not None:
            opp_returns[(symbol_id, opp['decision_date'])] = fwd_ret

    # Training opportunities (those with decision_ts <= train max ts)
    train_max_ts = train[-1]['decision_ts'] if train else 0
    train_opps = [(k, v) for k, v in opp_returns.items() if k[1] <= datetime.utcfromtimestamp(train_max_ts).date().isoformat()]
    # Actually, better to split opportunities by time too
    opp_list = [(k[0], k[1], v) for k, v in opp_returns.items()]
    opp_list.sort(key=lambda x: datetime.fromisoformat(x[1]).timestamp())
    opp_split = int(len(opp_list) * 0.8)
    train_opps = opp_list[:opp_split]
    sealed_opps = opp_list[opp_split:]

    # Base rate in training opportunities
    train_opp_hits = sum(1 for _, _, ret in train_opps if ret > 0)
    train_opp_total = len(train_opps)
    base_rate = train_opp_hits / train_opp_total if train_opp_total > 0 else 0

    # Issued calls in training
    train_issued = [o for o in train if o['decision_ts'] <= train_max_ts]
    issued_count = len(train_issued)
    hits = sum(o['hit'] for o in train_issued)
    precision = hits / issued_count if issued_count > 0 else 0

    # Distinct days among issued
    distinct_days = len(set(o['decision_date'] for o in train_issued))

    # Design effect and effective N
    if distinct_days > 0:
        design_effect = issued_count / distinct_days
        effective_n = issued_count / design_effect
    else:
        design_effect = 1.0
        effective_n = 0.0

    # Sealed precision
    sealed_issued = [o for o in sealed if o['decision_ts'] > train_max_ts]
    sealed_issued_count = len(sealed_issued)
    sealed_hits = sum(o['hit'] for o in sealed_issued)
    sealed_precision = sealed_hits / sealed_issued_count if sealed_issued_count > 0 else 0

    # 8. Output
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={train_opp_total}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()