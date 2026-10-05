import datetime
import sys
import pathlib
import sqlite3
import json
import csv
import tempfile
import os

# Insert script's directory at front of sys.path
script_dir = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(script_dir))
import auditlib

# Instrument classifier setup
def setup_instrument_classifier(repo):
    try:
        harness_path = repo / 'research' / 'harness'
        sys.path.insert(0, str(harness_path))
        from instruments import instrument_class
        return instrument_class, None
    except Exception as e:
        return None, str(e)

def get_instrument_class(symbol, name, market, instrument_class_func, import_error):
    if market == 'crypto':
        return 'crypto'
    if instrument_class_func is not None:
        ic = instrument_class_func(symbol, name)
        if ic is not None:
            return ic
        return 'common'
    return 'unknown'

def get_ledger(conn):
    # Bar stats for 1d
    bar_stats = {}
    cur = conn.execute("""
        SELECT symbol_id, COUNT(*), MIN(ts), MAX(ts), 
               SUM(CASE WHEN high = low THEN 1 ELSE 0 END) 
        FROM bars WHERE tf='1d' GROUP BY symbol_id
    """)
    for row in cur:
        sid, n, min_ts, max_ts, flat = row
        bar_stats[sid] = {
            'n_bars_1d': n,
            'first_bar_utc': auditlib.utc_date(min_ts) if min_ts is not None else '',
            'last_bar_utc': auditlib.utc_date(max_ts) if max_ts is not None else '',
            'flat_share': round(flat / n, 6) if n > 0 else 0.0
        }

    # Membership stats
    memb_stats = {}
    cur = conn.execute("""
        SELECT symbol_id, COUNT(*), MIN(day), MAX(day) 
        FROM universe_membership GROUP BY symbol_id
    """)
    for row in cur:
        sid, count, min_day, max_day = row
        memb_stats[sid] = {
            'n_membership_days': count,
            'membership_first_utc': auditlib.utc_date(min_day) if min_day is not None else '',
            'membership_last_utc': auditlib.utc_date(max_day) if max_day is not None else ''
        }

    # Fundamentals existence
    cur = conn.execute("SELECT DISTINCT symbol_id FROM fundamentals")
    has_fundamentals_set = {row[0] for row in cur}

    # Companies existence
    cur = conn.execute("SELECT ticker FROM companies")
    in_companies_set = {row[0] for row in cur}

    # Get all symbols
    ledger = []
    cur = conn.execute("""
        SELECT id, symbol, market, name, active, added_at, delisted_at 
        FROM symbols
    """)
    for row in cur:
        sid, symbol, market, name, active, added_at, delisted_at = row
        bar = bar_stats.get(sid, {
            'n_bars_1d': 0,
            'first_bar_utc': '',
            'last_bar_utc': '',
            'flat_share': 0.0
        })
        memb = memb_stats.get(sid, {
            'n_membership_days': 0,
            'membership_first_utc': '',
            'membership_last_utc': ''
        })
        has_fund = 1 if sid in has_fundamentals_set else 0
        in_comp = 1 if symbol in in_companies_set else 0
        har_pass = 1 if (bar['n_bars_1d'] >= auditlib.MIN_BARS and bar['flat_share'] < auditlib.MAX_FLAT_SHARE) else 0
        har_comp_only_pass = 1 if (har_pass and has_fund) else 0
        ledger.append({
            'symbol_id': sid,
            'symbol': symbol,
            'market': market,
            'name': name,
            'instrument_class': '',  # Will be filled later
            'active': active,
            'added_at_utc': auditlib.utc_date(added_at),
            'delisted_at_utc': auditlib.utc_date(delisted_at),
            'first_bar_utc': bar['first_bar_utc'],
            'last_bar_utc': bar['last_bar_utc'],
            'n_bars_1d': bar['n_bars_1d'],
            'flat_share': bar['flat_share'],
            'n_membership_days': memb['n_membership_days'],
            'membership_first_utc': memb['membership_first_utc'],
            'membership_last_utc': memb['membership_last_utc'],
            'has_fundamentals': has_fund,
            'in_companies': in_comp,
            'har_screen_pass': har_pass,
            'har_companies_only_pass': har_comp_only_pass
        })
    return ledger

