import sqlite3
import argparse
import json
import csv
import math
from datetime import datetime, date, timezone
import sys

def load_benchmark_bars(con, benchmark):
    cur = con.execute("""
        SELECT b.ts, b.close
        FROM bars b
        JOIN symbols s ON s.id = b.symbol_id
        WHERE s.symbol = ? AND b.tf = '1d'
        ORDER BY b.ts
    """, (benchmark,))
    rows = cur.fetchall()
    if not rows:
        raise ValueError(f"no benchmark bars for {benchmark}")
    out = []
    for ts, close in rows:
        d = datetime.fromtimestamp(ts, timezone.utc).date()
        out.append((ts, d, close))
    return out

def load_paper_equity(con, book):
    cur = con.execute("""
        SELECT ts, cash, positions_value, equity
        FROM paper_equity
        WHERE strategy = ?
        ORDER BY ts
    """, (book,))
    rows = cur.fetchall()
    if not rows:
        raise ValueError(f"no paper equity for book {book}")
    return rows

def load_trades(con, book, anchor_ts, last_ts):
    cur = con.execute("""
        SELECT qty, px, cost
        FROM paper_trades
        WHERE strategy = ? AND ts > ? AND ts <= ?
    """, (book, anchor_ts, last_ts))
    return cur.fetchall()

