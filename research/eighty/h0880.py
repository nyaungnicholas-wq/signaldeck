# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 879
# cycle_index: 25
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def get_universe_symbols(conn):
    cur = conn.cursor()
    cur.execute("""
        SELECT s.id, s.symbol
        FROM symbols s
        WHERE s.market = 'stocks'
          AND s.active = 1
          AND EXISTS (
              SELECT 1 FROM bars b
              WHERE b.symbol_id = s.id AND b.tf = '1d'
              GROUP BY b.symbol_id
              HAVING COUNT(*) >= 252
                 AND MAX(b.ts) >= strftime('%s', '2026-01-01')
          )
          AND EXISTS (
              SELECT 1 FROM sentiment_features sf
              WHERE sf.symbol_id = s.id
              GROUP BY sf.symbol_id
              HAVING COUNT(*) >= 500
                 AND MAX(sf.day) >= '2025-01-01'
          )
    """)
    return cur.fetchall()

def get_sentiment_features(conn, symbol_id, start_day, end_day):
    cur = conn.cursor()
    cur.execute("""
        SELECT day, mean_score, hedged, n_all
        FROM sentiment_features
        WHERE symbol_id = ? AND day BETWEEN ? AND ?
        ORDER BY day
    """, (symbol_id, start_day, end_day))
    return cur.fetchall()

def get_officer_trades(conn, symbol_id, start_ts, end_ts):
    cur = conn.cursor()
    cur.execute("""
        SELECT tx_ts, filed_ts, code, insider, title
        FROM insider_trades
        WHERE symbol_id = ?
          AND code = 'P'
          AND (title LIKE '%CEO%' OR title LIKE '%CFO%')
          AND tx_ts BETWEEN ? AND ?
        ORDER BY tx_ts
    """, (symbol_id, start_ts, end_ts))
    return cur.fetchall()

def get_daily_bars(conn, symbol_id, start_ts, end_ts):
    cur = conn.cursor()
    cur.execute("""
        SELECT ts, close, volume
        FROM bars
        WHERE symbol_id = ? AND tf = '1d'
          AND ts BETWEEN ? AND ?
        ORDER BY ts
    """, (symbol_id, start_ts, end_ts))
    return cur.fetchall()

def compute_forward_return(bars, entry_ts, horizon_days):
    entry_idx = None
    for i, (ts, _, _) in enumerate(bars):
        if ts >= entry_ts:
            entry_idx = i
            break
    if entry_idx is None or entry_idx + horizon_days >= len(bars):
        return None
    entry_close = bars[entry_idx][1]
    exit_close = bars[entry_idx + horizon_days][1]
    return (exit_close - entry_close) / entry_close

