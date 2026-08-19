# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 808
# cycle_index: 4
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_trading_days(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return [row[0] for row in cur.fetchall()]

def get_bar(conn, symbol_id, ts):
    cur = conn.execute(
        "SELECT open, high, low, close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?",
        (symbol_id, ts)
    )
    return cur.fetchone()

def get_bars_range(conn, symbol_id, start_ts, end_ts):
    cur = conn.execute(
        "SELECT ts, close, volume FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, start_ts, end_ts)
    )
    return cur.fetchall()

def ts_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_ts(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def main():
    conn = connect()
    conn.row_factory = sqlite3.Row

    # Get all officer open-market purchases
    cur = conn.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code, it.shares, it.price, it.value, it.tx_ts, it.filed_ts
        FROM insider_trades it
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%CHIEF%' OR it.title LIKE '%PRESIDENT%')
          AND it.value >= 200000
        ORDER BY it.filed_ts
    """)
    trades = cur.fetchall()

    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Get symbols with sufficient history
    cur = conn.execute("""
        SELECT s.id, s.symbol, s.delisted_at
        FROM symbols s
        WHERE s.market = 'stocks' AND s.active = 1
    """)
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}

    # Precompute trading days per symbol for efficiency
    symbol_trading_days = {}
    for sym_id in symbols:
        cur = conn.execute(
            "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts", (sym_id,)
        )
        symbol_trading_days[sym_id] = [row[0] for row in cur.fetchall()]

    opportunities = []
    issued = []

    for trade in trades:
        sym_id = trade['symbol_id']
        if sym_id not in symbols:
            continue
        if symbols[sym_id]['delisted_at'] and symbols[sym_id]['delisted_at'] < trade['filed_ts']:
            continue

        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']
        decision_date = ts_to_date(filed_ts)
        decision_ts = date_to_ts(decision_date)

        # Need 252 trading days before tx_ts for lookback
        trading_days = symbol_trading_days.get(sym_id, [])
        if not trading_days:
            continue

        # Find index of tx_ts in trading days (or nearest prior)
        tx_idx = -1
        for i, td in enumerate(trading_days):
            if td <= tx_ts:
                tx_idx = i
            else:
                break
        if tx_idx < 252:
            continue  # insufficient history

        # Get 252-day window prior to tx_ts (inclusive)
        window_days = trading_days[tx_idx - 251:tx_idx + 1]
        if len(window_days) < 252:
            continue

        # Fetch bars for window
        bars = {}
        for td in window_days:
            bar = get_bar(conn, sym_id, td)
            if bar:
                bars[td] = {'close': bar[3], 'volume': bar[4]}

        if len(bars) < 252:
            continue

        # Condition 1: tx_ts close is 252-day low
        tx_bar = bars.get(tx_ts)
        if not tx_bar:
            continue
        tx_close = tx_bar['close']
        min_close = min(b['close'] for b in bars.values())
        if tx_close > min_close * 1.001:  # allow tiny floating point
            continue

        # Condition 2: tx_ts volume <= 10th percentile of prior 252-day volume
        volumes = [b['volume'] for b in bars.values()]
        volumes_sorted = sorted(volumes)
        p10_idx = max(0, int(len(volumes_sorted) * 0.1) - 1)
        p10_volume = volumes_sorted[p10_idx]
        if tx_bar['volume'] > p10_volume:
            continue

        # Condition 3: 21-day return <= -20%
        if tx_idx < 21:
            continue
        td_21_ago = trading_days[tx_idx - 21]
        bar_21 = bars.get(td_21_ago)
        if not bar_21:
            continue
        ret_21 = (tx_close / bar_21['close']) - 1
        if ret_21 > -0.20:
            continue

        # All conditions met - this is an opportunity
        # Check if we can get 63-day forward return from decision_ts
        # Need 63 trading days after decision_ts
        decision_idx = -1
        for i, td in enumerate(trading_days):
            if td >= decision_ts:
                decision_idx = i
                break
        if decision_idx == -1 or decision_idx + 63 >= len(trading_days):
            continue

        forward_ts = trading_days[decision_idx + 63]
        forward_bar = get_bar(conn, sym_id, forward_ts)
        if not forward_bar:
            continue
        forward_close = forward_bar[3]

        # Get decision day close
        decision_bar = get_bar(conn, sym_id, decision_ts)
        if not decision_bar:
            continue
        decision_close = decision_bar[3]

        fwd_return = (forward_close / decision_close) - 1
        hit = 1 if fwd_return > 0 else 0

        opportunities.append({
            'symbol_id': sym_id,
            'decision_ts': decision_ts,
            'decision_date': decision_date,
            'hit': hit,
            'fwd_return': fwd_return
        })

        issued.append({
            'symbol_id': sym_id,
            'decision_ts': decision_ts,
            'decision_date': decision_date,
            'hit': hit,
            'fwd_return': fwd_return
        })

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    # Deduplicate by (symbol_id, decision_date) - one observation per symbol per day
    seen = set()
    unique_issued = []
    for opp in issued:
        key = (opp['symbol_id'], opp['decision_date'])
        if key not in seen:
            seen.add(key)
            unique_issued.append(opp)

    issued = unique_issued

    # Sort by decision date
    issued.sort(key=lambda x: x['decision_ts'])

    # 80/20 split
    n = len(issued)
    split_idx = int(n * 0.8)
    train = issued[:split_idx]
    sealed = issued[split_idx:]

    # Metrics
    issued_count = len(issued)
    opportunities_count = len(opportunities)  # before dedup
    hits = sum(1 for x in issued if x['hit'])
    precision = hits / issued_count if issued_count else 0
    base_rate = precision  # base rate within issued subset is same as precision for binary outcome

    distinct_days = len(set(x['decision_date'] for x in issued))

    # Effective N: conservative estimate using distinct days and autocorrelation
    # Compute day-to-day autocorrelation of hits
    daily_hits = {}
    for x in issued:
        d = x['decision_date']
        if d not in daily_hits:
            daily_hits[d] = []
        daily_hits[d].append(x['hit'])

    daily_mean = {d: sum(v)/len(v) for d, v in daily_hits.items()}
    dates_sorted = sorted(daily_mean.keys())
    if len(dates_sorted) > 1:
        vals = [daily_mean[d] for d in dates_sorted]
        mean_val = sum(vals) / len(vals)
        var = sum((v - mean_val)**2 for v in vals) / len(vals)
        if var > 0:
            cov = sum((vals[i] - mean_val) * (vals[i-1] - mean_val) for i in range(1, len(vals))) / (len(vals) - 1)
            rho = cov / var
            rho = max(0, min(rho, 0.9))  # clamp
        else:
            rho = 0
    else:
        rho = 0

    # Design effect for daily means: 1 + 2*sum(rho^k) approx 1 + 2*rho/(1-rho) for AR(1)
    if rho > 0:
        deff = 1 + 2 * rho / (1 - rho)
    else:
        deff = 1
    # Also account for multiple calls per day
    avg_cluster = issued_count / distinct_days if distinct_days else 1
    deff *= (1 + (avg_cluster - 1) * 0.5)  # assume ICC=0.5 within day
    effective_n = issued_count / deff
    effective_n = max(1, min(effective_n, issued_count - 1e-9))

    # Sealed precision
    sealed_hits = sum(1 for x in sealed if x['hit'])
    sealed_precision = sealed_hits / len(sealed) if sealed else 0

    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_count}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())