# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 451
# cycle_index: 42
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, date, timedelta
from collections import defaultdict

def business_days_between(start_ts: int, end_ts: int) -> int:
    """Count business days between two unix timestamps (exclusive of start, inclusive of end?).
    We want delay = disclosure_date - trade_date in business days.
    If trade and disclosure same day -> 0 business days delay.
    If trade Friday, disclosure Monday -> 1 business day delay.
    """
    start_dt = datetime.fromtimestamp(start_ts).date()
    end_dt = datetime.fromtimestamp(end_ts).date()
    if end_dt <= start_dt:
        return 0
    days = 0
    current = start_dt + timedelta(days=1)
    while current <= end_dt:
        if current.weekday() < 5:  # Mon-Fri
            days += 1
        current += timedelta(days=1)
    return days

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all open-market purchases (code='P') with valid timestamps
    cur.execute("""
        SELECT symbol_id, tx_ts, filed_ts
        FROM insider_trades
        WHERE code = 'P' AND tx_ts IS NOT NULL AND filed_ts IS NOT NULL AND filed_ts >= tx_ts
        ORDER BY symbol_id, filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return

    # Group by symbol
    by_symbol = defaultdict(list)
    for row in trades:
        by_symbol[row['symbol_id']].append((row['tx_ts'], row['filed_ts']))

    # For each symbol, compute trailing 365-day average disclosure delay for each purchase
    signals = []  # (symbol_id, decision_ts, delay_bdays, avg_delay_bdays)
    for symbol_id, trades_list in by_symbol.items():
        # Need at least 5 purchases in trailing 365 days for universe membership
        # We'll compute for each trade the average delay of prior trades within 365 days
        for i, (tx_ts, filed_ts) in enumerate(trades_list):
            delay = business_days_between(tx_ts, filed_ts)
            # Look back 365 days from this trade's trade date (tx_ts)
            cutoff = tx_ts - 365 * 86400
            prior_delays = []
            for j in range(i):
                prior_tx, prior_filed = trades_list[j]
                if prior_tx >= cutoff:
                    prior_delays.append(business_days_between(prior_tx, prior_filed))
            if len(prior_delays) >= 5:
                avg_delay = sum(prior_delays) / len(prior_delays)
                # Entry condition: delay <= 1 AND delay <= avg_delay - 1
                if delay <= 1 and delay <= avg_delay - 1:
                    signals.append((symbol_id, filed_ts, delay, avg_delay))

    if not signals:
        print("INSUFFICIENT=1")
        return

    # Get labels from prediction_outcomes for horizon=21 at decision timestamps
    # prediction_outcomes has: symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, basis_epoch
    # We need to match on symbol_id, horizon=21, ts = decision_ts (filed_ts)
    # up is the realized direction (1 for up, 0 for down presumably)
    signal_keys = [(s[0], s[1]) for s in signals]  # (symbol_id, filed_ts)
    # Build a set for fast lookup
    signal_set = set(signal_keys)

    # Query prediction_outcomes for horizon=21 and matching timestamps
    placeholders = ','.join(['(?,?)'] * len(signal_keys))
    query = f"""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21 AND (symbol_id, ts) IN ({placeholders})
    """
    flat_keys = [item for pair in signal_keys for item in pair]
    cur.execute(query, flat_keys)
    labels = {(row['symbol_id'], row['ts']): row['up'] for row in cur.fetchall()}

    # Attach labels to signals
    labeled_signals = []
    for symbol_id, decision_ts, delay, avg_delay in signals:
        key = (symbol_id, decision_ts)
        if key in labels:
            labeled_signals.append((symbol_id, decision_ts, labels[key]))

    if not labeled_signals:
        print("INSUFFICIENT=1")
        return

    # Sort by decision timestamp
    labeled_signals.sort(key=lambda x: x[1])

    # Hold out most recent 20% as sealed era
    n_total = len(labeled_signals)
    n_sealed = max(1, int(n_total * 0.2))
    train_signals = labeled_signals[:-n_sealed]
    sealed_signals = labeled_signals[-n_sealed:]

    def compute_metrics(signals_list):
        if not signals_list:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = len(signals_list)
        hits = sum(1 for _, _, up in signals_list if up == 1)
        precision = hits / issued if issued > 0 else 0.0
        base_rate = hits / issued if issued > 0 else 0.0  # base rate within issued subset = precision for binary up/down
        # Distinct UTC days among issued calls
        distinct_days = len(set(datetime.fromtimestamp(ts).date() for _, ts, _ in signals_list))
        # Design effect: approximate by 1 + (avg cluster size - 1) * intraclass correlation
        # Simple approximation: group by day, compute design effect = 1 + (mean cluster size - 1) * rho
        # Use rho = 0.1 as conservative estimate for financial returns
        day_counts = defaultdict(int)
        for _, ts, _ in signals_list:
            day_counts[datetime.fromtimestamp(ts).date()] += 1
        cluster_sizes = list(day_counts.values())
        mean_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1
        rho = 0.1
        design_effect = 1 + (mean_cluster - 1) * rho
        effective_n = issued / design_effect if design_effect > 0 else issued
        return issued, hits, precision, base_rate, distinct_days, effective_n

    # Overall metrics (on training era for reporting, but spec says report on full? 
    # The spec says "Hold out the most recent 20% as a sealed era and report it separately"
    # And print ISSUED, OPPORTUNITIES, PRECISION, BASE_RATE, DISTINCT_DAYS, EFFECTIVE_N, SEALED_PRECISION
    # ISSUED should be total issued calls (train + sealed)? Or just train?
    # The sealed era is held out, so ISSUED likely refers to the training era (the ones we "issued" in backtest).
    # But SEALED_PRECISION is separate. Let's report on training era for main metrics, sealed for SEALED_PRECISION.
    # OPPORTUNITIES = count of decision points considered (universe-eligible days with at least one purchase?)
    # The spec: "OPPORTUNITIES=<count of decision points considered>"
    # A decision point is a (symbol, day) where we evaluate entry conditions.
    # We considered each open-market purchase as a potential decision point.
    # But universe requires >=5 purchases in trailing year. So opportunities = number of purchases meeting universe criteria.
    # Let's compute opportunities as the number of trades where prior_delays >= 5 (universe eligible).
    
    # Recompute opportunities
    opportunities = 0
    for symbol_id, trades_list in by_symbol.items():
        for i, (tx_ts, filed_ts) in enumerate(trades_list):
            cutoff = tx_ts - 365 * 86400
            prior_count = sum(1 for j in range(i) if trades_list[j][0] >= cutoff)
            if prior_count >= 5:
                opportunities += 1

    issued_train, hits_train, precision_train, base_rate_train, distinct_days_train, effective_n_train = compute_metrics(train_signals)
    _, _, sealed_precision, _, _, _ = compute_metrics(sealed_signals)

    # Print required lines
    print(f"ISSUED={issued_train}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision_train:.6f}")
    print(f"BASE_RATE={base_rate_train:.6f}")
    print(f"DISTINCT_DAYS={distinct_days_train}")
    print(f"EFFECTIVE_N={effective_n_train:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()