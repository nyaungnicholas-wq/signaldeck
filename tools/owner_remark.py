import sqlite3
import argparse
import json
import sys
import datetime
import math

def remark(con, book, benchmark="SPY", start_cash=100000.0):
    cur = con.cursor()
    
    # Get benchmark symbol_id
    cur.execute("SELECT id FROM symbols WHERE symbol=?", (benchmark,))
    row = cur.fetchone()
    if not row:
        raise ValueError(f"benchmark symbol not found: {benchmark}")
    bench_id = row[0]
    
    # Get book's first trade ts
    cur.execute("SELECT MIN(ts) FROM paper_trades WHERE strategy=?", (book,))
    row = cur.fetchone()
    if not row or row[0] is None:
        raise ValueError(f"no trades for book {book}")
    first_trade_ts = row[0]
    
    # Get benchmark daily bars from first_trade_ts onwards
    cur.execute("""
        SELECT ts, close FROM bars 
        WHERE symbol_id=? AND tf='1d' AND ts>=?
        ORDER BY ts
    """, (bench_id, first_trade_ts))
    bench_bars = cur.fetchall()
    if not bench_bars:
        raise ValueError("no benchmark bars")
    
    sessions = [(ts, close) for ts, close in bench_bars]
    
    # Get all trades for book
    cur.execute("""
        SELECT id, symbol_id, side, qty, px, cost, ts
        FROM paper_trades WHERE strategy=?
        ORDER BY ts, id
    """, (book,))
    trades = cur.fetchall()
    
    # Get all symbol_ids that appear in trades
    symbol_ids = set(t[1] for t in trades)
    
    # Load daily bars for each symbol into dict: symbol_id -> {ts: close}
    symbol_bars = {}
    for sid in symbol_ids:
        cur.execute("SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts", (sid,))
        symbol_bars[sid] = {ts: close for ts, close in cur.fetchall()}
    # Stored bars are split-adjusted and rewritten after a split, while a trade
    # keeps the quantity and price of its day. A fill is at the bar open, so a
    # stored open more than 20% away from the trade price means the bars were
    # rebased since; the quantity is rebased to match (value at the fill is kept).
    symbol_opens = {}
    for sid in symbol_ids:
        cur.execute("SELECT ts, open FROM bars WHERE symbol_id=? AND tf='1d'", (sid,))
        symbol_opens[sid] = {ts: o for ts, o in cur.fetchall()}
    rebased_trades = 0
    
    # Get stored equity rows for book
    cur.execute("SELECT ts, equity FROM paper_equity WHERE strategy=?", (book,))
    stored_equity = {ts: equity for ts, equity in cur.fetchall()}
    
    cash = start_cash
    holdings = {}
    trade_idx = 0
    rows = []
    stale_marks = 0
    unpriced_marks = 0
    
    for session_ts, bench_close in sessions:
        # Apply trades with trade.ts <= session_ts
        while trade_idx < len(trades) and trades[trade_idx][6] <= session_ts:
            _, sid, side, qty, px, cost, t_ts = trades[trade_idx]
            side = side.lower()
            notional = qty * px
            stored_open = symbol_opens.get(sid, {}).get(t_ts)
            if stored_open and px > 0 and abs(stored_open / px - 1.0) > 0.2:
                qty = notional / stored_open
                rebased_trades += 1
            if side.startswith('b'):
                cash -= notional + cost
                holdings[sid] = holdings.get(sid, 0.0) + qty
            else:
                cash += notional - cost
                holdings[sid] = holdings.get(sid, 0.0) - qty
            if abs(holdings[sid]) < 1e-9:
                del holdings[sid]
            trade_idx += 1
        
        # Value holdings
        positions_value = 0.0
        for sid, qty in holdings.items():
            bars = symbol_bars.get(sid, {})
            if session_ts in bars:
                positions_value += qty * bars[session_ts]
            else:
                # Find latest bar with ts < session_ts
                earlier = [ts for ts in bars if ts < session_ts]
                if earlier:
                    latest_ts = max(earlier)
                    positions_value += qty * bars[latest_ts]
                    stale_marks += 1
                else:
                    unpriced_marks += 1
        
        equity = cash + positions_value
        stored_eq = stored_equity.get(session_ts)
        date_str = datetime.datetime.fromtimestamp(session_ts, datetime.timezone.utc).date().isoformat()
        rows.append({
            "ts": session_ts,
            "date": date_str,
            "cash": cash,
            "positions_value": positions_value,
            "equity": equity,
            "stored_equity": stored_eq,
            "benchmark_close": bench_close
        })
    
    # Compute summary
    sessions_count = len(rows)
    first_date = rows[0]["date"] if rows else None
    last_date = rows[-1]["date"] if rows else None
    final_equity = rows[-1]["equity"] if rows else start_cash
    final_stored_equity = None
    for r in reversed(rows):
        if r["stored_equity"] is not None:
            final_stored_equity = r["stored_equity"]
            break
    
    ret = final_equity / start_cash - 1.0
    
    compared = sum(1 for r in rows if r["stored_equity"] is not None)
    differing = 0
    max_abs_diff = 0.0
    max_abs_diff_pct = 0.0
    max_abs_diff_date = None
    for r in rows:
        if r["stored_equity"] is not None:
            diff = abs(r["equity"] - r["stored_equity"])
            if diff > 0.01:
                differing += 1
            if diff > max_abs_diff:
                max_abs_diff = diff
                max_abs_diff_pct = (diff / r["equity"] * 100) if r["equity"] != 0 else 0.0
                max_abs_diff_date = r["date"]
    
    # Max drawdown
    peak = rows[0]["equity"] if rows else start_cash
    max_dd = 0.0
    for r in rows:
        eq = r["equity"]
        if eq > peak:
            peak = eq
        elif peak > 0:
            dd = (eq - peak) / peak
            if dd < max_dd:
                max_dd = dd
    
    # Avg exposure
    avg_exposure = 0.0
    if rows:
        total = sum(r["positions_value"] / r["equity"] for r in rows if r["equity"] != 0)
        avg_exposure = total / len(rows)
    
    # Beta (book returns vs benchmark returns)
    beta = None
    if len(rows) >= 3:
        book_rets = []
        bench_rets = []
        for i in range(1, len(rows)):
            prev_eq = rows[i-1]["equity"]
            curr_eq = rows[i]["equity"]
            prev_bc = rows[i-1]["benchmark_close"]
            curr_bc = rows[i]["benchmark_close"]
            if prev_eq != 0 and prev_bc != 0:
                book_rets.append((curr_eq - prev_eq) / prev_eq)
                bench_rets.append((curr_bc - prev_bc) / prev_bc)
        if len(book_rets) >= 2:
            n = len(book_rets)
            mean_b = sum(book_rets) / n
            mean_m = sum(bench_rets) / n
            cov = sum((b - mean_b) * (m - mean_m) for b, m in zip(book_rets, bench_rets)) / n
            var = sum((m - mean_m) ** 2 for m in bench_rets) / n
            if var != 0:
                beta = cov / var
    
    # Beta stored
    beta_stored = None
    stored_rows = [(r["ts"], r["stored_equity"]) for r in rows if r["stored_equity"] is not None]
    if len(stored_rows) >= 3:
        stored_rets = []
        bench_rets_s = []
        for i in range(1, len(stored_rows)):
            prev_ts, prev_eq = stored_rows[i-1]
            curr_ts, curr_eq = stored_rows[i]
            # Need consecutive sessions - check if there's a session between them
            prev_idx = next(j for j, r in enumerate(rows) if r["ts"] == prev_ts)
            curr_idx = next(j for j, r in enumerate(rows) if r["ts"] == curr_ts)
            if curr_idx == prev_idx + 1:
                prev_bc = rows[prev_idx]["benchmark_close"]
                curr_bc = rows[curr_idx]["benchmark_close"]
                if prev_eq != 0 and prev_bc != 0:
                    stored_rets.append((curr_eq - prev_eq) / prev_eq)
                    bench_rets_s.append((curr_bc - prev_bc) / prev_bc)
        if len(stored_rets) >= 2:
            n = len(stored_rets)
            mean_b = sum(stored_rets) / n
            mean_m = sum(bench_rets_s) / n
            cov = sum((b - mean_b) * (m - mean_m) for b, m in zip(stored_rets, bench_rets_s)) / n
            var = sum((m - mean_m) ** 2 for m in bench_rets_s) / n
            if var != 0:
                beta_stored = cov / var
    
    return {
        "book": book,
        "sessions": sessions_count,
        "first_date": first_date,
        "last_date": last_date,
        "final_equity": final_equity,
        "final_stored_equity": final_stored_equity,
        "return": ret,
        "compared": compared,
        "differing": differing,
        "max_abs_diff": max_abs_diff,
        "max_abs_diff_pct": max_abs_diff_pct,
        "max_abs_diff_date": max_abs_diff_date,
        "max_drawdown": max_dd,
        "avg_exposure": avg_exposure,
        "beta": beta,
        "beta_stored": beta_stored,
        "stale_marks": stale_marks,
        "rebased_trades": rebased_trades,
        "unpriced_marks": unpriced_marks,
        "rows": rows
    }

