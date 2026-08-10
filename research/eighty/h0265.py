import sqlite3
import sys
import math
from collections import defaultdict

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all symbols with daily bars and sentiment_features
    cur.execute("""
        SELECT DISTINCT b.symbol_id
        FROM bars b
        JOIN sentiment_features sf ON sf.symbol_id = b.symbol_id
        WHERE b.tf = '1d'
    """)
    symbol_ids = [row['symbol_id'] for row in cur.fetchall()]
    if not symbol_ids:
        print("INSUFFICIENT=1")
        return

    # Fetch all daily bars for these symbols
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, ts, close, volume
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close'], row['volume']))

    # Fetch sentiment_features (day, mean_score)
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)
    sent_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        sent_by_symbol[row['symbol_id']].append((row['day'], row['mean_score']))

    # Fetch prediction_outcomes for horizon=10 (10 trading days)
    cur.execute(f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 10 AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    labels_by_symbol = defaultdict(dict)
    for row in cur.fetchall():
        labels_by_symbol[row['symbol_id']][row['ts']] = row['up']

    # Helper: convert ts to date string
    def ts_to_date(ts):
        # ts is unix epoch seconds, assume UTC midnight
        import datetime
        return datetime.datetime.utcfromtimestamp(ts).strftime('%Y-%m-%d')

    # For each symbol, compute rolling indicators and find opportunities
    all_opportunities = []  # list of (symbol_id, ts, date_str, close, volume, ...)
    all_issued = []         # list of (symbol_id, ts, date_str, label_up)

    # We'll need cross-sectional volatility deciles per date
    # First pass: compute 20-day realized volatility for each symbol at each ts
    vol_by_date_symbol = defaultdict(dict)  # date_str -> {symbol_id: vol}

    for sym in symbol_ids:
        bars = bars_by_symbol.get(sym, [])
        if len(bars) < 252 + 60 + 20:  # need enough history
            continue
        sent = sent_by_symbol.get(sym, [])
        if not sent:
            continue
        labels = labels_by_symbol.get(sym, {})
        if not labels:
            continue

        # Build maps for fast lookup
        sent_map = {day: score for day, score in sent}
        label_map = labels

        # Precompute daily dollar volume
        dv = [close * vol for (_, close, vol) in bars]
        closes = [close for (_, close, _) in bars]
        dates = [ts_to_date(ts) for (ts, _, _) in bars]
        tss = [ts for (ts, _, _) in bars]

        n = len(bars)
        # Rolling 200-day SMA (index i uses bars[i-200:i] for SMA at i-1? Wait)
        # Condition: close at T-1 > 200-day SMA at T-1
        # SMA at T-1 = average of closes[T-200 : T-1] (200 days ending at T-1)
        # So for index i (T), we need SMA at i-1 = mean(closes[i-200:i])
        # i must be >= 200
        # Also need 252 prior sessions: i >= 252
        # Also need 60-day avg dollar volume T-60..T-1: i >= 60
        # Also need 20-day rolling avg dollar volume for T-2, T-1, T: i >= 20
        # Also need 60-day median sentiment: need 60 sentiment days up to T
        # Also need 20-day realized vol at T: need 20 returns up to T

        # Precompute prefix sums for dollar volume for 60-day avg
        dv_prefix = [0.0]
        for v in dv:
            dv_prefix.append(dv_prefix[-1] + v)

        # Precompute prefix sums for closes for 200-day SMA
        close_prefix = [0.0]
        for c in closes:
            close_prefix.append(close_prefix[-1] + c)

        # Rolling 20-day avg dollar volume for each day (using 20 days ending at that day)
        # For day j, avg_dv_20[j] = mean(dv[j-19:j+1]) if j>=19 else None
        avg_dv_20 = [None] * n
        for j in range(19, n):
            avg_dv_20[j] = (dv_prefix[j+1] - dv_prefix[j-19]) / 20.0

        # Rolling 200-day SMA for each day (200 days ending at that day)
        sma_200 = [None] * n
        for j in range(199, n):
            sma_200[j] = (close_prefix[j+1] - close_prefix[j-199]) / 200.0

        # Rolling 20-day realized volatility (std of log returns over 20 days ending at j)
        # log returns: ln(close[k]/close[k-1]) for k=j-19..j
        vol_20 = [None] * n
        for j in range(20, n):  # need 20 returns, so 21 closes
            rets = []
            for k in range(j-19, j+1):
                if closes[k-1] > 0:
                    rets.append(math.log(closes[k] / closes[k-1]))
            if len(rets) == 20:
                mean_ret = sum(rets) / 20.0
                var = sum((r - mean_ret)**2 for r in rets) / 20.0
                vol_20[j] = math.sqrt(var) * math.sqrt(252)  # annualized
            else:
                vol_20[j] = None

        # Sentiment: need 3-day avg at T (days T-2, T-1, T) and 60-day median up to T
        # Sentiment days are in sent_map keyed by date string
        # For each bar index i (T), get date_str = dates[i]
        # Check if sent_map has date_str, dates[i-1], dates[i-2]
        # 60-day median: collect last 60 sentiment scores up to date_str

        # Build list of sentiment scores in order of dates for this symbol
        sent_dates = sorted(sent_map.keys())
        sent_scores = [sent_map[d] for d in sent_dates]
        # Map date to index in sent_dates
        sent_date_to_idx = {d: idx for idx, d in enumerate(sent_dates)}

        # For cross-sectional vol decile, collect vol_20 per date
        for i in range(n):
            if vol_20[i] is not None:
                vol_by_date_symbol[dates[i]][sym] = vol_20[i]

        # Now scan for opportunities at each i (T)
        # Universe: i >= 252 (252 prior sessions), close >= 5, 60-day avg dv >= 5M
        # 60-day avg dv over T-60..T-1: indices i-60 to i-1 inclusive (60 days)
        for i in range(252, n):
            ts = tss[i]
            date_str = dates[i]
            close = closes[i]
            if close < 5.0:
                continue
            # 60-day avg dollar volume T-60..T-1
            if i < 60:
                continue
            avg_dv_60 = (dv_prefix[i] - dv_prefix[i-60]) / 60.0
            if avg_dv_60 < 5_000_000:
                continue
            # Must have sentiment for T, T-1, T-2
            if date_str not in sent_map:
                continue
            date_t1 = dates[i-1]
            date_t2 = dates[i-2]
            if date_t1 not in sent_map or date_t2 not in sent_map:
                continue
            # Must have label for this ts (horizon=10)
            if ts not in label_map:
                continue

            # Entry conditions
            # (1) close at T-1 > 200-day SMA at T-1
            if i-1 < 199 or sma_200[i-1] is None:
                continue
            if closes[i-1] <= sma_200[i-1]:
                continue
            # (2) 3-day close-to-close return T-3..T <= -3%
            if i < 3:
                continue
            ret_3 = (closes[i] - closes[i-3]) / closes[i-3]
            if ret_3 > -0.03:
                continue
            # (3) each day T-2, T-1, T has dollar volume below its 20-day rolling average
            ok_vol = True
            for j in (i-2, i-1, i):
                if j < 19 or avg_dv_20[j] is None or dv[j] >= avg_dv_20[j]:
                    ok_vol = False
                    break
            if not ok_vol:
                continue
            # (4) 3-day average news sentiment at T >= 60-day rolling median
            sent_t = sent_map[date_str]
            sent_t1 = sent_map[date_t1]
            sent_t2 = sent_map[date_t2]
            avg_sent_3 = (sent_t + sent_t1 + sent_t2) / 3.0
            # 60-day median up to T
            if date_str not in sent_date_to_idx:
                continue
            idx_sent = sent_date_to_idx[date_str]
            if idx_sent < 59:
                continue
            window_scores = sent_scores[idx_sent-59:idx_sent+1]
            window_scores.sort()
            median_60 = window_scores[30]  # 61 elements, median at 30
            if avg_sent_3 < median_60:
                continue

            # All entry conditions passed. Now abstain conditions (additional)
            # 20-session realized volatility at T in top cross-sectional decile
            # We'll check this after collecting all opportunities for cross-sectional decile
            # Cooldown: call issued for same symbol in prior 20 trading days
            # Fewer than 30 independent observations remain - global check later

            all_opportunities.append({
                'symbol_id': sym,
                'ts': ts,
                'date': date_str,
                'close': close,
                'vol_20': vol_20[i],
                'label': label_map[ts],
                'index': i
            })

    if len(all_opportunities) < 30:
        print("INSUFFICIENT=1")
        return

    # Compute cross-sectional volatility deciles per date
    vol_threshold_by_date = {}
    for date, sym_vols in vol_by_date_symbol.items():
        if len(sym_vols) < 10:
            vol_threshold_by_date[date] = float('inf')
        else:
            vols = sorted(sym_vols.values())
            # top decile = 90th percentile
            idx = int(len(vols) * 0.9)
            if idx >= len(vols):
                idx = len(vols) - 1
            vol_threshold_by_date[date] = vols[idx]

    # Now filter opportunities with abstain conditions
    # Sort opportunities by date then symbol for deterministic cooldown
    all_opportunities.sort(key=lambda x: (x['date'], x['symbol_id']))

    last_call_date = {}  # symbol_id -> last call date (as index in sorted dates)
    # We need to track trading days, not calendar days. Use the index in the global sorted unique dates.
    # But cooldown is "prior 20 trading days" for the same symbol.
    # We'll track the last call's bar index for that symbol.
    last_call_bar_index = {}

    issued = []
    for opp in all_opportunities:
        sym = opp['symbol_id']
        i = opp['index']
        date = opp['date']
        vol = opp['vol_20']

        # Volatility decile abstain
        if vol is not None:
            threshold = vol_threshold_by_date.get(date, float('inf'))
            if vol >= threshold:
                continue

        # Cooldown: prior 20 trading days for same symbol
        if sym in last_call_bar_index:
            if i - last_call_bar_index[sym] <= 20:
                continue

        # All checks passed, issue call
        issued.append(opp)
        last_call_bar_index[sym] = i

    if not issued:
        print("INSUFFICIENT=1")
        return

    # Hold out most recent 20% as sealed era
    # Split by date: sort issued by date, take last 20% by count
    issued.sort(key=lambda x: x['date'])
    n_issued = len(issued)
    split_idx = int(n_issued * 0.8)
    train_issued = issued[:split_idx]
    sealed_issued = issued[split_idx:]

    # Also split opportunities for abstention rate
    all_opportunities.sort(key=lambda x: x['date'])
    n_opp = len(all_opportunities)
    split_idx_opp = int(n_opp * 0.8)
    train_opp = all_opportunities[:split_idx_opp]
    sealed_opp = all_opportunities[split_idx_opp:]

    def compute_metrics(issued_list, opp_list):
        if not issued_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        n_issued = len(issued_list)
        n_opp = len(opp_list)
        hits = sum(1 for x in issued_list if x['label'] == 1)
        precision = hits / n_issued if n_issued > 0 else 0.0
        base_rate = hits / n_issued if n_issued > 0 else 0.0  # base rate within issued subset
        distinct_days = len(set(x['date'] for x in issued_list))
        # Design effect: ISSUED / DISTINCT_DAYS, minimum 1.01
        if distinct_days > 0:
            deff = n_issued / distinct_days
            if deff < 1.01:
                deff = 1.01
        else:
            deff = 1.01
        effective_n = n_issued / deff
        return n_issued, n_opp, precision, base_rate, distinct_days, effective_n

    train_issued_cnt, train_opp_cnt, train_prec, train_base, train_days, train_eff = compute_metrics(train_issued, train_opp)
    sealed_issued_cnt, sealed_opp_cnt, sealed_prec, sealed_base, sealed_days, sealed_eff = compute_metrics(sealed_issued, sealed_opp)

    # Overall metrics (on full dataset? The requirement says report sealed separately, but the print lines seem to be for the main era? 
    # The instruction: "PRINT exactly these lines at the end... SEALED_PRECISION=<precision on the sealed era>"
    # The other metrics (ISSUED, OPPORTUNITIES, PRECISION, BASE_RATE, DISTINCT_DAYS, EFFECTIVE_N) are for the main (non-sealed) era? 
    # Or for the full dataset? The claim is about the hypothesis test, typically on the training era.
    # But the instruction says "Hold out the most recent 20% as a sealed era and report it separately."
    # So the main metrics should be on the 80% (training), and SEALED_PRECISION on the 20%.
    # However, the print lines don't specify era for the first six. I'll assume they are for the training era (80%).
    # But the instruction says "PRINT exactly these lines at the end, one per line, with real computed numbers:"
    # It doesn't say "for the training era". But SEALED_PRECISION is explicitly sealed.
    # I'll output the training era metrics for the first six, and sealed precision for the last.

    print(f"ISSUED={train_issued_cnt}")
    print(f"OPPORTUNITIES={train_opp_cnt}")
    print(f"PRECISION={train_prec:.6f}")
    print(f"BASE_RATE={train_base:.6f}")
    print(f"DISTINCT_DAYS={train_days}")
    print(f"EFFECTIVE_N={train_eff:.6f}")
    print(f"SEALED_PRECISION={sealed_prec:.6f}")

if __name__ == '__main__':
    main()