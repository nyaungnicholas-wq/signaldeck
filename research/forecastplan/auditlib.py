"""Shared constants, helpers and a deterministic fixture for the forecastplan audit scripts.

Read-only by construction: open_ro() opens SQLite in mode=ro. Every constant names its origin
so a reviewer can check it has not drifted from the production source.
"""
import csv
import datetime
import json
import pathlib
import sqlite3
import sys

TRADING_DAY_OFFSET_SECS = 5 * 3600  # tools/accuracy_registry.py
SECONDS_PER_DAY = 86400  # tools/accuracy_registry.py
SURVIVORSHIP_EPOCH_TS = int(datetime.datetime(2026, 7, 24, tzinfo=datetime.timezone.utc).timestamp())  # tools/accuracy_registry.py
MIN_BARS = 530  # tools/rv_forecast_backtest.py MIN_HISTORY
MAX_FLAT_SHARE = 0.01  # tools/rv_forecast_backtest.py
HC_CUTOFF = 0.15  # high conviction = abs(prob - 0.5) >= 0.15 (tools/accuracy_registry.py)
MIN_SYMBOLS_FOR_COLLAPSE = 30  # forecastmon.MinSymbolsForCollapse


def trading_day(ts):
    return (ts - TRADING_DAY_OFFSET_SECS) // SECONDS_PER_DAY


def settle_day(settle_ts, ts):
    if settle_ts is not None and settle_ts > 0:
        return trading_day(settle_ts)
    return trading_day(ts)


def day_str(day_index):
    return datetime.datetime.fromtimestamp(day_index * SECONDS_PER_DAY, datetime.timezone.utc).strftime("%Y-%m-%d")


def utc_date(ts):
    if ts is None:
        return ""
    return datetime.datetime.fromtimestamp(ts, datetime.timezone.utc).strftime("%Y-%m-%d")


def bar_day(ts):
    return (ts // SECONDS_PER_DAY) * SECONDS_PER_DAY  # daemon/internal/store/pituniverse.go UTC floor


def open_ro(path):
    return sqlite3.connect("file:%s?mode=ro" % str(path).replace("\\", "/"), uri=True)


def repo_root(script_file):
    return pathlib.Path(script_file).resolve().parent.parent.parent


def write_json(path, obj):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(obj, f, indent=2, sort_keys=True, default=str)
        f.write("\n")


def write_csv(path, rows, columns):
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="") as f:
        w = csv.DictWriter(f, fieldnames=columns, extrasaction="ignore")
        w.writeheader()
        for r in rows:
            w.writerow(r)


def stdout_utf8():
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")


SCHEMA_SQL = """
CREATE TABLE symbols(id INTEGER PRIMARY KEY, symbol TEXT NOT NULL, market TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL DEFAULT 1, added_at INTEGER NOT NULL, delisted_at INTEGER);
CREATE TABLE bars(symbol_id INTEGER NOT NULL, tf TEXT NOT NULL, ts INTEGER NOT NULL, open REAL NOT NULL, high REAL NOT NULL, low REAL NOT NULL, close REAL NOT NULL, volume REAL NOT NULL DEFAULT 0, PRIMARY KEY(symbol_id, tf, ts));
CREATE TABLE universe_membership(day INTEGER NOT NULL, symbol_id INTEGER NOT NULL, source TEXT NOT NULL, PRIMARY KEY(day, symbol_id));
CREATE TABLE fundamentals(symbol_id INTEGER NOT NULL, metric TEXT NOT NULL, value REAL NOT NULL, as_of INTEGER NOT NULL, fetched_at INTEGER NOT NULL, PRIMARY KEY(symbol_id, metric, as_of));
CREATE TABLE companies(cik INTEGER NOT NULL, ticker TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', exchange TEXT NOT NULL DEFAULT '', sic TEXT NOT NULL DEFAULT '', sic_desc TEXT NOT NULL DEFAULT '', updated_ts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE predictions(symbol_id INTEGER NOT NULL, horizon TEXT NOT NULL, ts INTEGER NOT NULL, raw_prob REAL NOT NULL, cal_prob REAL NOT NULL, n_used INTEGER NOT NULL, components TEXT NOT NULL DEFAULT '{}', weights TEXT NOT NULL DEFAULT '', basis TEXT NOT NULL DEFAULT '', PRIMARY KEY(symbol_id, horizon, ts));
CREATE TABLE prediction_outcomes(symbol_id INTEGER NOT NULL, horizon TEXT NOT NULL, ts INTEGER NOT NULL, prob REAL NOT NULL, up INTEGER, fwd_return REAL, resolved_at INTEGER, basis_epoch INTEGER, settle_ts INTEGER, PRIMARY KEY(symbol_id, horizon, ts));
CREATE TABLE worker_runs(id INTEGER PRIMARY KEY, worker TEXT NOT NULL, started_at INTEGER NOT NULL, finished_at INTEGER, status TEXT NOT NULL DEFAULT 'running', detail TEXT NOT NULL DEFAULT '', revision TEXT NOT NULL DEFAULT '');
"""

FIXTURE_D0 = int(datetime.datetime(2024, 1, 1, tzinfo=datetime.timezone.utc).timestamp())
FIXTURE_P = SURVIVORSHIP_EPOCH_TS + 10 * SECONDS_PER_DAY + 14 * 3600
FIXTURE_PRED_DAY = day_str(trading_day(FIXTURE_P))
FIXTURE_N_UNCOVERED_DAYS = 2
FIXTURE_SETTLE_TS = FIXTURE_P - 14 * 3600  # 00:00Z stamp: folds to the PREVIOUS trading-day index under the 5h offset
FIXTURE_OUTCOME_DAY = day_str(settle_day(FIXTURE_SETTLE_TS, FIXTURE_P))