def run_selfcheck():
    con = sqlite3.connect(":memory:")
    cur = con.cursor()
    cur.executescript("""
        CREATE TABLE symbols(id INTEGER PRIMARY KEY, symbol TEXT);
        CREATE TABLE bars(symbol_id INTEGER, tf TEXT, ts INTEGER, open REAL, high REAL, low REAL, close REAL, volume REAL);
        CREATE TABLE paper_trades(id INTEGER PRIMARY KEY, strategy TEXT, symbol_id INTEGER, side TEXT, qty REAL, px REAL, cost REAL, ts INTEGER, reason TEXT);
        CREATE TABLE paper_equity(strategy TEXT, ts INTEGER, cash REAL, positions_value REAL, equity REAL);
    """)
    # SPY id=1, A id=2, B id=3
    cur.executemany("INSERT INTO symbols VALUES (?,?)", [(1,"SPY"),(2,"A"),(3,"B")])
    # SPY bars at 1000,2000,3000,4000
    cur.executemany("INSERT INTO bars VALUES (?,?,?,?,?,?,?,?)", [
        (1,"1d",1000,0,0,0,100,0),
        (1,"1d",2000,0,0,0,101,0),
        (1,"1d",3000,0,0,0,99.99,0),
        (1,"1d",4000,0,0,0,102,0),
    ])
    # A bars at 1000,2000,3000,4000
    cur.executemany("INSERT INTO bars VALUES (?,?,?,?,?,?,?,?)", [
        (2,"1d",1000,0,0,0,10,0),
        (2,"1d",2000,0,0,0,11,0),
        (2,"1d",3000,0,0,0,12,0),
        (2,"1d",4000,0,0,0,13,0),
    ])
    # B bars at 1000 and 4000 only
    cur.executemany("INSERT INTO bars VALUES (?,?,?,?,?,?,?,?)", [
        (3,"1d",1000,0,0,0,50,0),
        (3,"1d",4000,0,0,0,60,0),
    ])
    # Trades for book "t"
    cur.executemany("INSERT INTO paper_trades VALUES (?,?,?,?,?,?,?,?,?)", [
        (1,"t",2,"buy",100,10,1,1000,""),
        (2,"t",3,"buy",10,50,0.5,2000,""),
        (3,"t",2,"sell",100,12,1,4000,""),
    ])
    # Stored equity rows
    cur.executemany("INSERT INTO paper_equity VALUES (?,?,?,?,?)", [
        ("t",1000,0,0,99999.0),
        ("t",2000,0,0,99998.5),
        ("t",4000,0,0,100297.5),
    ])
    con.commit()
    
    res = remark(con, "t", "SPY", 100000.0)
    
    cash_1000 = res["rows"][0]["cash"]
    assert abs(cash_1000 - 98999.0) < 1e-9, f"cash 1000: {cash_1000}"
    assert abs(res["rows"][1]["cash"] - 98498.5) < 1e-9, f"cash 2000: {res['rows'][1]['cash']}"
    assert abs(res["rows"][3]["cash"] - 99697.5) < 1e-9, f"cash 4000: {res['rows'][3]['cash']}"
    
    assert abs(res["rows"][0]["equity"] - 99999.0) < 1e-9
    assert abs(res["rows"][1]["equity"] - 100098.5) < 1e-9
    assert abs(res["rows"][2]["equity"] - 100198.5) < 1e-9
    assert abs(res["rows"][3]["equity"] - 100297.5) < 1e-9
    
    assert res["stale_marks"] == 2
    assert res["unpriced_marks"] == 0
    assert res["sessions"] == 4
    assert abs(res["final_equity"] - 100297.5) < 1e-9
    
    assert res["compared"] == 3
    assert res["differing"] == 1
    assert abs(res["max_abs_diff"] - 100.0) < 1e-9
    assert res["max_abs_diff_date"] == res["rows"][1]["date"]
    
    assert abs(res["max_drawdown"] - 0.0) < 1e-9
    assert res["beta"] is not None
    assert res["beta_stored"] is None
    
    # No trades book
    try:
        remark(con, "empty")
        assert False, "should raise"
    except ValueError as e:
        assert "no trades for book empty" in str(e)
    
    # Rebased bars: the stored open (20) is twice the trade price (10), so the
    # 10 shares bought are 5 shares on today's basis.
    cur.execute("INSERT INTO symbols VALUES (4,'C')")
    cur.executemany("INSERT INTO bars VALUES (?,?,?,?,?,?,?,?)", [(4,"1d",1000,20,0,0,22,0), (4,"1d",2000,22,0,0,24,0)])
    cur.execute("INSERT INTO paper_trades VALUES (10,'s',4,'buy',10,10,0,1000,'')")
    con.commit()
    rs = remark(con, "s")
    assert rs["rebased_trades"] == 1 and res["rebased_trades"] == 0
    assert abs(rs["rows"][0]["equity"] - 100010.0) < 1e-9 and abs(rs["rows"][1]["equity"] - 100020.0) < 1e-9

    print("SELFCHECK OK")

def main():
    if len(sys.argv) == 2 and sys.argv[1] == "__selfcheck":
        run_selfcheck()
        return
    
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", required=True)
    ap.add_argument("--book", required=True)
    ap.add_argument("--benchmark", default="SPY")
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--rows", action="store_true")
    args = ap.parse_args()
    
    path = args.db.replace("\\", "/")
    con = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        res = remark(con, args.book, args.benchmark)
    finally:
        con.close()
    
    if args.json:
        out = {k: v for k, v in res.items() if k != "rows"}
        out["rows"] = res["rows"]
        json.dump(out, sys.stdout, default=str)
        print()
    else:
        for k, v in res.items():
            if k == "rows":
                continue
            if isinstance(v, float):
                print(f"{k}: {v:.6f}")
            else:
                print(f"{k}: {v}")
        if args.rows:
            for r in res["rows"]:
                stored = r["stored_equity"]
                diff = (r["equity"] - stored) if stored is not None else ""
                print(f"{r['date']} {r['equity']:.6f} {stored if stored is not None else ''} {diff if diff != '' else ''}")

if __name__ == "__main__":
    main()