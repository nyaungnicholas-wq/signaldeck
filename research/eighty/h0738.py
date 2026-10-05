# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 737
# cycle_index: 7
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_epoch(d):
    return int(datetime.combine(d, datetime.min.time()).timestamp())

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # 1. Get SPY symbol_id
    spy_row = cur.execute("SELECT id FROM symbols WHERE symbol = 'SPY'").fetchone()
    if not spy_row:
        print("INSUFFICIENT=1")
        return
    spy_id = spy_row['id']

    # 2. Load SPY daily bars (tf='1d') and compute daily returns
    spy_bars = cur.execute(
        "SELECT ts, close FROM bars WHERE symbol_id = ? AND tf = '1d' ORDER BY ts",
        (spy_id,)
    ).fetchall()
    if len(spy_bars) < 2:
        print("INSUFFICIENT=1")
        return
    spy_ts = [r['ts'] for r in spy_bars]
    spy_close = [r['close'] for r in spy_bars]
    spy_ret = {}
    for i in range(1, len(spy_ts)):
        ret = (spy_close[i] - spy_close[i-1]) / spy_close[i-1]
        spy_ret[spy_ts[i]] = ret
    spy_ts_set = set(spy_ts)

    # 3. Get all symbols with daily bars from 2018-07-26 onward (1532563200)
    START_EPOCH = 1532563200  # 2018-07-26
    symbols_rows = cur.execute(
        """SELECT s.id, s.symbol, s.market, s.active, s.delisted_at
           FROM symbols s
           WHERE EXISTS (
               SELECT 1 FROM bars b
               WHERE b.symbol_id = s.id AND b.tf = '1d' AND b.ts >= ?
           )""",
        (START_EPOCH,)
    ).fetchall()
    if not symbols_rows:
        print("INSUFFICIENT=1")
        return

    symbol_info = {r['id']: dict(r) for r in symbols_rows}
    valid_symbol_ids = set(symbol_info.keys())

    # 4. Load insider purchases (code='P')
    insider_rows = cur.execute(
        """SELECT accession, symbol_id, tx_ts, filed_ts
           FROM insider_trades
           WHERE code = 'P' AND symbol_id IN ({})
           ORDER BY tx_ts""".format(','.join('?'*len(valid_symbol_ids))),
        list(valid_symbol_ids)
    ).fetchall()
    if not insider_rows:
        print("INSUFFICIENT=1")
        return

    # 5. Load fundamentals for SharesOutstanding
    fund_rows = cur.execute(
        """SELECT symbol_id, value, fetched_at
           FROM fundamentals
           WHERE metric = 'SharesOutstanding' AND symbol_id IN ({})
           ORDER BY symbol_id, fetched_at""".format(','.join('?'*len(valid_symbol_ids))),
        list(valid_symbol_ids)
    ).fetchall()
    fund_by_symbol = defaultdict(list)
    for r in fund_rows:
        fund_by_symbol[r['symbol_id']].append((r['fetched_at'], r['value']))

    # 6. Load daily bars for all valid symbols (tf='1d') - we need returns, volume, close
    # Do this in chunks to avoid memory issues, but 15M rows is manageable
    # We'll load per symbol as needed, but better to load all once
    print("Loading daily bars...", file=sys.stderr)
    bars_rows = cur.execute(
        """SELECT symbol_id, ts, close, volume
           FROM bars
           WHERE tf = '1d' AND symbol_id IN ({})
           ORDER BY symbol_id, ts""".format(','.join('?'*len(valid_symbol_ids))),
        list(valid_symbol_ids)
    ).fetchall()

    # Organize bars by symbol
    bars_by_symbol = defaultdict(list)
    for r in bars_rows:
        bars_by_symbol[r['symbol_id']].append((r['ts'], r['close'], r['volume']))

    # 7. Compute daily returns, dollar volume for each symbol
    symbol_data = {}
    for sid, bars in bars_by_symbol.items():
        if len(bars) < 2:
            continue
        ts_list = [b[0] for b in bars]
        close_list = [b[1] for b in bars]
        vol_list = [b[2] for b in bars]
        ret = {}
        dollar_vol = {}
        for i in range(1, len(ts_list)):
            if close_list[i-1] > 0:
                ret[ts_list[i]] = (close_list[i] - close_list[i-1]) / close_list[i-1]
            dollar_vol[ts_list[i]] = close_list[i] * vol_list[i] if vol_list[i] > 0 else 0
        symbol_data[sid] = {
            'ts': ts_list,
            'close': close_list,
            'ret': ret,
            'dollar_vol': dollar_vol,
            'ts_set': set(ts_list)
        }

    # 8. For each symbol, get list of insider purchase trade dates (tx_ts) sorted
    insider_by_symbol = defaultdict(list)
    for r in insider_rows:
        insider_by_symbol[r['symbol_id']].append((r['tx_ts'], r['filed_ts'], r['accession']))

    # 9. Helper: get trailing avg dollar volume (60 trading days) at trade date
    def get_avg_dollar_vol(sid, trade_ts, window=60):
        data = symbol_data.get(sid)
        if not data:
            return 0
        ts_list = data['ts']
        # find index of trade_ts (or next trading day if trade_ts not exact)
        # trade_ts is epoch of trade time; we need the trading day bar
        # Find the bar with ts >= trade_ts date (same day or next)
        trade_date = epoch_to_date(trade_ts)
        trade_day_epoch = date_to_epoch(trade_date)
        # Find index where ts >= trade_day_epoch
        idx = -1
        for i, ts in enumerate(ts_list):
            if ts >= trade_day_epoch:
                idx = i
                break
        if idx == -1:
            return 0
        # trailing window: indices [idx-window, idx-1]
        start = max(0, idx - window)
        end = idx
        vals = [data['dollar_vol'].get(ts_list[i], 0) for i in range(start, end) if ts_list[i] in data['dollar_vol']]
        if not vals:
            return 0
        return sum(vals) / len(vals)

    # 10. Helper: get market cap at trade date (SharesOutstanding * price)
    def get_market_cap(sid, trade_ts):
        data = symbol_data.get(sid)
        if not data:
            return 0
        fund_list = fund_by_symbol.get(sid, [])
        if not fund_list:
            return 0
        # find latest fundamental with fetched_at <= trade_ts
        shares = None
        for fetched_at, val in fund_list:
            if fetched_at <= trade_ts:
                shares = val
            else:
                break
        if shares is None or shares <= 0:
            return 0
        # get price at trade date
        trade_date = epoch_to_date(trade_ts)
        trade_day_epoch = date_to_epoch(trade_date)
        # find close on or after trade_day_epoch
        ts_list = data['ts']
        close_list = data['close']
        price = None
        for i, ts in enumerate(ts_list):
            if ts >= trade_day_epoch:
                price = close_list[i]
                break
        if price is None or price <= 0:
            return 0
        return shares * price

    # 11. Helper: check no purchases in prior 21 trading days
    def no_recent_purchases(sid, trade_ts, lookback=21):
        purchases = insider_by_symbol.get(sid, [])
        if not purchases:
            return True
        trade_date = epoch_to_date(trade_ts)
        trade_day_epoch = date_to_epoch(trade_date)
        data = symbol_data.get(sid)
        if not data:
            return True
        ts_list = data['ts']
        # find index of trade_day_epoch in ts_list
        try:
            trade_idx = ts_list.index(trade_day_epoch)
        except ValueError:
            # trade date not a trading day for this symbol? use next
            trade_idx = -1
            for i, ts in enumerate(ts_list):
                if ts > trade_day_epoch:
                    trade_idx = i
                    break
            if trade_idx == -1:
                return True
        # check prior lookback trading days
        start_idx = max(0, trade_idx - lookback)
        for tx_ts, _, _ in purchases:
            tx_date = epoch_to_date(tx_ts)
            tx_day_epoch = date_to_epoch(tx_date)
            if tx_day_epoch in data['ts_set']:
                tx_idx = ts_list.index(tx_day_epoch)
                if start_idx <= tx_idx < trade_idx:
                    return False
        return True

    # 12. Helper: disclosure lag in trading days
    def disclosure_lag_days(tx_ts, filed_ts):
        tx_date = epoch_to_date(tx_ts)
        filed_date = epoch_to_date(filed_ts)
        # count trading days between (exclusive of tx_date, inclusive of filed_date?)
        # "disclosure lag > 5 business days" - lag is filed_ts - tx_ts in business days
        # Use SPY trading days as proxy for market trading days
        tx_day_epoch = date_to_epoch(tx_date)
        filed_day_epoch = date_to_epoch(filed_date)
        # Count SPY trading days in (tx_day_epoch, filed_day_epoch]
        count = 0
        for ts in spy_ts:
            if tx_day_epoch < ts <= filed_day_epoch:
                count += 1
        return count

    # 13. Helper: get 21-day forward return from disclosure date
    def get_forward_return(sid, filed_ts, horizon=21):
        data = symbol_data.get(sid)
        if not data:
            return None
        filed_date = epoch_to_date(filed_ts)
        filed_day_epoch = date_to_epoch(filed_date)
        ts_list = data['ts']
        close_list = data['close']
        # find index of filed_day_epoch or next trading day
        start_idx = -1
        for i, ts in enumerate(ts_list):
            if ts >= filed_day_epoch:
                start_idx = i
                break
        if start_idx == -1 or start_idx + horizon >= len(ts_list):
            return None
        start_price = close_list[start_idx]
        end_price = close_list[start_idx + horizon]
        if start_price <= 0:
            return None
        return (end_price - start_price) / start_price

    # 14. Process each insider purchase
    calls = []  # list of (filed_ts, sid, fwd_ret, trade_ts)
    for r in insider_rows:
        sid = r['symbol_id']
        tx_ts = r['tx_ts']
        filed_ts = r['filed_ts']
        accession = r['accession']

        # Check symbol in universe at trade date
        if sid not in symbol_data:
            continue
        info = symbol_info.get(sid)
        if not info:
            continue
        # Check active and not delisted at trade date
        if not info['active']:
            continue
        if info['delisted_at'] and info['delisted_at'] <= tx_ts:
            continue

        # Check trade date conditions
        trade_date = epoch_to_date(tx_ts)
        trade_day_epoch = date_to_epoch(trade_date)

        # SPY return < -1.5% on trade date
        spy_r = spy_ret.get(trade_day_epoch)
        if spy_r is None or spy_r >= -0.015:
            continue

        # Symbol return > 0% on trade date
        sym_r = symbol_data[sid]['ret'].get(trade_day_epoch)
        if sym_r is None or sym_r <= 0:
            continue

        # No open-market purchases in prior 21 trading sessions
        if not no_recent_purchases(sid, tx_ts, 21):
            continue

        # Disclosure lag <= 5 business days
        lag = disclosure_lag_days(tx_ts, filed_ts)
        if lag > 5:
            continue

        # Avg daily dollar volume > $5M at trade date
        avg_dv = get_avg_dollar_vol(sid, tx_ts, 60)
        if avg_dv <= 5_000_000:
            continue

        # Market cap > $1B at trade date
        mcap = get_market_cap(sid, tx_ts)
        if mcap <= 1_000_000_000:
            continue

        # All conditions met - this is a call issued at disclosure date
        fwd_ret = get_forward_return(sid, filed_ts, 21)
        if fwd_ret is None:
            continue
        up = 1 if fwd_ret > 0 else 0
        calls.append((filed_ts, sid, up, fwd_ret, trade_ts))

    if not calls:
        print("INSUFFICIENT=1")
        return

    # 15. Sort calls by disclosure date (filed_ts)
    calls.sort(key=lambda x: x[0])

    # 16. Hold out most recent 20% as sealed era
    n_calls = len(calls)
    split_idx = int(n_calls * 0.8)
    in_sample = calls[:split_idx]
    sealed = calls[split_idx:]

    # 17. Compute metrics for in-sample
    def compute_metrics(call_list):
        if not call_list:
            return None
        issued = len(call_list)
        hits = sum(c[2] for c in call_list)
        precision = hits / issued if issued > 0 else 0
        base_rate = hits / issued if issued > 0 else 0  # base rate of predicted class (up) within issued subset
        # Distinct UTC days on which a call was issued
        distinct_days = len(set(epoch_to_date(c[0]) for c in call_list))
        # Design effect: cluster by day, compute variance inflation
        # Simple approach: group by day, compute design effect = 1 + (avg_cluster_size - 1) * ICC
        # But we can approximate: design_effect = issued / effective_n
        # For clustered data, effective_n = issued / design_effect
        # We'll compute design effect using Kish's formula: deff = 1 + (n_avg - 1) * rho
        # Simpler: use the ratio of distinct days to issued as a lower bound on independence
        # Actually, the requirement says EFFECTIVE_N must be strictly less than ISSUED
        # and design effect is always > 1. We'll compute a simple design effect.
        day_counts = defaultdict(int)
        for c in call_list:
            day_counts[epoch_to_date(c[0])] += 1
        cluster_sizes = list(day_counts.values())
        n_clusters = len(cluster_sizes)
        if n_clusters == 0:
            effective_n = 0
        else:
            avg_cluster = issued / n_clusters
            # Intraclass correlation - estimate from binary outcomes
            # For simplicity, use a conservative design effect
            # deff = 1 + (avg_cluster - 1) * 0.05 (conservative ICC)
            # But we need to measure it. Let's compute actual ICC from data.
            # ICC = (between_cluster_var - within_cluster_var) / (between_cluster_var + (n_avg-1)*within_cluster_var)
            # For binary data, use ANOVA estimator
            overall_mean = hits / issued
            between_var = sum(sz * ((sum(c[2] for c in call_list if epoch_to_date(c[0]) == d) / sz) - overall_mean)**2
                              for d, sz in day_counts.items()) / (n_clusters - 1) if n_clusters > 1 else 0
            within_var = overall_mean * (1 - overall_mean)
            if within_var > 0 and n_clusters > 1:
                icc = max(0, (between_var - within_var) / (between_var + (avg_cluster - 1) * within_var))
            else:
                icc = 0
            deff = 1 + (avg_cluster - 1) * icc
            if deff < 1:
                deff = 1
            effective_n = issued / deff
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n
        }

    in_metrics = compute_metrics(in_sample)
    sealed_metrics = compute_metrics(sealed)

    if not in_metrics or in_metrics['issued'] == 0:
        print("INSUFFICIENT=1")
        return

    # 18. OPPORTUNITIES = count of decision points considered
    # Decision points = each insider purchase that passed universe filters (before trade date conditions)
    # Actually "opportunities considered" = each insider purchase event evaluated
    opportunities = len(insider_rows)

    # 19. Print required lines
    print(f"ISSUED={in_metrics['issued']}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={in_metrics['precision']:.6f}")
    print(f"BASE_RATE={in_metrics['base_rate']:.6f}")
    print(f"DISTINCT_DAYS={in_metrics['distinct_days']}")
    print(f"EFFECTIVE_N={in_metrics['effective_n']:.6f}")
    if sealed_metrics:
        print(f"SEALED_PRECISION={sealed_metrics['precision']:.6f}")
    else:
        print("SEALED_PRECISION=0.000000")

if __name__ == '__main__':
    main()