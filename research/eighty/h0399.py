# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 398
# cycle_index: 66
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from bisect import bisect_left, bisect_right
from collections import defaultdict
from datetime import datetime, timezone

def connect_ro(db_path):
    return sqlite3.connect(f'file:{db_path}?mode=ro', uri=True)

def unix_to_date(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).date()

def date_to_unix(d):
    return int(datetime.combine(d, datetime.min.time(), tzinfo=timezone.utc).timestamp())

def main():
    db = connect_ro('data/signaldeck.db')
    db.row_factory = sqlite3.Row
    cur = db.cursor()

    # 1. Find 21-day horizon identifier
    cur.execute("SELECT DISTINCT horizon FROM prediction_outcomes WHERE horizon LIKE '%21%' OR horizon LIKE '%3w%' LIMIT 10")
    horizons = [r['horizon'] for r in cur.fetchall()]
    if not horizons:
        print("INSUFFICIENT=1")
        return
    # Prefer '21d' if exists, else first match
    horizon = '21d' if '21d' in horizons else horizons[0]
    print(f"Using horizon: {horizon}", file=sys.stderr)

    # 2. Load decision points (symbol_id, ts, up)
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = ?
        ORDER BY ts
    """, (horizon,))
    decisions = cur.fetchall()
    if not decisions:
        print("INSUFFICIENT=1")
        return

    # 3. Load sentiment_features (symbol_id, day, mean_score)
    cur.execute("""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE mean_score IS NOT NULL
        ORDER BY symbol_id, day
    """)
    sent_rows = cur.fetchall()
    sent_by_sym = defaultdict(list)
    for r in sent_rows:
        sent_by_sym[r['symbol_id']].append((r['day'], r['mean_score']))

    # 4. Load fundamentals SharesOutstanding (symbol_id, value, fetched_at)
    cur.execute("""
        SELECT symbol_id, value, fetched_at
        FROM fundamentals
        WHERE metric = 'SharesOutstanding' AND value IS NOT NULL AND fetched_at IS NOT NULL
        ORDER BY symbol_id, fetched_at
    """)
    fund_rows = cur.fetchall()
    fund_by_sym = defaultdict(list)
    for r in fund_rows:
        try:
            val = float(r['value'])
            ft = int(r['fetched_at'])
            fund_by_sym[r['symbol_id']].append((ft, val))
        except:
            pass

    # 5. Load daily bars for volume/price (symbol_id, ts, close, volume)
    # Only need last ~20 days per decision point. But loading all 13M rows is heavy.
    # Instead, load bars for symbols that appear in decisions, for relevant time range.
    sym_ids = set(d['symbol_id'] for d in decisions)
    min_ts = min(d['ts'] for d in decisions) - 30*86400  # 30 days before earliest decision
    max_ts = max(d['ts'] for d in decisions)
    placeholders = ','.join('?' * len(sym_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders}) AND ts >= ? AND ts <= ?
        ORDER BY symbol_id, ts
    """, list(sym_ids) + [min_ts, max_ts])
    bar_rows = cur.fetchall()
    bars_by_sym = defaultdict(list)
    for r in bar_rows:
        bars_by_sym[r['symbol_id']].append((r['ts'], r['close'], r['volume']))

    # 6. Process decisions in chronological order, split 80/20 by time
    decisions_sorted = sorted(decisions, key=lambda x: x['ts'])
    n = len(decisions_sorted)
    split_idx = int(n * 0.8)
    train_decisions = decisions_sorted[:split_idx]
    sealed_decisions = decisions_sorted[split_idx:]

    def compute_features(sym_id, decision_ts):
        """Return (so_decreased, sent_slope_pos, vwap_return_pos) as-of decision_ts"""
        # SharesOutstanding: two most recent fetched_at <= decision_ts
        so_vals = []
        for ft, val in fund_by_sym.get(sym_id, []):
            if ft <= decision_ts:
                so_vals.append(val)
            else:
                break
        so_decreased = False
        if len(so_vals) >= 2:
            so_decreased = so_vals[-1] < so_vals[-2]  # latest < previous = decrease

        # News sentiment slope over last 10 days (day < decision_date)
        dec_date = unix_to_date(decision_ts)
        sent_data = sent_by_sym.get(sym_id, [])
        # Filter days < dec_date
        recent = [(d, s) for d, s in sent_data if d < dec_date][-10:]
        sent_slope_pos = False
        if len(recent) >= 5:
            # Simple linear regression slope
            x = list(range(len(recent)))
            y = [s for _, s in recent]
            n_pts = len(x)
            sum_x = sum(x)
            sum_y = sum(y)
            sum_xy = sum(xi*yi for xi, yi in zip(x, y))
            sum_x2 = sum(xi*xi for xi in x)
            denom = n_pts * sum_x2 - sum_x * sum_x
            if denom != 0:
                slope = (n_pts * sum_xy - sum_x * sum_y) / denom
                sent_slope_pos = slope > 0

        # Volume-weighted return over last 20 bars (ts < decision_ts)
        bars = bars_by_sym.get(sym_id, [])
        # Filter ts < decision_ts
        recent_bars = [(ts, c, v) for ts, c, v in bars if ts < decision_ts][-20:]
        vwap_return_pos = False
        if len(recent_bars) >= 10:
            total_vol = sum(v for _, _, v in recent_bars)
            if total_vol > 0:
                vwap = sum(c * v for _, c, v in recent_bars) / total_vol
                first_close = recent_bars[0][1]
                if first_close > 0:
                    vwap_ret = (vwap / first_close) - 1
                    vwap_return_pos = vwap_ret > 0

        return so_decreased, sent_slope_pos, vwap_return_pos

    def evaluate(decisions_list, label):
        issued = 0
        hits = 0
        opportunities = len(decisions_list)
        issued_days = set()
        for d in decisions_list:
            sym_id = d['symbol_id']
            ts = d['ts']
            up = d['up']
            so_dec, sent_slope, vwap_ret = compute_features(sym_id, ts)
            if so_dec and sent_slope and vwap_ret:
                issued += 1
                issued_days.add(unix_to_date(ts))
                if up == 1:
                    hits += 1
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate of predicted class (up=1) within issued
        distinct_days = len(issued_days)
        design_effect = issued / distinct_days if distinct_days > 0 else 1.0
        effective_n = issued / design_effect if design_effect > 0 else 0.0
        print(f"{label}: ISSUED={issued} OPP={opportunities} PREC={precision:.4f} BASE={base_rate:.4f} DAYS={distinct_days} EFF_N={effective_n:.1f}", file=sys.stderr)
        return {
            'issued': issued, 'opportunities': opportunities, 'hits': hits,
            'precision': precision, 'base_rate': base_rate,
            'distinct_days': distinct_days, 'effective_n': effective_n
        }

    train_res = evaluate(train_decisions, "TRAIN")
    sealed_res = evaluate(sealed_decisions, "SEALED")

    if train_res['issued'] == 0 and sealed_res['issued'] == 0:
        print("INSUFFICIENT=1")
        return

    # Combined metrics (train + sealed for overall, but sealed reported separately)
    total_issued = train_res['issued'] + sealed_res['issued']
    total_hits = train_res['hits'] + sealed_res['hits']
    total_opp = train_res['opportunities'] + sealed_res['opportunities']
    total_days = len(set().union(
        {unix_to_date(d['ts']) for d in train_decisions if compute_features(d['symbol_id'], d['ts']) == (True, True, True)},
        {unix_to_date(d['ts']) for d in sealed_decisions if compute_features(d['symbol_id'], d['ts']) == (True, True, True)}
    )) if total_issued > 0 else 0
    # Recompute combined distinct days properly
    all_issued_days = set()
    for d in decisions_sorted:
        sym_id = d['symbol_id']
        ts = d['ts']
        so_dec, sent_slope, vwap_ret = compute_features(sym_id, ts)
        if so_dec and sent_slope and vwap_ret:
            all_issued_days.add(unix_to_date(ts))
    total_days = len(all_issued_days)

    overall_precision = total_hits / total_issued if total_issued > 0 else 0.0
    overall_base = total_hits / total_issued if total_issued > 0 else 0.0
    overall_de = total_issued / total_days if total_days > 0 else 1.0
    overall_eff_n = total_issued / overall_de if overall_de > 0 else 0.0
    sealed_precision = sealed_res['precision']

    print(f"ISSUED={total_issued}")
    print(f"OPPORTUNITIES={total_opp}")
    print(f"PRECISION={overall_precision:.6f}")
    print(f"BASE_RATE={overall_base:.6f}")
    print(f"DISTINCT_DAYS={total_days}")
    print(f"EFFECTIVE_N={overall_eff_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()