def scorecard(con, book, start=None, end=None, benchmark="SPY", dividends=None, window=252, path="trades"):
    bench = load_benchmark_bars(con, benchmark)
    equity_rows = load_paper_equity(con, book)

    if start:
        t0 = int(datetime.strptime(start, "%Y-%m-%d").replace(tzinfo=timezone.utc).timestamp())
    else:
        t0 = equity_rows[0][0]

    anchor_idx = None
    for i, (ts, _, _) in enumerate(bench):
        if ts <= t0:
            anchor_idx = i
        else:
            break
    if anchor_idx is None:
        anchor_idx = 0

    end_date = None
    if end:
        end_date = datetime.strptime(end, "%Y-%m-%d").date()

    sessions = []
    for i in range(anchor_idx, len(bench)):
        ts, d, close = bench[i]
        if end_date and d > end_date:
            break
        sessions.append((ts, d, close))

    last_equity_ts = equity_rows[-1][0]
    while sessions and sessions[-1][0] > last_equity_ts:
        sessions.pop()

    if len(sessions) < 2:
        raise ValueError("fewer than 2 sessions in window")

    eq_vals = []
    pv_vals = []
    eq_idx = 0
    for sess_ts, _, _ in sessions:
        while eq_idx + 1 < len(equity_rows) and equity_rows[eq_idx + 1][0] <= sess_ts:
            eq_idx += 1
        if eq_idx == 0 and equity_rows[0][0] > sess_ts:
            _, _, pv, eq = equity_rows[0]
        else:
            _, _, pv, eq = equity_rows[eq_idx]
        eq_vals.append(eq)
        pv_vals.append(pv)

    # Path from the trade log, valued at each session close, when the book has
    # trades. The stored rows are written when the first daily bar of a session
    # lands and are never refreshed, so they mix closes of two days (2026-11-18).
    marks = "stored paper_equity rows"
    try:
        if path != "trades":
            raise ValueError("stored rows requested")
        from owner_remark import remark
        rm = {r["ts"]: r for r in remark(con, book, benchmark)["rows"]}
        for i, (sess_ts, _, _) in enumerate(sessions):
            if sess_ts in rm:
                eq_vals[i], pv_vals[i] = rm[sess_ts]["equity"], rm[sess_ts]["positions_value"]
        marks = "re-marked from paper_trades at each session close"
    except (ValueError, sqlite3.Error, ImportError):
        pass

    E0 = eq_vals[0]
    Elast = eq_vals[-1]
    C0 = sessions[0][2]
    Clast = sessions[-1][2]

    book_return = Elast / E0 - 1.0
    spy_price_return = Clast / C0 - 1.0

    spy_total_return = None
    spy_total_return_status = "UNAVAILABLE: no distributions supplied (stored bars are split-adjusted only)"
    if dividends is not None:
        anchor_date = sessions[0][1]
        last_date = sessions[-1][1]
        units = 1.0
        for d, amt in dividends:
            if anchor_date < d <= last_date:
                for sess_ts, sess_d, sess_close in sessions:
                    if sess_d >= d:
                        units *= 1.0 + amt / sess_close
                        break
        spy_total_return = units * Clast / C0 - 1.0
        spy_total_return_status = f"OK: {len([d for d, _ in dividends if anchor_date < d <= last_date])} distributions reinvested at the ex-date close"

    if spy_total_return is not None:
        benchmark_return = spy_total_return
        benchmark_basis = "total"
    else:
        benchmark_return = spy_price_return
        benchmark_basis = "price"

    excess_pp = (book_return - benchmark_return) * 100.0

    sessions_after_anchor = len(sessions) - 1
    window_complete = sessions_after_anchor >= window
    if benchmark_return <= 0:
        target_min = None
        target_stretch = None
        base = "INAPPLICABLE: benchmark flat or negative"
        if not window_complete:
            base += f" (incomplete window {sessions_after_anchor}/{window})"
        target_status = base
    else:
        target_min = 1.2 * benchmark_return
        target_stretch = 1.3 * benchmark_return
        if not window_complete:
            target_status = f"PROGRESS: incomplete window ({sessions_after_anchor}/{window} sessions)"
        else:
            if book_return >= target_stretch:
                target_status = "STRETCH REACHED"
            elif book_return >= target_min:
                target_status = "TARGET REACHED"
            else:
                target_status = "TARGET MISSED"
            if benchmark_basis == "price":
                # TARGET.md section 1: a price-return benchmark understates SPY by its
                # dividend yield, so no reached/missed verdict is stated on it.
                target_status = "UNAVAILABLE: SPY total return missing (on price basis: " + target_status + ")"

    def max_drawdown(vals):
        peak = vals[0]
        max_dd = 0.0
        for v in vals:
            if v > peak:
                peak = v
            dd = v / peak - 1.0
            if dd < max_dd:
                max_dd = dd
        return max_dd

    book_max_drawdown = max_drawdown(eq_vals)
    spy_closes = [s[2] for s in sessions]
    spy_max_drawdown = max_drawdown(spy_closes)

    exposures = []
    for pv, eq in zip(pv_vals, eq_vals):
        exposures.append(pv / eq if eq != 0 else 0.0)
    avg_exposure = sum(exposures) / len(exposures)

    beta = None
    if sessions_after_anchor >= 20:
        rb = []
        rs = []
        for i in range(1, len(eq_vals)):
            rb.append(eq_vals[i] / eq_vals[i-1] - 1.0)
            rs.append(spy_closes[i] / spy_closes[i-1] - 1.0)
        n = len(rb)
        mean_rb = sum(rb) / n
        mean_rs = sum(rs) / n
        cov = sum((rb[i] - mean_rb) * (rs[i] - mean_rs) for i in range(n)) / n
        var_rs = sum((x - mean_rs) ** 2 for x in rs) / n
        if var_rs != 0:
            beta = cov / var_rs

    trades = load_trades(con, book, sessions[0][0], sessions[-1][0])
    n_trades = len(trades)
    costs = sum(t[2] for t in trades)
    traded_notional = sum(t[0] * t[1] for t in trades)
    mean_equity = sum(eq_vals) / len(eq_vals)
    turnover = traded_notional / mean_equity if mean_equity != 0 else 0.0
    turnover_annualized = turnover * 252.0 / sessions_after_anchor if sessions_after_anchor > 0 else 0.0

    cash_days = sum(1 for pv in pv_vals[1:] if pv == 0)

    return {
        "sessions": sessions_after_anchor,
        "first_date": sessions[0][1].isoformat(),
        "last_date": sessions[-1][1].isoformat(),
        "book_return": book_return,
        "spy_price_return": spy_price_return,
        "spy_total_return": spy_total_return,
        "spy_total_return_status": spy_total_return_status,
        "benchmark_return": benchmark_return,
        "benchmark_basis": benchmark_basis,
        "excess_pp": excess_pp,
        "target_status": target_status,
        "target_min": target_min,
        "target_stretch": target_stretch,
        "book_max_drawdown": book_max_drawdown,
        "spy_max_drawdown": spy_max_drawdown,
        "avg_exposure": avg_exposure,
        "beta": beta,
        "n_trades": n_trades,
        "costs": costs,
        "traded_notional": traded_notional,
        "turnover": turnover,
        "turnover_annualized": turnover_annualized,
        "cash_days": cash_days,
        "book": book,
        "benchmark": benchmark,
        "basis_note": "forward paper marks from paper_equity; not a backtest",
        "marks": marks,
    }

