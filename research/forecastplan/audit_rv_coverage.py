import sqlite3, argparse, os, csv, json, statistics, datetime

def aggregate(con, horizon, min_symbols):
    cur = con.cursor()
    cur.execute("""
        SELECT
            date(ts, 'unixepoch') AS day,
            COUNT(*) AS n_rows,
            COUNT(DISTINCT symbol_id) AS n_symbols,
            SUM(CASE WHEN s.market = 'stocks' THEN 1 ELSE 0 END) AS n_stocks,
            SUM(CASE WHEN s.market = 'crypto' THEN 1 ELSE 0 END) AS n_crypto,
            SUM(CASE WHEN actual IS NOT NULL THEN 1 ELSE 0 END) AS n_resolved,
            SUM(CASE WHEN ungradable IS NOT NULL THEN 1 ELSE 0 END) AS n_ungradable,
            SUM(CASE WHEN actual IS NULL AND ungradable IS NULL THEN 1 ELSE 0 END) AS n_pending,
            MIN(created_ts) AS min_created,
            MAX(created_ts) AS max_created,
            GROUP_CONCAT(DISTINCT revision ORDER BY revision) AS revisions
        FROM rv_forecasts f
        JOIN symbols s ON f.symbol_id = s.id
        WHERE f.horizon = ?
        GROUP BY day
        ORDER BY day
    """, (horizon,))
    rows_raw = cur.fetchall()
    rows = []
    for r in rows_raw:
        day, n_rows, n_symbols, n_stocks, n_crypto, n_resolved, n_ungradable, n_pending, min_created, max_created, revisions = r
        created_first_utc = datetime.datetime.fromtimestamp(min_created, tz=datetime.timezone.utc).isoformat() if min_created is not None else ''
        created_last_utc = datetime.datetime.fromtimestamp(max_created, tz=datetime.timezone.utc).isoformat() if max_created is not None else ''
        revisions_str = revisions if revisions is not None else ''
        qualifies = 1 if n_symbols >= min_symbols else 0
        rows.append({
            'day': day,
            'n_rows': n_rows,
            'n_symbols': n_symbols,
            'n_stocks': n_stocks,
            'n_crypto': n_crypto,
            'n_resolved': n_resolved,
            'n_ungradable': n_ungradable,
            'n_pending': n_pending,
            'created_first_utc': created_first_utc,
            'created_last_utc': created_last_utc,
            'revisions': revisions_str,
            'qualifies': qualifies
        })
    days_total = len(rows)
    days_qualifying = sum(1 for r in rows if r['qualifies'])
    days_qualifying_resolved = sum(1 for r in rows if r['qualifies'] and r['n_resolved'] >= min_symbols)
    first_day = rows[0]['day'] if rows else None
    last_day = rows[-1]['day'] if rows else None
    max_symbols_per_day = max((r['n_symbols'] for r in rows), default=0)
    median_symbols_per_day = statistics.median([r['n_symbols'] for r in rows]) if rows else 0
    rows_total = sum(r['n_rows'] for r in rows)
    rows_resolved = sum(r['n_resolved'] for r in rows)
    rows_ungradable = sum(r['n_ungradable'] for r in rows)
    cur.execute("""
        SELECT ungradable, COUNT(*) AS cnt
        FROM rv_forecasts
        WHERE horizon = ? AND ungradable IS NOT NULL
        GROUP BY ungradable
        ORDER BY cnt DESC
        LIMIT 10
    """, (horizon,))
    ungradable_reasons = [[reason, cnt] for reason, cnt in cur.fetchall()]
    cur.execute("""
        SELECT s.market, COUNT(DISTINCT f.symbol_id)
        FROM rv_forecasts f
        JOIN symbols s ON f.symbol_id = s.id
        WHERE f.horizon = ?
        GROUP BY s.market
    """, (horizon,))
    symbols_by_market = {market: count for market, count in cur.fetchall()}
    generated_at_utc = datetime.datetime.now(datetime.timezone.utc).isoformat()
    summary = {
        'days_total': days_total,
        'days_qualifying': days_qualifying,
        'days_qualifying_resolved': days_qualifying_resolved,
        'first_day': first_day,
        'last_day': last_day,
        'max_symbols_per_day': max_symbols_per_day,
        'median_symbols_per_day': median_symbols_per_day,
        'rows_total': rows_total,
        'rows_resolved': rows_resolved,
        'rows_ungradable': rows_ungradable,
        'ungradable_reasons': ungradable_reasons,
        'symbols_by_market': symbols_by_market,
        'horizon': horizon,
        'min_symbols': min_symbols,
        'generated_at_utc': generated_at_utc
    }
    return rows, summary

