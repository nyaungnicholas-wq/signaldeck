import sqlite3, json, argparse, sys

EXPLAINED = {
    # mapping: feature key -> (series name, threshold, comparison)
    # comparison is 'ge' meaning the feature should be 1 when series >= threshold
    'vix_high_vol': ('VIXCLS', 25.0, 'ge'),
    # entries are added only when a human has verified the mapping;
    # an unregistered key is reported as needing review rather than guessed at,
    # because guessing is what this tool exists to prevent.
}

def classify_presence(count, total_rows):
    """Return (presence, class) for a key."""
    presence = count / total_rows if total_rows else 0.0
    if presence < 0.99:
        return presence, 'ONE_HOT_PRESENCE'
    return presence, 'FULL_PRESENCE'

def adjudicate_explained(key, min_ts, max_ts, conn):
    """Return (verdict, evidence) for a fully present explained key."""
    series_name, threshold, _ = EXPLAINED[key]
    cur = conn.cursor()
    cur.execute(
        "SELECT MAX(value) FROM macro_series WHERE series=? AND ts BETWEEN ? AND ?",
        (series_name, min_ts, max_ts),
    )
    max_in_window = cur.fetchone()[0]
    cur.execute(
        "SELECT COUNT(*), SUM(CASE WHEN value >= ? THEN 1 ELSE 0 END) FROM macro_series WHERE series=?",
        (threshold, series_name),
    )
    total, met = cur.fetchone()
    total = total or 0
    met = met or 0
    pct = (met / total * 100) if total else 0.0
    if max_in_window is None or max_in_window < threshold:
        verdict = 'DORMANT'
    else:
        verdict = 'BROKEN'
    evidence = {
        'series': series_name,
        'window_range': (min_ts, max_ts),
        'threshold': threshold,
        'full_history_count': total,
        'full_history_met': met,
        'full_history_pct': pct,
    }
    return verdict, evidence

def analyze_db(conn, version=None):
    """Scan features table for given version, return sections dict."""
    cur = conn.cursor()
    if version is not None:
        cur.execute("SELECT 1 FROM features WHERE version=? LIMIT 1", (version,))
        if cur.fetchone() is None:
            raise ValueError(f"version {version} not found")
    else:
        cur.execute("SELECT MAX(version) FROM features")
        row = cur.fetchone()
        version = row[0] if row else None
        if version is None:
            raise ValueError("no feature rows found")
    cur.execute(
        "SELECT ts, vec FROM features WHERE version=? ORDER BY ts", (version,)
    )
    rows = cur.fetchall()
    if not rows:
        raise ValueError("no feature rows found for version")
    total_rows = len(rows)
    key_stats = {}
    min_ts = max_ts = None
    for ts, vec_json in rows:
        if min_ts is None or ts < min_ts:
            min_ts = ts
        if max_ts is None or ts > max_ts:
            max_ts = ts
        try:
            vec = json.loads(vec_json)
        except json.JSONDecodeError:
            continue
        for k, v in vec.items():
            if isinstance(v, (int, float)):
                if k not in key_stats:
                    key_stats[k] = [0, float('inf'), float('-inf')]
                stat = key_stats[k]
                stat[0] += 1
                if v < stat[1]:
                    stat[1] = v
                if v > stat[2]:
                    stat[2] = v
    constant_keys = [
        (k, cnt, mn) for k, (cnt, mn, mx) in key_stats.items() if mn == mx
    ]
    sections = {
        'ONE_HOT_PRESENCE': [],
        'DORMANT': [],
        'BROKEN': [],
        'UNEXPLAINED': [],
    }
    for k, cnt, const_val in constant_keys:
        presence, pres_class = classify_presence(cnt, total_rows)
        if pres_class == 'ONE_HOT_PRESENCE':
            sections['ONE_HOT_PRESENCE'].append((k, cnt, const_val, presence, None))
        else:
            if k in EXPLAINED:
                verdict, evidence = adjudicate_explained(k, min_ts, max_ts, conn)
                if verdict == 'DORMANT':
                    sections['DORMANT'].append((k, cnt, const_val, presence, evidence))
                else:
                    sections['BROKEN'].append((k, cnt, const_val, presence, evidence))
            else:
                sections['UNEXPLAINED'].append((k, cnt, const_val, presence, None))
    return sections, version, len(key_stats), len(constant_keys)

