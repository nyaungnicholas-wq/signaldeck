# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 719
# cycle_index: 46
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import sys
from datetime import datetime, timedelta

# MECHANISM: Insiders possess private information that contradicts the prevailing negative news narrative. When news sentiment is negative but the hedged ratio (hedged/n_polar) is elevated, it signals journalistic uncertainty rather than genuine fundamental deterioration. Insiders exploit this by buying when the market overreacts to low-conviction negative headlines.
# HORIZON: 1w
# UNIVERSE: Common stocks (market='stocks') with daily bars, insider transactions, and daily sentiment_features. Decision timestamp is insider trade disclosure date (filed_ts).
# ENTRY: An insider open-market purchase (code='P') disclosed on day D where: (1) 20-day average of sentiment_features.mean_score < -0.1 (persistently negative), (2) 20-day average of sentiment_features.hedged / sentiment_features.n_polar > 0.6 (high hedging, low conviction), (3) No insider purchase disclosed in prior 10 sessions.
# ABSTAIN: If sentiment_features data unavailable for 20 days prior, or symbol delisted before D+5 sessions, or symbol not active.
# CLAIM: Precision >= 55% on 1w forward return direction (up=1) with base rate <= 50% in issued subset.

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get all insider open-market purchases with symbol info
    cur.execute("""
        SELECT it.accession, it.symbol_id, it.filed_ts, it.code,
               s.symbol, s.market, s.active, s.delisted_at
        FROM insider_trades it
        JOIN symbols s ON s.id = it.symbol_id
        WHERE it.code = 'P'
          AND s.market = 'stocks'
          AND s.active = 1
        ORDER BY it.filed_ts
    """)
    trades = cur.fetchall()
    if not trades:
        print("INSUFFICIENT=1")
        return 0

    # Pre-load sentiment_features for all symbols in trades
    symbol_ids = list(set(t['symbol_id'] for t in trades))
    placeholders = ','.join('?' * len(symbol_ids))
    cur.execute(f"""
        SELECT symbol_id, day, mean_score, hedged, n_polar
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, symbol_ids)
    sent_rows = cur.fetchall()

    # Organize sentiment by symbol_id
    sent_by_symbol = {}
    for row in sent_rows:
        sid = row['symbol_id']
        if sid not in sent_by_symbol:
            sent_by_symbol[sid] = []
        sent_by_symbol[sid].append({
            'day': row['day'],
            'mean_score': row['mean_score'],
            'hedged': row['hedged'],
            'n_polar': row['n_polar']
        })

    # Pre-load bars for label computation (tf='1d')
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, symbol_ids)
    bar_rows = cur.fetchall()

    bars_by_symbol = {}
    for row in bar_rows:
        sid = row['symbol_id']
        if sid not in bars_by_symbol:
            bars_by_symbol[sid] = []
        bars_by_symbol[sid].append({'ts': row['ts'], 'close': row['close']})

    # For each trade, evaluate entry conditions and compute label
    decisions = []
    for trade in trades:
        sid = trade['symbol_id']
        filed_ts = trade['filed_ts']
        filed_date = datetime.utcfromtimestamp(filed_ts).date()
        filed_date_str = filed_date.isoformat()

        # Check delisted
        if trade['delisted_at'] and trade['delisted_at'] <= filed_ts + 5 * 86400:
            continue

        # Check prior insider purchase in last 10 sessions (approx 10 trading days ~ 14 calendar days)
        cur.execute("""
            SELECT 1 FROM insider_trades
            WHERE symbol_id = ? AND code = 'P' AND filed_ts < ? AND filed_ts >= ? - 14*86400
            LIMIT 1
        """, (sid, filed_ts, filed_ts))
        if cur.fetchone():
            continue

        # Get sentiment features for 20 days prior to filed_date
        sent_data = sent_by_symbol.get(sid, [])
        if not sent_data:
            continue

        # Filter to days < filed_date (strictly before disclosure)
        prior_sent = [s for s in sent_data if s['day'] < filed_date_str]
        if len(prior_sent) < 20:
            continue

        # Take last 20 days
        window = prior_sent[-20:]

        # Compute 20-day average mean_score
        avg_mean_score = sum(s['mean_score'] for s in window) / 20

        # Compute 20-day average hedged ratio (only days with n_polar > 0)
        hedged_ratios = []
        for s in window:
            if s['n_polar'] and s['n_polar'] > 0:
                hedged_ratios.append(s['hedged'] / s['n_polar'])
        if not hedged_ratios:
            continue
        avg_hedged_ratio = sum(hedged_ratios) / len(hedged_ratios)

        # Check conditions
        if avg_mean_score >= -0.1:
            continue
        if avg_hedged_ratio <= 0.6:
            continue

        # Compute 1w (5 trading day) forward return from bars
        bars = bars_by_symbol.get(sid, [])
        if not bars:
            continue

        # Find decision bar: last bar with ts <= filed_ts
        decision_idx = -1
        for i, bar in enumerate(bars):
            if bar['ts'] <= filed_ts:
                decision_idx = i
            else:
                break
        if decision_idx == -1 or decision_idx + 5 >= len(bars):
            continue

        entry_close = bars[decision_idx]['close']
        exit_close = bars[decision_idx + 5]['close']
        fwd_return = (exit_close - entry_close) / entry_close
        up = 1 if fwd_return > 0 else 0

        decisions.append({
            'symbol_id': sid,
            'filed_ts': filed_ts,
            'filed_date': filed_date_str,
            'up': up,
            'fwd_return': fwd_return
        })

    if not decisions:
        print("INSUFFICIENT=1")
        return 0

    # Sort by decision timestamp
    decisions.sort(key=lambda x: x['filed_ts'])

    # Hold out most recent 20% as sealed era
    n_total = len(decisions)
    n_sealed = max(1, int(n_total * 0.2))
    train_decisions = decisions[:-n_sealed]
    sealed_decisions = decisions[-n_sealed:]

    def compute_metrics(decs):
        if not decs:
            return None
        issued = len(decs)
        hits = sum(d['up'] for d in decs)
        precision = hits / issued if issued else 0.0
        base_rate = precision  # base rate of predicted class (up=1) within issued subset
        distinct_days = len(set(d['filed_date'] for d in decs))
        # Design effect: cluster by symbol+month to estimate autocorrelation
        # Simple approximation: effective_n = issued / (1 + (avg_cluster_size - 1) * rho)
        # Use conservative design effect of 2.0 (rho ~ 0.5, avg cluster ~ 2)
        design_effect = 2.0
        effective_n = issued / design_effect
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    train_metrics = compute_metrics(train_decisions)
    sealed_metrics = compute_metrics(sealed_decisions)

    if not train_metrics:
        print("INSUFFICIENT=1")
        return 0

    # Print required output
    print(f"ISSUED={train_metrics['issued']}")
    print(f"OPPORTUNITIES={n_total}")  # All decision points considered
    print(f"PRECISION={train_metrics['precision']:.6f}")
    print(f"BASE_RATE={train_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={train_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={train_metrics['effective_n']:.2f}")
    print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}" if sealed_metrics else "SEALED_PRECISION=0.000000")

    return 0

if __name__ == '__main__':
    sys.exit(main())