def _bars(sid, n, offset, flat_below):
    out = []
    for i in range(n):
        c = 100.0 + i
        flat = i < flat_below
        out.append((sid, "1d", FIXTURE_D0 + i * SECONDS_PER_DAY + offset, c,
                    c if flat else c + 1.0, c if flat else c - 1.0, c, 1000.0))
    return out


def fixture_db():
    con = sqlite3.connect(":memory:")
    con.executescript(SCHEMA_SQL)
    d0 = FIXTURE_D0
    con.executemany("INSERT INTO symbols VALUES (?,?,?,?,?,?,?)", [
        (1, "AAA", "stocks", "Alpha Inc", 1, d0, None),
        (2, "BBB", "stocks", "Beta Corp", 1, d0, None),
        (3, "CCC", "stocks", "Gamma Ltd", 1, d0, None),
        (4, "DDD", "stocks", "Delta Dead Co", 0, d0, d0 + 700 * SECONDS_PER_DAY),
        (5, "BTC/USD", "crypto", "Bitcoin", 1, d0, None),
        (6, "EEEEW", "stocks", "Epsilon Warrant", 1, d0, None),
    ])
    bars = (_bars(1, 600, 14400, 0) + _bars(2, 600, 14400, 30) + _bars(3, 100, 14400, 0)
            + _bars(4, 600, 14400, 0) + _bars(5, 400, 0, 0) + _bars(6, 600, 14400, 0))
    con.executemany("INSERT INTO bars VALUES (?,?,?,?,?,?,?,?)", bars)
    con.executemany("INSERT INTO fundamentals VALUES (?,?,?,?,?)",
                    [(sid, "Revenues", 1.0, d0, d0) for sid in (1, 2, 6)])
    con.execute("INSERT INTO companies VALUES (1,'AAA','Alpha Inc','NYSE','','',0)")
    days = sorted({bar_day(b[2]) for b in bars})
    covered = set(days[:-FIXTURE_N_UNCOVERED_DAYS])
    con.executemany("INSERT OR IGNORE INTO universe_membership VALUES (?,?,?)",
                    [(bar_day(b[2]), b[0], "bars-1d") for b in bars if bar_day(b[2]) in covered])
    con.execute("INSERT INTO worker_runs VALUES (1,'universe-rebuild',?,?,'ok','rebuilt 6 symbols','abc123')",
                (d0 + 600 * SECONDS_PER_DAY, d0 + 600 * SECONDS_PER_DAY + 60))
    p = FIXTURE_P
    preds = [(sid, "1d", p, ((sid - 100) % 10) / 10.0 + 0.05, 0.6, 0 if sid <= 104 else 1, "{}", "", "")
             for sid in range(101, 141)]
    preds += [(141, "1d", p, 0.31, 0.3, 2, "{}", "", ""), (142, "1d", p, 0.72, 0.7, 1, "{}", "", ""),
              (101, "1d", p - 3600, 0.2, 0.2, 1, "{}", "", ""), (101, "1w", p, 0.55, 0.55, 1, "{}", "", "")]
    con.executemany("INSERT INTO predictions VALUES (?,?,?,?,?,?,?,?,?)", preds)
    settle = FIXTURE_SETTLE_TS
    outs = [(sid, "1d", p, 0.6, 1, 0.01, p + SECONDS_PER_DAY, None, settle) for sid in (201, 202, 203, 204)]
    outs += [(205, "1d", p, 0.3, 1, 0.01, p + SECONDS_PER_DAY, None, settle),
             (206, "1d", p, 0.55, None, None, None, None, None),
             (207, "1d", SURVIVORSHIP_EPOCH_TS - SECONDS_PER_DAY, 0.9, 1, 0.02, SURVIVORSHIP_EPOCH_TS, None, None)]
    con.executemany("INSERT INTO prediction_outcomes VALUES (?,?,?,?,?,?,?,?,?)", outs)
    con.commit()
    return con


def selfcheck():
    assert trading_day(TRADING_DAY_OFFSET_SECS) == 0 and trading_day(TRADING_DAY_OFFSET_SECS - 1) == -1
    assert settle_day(None, 100000) == trading_day(100000) and settle_day(0, 100000) == trading_day(100000)
    assert settle_day(200000, 100000) == trading_day(200000)
    assert day_str(0) == "1970-01-01" and utc_date(None) == "" and bar_day(86400 + 14400) == 86400
    con = fixture_db()
    q = lambda s: con.execute(s).fetchone()[0]
    assert q("SELECT COUNT(*) FROM symbols") == 6
    assert q("SELECT COUNT(*) FROM bars WHERE symbol_id=1") == 600
    assert q("SELECT COUNT(*) FROM bars WHERE symbol_id=2 AND high = low") == 30
    assert q("SELECT COUNT(*) FROM predictions") == 44
    assert q("SELECT COUNT(*) FROM prediction_outcomes") == 7
    nd = q("SELECT COUNT(DISTINCT (ts/86400)*86400) FROM bars") - q("SELECT COUNT(DISTINCT day) FROM universe_membership")
    assert nd == FIXTURE_N_UNCOVERED_DAYS, nd
    print("SELFCHECK OK")


if __name__ == "__main__":
    stdout_utf8()
    selfcheck()