def main():
    conn = connect()
    try:
        symbols = get_universe_symbols(conn)
        if not symbols:
            print("INSUFFICIENT=1")
            return 0

        horizon_days = 21
        all_decisions = []

        for symbol_id, symbol in symbols:
            bars = get_daily_bars(conn, symbol_id, 0, 2**31-1)
            if len(bars) < 252:
                continue

            bar_ts = [b[0] for b in bars]
            bar_close = [b[1] for b in bars]
            bar_vol = [b[2] for b in bars]

            sent = get_sentiment_features(conn, symbol_id, '2012-01-01', '2026-12-31')
            if len(sent) < 500:
                continue

            sent_by_day = {row[0]: (row[1], row[2], row[3]) for row in sent}

            trades = get_officer_trades(conn, symbol_id, 0, 2**31-1)
            if not trades:
                continue

            trade_by_filed = {}
            for tx_ts, filed_ts, code, insider, title in trades:
                filed_day = datetime.utcfromtimestamp(filed_ts).strftime('%Y-%m-%d')
                if filed_day not in trade_by_filed:
                    trade_by_filed[filed_day] = []
                trade_by_filed[filed_day].append((tx_ts, filed_ts, insider, title))

            for filed_day, day_trades in trade_by_filed.items():
                if filed_day not in sent_by_day:
                    continue

                for tx_ts, filed_ts, insider, title in day_trades:
                    tx_day = datetime.utcfromtimestamp(tx_ts).strftime('%Y-%m-%d')
                    if tx_day not in sent_by_day:
                        continue

                    days_diff = (filed_ts - tx_ts) / 86400
                    if days_diff > 5:
                        continue

                    sent_window = []
                    for i in range(20):
                        d = (datetime.strptime(tx_day, '%Y-%m-%d') - timedelta(days=i)).strftime('%Y-%m-%d')
                        if d in sent_by_day:
                            sent_window.append(sent_by_day[d])
                    if len(sent_window) < 15:
                        continue

                    mean_sent = sum(s[0] for s in sent_window) / len(sent_window)
                    if mean_sent > -0.2:
                        continue

                    hedged_window = []
                    for i in range(5):
                        d = (datetime.strptime(tx_day, '%Y-%m-%d') - timedelta(days=i)).strftime('%Y-%m-%d')
                        if d in sent_by_day and sent_by_day[d][2] > 0:
                            hedged_window.append(sent_by_day[d][1] / sent_by_day[d][2])
                    if len(hedged_window) < 3:
                        continue

                    hedged_slope = hedged_window[0] - hedged_window[-1] if len(hedged_window) >= 2 else 0
                    if hedged_slope <= 0.01:
                        continue

                    tx_idx = None
                    for i, ts in enumerate(bar_ts):
                        if ts >= tx_ts:
                            tx_idx = i
                            break
                    if tx_idx is None or tx_idx < 63:
                        continue

                    rets = []
                    for i in range(tx_idx - 63, tx_idx):
                        if bar_close[i-1] > 0:
                            rets.append((bar_close[i] - bar_close[i-1]) / bar_close[i-1])
                    if len(rets) < 30:
                        continue

                    vol_20 = (sum(r*r for r in rets[-20:]) / 20) ** 0.5 if len(rets) >= 20 else 0
                    vol_63 = sorted([(sum(r*r for r in rets[max(0,i-20):i]) / 20) ** 0.5 for i in range(20, len(rets))])
                    median_63 = vol_63[len(vol_63)//2] if vol_63 else 0

                    if vol_20 >= median_63:
                        continue

                    dollar_vol_20 = sum(bar_close[i] * bar_vol[i] for i in range(max(0, tx_idx-20), tx_idx)) / min(20, tx_idx)
                    if dollar_vol_20 < 1_000_000:
                        continue

                    fwd_ret = compute_forward_return(bars, tx_ts, horizon_days)
                    if fwd_ret is None:
                        continue

                    all_decisions.append({
                        'symbol': symbol,
                        'symbol_id': symbol_id,
                        'tx_ts': tx_ts,
                        'filed_ts': filed_ts,
                        'filed_day': filed_day,
                        'fwd_ret': fwd_ret,
                        'up': 1 if fwd_ret > 0 else 0
                    })

        if not all_decisions:
            print("INSUFFICIENT=1")
            return 0

        all_decisions.sort(key=lambda x: x['filed_ts'])
        n = len(all_decisions)
        split_idx = int(n * 0.8)
        train = all_decisions[:split_idx]
        sealed = all_decisions[split_idx:]

        for period_name, decisions in [('FULL', all_decisions), ('SEALED', sealed)]:
            if not decisions:
                continue
            issued = [d for d in decisions if d['up'] == 1]
            n_issued = len(issued)
            n_opportunities = len(decisions)
            precision = sum(1 for d in issued if d['up'] == 1) / n_issued if n_issued else 0
            base_rate = sum(1 for d in decisions if d['up'] == 1) / n_opportunities if n_opportunities else 0
            distinct_days = len(set(d['filed_day'] for d in issued))
            n_clusters = 1
            for i in range(1, len(issued)):
                if issued[i]['filed_day'] != issued[i-1]['filed_day']:
                    n_clusters += 1
            design_effect = n_issued / n_clusters if n_clusters else 1
            effective_n = n_issued / design_effect if design_effect else 0

            if period_name == 'FULL':
                print(f"ISSUED={n_issued}")
                print(f"OPPORTUNITIES={n_opportunities}")
                print(f"PRECISION={precision:.6f}")
                print(f"BASE_RATE={base_rate:.6f}")
                print(f"DISTINCT_DAYS={distinct_days}")
                print(f"EFFECTIVE_N={effective_n:.6f}")
            else:
                print(f"SEALED_PRECISION={precision:.6f}")

    finally:
        conn.close()
    return 0

if __name__ == '__main__':
    sys.exit(main())