def get_membership_gap(conn):
    # Max day in universe_membership
    cur = conn.execute("SELECT MAX(day) FROM universe_membership")
    max_memb_day = cur.fetchone()[0]
    if max_memb_day is None:
        max_memb_day = -1
    max_memb_day_utc = auditlib.utc_date(max_memb_day) if max_memb_day >= 0 else ''

    # Max bar day for 1d
    cur = conn.execute("""
        SELECT MAX(ts/86400)*86400 FROM bars WHERE tf='1d'
    """)
    max_bar_day_ts = cur.fetchone()[0]
    max_bar_day_utc = auditlib.utc_date(max_bar_day_ts) if max_bar_day_ts is not None else ''

    # Days with bars but no membership (bar day > max_memb_day)
    cur = conn.execute("""
        SELECT DISTINCT (ts/86400)*86400 AS bar_day 
        FROM bars WHERE tf='1d' AND (ts/86400)*86400 > ?
    """, (max_memb_day,))
    uncovered_days_ts = [row[0] for row in cur]
    uncovered_days_utc = sorted([auditlib.utc_date(ts) for ts in uncovered_days_ts])
    n_uncovered_days = len(uncovered_days_utc)

    # Uncovered symbol days: count distinct (symbol_id, bar_day) with bar_day > max_memb_day
    cur = conn.execute("""
        SELECT COUNT(*) FROM (SELECT DISTINCT symbol_id, (ts/86400)*86400 AS d
        FROM bars WHERE tf='1d' AND (ts/86400)*86400 > ?)
    """, (max_memb_day,))
    n_uncovered_symbol_days = cur.fetchone()[0]

    # Membership sources
    cur = conn.execute("""
        SELECT source, COUNT(*) FROM universe_membership GROUP BY source
    """)
    membership_sources = {row[0]: row[1] for row in cur}

    # Rebuild worker runs (up to 10)
    cur = conn.execute("""
        SELECT worker, started_at, status, detail 
        FROM worker_runs 
        WHERE worker LIKE '%univ%' 
        ORDER BY started_at DESC 
        LIMIT 10
    """)
    rebuild_worker_runs = []
    for row in cur:
        worker, started_at, status, detail = row
        rebuild_worker_runs.append({
            'worker': worker,
            'started_at_utc': auditlib.utc_date(started_at),
            'status': status,
            'detail': (detail[:200] + '...') if len(detail) > 200 else detail
        })

    return {
        'membership_max_day_utc': max_memb_day_utc,
        'bars_1d_max_day_utc': max_bar_day_utc,
        'days_with_bars_no_membership': uncovered_days_utc,
        'n_uncovered_days': n_uncovered_days,
        'n_uncovered_symbol_days': n_uncovered_symbol_days,
        'membership_sources': membership_sources,
        'rebuild_worker_runs': rebuild_worker_runs
    }

