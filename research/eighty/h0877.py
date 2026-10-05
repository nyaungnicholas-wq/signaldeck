# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 876
# cycle_index: 22
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

MATERIAL_KEYWORDS = [
    'contract', 'acquisition', 'merger', 'award', 'partnership',
    'agreement', 'license', 'approval', 'clearance', 'authorization',
    'grant', 'order', 'backlog', 'booking', 'expansion', 'facility', 'plant'
]

OFFICER_TITLES = [
    'ceo', 'cfo', 'chief executive', 'chief financial',
    'president', 'chief operating', 'coo'
]

def is_officer(title):
    if not title:
        return False
    t = title.lower()
    return any(kw in t for kw in OFFICER_TITLES)

def has_material_keyword(title):
    if not title:
        return False
    t = title.lower()
    return any(kw in t for kw in MATERIAL_KEYWORDS)

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime(d.year, d.month, d.day).timestamp())

def get_trading_days_between(conn, symbol_id, start_date, end_date):
    cur = conn.execute(
        "SELECT ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? AND ts<=? ORDER BY ts",
        (symbol_id, date_to_epoch(start_date), date_to_epoch(end_date))
    )
    return [epoch_to_date(row[0]) for row in cur.fetchall()]

def get_close_on_or_after(conn, symbol_id, target_date):
    cur = conn.execute(
        "SELECT close, ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts>=? ORDER BY ts LIMIT 1",
        (symbol_id, date_to_epoch(target_date))
    )
    row = cur.fetchone()
    if row:
        return row[0], epoch_to_date(row[1])
    return None, None

def get_close_on_or_before(conn, symbol_id, target_date):
    cur = conn.execute(
        "SELECT close, ts FROM bars WHERE symbol_id=? AND tf='1d' AND ts<=? ORDER BY ts DESC LIMIT 1",
        (symbol_id, date_to_epoch(target_date))
    )
    row = cur.fetchone()
    if row:
        return row[0], epoch_to_date(row[1])
    return None, None

def compute_forward_return_5d(conn, symbol_id, decision_date):
    entry_price, entry_date = get_close_on_or_after(conn, symbol_id, decision_date)
    if entry_price is None:
        return None, None
    target_date = entry_date + timedelta(days=10)
    exit_price, exit_date = get_close_on_or_before(conn, symbol_id, target_date)
    if exit_price is None:
        return None, None
    trading_days = get_trading_days_between(conn, symbol_id, entry_date, exit_date)
    if len(trading_days) < 5:
        return None, None
    return (exit_price - entry_price) / entry_price, entry_date

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row

    cur = conn.execute("""
        SELECT symbol_id, filed_ts, title
        FROM filings
        WHERE form = '8-K' AND title IS NOT NULL
        ORDER BY symbol_id, filed_ts
    """)
    filings_8k = {}
    for row in cur:
        if has_material_keyword(row['title']):
            fid = row['symbol_id']
            if fid not in filings_8k:
                filings_8k[fid] = []
            filings_8k[fid].append((row['filed_ts'], row['title']))

    cur = conn.execute("""
        SELECT symbol_id, insider, title, code, tx_ts, filed_ts, shares, price
        FROM insider_trades
        WHERE code = 'P' AND title IS NOT NULL
        ORDER BY symbol_id, tx_ts
    """)
    officer_trades = []
    for row in cur:
        if is_officer(row['title']):
            officer_trades.append({
                'symbol_id': row['symbol_id'],
                'insider': row['insider'],
                'title': row['title'],
                'tx_ts': row['tx_ts'],
                'filed_ts': row['filed_ts'],
                'shares': row['shares'],
                'price': row['price']
            })

    if not officer_trades:
        print("INSUFFICIENT=1")
        return 0

    opportunities = 0
    issued = 0
    hits = 0
    issued_dates = set()
    sealed_hits = 0
    sealed_issued = 0

    for trade in officer_trades:
        sym = trade['symbol_id']
        if sym not in filings_8k:
            continue
        trade_date = epoch_to_date(trade['tx_ts'])
        decision_date = epoch_to_date(trade['filed_ts'])
        if decision_date <= trade_date:
            continue

        recent_8k = None
        for f_ts, f_title in filings_8k[sym]:
            f_date = epoch_to_date(f_ts)
            if f_date < trade_date and (trade_date - f_date).days <= 30:
                recent_8k = (f_ts, f_title, f_date)
        if not recent_8k:
            continue

        f_ts, f_title, f_date = recent_8k
        entry_price, _ = get_close_on_or_after(conn, sym, f_date)
        trade_price, _ = get_close_on_or_after(conn, sym, trade_date)
        if entry_price is None or trade_price is None or entry_price == 0:
            continue
        ret_8k_to_trade = (trade_price - entry_price) / entry_price
        if ret_8k_to_trade < -0.15 or ret_8k_to_trade > 0.05:
            continue

        opportunities += 1
        fwd_ret, entry_date = compute_forward_return_5d(conn, sym, decision_date)
        if fwd_ret is None:
            continue

        issued += 1
        issued_dates.add(entry_date)
        if fwd_ret > 0:
            hits += 1

    if issued == 0:
        print("INSUFFICIENT=1")
        return 0

    all_dates = sorted(issued_dates)
    split_idx = int(len(all_dates) * 0.8)
    sealed_start = all_dates[split_idx] if split_idx < len(all_dates) else None

    if sealed_start:
        for trade in officer_trades:
            sym = trade['symbol_id']
            if sym not in filings_8k:
                continue
            trade_date = epoch_to_date(trade['tx_ts'])
            decision_date = epoch_to_date(trade['filed_ts'])
            if decision_date <= trade_date:
                continue
            recent_8k = None
            for f_ts, f_title in filings_8k[sym]:
                f_date = epoch_to_date(f_ts)
                if f_date < trade_date and (trade_date - f_date).days <= 30:
                    recent_8k = (f_ts, f_title, f_date)
            if not recent_8k:
                continue
            f_ts, f_title, f_date = recent_8k
            entry_price, _ = get_close_on_or_after(conn, sym, f_date)
            trade_price, _ = get_close_on_or_after(conn, sym, trade_date)
            if entry_price is None or trade_price is None or entry_price == 0:
                continue
            ret_8k_to_trade = (trade_price - entry_price) / entry_price
            if ret_8k_to_trade < -0.15 or ret_8k_to_trade > 0.05:
                continue
            fwd_ret, entry_date = compute_forward_return_5d(conn, sym, decision_date)
            if fwd_ret is None:
                continue
            if entry_date >= sealed_start:
                sealed_issued += 1
                if fwd_ret > 0:
                    sealed_hits += 1

    precision = hits / issued if issued else 0
    base_rate = hits / issued if issued else 0
    distinct_days = len(issued_dates)
    design_effect = 1.5
    effective_n = issued / design_effect
    sealed_precision = sealed_hits / sealed_issued if sealed_issued else 0

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    return 0

if __name__ == '__main__':
    sys.exit(main())