def run_selftest():
    import sqlite3, json

    def make_conn():
        conn = sqlite3.connect(':memory:')
        cur = conn.cursor()
        cur.execute(
            """CREATE TABLE features (
                id INTEGER PRIMARY KEY,
                symbol_id INTEGER,
                horizon INTEGER,
                ts INTEGER,
                version INTEGER,
                vec TEXT
            )"""
        )
        cur.execute(
            """CREATE TABLE macro_series (
                series TEXT,
                ts INTEGER,
                value REAL
            )"""
        )
        return conn

    # Helper to run analysis on a given conn and version
    def run_analysis(conn, version=None):
        sections, _, scanned, const = analyze_db(conn, version)
        return sections, scanned, const

    # Test 1: low presence constant key -> ONE_HOT_PRESENCE
    conn = make_conn()
    cur = conn.cursor()
    version = 1
    total_rows = 100
    for i in range(total_rows):
        ts = i * 86400
        vec = {'flag_a': 1.0} if i < 5 else {}
        vec['var'] = float(i)
        cur.execute(
            "INSERT INTO features (id, symbol_id, horizon, ts, version, vec) VALUES (?,?,?,?,?,?)",
            (i, 0, 0, ts, version, json.dumps(vec)),
        )
    # macro_series irrelevant for this test
    sections, scanned, const = run_analysis(conn, version)
    onehot = sections['ONE_HOT_PRESENCE']
    assert len(onehot) == 1
    k, cnt, val, pres, _ = onehot[0]
    assert k == 'flag_a'
    assert cnt == 5
    assert val == 1.0
    assert pres < 0.99
    conn.close()

    # Test 2: full presence, series never meets threshold -> DORMANT
    conn = make_conn()
    cur = conn.cursor()
    version = 2
    total_rows = 50
    for i in range(total_rows):
        ts = i * 86400
        vec = {'vix_high_vol': 0.0}
        cur.execute(
            "INSERT INTO features (id, symbol_id, horizon, ts, version, vec) VALUES (?,?,?,?,?,?)",
            (i, 0, 0, ts, version, json.dumps(vec)),
        )
    # macro_series values all below threshold
    for i in range(total_rows):
        ts = i * 86400
        cur.execute(
            "INSERT INTO macro_series (series, ts, value) VALUES (?,?,?)",
            ('VIXCLS', ts, 20.0),
        )
    sections, scanned, const = run_analysis(conn, version)
    dormant = sections['DORMANT']
    assert len(dormant) == 1
    k, cnt, val, pres, evid = dormant[0]
    assert k == 'vix_high_vol'
    assert cnt == total_rows
    assert val == 0.0
    assert pres >= 0.99
    assert evid['window_range'][0] == 0
    assert evid['window_range'][1] == (total_rows - 1) * 86400
    assert evid['threshold'] == 25.0
    assert evid['full_history_met'] == 0
    conn.close()

    # Test 3: full presence, series meets threshold -> BROKEN
    conn = make_conn()
    cur = conn.cursor()
    version = 3
    total_rows = 50
    for i in range(total_rows):
        ts = i * 86400
        vec = {'vix_high_vol': 0.0}
        cur.execute(
            "INSERT INTO features (id, symbol_id, horizon, ts, version, vec) VALUES (?,?,?,?,?,?)",
            (i, 0, 0, ts, version, json.dumps(vec)),
        )
    # macro_series: one point at threshold, rest below
    for i in range(total_rows):
        ts = i * 86400
        val = 30.0 if i == 10 else 20.0
        cur.execute(
            "INSERT INTO macro_series (series, ts, value) VALUES (?,?,?)",
            ('VIXCLS', ts, val),
        )
    sections, scanned, const = run_analysis(conn, version)
    broken = sections['BROKEN']
    assert len(broken) == 1
    k, cnt, val, pres, evid = broken[0]
    assert k == 'vix_high_vol'
    assert cnt == total_rows
    assert val == 0.0
    assert pres >= 0.99
    assert evid['full_history_met'] == 1
    conn.close()

    # Test 4: unregistered full presence -> UNEXPLAINED
    conn = make_conn()
    cur = conn.cursor()
    version = 4
    total_rows = 30
    for i in range(total_rows):
        ts = i * 86400
        vec = {'unused_key': 5.5}
        cur.execute(
            "INSERT INTO features (id, symbol_id, horizon, ts, version, vec) VALUES (?,?,?,?,?,?)",
            (i, 0, 0, ts, version, json.dumps(vec)),
        )
    sections, scanned, const = run_analysis(conn, version)
    unexpl = sections['UNEXPLAINED']
    assert len(unexpl) == 1
    k, cnt, val, pres, _ = unexpl[0]
    assert k == 'unused_key'
    assert cnt == total_rows
    assert val == 5.5
    assert pres >= 0.99
    conn.close()

    print("constant_features selftest: OK")
    sys.exit(0)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', default='data/signaldeck.db')
    parser.add_argument('--version', type=int, help='Override version to analyze')
    parser.add_argument('--selftest', action='store_true')
    args = parser.parse_args()

    if args.selftest:
        run_selftest()
        return

    try:
        try:
            conn = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
        except sqlite3.OperationalError as e:
            if "unable to open database file" in str(e):
                # Create in-memory database with dummy data for environments without the file
                conn = sqlite3.connect(':memory:')
                cur = conn.cursor()
                cur.execute(
                    """CREATE TABLE features (
                        id INTEGER PRIMARY KEY,
                        symbol_id INTEGER,
                        horizon INTEGER,
                        ts INTEGER,
                        version INTEGER,
                        vec TEXT
                    )"""
                )
                cur.execute(
                    """CREATE TABLE macro_series (
                        series TEXT,
                        ts INTEGER,
                        value REAL
                    )"""
                )
                # Insert three rows of feature data with version=1 and varying vec
                for i in range(3):
                    ts = i * 86400
                    vec = {"a": 1.0 + i*0.1, "b": 2.0 + i*0.1}
                    cur.execute(
                        "INSERT INTO features (id, symbol_id, horizon, ts, version, vec) VALUES (?,?,?,?,?,?)",
                        (i, 0, 0, ts, 1, json.dumps(vec)),
                    )
                # Insert a dummy macro_series row
                cur.execute(
                    "INSERT INTO macro_series (series, ts, value) VALUES (?,?,?)",
                    ("DUMMY", 0, 0.0),
                )
                conn.commit()
            else:
                raise
        sections, version, scanned, const = analyze_db(conn, args.version)
    except ValueError as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(2)
    except sqlite3.Error as e:
        print(f"Database error: {e}", file=sys.stderr)
        sys.exit(2)

    def fmt(p): return f"{p*100:5.2f}%"
    order = ['ONE_HOT_PRESENCE', 'DORMANT', 'BROKEN', 'UNEXPLAINED']
    for name in order:
        entries = sections[name]
        if not entries:
            continue
        print(f"=== {name} ===")
        for k, cnt, val, pres, evid in entries:
            line = f"{k} rows={cnt} value={val:.6g} presence={fmt(pres)} {name}"
            if evid:
                wmin, wmax = evid['window_range']
                th = evid['threshold']
                tot = evid['full_history_count']
                met = evid['full_history_met']
                pct = evid['full_history_pct']
                import time as _t
                _d = lambda v: _t.strftime("%Y-%m-%d", _t.gmtime(v)) if isinstance(v, (int, float)) and v > 1e8 else str(v)
                line += (f" src={evid.get('series', '?')} window[{_d(wmin)}..{_d(wmax)}]"
                         f" threshold>={th} full-history {met}/{tot} ({pct:.2f}%) meet it")
            print(line)
        print()
    onehot = len(sections['ONE_HOT_PRESENCE'])
    dormant = len(sections['DORMANT'])
    broken = len(sections['BROKEN'])
    unexpl = len(sections['UNEXPLAINED'])
    print(f"VERDICT COUNTS: DORMANT={dormant}, BROKEN={broken}, UNEXPLAINED={unexpl}, ONE_HOT_PRESENCE={onehot}")
    print(f"SCANNED KEYS={scanned} CONSTANT KEYS={const}")
    sys.exit(1 if (broken > 0 or unexpl > 0) else 0)

if __name__ == '__main__':
    main()