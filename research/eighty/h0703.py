# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 702
# cycle_index: 29
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date
from collections import defaultdict
import bisect

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def parse_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def percentile(arr, p):
    if not arr:
        return None
    arr = sorted(arr)
    k = (len(arr) - 1) * p
    f = int(k)
    c = min(f + 1, len(arr) - 1)
    if f == c:
        return arr[f]
    return arr[f] + (k - f) * (arr[c] - arr[f])

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get symbols with insider sales (code S), sentiment_features, and daily bars
    cur.execute("""
        SELECT DISTINCT s.id, s.symbol
        FROM symbols s
        JOIN insider_trades it ON it.symbol_id = s.id AND it.code = 'S'
        JOIN sentiment_features sf ON sf.symbol_id = s.id
        JOIN bars b ON b.symbol_id = s.id AND b.tf = '1d'
        WHERE s.market = 'stocks'
    """)
    symbols = [(row['id'], row['symbol']) for row in cur.fetchall()]
    if not symbols:
        print("INSUFFICIENT=1")
        return 0

    symbol_ids = [s[0] for s in symbols]
    placeholders = ','.join('?' * len(symbol_ids))

    # Pull daily bars (tf='1d')
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        d = epoch_to_date(row['ts'])
        bars_by_symbol[row['symbol_id']].append((d, row['close']))

    # Pull sentiment_features
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)
    sent_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        d = parse_date(row['day'])
        sent_by_symbol[row['symbol_id']].append((d, row['mean_score']))

    # Pull insider sales (code S)
    cur.execute(f"""
        SELECT symbol_id, filed_ts
        FROM insider_trades
        WHERE code = 'S' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, filed_ts
    """, symbol_ids)
    insider_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        d = epoch_to_date(row['filed_ts'])
        insider_by_symbol[row['symbol_id']].append(d)

    signals = []  # (date, symbol_id, close_t)

    for sym_id, sym in symbols:
        bars = bars_by_symbol.get(sym_id, [])
        sent = sent_by_symbol.get(sym_id, [])
        insider_days = set(insider_by_symbol.get(sym_id, []))

        if len(bars) < 252 + 21 or len(sent) < 252:
            continue

        # Build date-indexed maps
        bar_map = {d: c for d, c in bars}
        sent_map = {d: s for d, s in sent}

        # Align to bar dates (trading days)
        bar_dates = [d for d, _ in bars]
        bar_closes = [c for _, c in bars]

        # Precompute rolling 252-day high (including current day)
        high_252 = []
        for i in range(len(bar_closes)):
            if i < 251:
                high_252.append(None)
            else:
                high_252.append(max(bar_closes[i-251:i+1]))

        # Precompute rolling 252-day 95th percentile of sentiment (prior 252 days, excluding current)
        sent_values = []
        sent_dates = []
        for d, v in sent:
            sent_dates.append(d)
            sent_values.append(v)

        pctile_95 = {}
        for i, d in enumerate(sent_dates):
            if i < 252:
                pctile_95[d] = None
            else:
                window = sent_values[i-252:i]
                pctile_95[d] = percentile(window, 0.95)

        # Check each bar date for signal
        for i, d in enumerate(bar_dates):
            if d not in insider_days:
                continue
            if high_252[i] is None:
                continue
            if d not in pctile_95 or pctile_95[d] is None:
                continue
            close = bar_closes[i]
            if close < 0.98 * high_252[i]:
                continue
            sent_today = sent_map.get(d)
            if sent_today is None:
                continue
            if sent_today <= pctile_95[d]:
                continue
            # Check forward data availability
            if i + 21 >= len(bar_closes):
                continue
            signals.append((d, sym_id, close))

    if not signals:
        print("INSUFFICIENT=1")
        return 0

    # Compute forward returns and outcomes
    signals.sort(key=lambda x: x[0])
    issued = []
    for d, sym_id, close_t in signals:
        bars = bars_by_symbol[sym_id]
        bar_map = {bd: bc for bd, bc in bars}
        # Find index of d in bars
        bar_dates = [bd for bd, _ in bars]
        try:
            idx = bar_dates.index(d)
        except ValueError:
            continue
        if idx + 21 >= len(bars):
            continue
        close_t21 = bars[idx + 21][1]
        fwd_ret = (close_t21 / close_t) - 1
        outcome_down = 1 if fwd_ret < 0 else 0
        issued.append((d, sym_id, outcome_down))

    if not issued:
        print("INSUFFICIENT=1")
        return 0

    # Split sealed era: most recent 20% by date
    issued.sort(key=lambda x: x[0])
    n = len(issued)
    split_idx = int(n * 0.8)
    train = issued[:split_idx]
    sealed = issued[split_idx:]

    def compute_metrics(data):
        if not data:
            return 0, 0, 0, 0
        issued_count = len(data)
        hits = sum(1 for _, _, o in data if o == 1)
        precision = hits / issued_count if issued_count else 0
        base_rate = hits / issued_count if issued_count else 0  # same as precision for single-class prediction
        distinct_days = len(set(d for d, _, _ in data))
        # Design effect via ICC (ANOVA estimator)
        # Cluster by day
        day_outcomes = defaultdict(list)
        for d, _, o in data:
            day_outcomes[d].append(o)
        D = len(day_outcomes)
        if D <= 1:
            deff = 1.0
        else:
            n_total = issued_count
            n_bar = n_total / D
            p_overall = hits / n_total
            # Between and within mean squares
            bss = 0.0
            wss = 0.0
            for d, outcomes in day_outcomes.items():
                k = len(outcomes)
                p_d = sum(outcomes) / k
                bss += k * (p_d - p_overall) ** 2
                wss += k * p_d * (1 - p_d)
            if D > 1:
                bms = bss / (D - 1)
            else:
                bms = 0
            if n_total > D:
                wms = wss / (n_total - D)
            else:
                wms = 0
            denom = bms + (n_bar - 1) * wms
            if denom > 0:
                rho = (bms - wms) / denom
            else:
                rho = 0
            rho = max(rho, 0)
            deff = 1 + (n_bar - 1) * rho
        if deff <= 1:
            deff = 1.0001
        effective_n = issued_count / deff
        return issued_count, hits, precision, base_rate, distinct_days, effective_n

    train_issued, train_hits, train_prec, train_br, train_dd, train_en = compute_metrics(train)
    sealed_issued, sealed_hits, sealed_prec, sealed_br, sealed_dd, sealed_en = compute_metrics(sealed)
    all_issued, all_hits, all_prec, all_br, all_dd, all_en = compute_metrics(issued)

    # Opportunities: count of decision points considered (symbol-days with insider sale filed)
    # We need to count all (symbol, day) where insider sale filed and we had enough history to evaluate
    opportunities = 0
    for sym_id, sym in symbols:
        bars = bars_by_symbol.get(sym_id, [])
        sent = sent_by_symbol.get(sym_id, [])
        insider_days = set(insider_by_symbol.get(sym_id, []))
        if len(bars) < 252 + 21 or len(sent) < 252:
            continue
        bar_dates = [d for d, _ in bars]
        sent_map = {d: s for d, s in sent}
        pctile_95 = {}
        sent_values = [v for _, v in sent]
        sent_dates = [d for d, _ in sent]
        for i, d in enumerate(sent_dates):
            if i >= 252:
                window = sent_values[i-252:i]
                pctile_95[d] = percentile(window, 0.95)
        high_252 = {}
        bar_closes = [c for _, c in bars]
        for i in range(len(bar_closes)):
            if i >= 251:
                high_252[bar_dates[i]] = max(bar_closes[i-251:i+1])
        for d in bar_dates:
            if d in insider_days and d in high_252 and d in pctile_95 and pctile_95[d] is not None and d in sent_map:
                opportunities += 1

    print(f"ISSUED={all_issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={all_prec:.6f}")
    print(f"BASE_RATE={all_br:.6f}")
    print(f"DISTINCT_DAYS={all_dd}")
    print(f"EFFECTIVE_N={all_en:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")
    return 0

if __name__ == '__main__':
    sys.exit(main())