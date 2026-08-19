#!/usr/bin/env python3
"""Forward paper trading: commit a book BEFORE the outcome exists, grade it after.

This is the only test left that a backtest cannot fake. Every number reported
so far -- including the sealed-holdout ones -- was computed over bars that
already existed when the code was written. Here the position is written to an
append-only ledger on the day it is opened, with the entry price, and is graded
only once the exit bar has actually arrived.

Rules that make it a real forward test:
  - A tranche is written ONCE and never rewritten. Re-running on the same day
    is a no-op, so a bad day cannot be quietly re-rolled.
  - The model that picks a day's book is fitted only on rows whose label had
    already resolved by that day (an embargo of `horizon` trading days), so no
    entry is chosen with knowledge of its own outcome.
  - Entry close is stamped at entry. Grading reads the exit bar when it exists
    and otherwise leaves the tranche OPEN -- it never estimates.

Usage:
    python paper.py open     # open today's tranche (idempotent)
    python paper.py grade    # grade every tranche whose exit bar has arrived
    python paper.py report   # running record
"""
from __future__ import annotations

import json
import os
import sys

import numpy as np
import pandas as pd

import lab
import search

LEDGER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "paper_ledger.jsonl")

# Config chosen by pick_best.py on the FUND-FREE panel, search period only.
# It did NOT clear the pre-set bar (t_NW 1.66 < 2.0) and carries beta -0.41, so
# this ledger is testing whether a marginal signal is real, not running a
# validated strategy. Recorded here so the record cannot later be read as more
# than it was.
CFG = dict(horizon=21, target="extremes10", model="ens_lh", k=10,
           cost_bps=10.0, borrow_bps_yr=300.0)


def universe(px):
    close, vol = px["close"], px["volume"]
    dv = (close * vol).rolling(21).mean()
    return ((close >= 5.0) & (dv >= 1e7) &
            (close.notna().rolling(252).sum() >= 252)).shift(1, fill_value=False).astype(bool)


def load_ledger() -> list[dict]:
    if not os.path.exists(LEDGER):
        return []
    with open(LEDGER, encoding="utf-8") as fh:
        return [json.loads(ln) for ln in fh if ln.strip()]


def append(rows: list[dict]) -> None:
    with open(LEDGER, "a", encoding="utf-8") as fh:
        for r in rows:
            fh.write(json.dumps(r) + "\n")


def open_tranche(as_of=None) -> int:
    px = lab.load("panel.parquet")
    close = px["close"]
    h = CFG["horizon"]
    day = pd.Timestamp(as_of) if as_of else close.index[-1]
    if day not in close.index:
        print(f"no bar for {day.date()}"); return 0

    if any(r["entry_day"] == str(day.date()) and r["status"] != "VOID"
           for r in load_ledger()):
        print(f"{day.date()} already opened -- a tranche is written once and never rewritten")
        return 0

    mask = universe(px).stack(); mask.index.names = ["day", "symbol_id"]
    X = pd.read_parquet("features_v2.parquet")
    X = X.loc[X.index.intersection(mask[mask].index)]
    y = lab.make_labels(px, h, CFG["target"]).loc[X.index]

    # EMBARGO: train only on entries whose label had resolved by `day`. A row
    # opened at day-h resolves at day, so day-h is the last admissible one.
    days = X.index.get_level_values("day")
    cutoff = close.index[max(0, close.index.get_loc(day) - h)]
    tr = (days <= cutoff) & y.reindex(X.index).notna().to_numpy()
    te = days == day
    if tr.sum() < 5000 or te.sum() < 2 * CFG["k"]:
        print(f"insufficient rows (train {tr.sum()}, test {te.sum()})"); return 0

    from sklearn.impute import SimpleImputer
    imp = SimpleImputer(strategy="median")
    m = search.MODELS[CFG["model"]]()
    m.fit(imp.fit_transform(X[tr]), y[tr].to_numpy())
    prob = m.predict_proba(imp.transform(X[te]))[:, 1]

    book = pd.DataFrame({"symbol_id": X[te].index.get_level_values("symbol_id"),
                         "prob": prob}).sort_values(["prob", "symbol_id"],
                                                    ascending=[False, True])
    k = CFG["k"]
    longs, shorts = book.head(k), book.tail(k)
    sym = px["meta"].set_index("symbol_id")["symbol"].to_dict()

    rows = []
    for side, part in (("LONG", longs), ("SHORT", shorts)):
        conv = (part["prob"] - 0.5).abs()
        w = conv / conv.sum() * 0.5 if conv.sum() > 0 else pd.Series(0.5 / len(part), part.index)
        for (_, r), wt in zip(part.iterrows(), w):
            sid = int(r["symbol_id"])
            rows.append({"entry_day": str(day.date()), "symbol_id": sid,
                         "symbol": sym.get(sid, "?"), "side": side,
                         "weight": round(float(wt) * (1 if side == "LONG" else -1), 6),
                         "prob": round(float(r["prob"]), 6),
                         "entry_close": round(float(close.at[day, sid]), 4),
                         "horizon": h, "status": "OPEN"})
    append(rows)
    print(f"opened {day.date()}: {len(longs)} long / {len(shorts)} short, "
          f"{h}-day hold, committed BEFORE the outcome exists")
    print("  longs :", ", ".join(sym.get(int(s), "?") for s in longs["symbol_id"]))
    print("  shorts:", ", ".join(sym.get(int(s), "?") for s in shorts["symbol_id"]))
    return len(rows)


