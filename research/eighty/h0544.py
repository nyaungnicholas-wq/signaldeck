# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 543
# cycle_index: 1
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect_ro():
    return sqlite3.connect(DB_PATH, uri=True)

def epoch_to_date(epoch):
    return datetime.fromtimestamp(epoch, tz=timezone.utc).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time(), tzinfo=timezone.utc).timestamp())

def main():
    con = connect_ro()
    con.row_factory = sqlite3.Row
    cur = con.cursor()

    # Check available horizons in prediction_outcomes
    cur.execute("SELECT DISTINCT horizon FROM prediction_outcomes")
    horizons = [row['horizon'] for row in cur.fetchall()]
    print(f"Available horizons: {horizons}", file=sys.stderr)
    
    # Use 21d if available, else first available
    target_horizon = '21d' if '21d' in horizons else horizons[0] if horizons else None
    if not target_horizon:
        print("INSUFFICIENT=1")
        return
    print(f"Using horizon: {target_horizon}", file=sys.stderr)

    # Get all insider open-market purchases (code='P') for active stocks
    cur.execute("""
        SELECT it.symbol_id, it.filed_ts, it.insider, it.title, it.value, s.symbol
        FROM insider_trades it
        JOIN symbols s ON it.symbol_id = s.id
        WHERE it.code = 'P' AND s.market = 'stocks' AND s.active = 1
        ORDER BY it.filed_ts
    """)
    insider_trades = cur.fetchall()
    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    # Filter to period where 1d bars exist: 2018-07-26 onwards
    bars_start_epoch = 1532563200  # 2018-07-26 00:00:00 UTC
    insider_trades = [t for t in insider_trades if t['filed_ts'] >= bars_start_epoch]
    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    # Filter for CEO/CFO/President titles
    exec_titles = {'CEO', 'CFO', 'PRESIDENT', 'CHIEF EXECUTIVE', 'CHIEF FINANCIAL', 'CHIEF OPERATING', 'COO'}
    insider_trades = [t for t in insider_trades if t['title'] and any(et in t['title'].upper() for et in exec_titles)]
    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    # Filter for significant purchases (value > $50k)
    insider_trades = [t for t in insider_trades if t['value'] and t['value'] > 50000]
    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    print(f"Insider trades after filters: {len(insider_trades)}", file=sys.stderr)

    # Get all 1d bars for symbols involved
    symbol_ids = list(set(t['symbol_id'] for t in insider_trades))
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_rows = cur.fetchall()

    # Organize bars by symbol_id
    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    # Get fundamentals: Revenues for symbols involved
    cur.execute(f"""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'Revenues' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, as_of
    """, symbol_ids)
    fund_rows = cur.fetchall()

    # Organize revenues by symbol_id
    rev_by_symbol = defaultdict(list)
    for row in fund_rows:
        rev_by_symbol[row['symbol_id']].append({
            'value': row['value'],
            'as_of': row['as_of'],
            'fetched_at': row['fetched_at']
        })

    # Get prediction outcomes for target horizon
    cur.execute(f"""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = ?
    """, (target_horizon,))
    outcomes = {(row['symbol_id'], row['ts']): (row['up'], row['fwd_return']) for row in cur.fetchall()}

    # Process each insider trade
    opportunities = []
    for trade in insider_trades:
        sym_id = trade['symbol_id']
        filed_ts = trade['filed_ts']
        filed_date = epoch_to_date(filed_ts)

        # Get bars up to filed_ts
        bars = bars_by_symbol.get(sym_id, [])
        bars_up_to = [(ts, close) for ts, close in bars if ts <= filed_ts]
        if len(bars_up_to) < 252:
            continue

        # Compute 252-day return (approx 1 year)
        close_now = bars_up_to[-1][1]
        close_252d_ago = bars_up_to[-252][1]
        ret_252d = (close_now - close_252d_ago) / close_252d_ago

        # Require negative 252-day return (stock down over past year)
        if ret_252d >= 0:
            continue

        # Get latest revenue as of filed_ts (fetched_at <= filed_ts)
        revs = rev_by_symbol.get(sym_id, [])
        revs_known = [r for r in revs if r['fetched_at'] <= filed_ts]
        if len(revs_known) < 2:
            continue

        # Find most recent quarter and year-ago quarter
        revs_known.sort(key=lambda x: x['as_of'])
        latest = revs_known[-1]
        # Find quarter from ~1 year ago (as_of is period end)
        target_as_of = latest['as_of'] - 365 * 86400  # approximate
        year_ago = min(revs_known, key=lambda x: abs(x['as_of'] - target_as_of))
        
        if year_ago['value'] <= 0:
            continue
        rev_yoy = (latest['value'] - year_ago['value']) / year_ago['value']

        # Require YoY revenue growth > 5%
        if rev_yoy <= 0.05:
            continue

        # Find matching prediction outcome at filed_ts (or nearest prior)
        outcome_key = (sym_id, filed_ts)
        if outcome_key not in outcomes:
            # Find nearest outcome within 1 day
            matched = False
            for offset in range(0, 86400, 3600):  # search within 1 day
                for ts_adj in [filed_ts - offset, filed_ts + offset]:
                    if (sym_id, ts_adj) in outcomes:
                        outcome_key = (sym_id, ts_adj)
                        matched = True
                        break
                if matched:
                    break
            if not matched:
                continue

        up, fwd_return = outcomes[outcome_key]
        opportunities.append({
            'symbol_id': sym_id,
            'filed_ts': filed_ts,
            'filed_date': filed_date,
            'up': up,
            'fwd_return': fwd_return,
            'ret_252d': ret_252d,
            'rev_yoy': rev_yoy,
            'title': trade['title'],
            'value': trade['value']
        })

    if not opportunities:
        print("INSUFFICIENT=1")
        return

    print(f"Opportunities after all filters: {len(opportunities)}", file=sys.stderr)

    # Sort by filed_ts
    opportunities.sort(key=lambda x: x['filed_ts'])

    # Split: most recent 20% sealed
    split_idx = int(len(opportunities) * 0.8)
    train_ops = opportunities[:split_idx]
    sealed_ops = opportunities[split_idx:]

    def compute_metrics(ops, label):
        if not ops:
            return None
        issued = len(ops)
        hits = sum(1 for o in ops if o['up'] == 1)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate within issued subset
        distinct_days = len(set(o['filed_date'] for o in ops))
        
        # Design effect: cluster by week
        week_counts = defaultdict(int)
        for o in ops:
            week_key = o['filed_date'].isocalendar()[:2]  # (year, week)
            week_counts[week_key] += 1
        if week_counts:
            avg_per_week = sum(week_counts.values()) / len(week_counts)
            # Design effect = 1 + (avg_cluster_size - 1) * ICC, approximate ICC=0.1
            deff = 1 + (avg_per_week - 1) * 0.1
            effective_n = issued / deff
        else:
            effective_n = issued
        
        print(f"{label}: ISSUED={issued} HITS={hits} PRECISION={precision:.4f} BASE_RATE={base_rate:.4f} DISTINCT_DAYS={distinct_days} EFFECTIVE_N={effective_n:.1f}", file=sys.stderr)
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_metrics = compute_metrics(train_ops, "TRAIN")
    sealed_metrics = compute_metrics(sealed_ops, "SEALED")

    if not train_metrics or not sealed_metrics:
        print("INSUFFICIENT=1")
        return

    # Output required lines
    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={len(opportunities)}")
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.6f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")

    # Invariants check
    if train_metrics['distinct_days'] > train_metrics['issued']:
        print("ERROR: DISTINCT_DAYS > ISSUED", file=sys.stderr)
        sys.exit(1)
    if train_metrics['effective_n'] >= train_metrics['issued']:
        print("ERROR: EFFECTIVE_N >= ISSUED", file=sys.stderr)
        sys.exit(1)

if __name__ == '__main__':
    main()