def write_outputs(rows, summary, out_dir):
    os.makedirs(out_dir, exist_ok=True)
    csv_path = os.path.join(out_dir, 'rv_coverage_by_day.csv')
    fieldnames = ['day','n_rows','n_symbols','n_stocks','n_crypto','n_resolved','n_ungradable','n_pending','created_first_utc','created_last_utc','revisions','qualifies']
    with open(csv_path, 'w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)
    json_path = os.path.join(out_dir, 'rv_coverage_summary.json')
    with open(json_path, 'w') as f:
        json.dump(summary, f, indent=1, sort_keys=True)

def selfcheck():
    con = sqlite3.connect(':memory:')
    cur = con.cursor()
    cur.execute('''CREATE TABLE symbols (
        id INTEGER PRIMARY KEY,
        symbol TEXT,
        market TEXT,
        active INT,
        delisted_at INT NULL
    )''')
    cur.execute('''CREATE TABLE rv_forecasts (
        symbol_id INT,
        ts INT,
        horizon INT,
        rv_hat REAL,
        null_rw REAL,
        null_ewma REAL,
        beta0 REAL,
        beta_d REAL,
        beta_w REAL,
        beta_m REAL,
        resid_var REAL,
        n_train INT,
        revision TEXT,
        created_ts INT,
        actual REAL NULL,
        resolved_ts INT NULL,
        ungradable TEXT NULL
    )''')
    symbols_data = []
    for i in range(1, 41):
        symbols_data.append((i, f'SYM{i}', 'stocks', 1, None))
    symbols_data.append((41, 'CRYPTO1', 'crypto', 1, None))
    cur.executemany('INSERT INTO symbols VALUES (?,?,?,?,?)', symbols_data)
    base_ts = 1000000
    offset = 3600
    for sid in range(1, 36):
        cur.execute('''INSERT INTO rv_forecasts
            (symbol_id, ts, horizon, rv_hat, null_rw, null_ewma, beta0, beta_d, beta_w, beta_m, resid_var, n_train, revision, created_ts, actual, resolved_ts, ungradable)
            VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
            (sid, base_ts, 1, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 600, 'r1', base_ts+offset, 1.0, None, None))
    for sid in range(1, 6):
        ungradable = 'no bars' if sid <= 2 else None
        cur.execute('''INSERT INTO rv_forecasts
            (symbol_id, ts, horizon, rv_hat, null_rw, null_ewma, beta0, beta_d, beta_w, beta_m, resid_var, n_train, revision, created_ts, actual, resolved_ts, ungradable)
            VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
            (sid, base_ts+86400, 1, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 600, 'r1', base_ts+86400+offset, None, None, ungradable))
    for sid in range(1, 32):
        cur.execute('''INSERT INTO rv_forecasts
            (symbol_id, ts, horizon, rv_hat, null_rw, null_ewma, beta0, beta_d, beta_w, beta_m, resid_var, n_train, revision, created_ts, actual, resolved_ts, ungradable)
            VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
            (sid, base_ts+2*86400, 1, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 1.0, 600, 'r1', base_ts+2*86400+offset, None, None, None))
    con.commit()
    rows, summary = aggregate(con, 1, 30)
    assert len(rows) == 3
    assert summary['days_total'] == 3
    assert summary['days_qualifying'] == 2
    assert summary['days_qualifying_resolved'] == 1
    assert summary['rows_ungradable'] == 2
    assert summary['ungradable_reasons'] == [['no bars', 2]]
    assert summary['symbols_by_market'] == {'stocks': 35}
    import tempfile, shutil
    tmp = tempfile.mkdtemp()
    write_outputs(rows, summary, tmp)
    assert os.path.exists(os.path.join(tmp, 'rv_coverage_by_day.csv'))
    assert os.path.exists(os.path.join(tmp, 'rv_coverage_summary.json'))
    con.close()
    shutil.rmtree(tmp)
    print('SELFCHECK OK')

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', default='data/signaldeck.db')
    parser.add_argument('--out-dir', default='research/forecastplan/out')
    parser.add_argument('--horizon', type=int, default=1)
    parser.add_argument('--min-symbols', type=int, default=30)
    parser.add_argument('--selfcheck', action='store_true')
    args = parser.parse_args()
    if args.selfcheck:
        selfcheck()
        return
    con = sqlite3.connect('file:'+args.db+'?mode=ro', uri=True)
    rows, summary = aggregate(con, args.horizon, args.min_symbols)
    summary['db_path'] = args.db
    write_outputs(rows, summary, args.out_dir)
    con.close()
    print(f'RV COVERAGE OK days={summary["days_total"]} qualifying={summary["days_qualifying"]} max_symbols={summary["max_symbols_per_day"]}')

if __name__ == '__main__':
    main()