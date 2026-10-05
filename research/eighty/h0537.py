# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 536
# cycle_index: 66
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timezone, date
from collections import defaultdict
import bisect

def unix_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get insider purchases > $50k
    cur.execute("""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND value > 50000
        ORDER BY filed_ts
    """)
    purchases = cur.fetchall()
    if not purchases:
        print("INSUFFICIENT=1")
        return

    symbol_ids = sorted(set(p['symbol_id'] for p in purchases))
    sym_placeholder = ','.join('?' for _ in symbol_ids)

    # Get daily bars for these symbols (1d timeframe)
    cur.execute(f"""
        SELECT symbol_id, ts, open, high, low, close, volume
        FROM bars
        WHERE tf = '1d' AND ts >= 1532563200 AND symbol_id IN ({sym_placeholder})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_rows = cur.fetchall()

    # Get news count per symbol per day
    cur.execute(f"""
        SELECT symbol_id, date(ts, 'unixepoch') as day, COUNT(*) as cnt
        FROM news
        WHERE symbol_id IN ({sym_placeholder})
        GROUP BY symbol_id, day
    """, symbol_ids)
    news_counts = defaultdict(dict)
    for r in cur.fetchall():
        news_counts[r['symbol_id']][r['day']] = r['cnt']

    # Get sentiment features
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({sym_placeholder})
    """, symbol_ids)
    sentiment = defaultdict(dict)
    for r in cur.fetchall():
        sentiment[r['symbol_id']][r['day']] = r['mean_score']

    # Get prediction outcomes for horizon=21
    cur.execute(f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21 AND symbol_id IN ({sym_placeholder})
    """, symbol_ids)
    pred_outcomes = defaultdict(dict)
    for r in cur.fetchall():
        pred_outcomes[r['symbol_id']][r['ts']] = r['up']

    # Get all insider trades for "no insider transactions in prior 10 sessions" and "same insider purchased in prior 60 sessions"
    cur.execute(f"""
        SELECT symbol_id, insider, code, tx_ts
        FROM insider_trades
        WHERE symbol_id IN ({sym_placeholder})
        ORDER BY symbol_id, tx_ts
    """, symbol_ids)
    all_insider = cur.fetchall()

    # Build per-symbol structures
    bars_by_sym = defaultdict(list)
    for r in bars_rows:
        bars_by_sym[r['symbol_id']].append((
            r['ts'], r['open'], r['high'], r['low'], r['close'], r['volume']
        ))

    insider_by_sym = defaultdict(list)
    for r in all_insider:
        insider_by_sym[r['symbol_id']].append((
            r['insider'], r['code'], r['tx_ts']
        ))

    # Prepare per-symbol trading calendar and precomputed arrays
    sym_data = {}
    for sid in symbol_ids:
        b = bars_by_sym.get(sid, [])
        if len(b) < 60:
            continue
        dates = [unix_to_date(ts) for ts,_,_,_,_,_ in b]
        date_to_idx = {d:i for i,d in enumerate(dates)}
        closes = [c for _,_,_,_,c,_ in b]
        volumes = [v for *_,v in b]
        dollar_vols = [c*v for _,_,_,_,c,v in b]
        # 20-day avg volume (trailing, not including current)
        avg_vol_20 = [0.0]*len(b)
        avg_dollar_20 = [0.0]*len(b)
        for i in range(20, len(b)):
            avg_vol_20[i] = sum(volumes[i-20:i]) / 20.0
            avg_dollar_20[i] = sum(dollar_vols[i-20:i]) / 20.0
        # 60-day return (trailing)
        ret_60 = [0.0]*len(b)
        for i in range(60, len(b)):
            if closes[i-60] != 0:
                ret_60[i] = (closes[i] - closes[i-60]) / closes[i-60]
        # Daily returns
        rets = [0.0]*len(b)
        for i in range(1, len(b)):
            if closes[i-1] != 0:
                rets[i] = (closes[i] - closes[i-1]) / closes[i-1]
        sym_data[sid] = {
            'dates': dates,
            'date_to_idx': date_to_idx,
            'closes': closes,
            'volumes': volumes,
            'dollar_vols': dollar_vols,
            'avg_vol_20': avg_vol_20,
            'avg_dollar_20': avg_dollar_20,
            'ret_60': ret_60,
            'rets': rets,
            'bars_ts': [ts for ts,_,_,_,_,_ in b],
        }

    # Compute 5-day sentiment averages and 80th percentile threshold
    five_day_sents = []
    for sid, sdata in sym_data.items():
        sent_dict = sentiment.get(sid, {})
        dates = sdata['dates']
        for i in range(5, len(dates)):
            window = dates[i-5:i]
            vals = [sent_dict.get(d) for d in window if d in sent_dict]
            if len(vals) == 5:
                five_day_sents.append(sum(vals)/5.0)
    if not five_day_sents:
        print("INSUFFICIENT=1")
        return
    five_day_sents.sort()
    p80 = five_day_sents[int(len(five_day_sents)*0.8)]

    # Process each purchase
    calls = []  # (decision_ts, symbol_id, hit, decision_date)
    opportunities = 0

    for p in purchases:
        sid = p['symbol_id']
        insider = p['insider']
        tx_ts = p['tx_ts']
        filed_ts = p['filed_ts']
        value = p['value']

        if sid not in sym_data:
            continue
        opportunities += 1

        sdata = sym_data[sid]
        dates = sdata['dates']
        date_to_idx = sdata['date_to_idx']
        rets = sdata['rets']
        volumes = sdata['volumes']
        avg_vol_20 = sdata['avg_vol_20']
        avg_dollar_20 = sdata['avg_dollar_20']
        ret_60 = sdata['ret_60']
        bars_ts = sdata['bars_ts']

        tx_date = unix_to_date(tx_ts)
        filed_date = unix_to_date(filed_ts)

        # Find trigger day: within 3 sessions before tx_date (inclusive)
        if tx_date not in date_to_idx:
            continue
        tx_idx = date_to_idx[tx_date]
        trigger_found = False
        trigger_idx = -1
        for offset in range(4):  # 0,1,2,3 sessions before
            ti = tx_idx - offset
            if ti < 0:
                break
            # Check trigger conditions at ti
            if rets[ti] > -0.05:
                continue
            if avg_vol_20[ti] == 0 or volumes[ti] >= 0.5 * avg_vol_20[ti]:
                continue
            trig_date = dates[ti]
            if news_counts.get(sid, {}).get(trig_date.isoformat(), 0) > 0:
                continue
            # No insider transactions in prior 10 sessions (ti-10 to ti-1)
            has_insider = False
            for ins in insider_by_sym.get(sid, []):
                ins_date = unix_to_date(ins[2])
                if ins_date in date_to_idx:
                    ins_idx = date_to_idx[ins_date]
                    if ti-10 <= ins_idx < ti:
                        has_insider = True
                        break
            if has_insider:
                continue
            trigger_found = True
            trigger_idx = ti
            break

        if not trigger_found:
            continue

        # Abstain conditions at decision time (filed_date)
        if filed_date not in date_to_idx:
            # find next trading day
            fd_idx = bisect.bisect_left(dates, filed_date)
            if fd_idx >= len(dates):
                continue
        else:
            fd_idx = date_to_idx[filed_date]

        # 60-day return < -30%
        if ret_60[fd_idx] < -0.30:
            continue

        # Prior 5-session news sentiment > 80th percentile
        if fd_idx >= 5:
            window = dates[fd_idx-5:fd_idx]
            sent_dict = sentiment.get(sid, {})
            vals = [sent_dict.get(d.isoformat()) for d in window if d.isoformat() in sent_dict]
            if len(vals) == 5:
                avg_sent = sum(vals)/5.0
                if avg_sent > p80:
                    continue

        # Same insider purchased in prior 60 sessions
        same_insider_purchase = False
        for ins in insider_by_sym.get(sid, []):
            if ins[0] == insider and ins[1] == 'P':
                ins_date = unix_to_date(ins[2])
                if ins_date in date_to_idx:
                    ins_idx = date_to_idx[ins_date]
                    if fd_idx-60 <= ins_idx < fd_idx:
                        same_insider_purchase = True
                        break
        if same_insider_purchase:
            continue

        # 20-day dollar volume < $1M
        if avg_dollar_20[fd_idx] < 1_000_000:
            continue

        # All conditions passed - issue call
        decision_ts = bars_ts[fd_idx]
        hit = 0
        if decision_ts in pred_outcomes.get(sid, {}):
            hit = 1 if pred_outcomes[sid][decision_ts] == 1 else 0
        calls.append((filed_ts, sid, hit, filed_date))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # Sort by decision time
    calls.sort(key=lambda x: x[0])

    # Split: most recent 20% as sealed
    n_total = len(calls)
    n_sealed = max(1, int(n_total * 0.2))
    train_calls = calls[:-n_sealed]
    sealed_calls = calls[-n_sealed:]

    def compute_metrics(call_list):
        if not call_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(call_list)
        hits = sum(c[2] for c in call_list)
        precision = hits / issued if issued else 0.0
        base_rate = hits / issued if issued else 0.0  # base rate within issued subset
        distinct_days = len(set(c[3] for c in call_list))
        # Design effect: cluster by day, estimate ICC
        if distinct_days > 1:
            day_hits = defaultdict(lambda: [0,0])
            for c in call_list:
                day_hits[c[3]][0] += c[2]
                day_hits[c[3]][1] += 1
            p = precision
            m = issued / distinct_days
            between = sum(n * ((h/n) - p)**2 for h,n in day_hits.values()) / (distinct_days - 1)
            within = sum(n * (h/n) * (1 - h/n) for h,n in day_hits.values()) / (issued - distinct_days)
            if between + (m-1)*within > 0:
                icc = max(0.0, (between - within) / (between + (m-1)*within))
            else:
                icc = 0.0
            deff = 1 + (m - 1) * icc
        else:
            deff = float(issued) if issued > 1 else 1.0
        effective_n = issued / deff if deff > 0 else 0.0
        return issued, hits, precision, base_rate, distinct_days, effective_n

    issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(train_calls)
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_calls)

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

    conn.close()

if __name__ == '__main__':
    main()