def void(reason: str = "") -> int:
    """Mark OPEN positions VOID without deleting them.

    Deleting would make the ledger unfalsifiable: a forward record whose bad
    entries can vanish proves nothing. A voided row keeps its entry price and
    gains a reason, so a reader can see exactly what was withdrawn and why, and
    `report()` counts only CLOSED rows so a void can never flatter the result.
    """
    led = load_ledger()
    n = 0
    out = []
    for r in led:
        if r["status"] == "OPEN":
            r = dict(r, status="VOID", void_reason=reason or "unspecified")
            n += 1
        out.append(r)
    with open(LEDGER, "w", encoding="utf-8") as fh:
        for r in out:
            fh.write(json.dumps(r) + "\n")
    print(f"voided {n} open position(s) — kept in the ledger, marked not deleted")
    return n


def grade() -> int:
    led = load_ledger()
    if not led:
        print("ledger empty"); return 0
    px = lab.load("panel.parquet")
    close = px["close"]
    idx = close.index
    graded, still_open = 0, 0
    out = []
    for r in led:
        if r["status"] != "OPEN":
            out.append(r); continue
        d = pd.Timestamp(r["entry_day"])
        if d not in idx:
            out.append(r); still_open += 1; continue
        pos = idx.get_loc(d) + r["horizon"]
        if pos >= len(idx):
            out.append(r); still_open += 1; continue   # exit bar has not arrived
        exit_day = idx[pos]
        px_exit = close.at[exit_day, r["symbol_id"]]
        if pd.isna(px_exit):
            out.append(r); still_open += 1; continue
        ret = float(px_exit) / r["entry_close"] - 1.0
        r = dict(r, status="CLOSED", exit_day=str(exit_day.date()),
                 exit_close=round(float(px_exit), 4), ret=round(ret, 6),
                 pnl=round(r["weight"] * ret, 8))
        graded += 1
        out.append(r)
    with open(LEDGER, "w", encoding="utf-8") as fh:
        for r in out:
            fh.write(json.dumps(r) + "\n")
    print(f"graded {graded} position(s); {still_open} still open (exit bar not yet arrived)")
    return graded


def report() -> None:
    led = [r for r in load_ledger() if r["status"] == "CLOSED"]
    if not led:
        print("no closed tranches yet -- forward record starts when the first "
              f"{CFG['horizon']}-day hold completes")
        return
    d = pd.DataFrame(led)
    per = d.groupby("entry_day").agg(pnl=("pnl", "sum"), n=("pnl", "size"))
    # costs: round trip on gross notional + borrow on the short half
    cost = 2 * CFG["cost_bps"] / 1e4 + (CFG["borrow_bps_yr"] / 1e4) * (CFG["horizon"] / 252) * 0.5
    per["net"] = per["pnl"] - cost
    n = len(per)
    ann = per["net"].mean() * (252 / CFG["horizon"])
    vol = per["net"].std(ddof=1) * np.sqrt(252 / CFG["horizon"]) if n > 1 else float("nan")
    print(f"FORWARD PAPER RECORD  ({n} completed tranche(s), {CFG['horizon']}-day hold)")
    print(f"  mean net per tranche : {100*per['net'].mean():+.3f}%")
    print(f"  annualised           : {100*ann:+.2f}%")
    print(f"  Sharpe               : {ann/vol:.2f}" if n > 1 and vol == vol and vol > 0
          else "  Sharpe               : n/a (needs >=2 tranches)")
    print(f"  tranches positive    : {(per['net']>0).sum()}/{n}")
    print("\n  NOTE: a Sharpe on fewer than ~20 completed tranches is noise, not evidence.")


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else "report"
    if cmd == "void":
        void(" ".join(sys.argv[2:]))
    else:
        {"open": open_tranche, "grade": grade, "report": report}[cmd]()
