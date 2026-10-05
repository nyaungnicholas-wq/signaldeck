# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 819
# cycle_index: 15
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def business_days_between(start_ts, end_ts):
    """Count business days between two unix timestamps (inclusive of start, exclusive of end)."""
    start = datetime.utcfromtimestamp(start_ts).date()
    end = datetime.utcfromtimestamp(end_ts).date()
    if start >= end:
        return 0
    days = 0
    cur = start
    while cur < end:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days

def next_n_business_days(start_ts, n):
    """Return unix timestamp of the nth business day after start_ts (start_ts is day 0)."""
    cur = datetime.utcfromtimestamp(start_ts).date()
    count = 0
    while count < n:
        cur += timedelta(days=1)
        if cur.weekday() < 5:
            count += 1
    return int(datetime.combine(cur, datetime.min.time()).timestamp())

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Check fundamentals quarterly history availability
    # Need at least 3 quarters of EPS, Revenues, SharesOutstanding for a reasonable sample
    cur.execute("""
        SELECT metric, COUNT(DISTINCT symbol_id) as symbols, COUNT(*) as rows,
               MIN(as_of) as min_as_of, MAX(as_of) as max_as_of
        FROM fundamentals
        WHERE metric IN ('EPS', 'Revenues', 'SharesOutstanding') AND as_of > 0
        GROUP BY metric
    """)
    fund_check = cur.fetchall()
    print("Fundamentals check:", [dict(r) for r in fund_check], file=sys.stderr)

    # Check how many symbols have >=3 quarters for all 3 metrics
    cur.execute("""
        SELECT symbol_id, COUNT(DISTINCT as_of) as quarters
        FROM fundamentals
        WHERE metric IN ('EPS', 'Revenues', 'SharesOutstanding') AND as_of > 0
        GROUP BY symbol_id
        HAVING quarters >= 3
    """)
    symbols_with_3q = cur.fetchall()
    print(f"Symbols with >=3 quarters of fundamental data: {len(symbols_with_3q)}", file=sys.stderr)

    if len(symbols_with_3q) < 50:
        print("INSUFFICIENT=1")
        return 0

    # 2. Get CEO/CFO open-market purchases (code='P')
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.insider, it.title, it.code,
               it.shares, it.price, it.value, it.tx_ts, it.filed_ts
        FROM insider_trades it
        WHERE it.code = 'P'
          AND (it.title LIKE '%CEO%' OR it.title LIKE '%CFO%' OR it.title LIKE '%Chief Executive%' OR it.title LIKE '%Chief Financial%')
          AND it.tx_ts > 0 AND it.filed_ts > 0
        ORDER BY it.tx_ts
    """)
    trades = cur.fetchall()
    print(f"CEO/CFO open-market purchases: {len(trades)}", file=sys.stderr)

    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # 3. Get symbols with daily bars
    cur.execute("SELECT id, symbol FROM symbols WHERE active = 1")
    symbol_map = {row['id']: row['symbol'] for row in cur.fetchall()}

    # 4. Pre-load fundamentals for symbols with 3+ quarters
    valid_symbol_ids = {r['symbol_id'] for r in symbols_with_3q}
    cur.execute("""
        SELECT symbol_id, metric, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric IN ('EPS', 'Revenues', 'SharesOutstanding')
          AND as_of > 0
          AND symbol_id IN ({})
        ORDER BY symbol_id, metric, as_of
    """.format(','.join('?'*len(valid_symbol_ids))), list(valid_symbol_ids))
    fund_rows = cur.fetchall()

    # Organize fundamentals by symbol_id -> metric -> list of (as_of, value, fetched_at)
    fundamentals = {}
    for row in fund_rows:
        sid = row['symbol_id']
        metric = row['metric']
        fundamentals.setdefault(sid, {}).setdefault(metric, []).append(
            (row['as_of'], row['value'], row['fetched_at'])
        )

    # 5. Pre-load sentiment_features for news sentiment filtering
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE ver = (SELECT MAX(ver) FROM sentiment_features)
    """)
    sentiment_rows = cur.fetchall()
    sentiment_by_symbol_day = {}
    for row in sentiment_rows:
        sentiment_by_symbol_day.setdefault(row['symbol_id'], {})[row['day']] = row['mean_score']

    # 6. Process each trade
    opportunities = []
    issued_calls = []

    for trade in trades:
        sid = trade['symbol_id']
        tx_ts = trade['tx_ts']
        filed_ts = trade['filed_ts']

        # Disclosure delay in business days
        disc_delay = business_days_between(tx_ts, filed_ts)
        if disc_delay > 2:
            opportunities.append((sid, tx_ts, 'disclosure_delay'))
            continue

        # 20-day avg dollar volume before trade date
        cur.execute("""
            SELECT AVG(close * volume) as avg_dollar_vol
            FROM bars
            WHERE symbol_id = ? AND tf = '1d' AND ts < ?
            ORDER BY ts DESC
            LIMIT 20
        """, (sid, tx_ts))
        row = cur.fetchone()
        if not row or not row['avg_dollar_vol'] or row['avg_dollar_vol'] < 1_000_000:
            opportunities.append((sid, tx_ts, 'low_liquidity'))
            continue

        # News sentiment on trade date (from sentiment_features)
        trade_date = datetime.utcfromtimestamp(tx_ts).strftime('%Y-%m-%d')
        sent_score = sentiment_by_symbol_day.get(sid, {}).get(trade_date)
        if sent_score is not None:
            # We'll compute quartiles/deciles later, for now just collect
            pass

        # Officer prior trades in 252 sessions (approx 1 year)
        cur.execute("""
            SELECT COUNT(*) as prior_count
            FROM insider_trades
            WHERE symbol_id = ? AND insider = ? AND code = 'P'
              AND tx_ts < ? AND tx_ts >= ?
        """, (sid, trade['insider'], tx_ts, tx_ts - 252*86400))
        prior_row = cur.fetchone()
        if not prior_row or prior_row['prior_count'] == 0:
            opportunities.append((sid, tx_ts, 'no_track_record'))
            continue

        # Fundamentals as of trade date (fetched_at <= tx_ts)
        if sid not in fundamentals:
            opportunities.append((sid, tx_ts, 'no_fundamentals'))
            continue

        # Get latest quarter for each metric where fetched_at <= tx_ts
        latest_quarters = {}
        for metric in ('EPS', 'Revenues', 'SharesOutstanding'):
            series = fundamentals[sid].get(metric, [])
            # Filter by fetched_at <= tx_ts and take latest as_of
            available = [(as_of, val) for as_of, val, fetched in series if fetched <= tx_ts]
            if len(available) < 3:
                opportunities.append((sid, tx_ts, f'insufficient_{metric}_history'))
                break
            available.sort(key=lambda x: x[0], reverse=True)
            latest_quarters[metric] = available[:3]  # Most recent 3 quarters
        else:
            # All 3 metrics have 3+ quarters
            # Compute growth rates for each metric (q0/q1 - 1, q1/q2 - 1)
            growth = {}
            for metric, quarters in latest_quarters.items():
                q0_val, q1_val, q2_val = quarters[0][1], quarters[1][1], quarters[2][1]
                if q1_val == 0 or q2_val == 0:
                    opportunities.append((sid, tx_ts, f'zero_{metric}'))
                    break
                g0 = q0_val / q1_val - 1
                g1 = q1_val / q2_val - 1
                growth[metric] = (g0, g1)
            else:
                # Check SharesOutstanding growth in (0%, 2%)
                so_g0 = growth['SharesOutstanding'][0]
                if not (0 < so_g0 < 0.02):
                    opportunities.append((sid, tx_ts, 'so_growth_out_of_range'))
                # Check Revenue acceleration: g0 > g1
                rev_g0, rev_g1 = growth['Revenues']
                if not (rev_g0 > rev_g1):
                    opportunities.append((sid, tx_ts, 'rev_not_accelerating'))
                # Check EPS acceleration: g0 > g1
                eps_g0, eps_g1 = growth['EPS']
                if not (eps_g0 > eps_g1):
                    opportunities.append((sid, tx_ts, 'eps_not_accelerating'))
                else:
                    # All entry conditions passed - this is an issued call candidate
                    # Compute 21-trading-day forward return
                    label_ts = next_n_business_days(tx_ts, 21)
                    cur.execute("""
                        SELECT close FROM bars
                        WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts < ?
                        ORDER BY ts LIMIT 1
                    """, (sid, label_ts - 86400, label_ts + 86400))
                    label_row = cur.fetchone()
                    cur.execute("""
                        SELECT close FROM bars
                        WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                        ORDER BY ts DESC LIMIT 1
                    """, (sid, tx_ts))
                    entry_row = cur.fetchone()
                    if label_row and entry_row and entry_row['close'] > 0:
                        fwd_ret = label_row['close'] / entry_row['close'] - 1
                        hit = 1 if fwd_ret > 0 else 0
                        issued_calls.append({
                            'symbol_id': sid,
                            'tx_ts': tx_ts,
                            'fwd_ret': fwd_ret,
                            'hit': hit,
                            'sent_score': sent_score
                        })
                    else:
                        opportunities.append((sid, tx_ts, 'no_label'))
                    continue
            # If we broke out of the for-else, continue to next trade
            continue
        # If we broke out of the metric loop, continue
        continue

    print(f"Opportunities considered: {len(opportunities) + len(issued_calls)}", file=sys.stderr)
    print(f"Issued calls: {len(issued_calls)}", file=sys.stderr)

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 7. Apply sentiment filters (top quartile euphoria, bottom decile panic)
    sent_scores = [c['sent_score'] for c in issued_calls if c['sent_score'] is not None]
    if sent_scores:
        sent_scores.sort()
        q75 = sent_scores[int(len(sent_scores) * 0.75)]
        d10 = sent_scores[int(len(sent_scores) * 0.10)]
        filtered_calls = []
        for c in issued_calls:
            if c['sent_score'] is not None and (c['sent_score'] > q75 or c['sent_score'] < d10):
                continue
            filtered_calls.append(c)
        issued_calls = filtered_calls

    print(f"After sentiment filter: {len(issued_calls)}", file=sys.stderr)

    if not issued_calls:
        print("INSUFFICIENT=1")
        return 0

    # 8. Hold out most recent 20% as sealed era
    issued_calls.sort(key=lambda x: x['tx_ts'])
    split_idx = int(len(issued_calls) * 0.8)
    train_calls = issued_calls[:split_idx]
    sealed_calls = issued_calls[split_idx:]

    def compute_metrics(calls):
        if not calls:
            return 0, 0, 0, 0, 0
        issued = len(calls)
        hits = sum(c['hit'] for c in calls)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # base rate of positive class in issued subset
        distinct_days = len(set(datetime.utcfromtimestamp(c['tx_ts']).date() for c in calls))
        # Design effect approximation: 1 + (avg_cluster_size - 1) * intraclass_corr
        # Simple approximation: group by day, compute variance inflation
        day_counts = {}
        for c in calls:
            day = datetime.utcfromtimestamp(c['tx_ts']).date()
            day_counts[day] = day_counts.get(day, 0) + 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        # Conservative intraclass correlation of 0.1 for same-day calls
        deff = 1 + (avg_cluster - 1) * 0.1
        effective_n = issued / deff if deff > 1 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_days, train_eff = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_days, sealed_eff = compute_metrics(sealed_calls)

    # 9. Output required lines
    print(f"ISSUED={train_issued + sealed_issued}")
    print(f"OPPORTUNITIES={len(opportunities) + len(issued_calls)}")
    print(f"PRECISION={(train_hits + sealed_hits) / (train_issued + sealed_issued) if (train_issued + sealed_issued) else 0:.6f}")
    print(f"BASE_RATE={(train_hits + sealed_hits) / (train_issued + sealed_issued) if (train_issued + sealed_issued) else 0:.6f}")
    print(f"DISTINCT_DAYS={train_days + sealed_days}")
    print(f"EFFECTIVE_N={train_eff + sealed_eff:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())