def parse_dividends(path):
    with open(path, newline='') as f:
        reader = csv.DictReader(f)
        out = []
        for row in reader:
            d = datetime.strptime(row['ex_date'], "%Y-%m-%d").date()
            amt = float(row['amount'])
            out.append((d, amt))
        return out

def run_selfcheck():
    con = sqlite3.connect(":memory:")
    cur = con.cursor()
    cur.execute("CREATE TABLE symbols(id INTEGER, symbol TEXT)")
    cur.execute("CREATE TABLE bars(symbol_id INTEGER, tf TEXT, ts INTEGER, open REAL, high REAL, low REAL, close REAL, volume REAL)")
    cur.execute("CREATE TABLE paper_equity(strategy TEXT, ts INTEGER, cash REAL, positions_value REAL, equity REAL)")
    cur.execute("CREATE TABLE paper_trades(id INTEGER, strategy TEXT, symbol_id INTEGER, side TEXT, qty REAL, px REAL, cost REAL, ts INTEGER, reason TEXT)")

    # Case A
    ts1 = int(datetime(2026,1,5,5,0,0, tzinfo=timezone.utc).timestamp())
    ts2 = int(datetime(2026,1,6,5,0,0, tzinfo=timezone.utc).timestamp())
    ts3 = int(datetime(2026,1,7,5,0,0, tzinfo=timezone.utc).timestamp())
    cur.execute("INSERT INTO symbols VALUES (1, 'SPY')")
    cur.executemany("INSERT INTO bars VALUES (?, '1d', ?, ?, ?, ?, ?, ?)", [
        (1, ts1, 100, 100, 100, 100, 1000),
        (1, ts2, 110, 110, 110, 110, 1000),
        (1, ts3, 121, 121, 121, 121, 1000),
    ])
    cur.executemany("INSERT INTO paper_equity VALUES (?, ?, ?, ?, ?)", [
        ("b", ts1, 100000, 0, 100000),
        ("b", ts2, 0, 105000, 105000),
        ("b", ts3, 0, 115500, 115500),
    ])
    cur.execute("INSERT INTO paper_trades VALUES (1, 'b', 1, 'buy', 1000, 100, 10, ?, 'test')", (ts2,))

    # Case B dividends
    divs = [(date(2026,1,6), 1.1)]

    # Case C window=2
    # Case D SPY2
    cur.execute("INSERT INTO symbols VALUES (2, 'SPY2')")
    cur.executemany("INSERT INTO bars VALUES (?, '1d', ?, ?, ?, ?, ?, ?)", [
        (2, ts1, 100, 100, 100, 100, 1000),
        (2, ts2, 90, 90, 90, 90, 1000),
        (2, ts3, 95, 95, 95, 95, 1000),
    ])
    cur.executemany("INSERT INTO paper_equity VALUES (?, ?, ?, ?, ?)", [
        ("b", ts1, 100000, 0, 100000),
        ("b", ts2, 0, 105000, 105000),
        ("b", ts3, 0, 115500, 115500),
    ])

    # Case E late book
    ts_late1 = int(datetime(2026,1,6,0,0,0, tzinfo=timezone.utc).timestamp())
    ts_late2 = int(datetime(2026,1,7,5,0,0, tzinfo=timezone.utc).timestamp())
    cur.executemany("INSERT INTO paper_equity VALUES (?, ?, ?, ?, ?)", [
        ("late", ts_late1, 100000, 0, 100000),
        ("late", ts_late2, 0, 101000, 101000),
    ])

    # Case F lev book over 25 sessions, SPY3 alternates +1%, -0.5%
    cur.execute("INSERT INTO symbols VALUES (3, 'SPY3')")
    spy3_closes = [100.0]
    for i in range(1, 25):
        if i % 2 == 1:
            spy3_closes.append(spy3_closes[-1] * 1.01)
        else:
            spy3_closes.append(spy3_closes[-1] * 0.995)
    spy3_ts = [int(datetime(2026,1,5,5,0,0, tzinfo=timezone.utc).timestamp()) + i*86400 for i in range(25)]
    for i in range(25):
        c = spy3_closes[i]
        cur.execute("INSERT INTO bars VALUES (3, '1d', ?, ?, ?, ?, ?, ?)", (spy3_ts[i], c, c, c, c, 1000))
    lev_eq = [100000.0]
    for i in range(1, 25):
        r = (spy3_closes[i] / spy3_closes[i-1]) - 1.0
        lev_eq.append(lev_eq[-1] * (1 + 2*r))
    for i in range(25):
        cur.execute("INSERT INTO paper_equity VALUES ('lev', ?, 0, ?, ?)", (spy3_ts[i], lev_eq[i], lev_eq[i]))

    # Case G unknown book (no equity rows)
    con.commit()

    # The default path is rebuilt from the trade log; the cases below pin the
    # arithmetic on stored rows, whose fixture trade does not reconcile with them.
    assert scorecard(con, "b", benchmark="SPY", window=252)["marks"].startswith("re-marked")
    assert scorecard(con, "b", path="stored", benchmark="SPY", window=252)["marks"].startswith("stored")

    # Case A
    res = scorecard(con, "b", path="stored", benchmark="SPY", window=252)
    assert abs(res["sessions"] - 2) < 1e-9
    assert abs(res["book_return"] - 0.155) < 1e-9
    assert abs(res["spy_price_return"] - 0.21) < 1e-9
    assert res["benchmark_basis"] == "price"
    assert abs(res["excess_pp"] - (-5.5)) < 1e-9
    assert res["target_status"].startswith("PROGRESS")
    assert abs(res["target_min"] - 0.252) < 1e-9
    assert abs(res["book_max_drawdown"] - 0.0) < 1e-9
    assert res["n_trades"] == 1
    assert abs(res["costs"] - 10) < 1e-9
    assert res["cash_days"] == 0
    assert res["spy_total_return"] is None

    # Case B
    res = scorecard(con, "b", path="stored", benchmark="SPY", dividends=divs, window=252)
    assert abs(res["spy_total_return"] - 0.2221) < 1e-9
    assert res["benchmark_basis"] == "total"
    assert abs(res["excess_pp"] - (0.155 - 0.2221)*100) < 1e-9

    # Case C
    res = scorecard(con, "b", path="stored", benchmark="SPY", window=2)
    assert res["target_status"].startswith("UNAVAILABLE") and "TARGET MISSED" in res["target_status"]
    res = scorecard(con, "b", path="stored", benchmark="SPY", dividends=[], window=2)
    assert res["target_status"] == "TARGET MISSED"
    cur.executemany("INSERT INTO paper_equity VALUES (?, ?, ?, ?, ?)", [
        ("win", ts1, 100000, 0, 100000),
        ("win", ts2, 0, 110000, 110000),
        ("win", ts3, 0, 130000, 130000),
    ])
    res = scorecard(con, "win", benchmark="SPY", dividends=[], window=2)
    assert res["target_status"] == "STRETCH REACHED"

    # Case D
    res = scorecard(con, "b", benchmark="SPY2", window=252)
    assert res["target_status"].startswith("INAPPLICABLE")
    assert res["target_min"] is None
    assert abs(res["spy_max_drawdown"] - (-0.1)) < 1e-9

    # Case E
    res = scorecard(con, "late", benchmark="SPY", window=252)
    assert res["first_date"] == "2026-01-05"
    assert res["sessions"] == 2
    assert abs(res["book_return"] - 0.01) < 1e-9

    # Case F
    res = scorecard(con, "lev", benchmark="SPY3", window=252)
    assert abs(res["beta"] - 2.0) < 1e-6

    # Case G
    try:
        scorecard(con, "unknown", benchmark="SPY")
        assert False, "should raise"
    except ValueError as e:
        assert "no paper equity" in str(e)

    print("SELFCHECK OK")
    con.close()
    sys.exit(0)

def main():
    if len(sys.argv) == 2 and sys.argv[1] == "__selfcheck":
        run_selfcheck()
        return

    parser = argparse.ArgumentParser()
    parser.add_argument("--db", required=True)
    parser.add_argument("--book", required=True)
    parser.add_argument("--start")
    parser.add_argument("--end")
    parser.add_argument("--benchmark", default="SPY")
    parser.add_argument("--dividends")
    parser.add_argument("--window", type=int, default=252)
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()

    dividends = parse_dividends(args.dividends) if args.dividends else None
    con = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    try:
        res = scorecard(con, args.book, args.start, args.end, args.benchmark, dividends, args.window)
    except ValueError as e:
        print(f"ERROR: {e}", file=sys.stderr)
        sys.exit(2)
    finally:
        con.close()

    if args.json:
        print(json.dumps(res, indent=2))
    else:
        for k, v in res.items():
            if isinstance(v, float):
                print(f"{k}: {v:.6f}")
            else:
                print(f"{k}: {v}")

if __name__ == "__main__":
    main()