def get_har_screen_bias(ledger):
    stock_ledger = [row for row in ledger if row['market'] == 'stocks']
    n_stock_symbols = len(stock_ledger)
    n_delisted = sum(1 for row in stock_ledger if row['delisted_at_utc'] != '' or row['active'] == 0)
    n_pass_screen = sum(1 for row in stock_ledger if row['har_screen_pass'] == 1)
    n_pass_screen_delisted = sum(1 for row in stock_ledger if row['har_screen_pass'] == 1 and (row['delisted_at_utc'] != '' or row['active'] == 0))
    share_pass_screen_delisted = round(n_pass_screen_delisted / n_pass_screen, 4) if n_pass_screen > 0 else 0.0
    n_pass_companies_only = sum(1 for row in stock_ledger if row['har_companies_only_pass'] == 1)
    n_pass_companies_only_delisted = sum(1 for row in stock_ledger if row['har_companies_only_pass'] == 1 and (row['delisted_at_utc'] != '' or row['active'] == 0))
    share_pass_companies_only_delisted = round(n_pass_companies_only_delisted / n_pass_companies_only, 4) if n_pass_companies_only > 0 else 0.0
    delisted_removed_by_companies_only = n_pass_screen_delisted - n_pass_companies_only_delisted
    survivors_removed_by_companies_only = sum(1 for row in stock_ledger if row['har_screen_pass'] == 1 and row['has_fundamentals'] == 0 and row['delisted_at_utc'] == '' and row['active'] == 1)
    by_instrument_class = {}
    for row in stock_ledger:
        ic = row['instrument_class']
        if ic not in by_instrument_class:
            by_instrument_class[ic] = {'n_pass_screen': 0, 'n_pass_companies_only': 0}
        if row['har_screen_pass'] == 1:
            by_instrument_class[ic]['n_pass_screen'] += 1
        if row['har_companies_only_pass'] == 1:
            by_instrument_class[ic]['n_pass_companies_only'] += 1
    n_with_fundamentals = sum(1 for row in stock_ledger if row['has_fundamentals'] == 1)
    n_with_fundamentals_delisted = sum(1 for row in stock_ledger if row['has_fundamentals'] == 1 and (row['delisted_at_utc'] != '' or row['active'] == 0))
    return {
        'n_stock_symbols': n_stock_symbols,
        'n_delisted': n_delisted,
        'n_pass_screen': n_pass_screen,
        'n_pass_screen_delisted': n_pass_screen_delisted,
        'share_pass_screen_delisted': share_pass_screen_delisted,
        'n_pass_companies_only': n_pass_companies_only,
        'n_pass_companies_only_delisted': n_pass_companies_only_delisted,
        'share_pass_companies_only_delisted': share_pass_companies_only_delisted,
        'delisted_removed_by_companies_only': delisted_removed_by_companies_only,
        'survivors_removed_by_companies_only': survivors_removed_by_companies_only,
        'by_instrument_class': by_instrument_class,
        'n_with_fundamentals': n_with_fundamentals,
        'n_with_fundamentals_delisted': n_with_fundamentals_delisted,
        'instruments_import_error': None  # Will be set in run
    }

def get_history_depth(conn):
    # Get distinct markets
    cur = conn.execute("SELECT DISTINCT market FROM symbols")
    markets = [row[0] for row in cur]
    result = {}
    cutoff_ts = int(datetime.datetime(2023, 7, 3, tzinfo=datetime.timezone.utc).timestamp())
    for market in markets:
        # n_symbols_with_bars and bar counts
        cur = conn.execute("""
            SELECT s.id, COUNT(b.ts) 
            FROM symbols s 
            LEFT JOIN bars b ON s.id = b.symbol_id AND b.tf='1d' 
            WHERE s.market = ? 
            GROUP BY s.id
        """, (market,))
        counts = [row[1] for row in cur]
        n_symbols_with_bars = sum(1 for c in counts if c > 0)
        if n_symbols_with_bars == 0:
            result[market] = {
                'n_symbols_with_bars': 0,
                'n_bars_quantiles': {'p10': 0, 'p25': 0, 'p50': 0, 'p75': 0, 'p90': 0},
                'n_ge_500': 0,
                'n_ge_530': 0,
                'n_ge_756': 0,
                'n_ge_756_before_2023_07_03': 0,
                'method': 'nearest-rank'
            }
            continue
        sorted_counts = sorted(counts)
        def quantile(q):
            idx = int(q * (len(sorted_counts) - 1))
            return sorted_counts[idx]
        n_ge_500 = sum(1 for c in counts if c >= 500)
        n_ge_530 = sum(1 for c in counts if c >= 530)
        n_ge_756 = sum(1 for c in counts if c >= 756)
        # n_ge_756_before_2023_07_03
        cur = conn.execute("""
            SELECT COUNT(*) FROM (SELECT s.id FROM symbols s
            JOIN bars b ON s.id = b.symbol_id
            WHERE s.market = ? AND b.tf='1d' AND b.ts < ?
            GROUP BY s.id HAVING COUNT(b.ts) >= 756)
        """, (market, cutoff_ts))
        n_ge_756_before_2023_07_03 = cur.fetchone()[0]
        result[market] = {
            'n_symbols_with_bars': n_symbols_with_bars,
            'n_bars_quantiles': {
                'p10': quantile(0.1),
                'p25': quantile(0.25),
                'p50': quantile(0.5),
                'p75': quantile(0.75),
                'p90': quantile(0.9)
            },
            'n_ge_500': n_ge_500,
            'n_ge_530': n_ge_530,
            'n_ge_756': n_ge_756,
            'n_ge_756_before_2023_07_03': n_ge_756_before_2023_07_03,
            'method': 'nearest-rank'
        }
    return result

