# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 859
# cycle_index: 5
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all daily bars for symbols with enough history
    cur.execute("""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d'
        ORDER BY symbol_id, ts
    """)
    bars_rows = cur.fetchall()

    if not bars_rows:
        print("INSUFFICIENT=1")
        return 0

    # Build symbol -> list of (ts, open, high, low, close, volume)
    from collections import defaultdict
    bars_by_symbol = defaultdict(list)
    for row in bars_rows:
        bars_by_symbol[row['symbol_id']].append((
            row['ts'], row['open'], row['high'], row['low'], row['close'], row['volume']
        ))

    # Filter symbols with >= 252 daily bars
    valid_symbols = {sid for sid, bars in bars_by_symbol.items() if len(bars) >= 252}
    if not valid_symbols:
        print("INSUFFICIENT=1")
        return 0

    # Get insider trades: open-market purchases (code P) by officers
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts, price, shares, title, code
        FROM insider_trades
        WHERE code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%' 
               OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
    """)
    insider_rows = cur.fetchall()

    if not insider_rows:
        print("INSUFFICIENT=1")
        return 0

    # Map insider trades by symbol_id and trade date (from tx_ts)
    insider_by_symbol = defaultdict(list)
    for row in insider_rows:
        if row['symbol_id'] not in valid_symbols:
            continue
        trade_date = row['tx_ts'] // 86400  # unix epoch to days
        file_date = row['filed_ts'] // 86400
        delay_days = file_date - trade_date
        insider_by_symbol[row['symbol_id']].append({
            'trade_date': trade_date,
            'file_date': file_date,
            'delay_days': delay_days,
            'price': row['price'],
            'shares': row['shares'],
            'title': row['title']
        })

    # For each symbol, build daily bar lookup by date (unix day)
    bar_by_symbol_date = {}
    for sid, bars in bars_by_symbol.items():
        if sid not in valid_symbols:
            continue
        d = {}
        for ts, o, h, l, c, v in bars:
            day = ts // 86400
            d[day] = (o, h, l, c, v)
        bar_by_symbol_date[sid] = d

    # Find entry signals
    signals = []  # (symbol_id, decision_day, entry_price, forward_return_21d, delay_days, trade_price)
    for sid in valid_symbols:
        bars_dict = bar_by_symbol_date[sid]
        if not bars_dict:
            continue
        sorted_days = sorted(bars_dict.keys())
        if len(sorted_days) < 22:  # need prior day + 21 forward
            continue

        # Build forward return map: for each day, 21-day forward return
        fwd_ret = {}
        for i, day in enumerate(sorted_days):
            if i + 21 < len(sorted_days):
                entry_close = bars_dict[day][3]  # close
                exit_close = bars_dict[sorted_days[i + 21]][3]
                fwd_ret[day] = (exit_close - entry_close) / entry_close

        # Check insider trades for this symbol
        for trade in insider_by_symbol.get(sid, []):
            td = trade['trade_date']
            if td not in bars_dict:
                continue
            # Need prior day for gap calculation
            prior_idx = sorted_days.index(td) - 1 if td in sorted_days else -1
            if prior_idx < 0:
                continue
            prior_day = sorted_days[prior_idx]
            prior_close = bars_dict[prior_day][3]
            day_open, day_high, day_low, day_close, day_vol = bars_dict[td]

            # Gap down >= 2%: open <= prior_close * 0.98
            if day_open > prior_close * 0.98:
                continue
            # Close > open (reversal)
            if day_close <= day_open:
                continue
            # Disclosure delay <= 5 days
            if trade['delay_days'] > 5:
                continue
            # Trade price near day's low (within 2%): trade_price <= low * 1.02
            if trade['price'] > day_low * 1.02:
                continue
            # Forward return available
            if td not in fwd_ret:
                continue

            signals.append({
                'symbol_id': sid,
                'day': td,
                'fwd_ret': fwd_ret[td],
                'delay_days': trade['delay_days'],
                'trade_price': trade['price'],
                'day_low': day_low,
                'day_high': day_high,
                'day_close': day_close,
                'prior_close': prior_close,
                'gap_pct': (day_open - prior_close) / prior_close
            })

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # Sort by day
    signals.sort(key=lambda x: x['day'])

    # Hold out most recent 20% as sealed era
    n_total = len(signals)
    n_sealed = max(1, int(n_total * 0.2))
    unsealed = signals[:-n_sealed]
    sealed = signals[-n_sealed:]

    def compute_metrics(sig_list):
        if not sig_list:
            return 0, 0, 0, 0, 0
        issued = len(sig_list)
        hits = sum(1 for s in sig_list if s['fwd_ret'] > 0)
        precision = hits / issued if issued else 0
        base_rate = hits / issued if issued else 0  # base rate within issued subset
        distinct_days = len(set(s['day'] for s in sig_list))
        # Design effect: cluster by day, compute variance inflation
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use conservative rho=0.5, cluster by day
        day_counts = defaultdict(int)
        for s in sig_list:
            day_counts[s['day']] += 1
        avg_cluster = sum(day_counts.values()) / len(day_counts) if day_counts else 1
        design_effect = 1 + (avg_cluster - 1) * 0.5
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    u_issued, u_hits, u_prec, u_base, u_days, u_eff = compute_metrics(unsealed)
    s_issued, s_hits, s_prec, s_base, s_days, s_eff = compute_metrics(sealed)

    print(f"ISSUED={u_issued}")
    print(f"OPPORTUNITIES={u_issued}")  # Each signal is an opportunity considered and acted on
    print(f"PRECISION={u_prec:.6f}")
    print(f"BASE_RATE={u_base:.6f}")
    print(f"DISTINCT_DAYS={u_days}")
    print(f"EFFECTIVE_N={u_eff:.2f}")
    print(f"SEALED_PRECISION={s_prec:.6f}")

    return 0

if __name__ == '__main__':
    sys.exit(main())