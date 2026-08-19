# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 778
# cycle_index: 48
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
import math
from datetime import datetime, timezone
from collections import defaultdict
from itertools import groupby

def main():
    # Hypothesis definition
    HYPOTHESIS = {
        "MECHANISM": "Officers (CEO/CFO/President) buying open-market shares during sustained negative news sentiment (20-day mean_score < -0.15) and low volatility (20-day realized vol < 20th percentile of 252-day history) signal private conviction contradicting public pessimism; the market underreacts due to attention capture by negative headlines.",
        "HORIZON": "21d",
        "UNIVERSE": "Symbols with >=252 daily bars (tf='1d'), non-null sentiment_features, and insider_trades from 2018-07-26 to 2024-10-01 (80% cutoff), active and not delisted before decision date.",
        "ENTRY": "On filed_ts of insider_trades where code='P', shares>0, value>50000, title indicates officer (CEO/CFO/President), 20-day sentiment_features.mean_score < -0.15 (ending on filing date), 20-day realized volatility from bars < 20th percentile of trailing 252-day volatility (ending on filing date), issue long call.",
        "ABSTAIN": "Disclosure delay > 5 days (filed_ts - tx_ts > 432000), insufficient sentiment_features history (<20 days), insufficient bars history (<252 days), symbol delisted before filing date, forward return window extends beyond data availability.",
        "CLAIM": "Precision >= 0.62 on issued calls with issued-subset base rate <= 0.55."
    }

    # Constants
    HORIZON_DAYS = 21
    MIN_BARS = 252
    VOL_LOOKBACK = 252
    VOL_WINDOW = 20
    SENT_WINDOW = 20
    SENT_THRESHOLD = -0.15
    VOL_PERCENTILE = 0.20
    MAX_DISCLOSURE_DELAY = 5 * 86400
    MIN_TRADE_VALUE = 50000
    OFFICER_KEYWORDS = ('CEO', 'CFO', 'PRESIDENT', 'CHIEF EXECUTIVE', 'CHIEF FINANCIAL')
    BARS_START_TS = 1532563200  # 2018-07-26 00:00:00 UTC

    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get universe symbols
    cur.execute("""
        SELECT s.id, s.symbol, s.delisted_at
        FROM symbols s
        JOIN (
            SELECT symbol_id, COUNT(*) as cnt
            FROM bars
            WHERE tf = '1d'
            GROUP BY symbol_id
            HAVING cnt >= ?
        ) b ON s.id = b.symbol_id
        WHERE s.active = 1 AND (s.delisted_at IS NULL OR s.delisted_at > ?)
    """, (MIN_BARS, BARS_START_TS))
    symbols = {row['id']: {'symbol': row['symbol'], 'delisted_at': row['delisted_at']} for row in cur.fetchall()}
    if not symbols:
        print("INSUFFICIENT=1")
        return
    symbol_ids = list(symbols.keys())
    placeholders = ','.join('?' * len(symbol_ids))

    # 2. Load sentiment_features
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)
    sentiment_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        sentiment_by_symbol[row['symbol_id']].append((row['day'], row['mean_score']))

    # 3. Load insider_trades (code='P')
    cur.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE symbol_id IN ({placeholders}) AND code = 'P' AND shares > 0
        ORDER BY filed_ts
    """, symbol_ids)
    insider_trades = []
    for row in cur.fetchall():
        title_upper = (row['title'] or '').upper()
        is_officer = any(kw in title_upper for kw in OFFICER_KEYWORDS)
        if is_officer and row['value'] >= MIN_TRADE_VALUE:
            insider_trades.append(dict(row))

    if not insider_trades:
        print("INSUFFICIENT=1")
        return

    # 4. Load bars (tf='1d')
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bars_by_symbol = defaultdict(list)
    for row in cur.fetchall():
        bars_by_symbol[row['symbol_id']].append((row['ts'], row['close']))

    conn.close()

    # Process each symbol
    decisions = []  # (symbol_id, filed_ts, issued, hit, decision_date_str)

    for sym_id, trades in groupby(insider_trades, key=lambda x: x['symbol_id']):
        trades = list(trades)
        sent_data = sentiment_by_symbol.get(sym_id, [])
        bars_data = bars_by_symbol.get(sym_id, [])
        if len(bars_data) < MIN_BARS + VOL_WINDOW + HORIZON_DAYS:
            continue
        if len(sent_data) < SENT_WINDOW:
            continue

        # Precompute bars data
        bars_ts = [ts for ts, _ in bars_data]
        bars_close = [close for _, close in bars_data]

        # Daily log returns
        returns = []
        for i in range(1, len(bars_close)):
            if bars_close[i-1] > 0 and bars_close[i] > 0:
                returns.append(math.log(bars_close[i] / bars_close[i-1]))
            else:
                returns.append(0.0)

        # Rolling 20-day volatility (annualized)
        vol_20 = [0.0] * len(returns)
        for i in range(VOL_WINDOW - 1, len(returns)):
            window = returns[i-VOL_WINDOW+1:i+1]
            mean_ret = sum(window) / VOL_WINDOW
            var = sum((x - mean_ret) ** 2 for x in window) / VOL_WINDOW
            vol_20[i] = math.sqrt(var) * math.sqrt(252)

        # Rolling 20th percentile of vol_20 over trailing 252 days
        vol_pct_20 = [0.0] * len(vol_20)
        for i in range(VOL_LOOKBACK - 1, len(vol_20)):
            window = vol_20[i-VOL_LOOKBACK+1:i+1]
            sorted_win = sorted(window)
            idx = int(VOL_PERCENTILE * (len(sorted_win) - 1))
            vol_pct_20[i] = sorted_win[idx]

        # Sentiment: convert day strings to timestamps
        sent_dates = []
        sent_scores = []
        for day_str, score in sent_data:
            try:
                dt = datetime.strptime(day_str, '%Y-%m-%d').replace(tzinfo=timezone.utc)
                sent_dates.append(int(dt.timestamp()))
                sent_scores.append(score)
            except:
                continue

        # Rolling 20-day mean sentiment
        sent_mean_20 = [0.0] * len(sent_scores)
        for i in range(SENT_WINDOW - 1, len(sent_scores)):
            sent_mean_20[i] = sum(sent_scores[i-SENT_WINDOW+1:i+1]) / SENT_WINDOW

        # For each trade
        for trade in trades:
            filed_ts = trade['filed_ts']
            tx_ts = trade['tx_ts']
            disclosure_delay = filed_ts - tx_ts
            if disclosure_delay > MAX_DISCLOSURE_DELAY:
                continue

            # Find bars index for filing date
            filed_date = datetime.fromtimestamp(filed_ts, tz=timezone.utc).date()
            filed_day_start = int(datetime.combine(filed_date, datetime.min.time()).replace(tzinfo=timezone.utc).timestamp())

            bars_idx = -1
            for i, ts in enumerate(bars_ts):
                if ts == filed_day_start:
                    bars_idx = i
                    break
                elif ts > filed_day_start:
                    bars_idx = i - 1
                    break
            if bars_idx < VOL_LOOKBACK + VOL_WINDOW - 1:
                continue
            if bars_idx + HORIZON_DAYS >= len(bars_close):
                continue

            # Find sentiment index
            sent_idx = -1
            for i, ts in enumerate(sent_dates):
                if ts == filed_day_start:
                    sent_idx = i
                    break
                elif ts > filed_day_start:
                    sent_idx = i - 1
                    break
            if sent_idx < SENT_WINDOW - 1:
                continue

            # ENTRY conditions
            sent_ok = sent_mean_20[sent_idx] < SENT_THRESHOLD
            vol_ok = vol_20[bars_idx] < vol_pct_20[bars_idx] if vol_pct_20[bars_idx] > 0 else False

            if not (sent_ok and vol_ok):
                # Opportunity but not issued
                decisions.append((sym_id, filed_ts, False, None, filed_date.isoformat()))
                continue

            # Compute 21-day forward return
            entry_close = bars_close[bars_idx]
            exit_close = bars_close[bars_idx + HORIZON_DAYS]
            if entry_close <= 0 or exit_close <= 0:
                continue
            fwd_return = (exit_close - entry_close) / entry_close
            hit = 1 if fwd_return > 0 else 0

            decisions.append((sym_id, filed_ts, True, hit, filed_date.isoformat()))

    if not decisions:
        print("INSUFFICIENT=1")
        return

    # Split into sealed (most recent 20%) and rest
    decisions.sort(key=lambda x: x[1])  # sort by filed_ts
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    sealed_decisions = decisions[-n_sealed:]
    train_decisions = decisions[:-n_sealed]

    # Compute metrics for all decisions
    issued_all = [d for d in decisions if d[2]]
    opportunities_all = len(decisions)
    issued_count = len(issued_all)

    if issued_count == 0:
        print("INSUFFICIENT=1")
        return

    hits = sum(d[3] for d in issued_all)
    precision = hits / issued_count

    # Base rate within issued subset
    base_rate = hits / issued_count  # same as precision for binary long calls

    # Distinct days among issued calls
    distinct_days = len(set(d[4] for d in issued_all))

    # Effective N: account for time clustering
    # Group issued calls by day
    day_counts = defaultdict(int)
    day_hits = defaultdict(int)
    for d in issued_all:
        day_counts[d[4]] += 1
        day_hits[d[4]] += d[3]

    if len(day_counts) > 1:
        daily_rates = [day_hits[day] / day_counts[day] for day in day_counts]
        avg_daily_issued = sum(day_counts.values()) / len(day_counts)
        overall_p = precision
        expected_var = overall_p * (1 - overall_p) / avg_daily_issued if avg_daily_issued > 0 else 0
        actual_var = sum((r - overall_p) ** 2 for r in daily_rates) / len(daily_rates) if daily_rates else 0
        design_effect = actual_var / expected_var if expected_var > 0 else 1.0
        design_effect = max(1.0, design_effect)
    else:
        design_effect = 1.0

    effective_n = issued_count / design_effect

    # Sealed precision
    sealed_issued = [d for d in sealed_decisions if d[2]]
    sealed_precision = 0.0
    if sealed_issued:
        sealed_hits = sum(d[3] for d in sealed_issued)
        sealed_precision = sealed_hits / len(sealed_issued)

    # Output
    print(f"ISSUED={issued_count}")
    print(f"OPPORTUNITIES={opportunities_all}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()