def write_eligibility_ledger(ledger, out_dir):
    path = out_dir / 'eligibility_ledger.csv'
    columns = [
        'symbol_id', 'symbol', 'market', 'name', 'instrument_class', 'active',
        'added_at_utc', 'delisted_at_utc', 'first_bar_utc', 'last_bar_utc',
        'n_bars_1d', 'flat_share', 'n_membership_days', 'membership_first_utc',
        'membership_last_utc', 'has_fundamentals', 'in_companies',
        'har_screen_pass', 'har_companies_only_pass'
    ]
    auditlib.write_csv(path, ledger, columns)

def write_membership_gap(data, out_dir):
    path = out_dir / 'membership_gap.json'
    auditlib.write_json(path, data)

def write_har_screen_bias(data, out_dir):
    path = out_dir / 'har_screen_bias.json'
    auditlib.write_json(path, data)

def write_history_depth(data, out_dir):
    path = out_dir / 'history_depth.json'
    auditlib.write_json(path, data)

def run(conn, out_dir, repo):
    # Setup instrument classifier
    global INSTRUMENTS_IMPORT_ERROR
    instrument_class_func, INSTRUMENTS_IMPORT_ERROR = setup_instrument_classifier(repo)
    # Get ledger and fill instrument_class
    ledger = get_ledger(conn)
    for row in ledger:
        row['instrument_class'] = get_instrument_class(
            row['symbol'], row['name'], row['market'],
            instrument_class_func, INSTRUMENTS_IMPORT_ERROR
        )
    # Write outputs
    write_eligibility_ledger(ledger, out_dir)
    membership_gap = get_membership_gap(conn)
    write_membership_gap(membership_gap, out_dir)
    har_screen_bias = get_har_screen_bias(ledger)
    har_screen_bias['instruments_import_error'] = INSTRUMENTS_IMPORT_ERROR
    write_har_screen_bias(har_screen_bias, out_dir)
    history_depth = get_history_depth(conn)
    write_history_depth(history_depth, out_dir)
    # Print final message
    n_ledger_rows = len(ledger)
    n_uncovered_days = membership_gap['n_uncovered_days']
    print(f"LEDGER OK symbols={n_ledger_rows} uncovered_days={n_uncovered_days}")

