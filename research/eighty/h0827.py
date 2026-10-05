# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 826
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

HORIZON_DAYS = 21
MIN_BARS = 252
MIN_INSIDER_TRADES = 50
MIN_NEWS_DAYS = 100
LOOKBACK_SENTIMENT = 20
SLOPE_WINDOW = 5
MAX_DISCLOSURE_DELAY_DAYS = 2
MOMENTUM_WINDOW = 63
MIN_SIGNALS_PER_WINDOW = 3
SEALED_FRACTION = 0.20

OFFICER_KEYWORDS = ('CEO', 'CFO', 'COO', 'PRESIDENT', 'CHIEF', 'OFFICER', 'VP ', 'VICE PRESIDENT')

def is_officer(title: str) -> bool:
    if not title:
        return False
    t = title.upper()
    return any(kw in t for kw in OFFICER_KEYWORDS)

def epoch_to_date(ts: int) -> datetime:
    return datetime.utcfromtimestamp(ts)

def date_to_str(dt: datetime) -> str:
    return dt.strftime('%Y-%m-%d')

def str_to_date(s: str) -> datetime:
    return datetime.strptime(s, '%Y-%m-%d')

def business_days_between(start: datetime, end: datetime) -> int:
    days = 0
    cur = start
    while cur < end:
        if cur.weekday() < 5:
            days += 1
        cur += timedelta(days=1)
    return days

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    cur.execute("SELECT MIN(ts), MAX(ts) FROM bars WHERE tf='1d'")
    row = cur.fetchone()
    if not row or row[0] is None:
        print("INSUFFICIENT=1")
        return
    min_bar_ts, max_bar_ts = row[0], row[1]
    min_bar_date = epoch_to_date(min_bar_ts).date()
    max_bar_date = epoch_to_date(max_bar_ts).date()

    label_end_date = max_bar_date - timedelta(days=HORIZON_DAYS)
    total_days = (label_end_date - min_bar_date).days
    sealed_start_date = min_bar_date + timedelta(days=int(total_days * (1 - SEALED_FRACTION)))

    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM bars
        WHERE tf='1d' AND ts <= ?
        GROUP BY symbol_id
        HAVING cnt >= ?
    """, (int(datetime.combine(label_end_date, datetime.min.time()).timestamp()), MIN_BARS))
    qualified_symbols = {row[0] for row in cur.fetchall()}

    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({})
        GROUP BY symbol_id
        HAVING cnt >= ?
    """.format(','.join('?'*len(qualified_symbols))), list(qualified_symbols) + [MIN_INSIDER_TRADES])
    qualified_symbols = {row[0] for row in cur.fetchall()}

    cur.execute("""
        SELECT symbol_id, COUNT(*) as cnt
        FROM sentiment_features
        WHERE symbol_id IN ({})
        GROUP BY symbol_id
        HAVING cnt >= ?
    """.format(','.join('?'*len(qualified_symbols))), list(qualified_symbols) + [MIN_NEWS_DAYS])
    qualified_symbols = {row[0] for row in cur.fetchall()}

    if not qualified_symbols:
        print("INSUFFICIENT=1")
        return

    placeholders = ','.join('?'*len(qualified_symbols))
    sym_list = list(qualified_symbols)

    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE symbol_id IN ({placeholders})
        ORDER BY symbol_id, day
    """, sym_list)
    sentiment_rows = cur.fetchall()

    sentiment_by_sym = {}
    for row in sentiment_rows:
        sentiment_by_sym.setdefault(row['symbol_id'], []).append((row['day'], row['mean_score']))

    cur.execute(f"""
        SELECT accession, symbol_id, insider, title, code, shares, price, value, tx_ts, filed_ts
        FROM insider_trades
        WHERE code='P' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, filed_ts
    """, sym_list)
    insider_rows = cur.fetchall()

    cur.execute(f"""
        SELECT symbol_id, tf, ts, close
        FROM bars
        WHERE tf='1d' AND symbol_id IN ({placeholders})
        ORDER BY symbol_id, ts
    """, sym_list)
    bar_rows = cur.fetchall()

    bars_by_sym = {}
    for row in bar_rows:
        dt = epoch_to_date(row['ts']).date()
        bars_by_sym.setdefault(row['symbol_id'], []).append((dt, row['close']))

    opportunities = 0
    issued = 0
    hits = 0
    issued_days = set()
    sealed_issued = 0
    sealed_hits = 0

    for sym_id in qualified_symbols:
        bars = bars_by_sym.get(sym_id, [])
        if len(bars) < MIN_BARS + HORIZON_DAYS + MOMENTUM_WINDOW:
            continue
        bar_dates = [b[0] for b in bars]
        bar_closes = [b[1] for b in bars]
        date_to_idx = {d: i for i, d in enumerate(bar_dates)}

        sent = sentiment_by_sym.get(sym_id, [])
        if len(sent) < LOOKBACK_SENTIMENT + SLOPE_WINDOW:
            continue
        sent_dates = [str_to_date(s[0]).date() for s in sent]
        sent_scores = [s[1] for s in sent]
        sent_date_to_idx = {d: i for i, d in enumerate(sent_dates)}

        insider_trades = [t for t in insider_rows if t['symbol_id'] == sym_id]
        if not insider_trades:
            continue

        for trade in insider_trades:
            if not is_officer(trade['title']):
                continue
            filed_ts = trade['filed_ts']
            tx_ts = trade['tx_ts']
            filed_date = epoch_to_date(filed_ts).date()
            tx_date = epoch_to_date(tx_ts).date()

            if filed_date > label_end_date:
                continue
            if filed_date < min_bar_date:
                continue

            delay_bdays = business_days_between(tx_date, filed_date)
            if delay_bdays > MAX_DISCLOSURE_DELAY_DAYS:
                continue

            if filed_date not in date_to_idx:
                continue
            filed_idx = date_to_idx[filed_date]

            if filed_idx < MOMENTUM_WINDOW:
                continue
            mom_start_idx = filed_idx - MOMENTUM_WINDOW
            mom_return = (bar_closes[filed_idx] - bar_closes[mom_start_idx]) / bar_closes[mom_start_idx]
            if mom_return >= 0:
                continue

            if filed_date not in sent_date_to_idx:
                continue
            sent_idx = sent_date_to_idx[filed_date]
            if sent_idx < LOOKBACK_SENTIMENT + SLOPE_WINDOW - 1:
                continue

            prev_scores = sent_scores[sent_idx - LOOKBACK_SENTIMENT - SLOPE_WINDOW + 1 : sent_idx - SLOPE_WINDOW + 1]
            if len(prev_scores) < LOOKBACK_SENTIMENT:
                continue
            if not all(s < 0 for s in prev_scores):
                continue

            slope_scores = sent_scores[sent_idx - SLOPE_WINDOW + 1 : sent_idx + 1]
            if len(slope_scores) < SLOPE_WINDOW:
                continue
            slope = (slope_scores[-1] - slope_scores[0]) / SLOPE_WINDOW
            if slope <= 0:
                continue

            window_start = filed_date - timedelta(days=63)
            recent_signals = 0
            for t2 in insider_trades:
                if not is_officer(t2['title']):
                    continue
                f2 = epoch_to_date(t2['filed_ts']).date()
                if window_start <= f2 <= filed_date:
                    recent_signals += 1
            if recent_signals < MIN_SIGNALS_PER_WINDOW:
                continue

            label_idx = filed_idx + HORIZON_DAYS
            if label_idx >= len(bar_closes):
                continue
            fwd_return = (bar_closes[label_idx] - bar_closes[filed_idx]) / bar_closes[filed_idx]
            hit = 1 if fwd_return > 0 else 0

            opportunities += 1
            issued += 1
            hits += hit
            issued_days.add(filed_date)

            is_sealed = filed_date >= sealed_start_date
            if is_sealed:
                sealed_issued += 1
                sealed_hits += hit

    if issued == 0:
        print("INSUFFICIENT=1")
        return

    distinct_days = len(issued_days)
    precision = hits / issued
    base_rate = hits / issued
    design_effect = 1.0
    if distinct_days > 0:
        design_effect = issued / distinct_days
    effective_n = issued / design_effect if design_effect > 0 else 0
    sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0.0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == '__main__':
    main()