def main():
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', type=pathlib.Path)
    parser.add_argument('--out', type=pathlib.Path)
    parser.add_argument('--repo', type=pathlib.Path)
    parser.add_argument('--selfcheck', action='store_true')
    args = parser.parse_args()

    if args.selfcheck:
        # Selfcheck mode
        with tempfile.TemporaryDirectory() as tmpdir:
            tmpdir = pathlib.Path(tmpdir)
            repo = auditlib.repo_root(__file__)  # For selfcheck, use script's repo root
            conn = auditlib.fixture_db()
            try:
                run(conn, tmpdir, repo)
                # Assertions
                ledger_path = tmpdir / 'eligibility_ledger.csv'
                assert ledger_path.exists()
                import csv
                with open(ledger_path) as f:
                    reader = csv.DictReader(f)
                    ledger = list(reader)
                # Find symbols
                by_symbol = {row['symbol']: row for row in ledger}
                assert by_symbol['AAA']['har_screen_pass'] == '1'
                assert by_symbol['AAA']['har_companies_only_pass'] == '1'
                assert by_symbol['BBB']['har_screen_pass'] == '0'
                assert float(by_symbol['BBB']['flat_share']) == 0.05
                assert by_symbol['CCC']['har_screen_pass'] == '0'
                assert int(by_symbol['CCC']['n_bars_1d']) == 100
                assert by_symbol['DDD']['har_screen_pass'] == '1'
                assert by_symbol['DDD']['has_fundamentals'] == '0'
                assert by_symbol['DDD']['har_companies_only_pass'] == '0'
                # Count delisted screen passers
                n_pass_screen_delisted = sum(1 for row in ledger if row['har_screen_pass'] == '1' and (row['delisted_at_utc'] != '' or row['active'] == '0'))
                assert n_pass_screen_delisted == 1
                # Count delisted companies-only passers
                n_pass_companies_only_delisted = sum(1 for row in ledger if row['har_companies_only_pass'] == '1' and (row['delisted_at_utc'] != '' or row['active'] == '0'))
                assert n_pass_companies_only_delisted == 0
                assert (n_pass_screen_delisted - n_pass_companies_only_delisted) == 1
                # Survivors removed by companies only: non-delisted screen passers without fundamentals
                survivors_removed = sum(1 for row in ledger if row['har_screen_pass'] == '1' and row['has_fundamentals'] == '0' and row['delisted_at_utc'] == '' and row['active'] == '1')
                assert survivors_removed == 0  # AAA and EEEW have fundamentals; DDD is delisted
                # Actually, from fixture: AAA has fundamentals, BBB no, CCC no, DDD delisted, EEEW has fundamentals, BTC/USD crypto.
                # Screen passers: AAA (600 bars, flat 0), DDD (600 bars, flat 0) -> 2 passers.
                # AAA: has fundamentals -> not removed by companies only.
                # DDD: delisted -> not in survivors.
                # So survivors_removed should be 0? Wait, check the fixture again.
                # Let's recompute: 
                #   AAA: har_screen_pass=1, has_fundamentals=1 -> not removed
                #   BBB: har_screen_pass=0
                #   CCC: har_screen_pass=0
                #   DDD: har_screen_pass=1, has_fundamentals=0, but delisted -> not survivor
                #   EEEW: har_screen_pass=1, has_fundamentals=1 -> not removed
                #   BTC/USD: market=crypto -> not in stocks ledger
                # So survivors_removed_by_companies_only should be 0.
                # But the assertion in the problem says: 
                #   'DDD' har_screen_pass == 1, has_fundamentals == 0, har_companies_only_pass == 0 
                #   and it is counted in n_pass_screen_delisted == 1 and delisted_removed_by_companies_only == 1
                # So delisted_removed_by_companies_only = n_pass_screen_delisted - n_pass_companies_only_delisted = 1 - 0 = 1.
                # And survivors_removed_by_companies_only is for non-delisted screen passers without fundamentals -> 0.
                # We'll adjust the assertion below to match the problem's expectation for the fixture.
                # The problem says: 
                #   'EEEEW' instrument_class == 'suffix-W' when instruments imported (else 'unknown')
                #   'BTC/USD' instrument_class == 'crypto'
                #   'AAA' in_companies == 1 and 'BBB' in_companies == 0
                assert by_symbol['EEEEW']['instrument_class'] in ('suffix-W', 'unknown')
                assert by_symbol['BTC/USD']['instrument_class'] == 'crypto'
                assert by_symbol['AAA']['in_companies'] == '1'
                assert by_symbol['BBB']['in_companies'] == '0'
                # Membership gap
                gap_path = tmpdir / 'membership_gap.json'
                assert gap_path.exists()
                import json
                with open(gap_path) as f:
                    gap = json.load(f)
                assert gap['n_uncovered_days'] == auditlib.FIXTURE_N_UNCOVERED_DAYS
                assert len(gap['rebuild_worker_runs']) == 1
                assert gap['rebuild_worker_runs'][0]['worker'] == 'universe-rebuild'
                # History depth
                depth_path = tmpdir / 'history_depth.json'
                assert depth_path.exists()
                with open(depth_path) as f:
                    depth = json.load(f)
                assert depth['stocks']['n_ge_530'] == 4
                assert depth['crypto']['n_symbols_with_bars'] == 1
                # Check all output files exist
                assert (tmpdir / 'eligibility_ledger.csv').exists()
                assert (tmpdir / 'har_screen_bias.json').exists()
                print("SELFCHECK OK")
            finally:
                conn.close()
    else:
        # Normal run
        repo = args.repo if args.repo is not None else auditlib.repo_root(__file__)
        db_path = args.db if args.db is not None else (repo / 'data' / 'signaldeck.db')
        out_dir = args.out if args.out is not None else (repo / 'research' / 'forecastplan' / 'out')
        out_dir.mkdir(parents=True, exist_ok=True)
        conn = auditlib.open_ro(db_path)
        try:
            run(conn, out_dir, repo)
        finally:
            conn.close()

if __name__ == '__main__':
    main()