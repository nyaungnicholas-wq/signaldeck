"""Recover the big delistings Alpaca's asset list forgot, via Wikipedia.

fetch_delisted.py enumerated Alpaca's `status=inactive` assets and got 650
companies -- but NOT First Republic, SVB, Twitter, Activision or Signature Bank.
Those are in neither Alpaca asset list, active or inactive, even though the BARS
endpoint serves their full history. The asset API is not a complete enumeration
of what the bars API knows, so enumeration was the wrong approach: bring an
external ticker list and query bars directly.

Wikipedia's "List of S&P 500 companies" carries 383 index-removal events with
tickers and effective dates -- free, and weighted toward exactly the large-cap
failures and acquisitions the Alpaca-derived set was missing.

CRITICAL DISTINCTION: index removal is NOT delisting. Conagra left the S&P 500
in June 2026 and trades today. Bars are the arbiter -- if a symbol is still
printing recently it was an index change, not a death, and marking it delisted
would erase a live company from every point-in-time universe.

Writes the same staging schema fetch_delisted.py produces, so
`sdmaint import-delisted` consumes it unchanged. Never touches production.
"""
import argparse
import io
import json
import re
import sqlite3
import time
import urllib.request
from datetime import datetime, timezone

ENV = r"C:\Users\Nicholas_N\Desktop\claude code\stock-trader\.env"
WIKI = "https://en.wikipedia.org/wiki/List_of_S%26P_500_companies"
UA = "SignalDeck Research (dimples.n3fam@gmail.com)"
# adjustment MUST match daemon/internal/ingest/alpaca/client.go's barAdjustment.
# "all" is split PLUS dividends; the live backfill requests "split". Two
# conventions in one bars column give a symbol fed by both paths a seam that
# reads exactly like a real move. TestAdjustmentModesAgree reads every fetcher
# in this directory, so a new one cannot drift either.
BARS = ("https://data.alpaca.markets/v2/stocks/{sym}/bars"
        "?timeframe=1Day&start=2015-01-01&end={end}&limit=10000"
        "&feed=iex&adjustment=split")
STILL_TRADING_DAYS = 45     # last bar newer than this => index change, not death
MIN_BARS = 60


def alpaca_headers():
    kv = {}
    with open(ENV) as f:
        for line in f:
            line = line.strip()
            if "=" in line and not line.startswith("#"):
                k, v = line.split("=", 1)
                kv[k.strip()] = v.strip().strip("\"'")
    return {"APCA-API-KEY-ID": kv["ALPACA_KEY"],
            "APCA-API-SECRET-KEY": kv["ALPACA_SECRET"]}


def removed_tickers():
    import pandas as pd
    req = urllib.request.Request(WIKI, headers={"User-Agent": UA})
    html = urllib.request.urlopen(req, timeout=60).read().decode("utf-8", "replace")
    tables = pd.read_html(io.StringIO(html))
    ch = tables[1]
    ch.columns = ["date", "add_tkr", "add_sec", "rm_tkr", "rm_sec", "reason"]
    ch = ch.dropna(subset=["rm_tkr"])
    out = {}
    for _, row in ch.iterrows():
        t = str(row["rm_tkr"]).strip().upper()
        if not re.fullmatch(r"[A-Z.]{1,6}", t):
            continue
        out.setdefault(t, (str(row["rm_sec"]), str(row["date"])))
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--staging", required=True)
    args = ap.parse_args()

    tickers = removed_tickers()
    print(f"S&P 500 removal events -> {len(tickers)} unique tickers", flush=True)

    h = alpaca_headers()
    now = datetime.now(timezone.utc)
    end = now.strftime("%Y-%m-%d")

    db = sqlite3.connect(args.staging)
    db.executescript("""
        CREATE TABLE IF NOT EXISTS delisted_symbol(
            symbol TEXT PRIMARY KEY, name TEXT, exchange TEXT,
            first_bar TEXT, last_bar TEXT, n_bars INT, last_close REAL,
            reused INT DEFAULT 0, cohort TEXT);
        CREATE TABLE IF NOT EXISTS delisted_bar(
            symbol TEXT, ts INT, open REAL, high REAL, low REAL,
            close REAL, volume REAL, PRIMARY KEY(symbol, ts));
    """)
    have = {r[0] for r in db.execute("SELECT symbol FROM delisted_symbol")}

    added = still = nodata = short = 0
    added_bars = 0
    examples = []

    for i, (sym, (name, when)) in enumerate(sorted(tickers.items()), 1):
        if sym in have:
            continue
        bars = []
        try:
            req = urllib.request.Request(BARS.format(sym=sym, end=end), headers=h)
            with urllib.request.urlopen(req, timeout=60) as r:
                bars = json.load(r).get("bars") or []
        except Exception:
            pass
        time.sleep(0.32)

        if not bars:
            nodata += 1
            continue
        last = datetime.fromisoformat(bars[-1]["t"].replace("Z", "+00:00"))
        if (now - last).days < STILL_TRADING_DAYS:
            still += 1                      # index removal, company alive
            continue
        if len(bars) < MIN_BARS:
            short += 1
            continue

        closes = [b["c"] for b in bars]
        cohort = "collapse" if closes[-1] < 0.2 * max(closes) else "ordinary"
        db.execute(
            "INSERT OR REPLACE INTO delisted_symbol VALUES(?,?,?,?,?,?,?,?,?)",
            (sym, name, "SP500-REMOVAL", bars[0]["t"][:10], bars[-1]["t"][:10],
             len(bars), closes[-1], 0, cohort))
        db.executemany(
            "INSERT OR REPLACE INTO delisted_bar VALUES(?,?,?,?,?,?,?)",
            [(sym, int(datetime.fromisoformat(
                b["t"].replace("Z", "+00:00")).timestamp()),
              b["o"], b["h"], b["l"], b["c"], b["v"]) for b in bars])
        added += 1
        added_bars += len(bars)
        if cohort == "collapse" or sym in ("FRC", "SIVB", "TWTR", "ATVI"):
            examples.append(f"{sym} {name[:28]} peak={max(closes):.2f} "
                            f"last={closes[-1]:.2f} ({100*(closes[-1]/max(closes)-1):+.0f}%)")
        if i % 50 == 0:
            db.commit()
            print(f"  {i}/{len(tickers)} added={added} still-trading={still} "
                  f"no-data={nodata}", flush=True)

    db.commit()
    report = {
        "tickers_considered": len(tickers),
        "added_delistings": added,
        "added_bars": added_bars,
        "still_trading_index_change_only": still,
        "no_alpaca_data": nodata,
        "too_short": short,
        "examples": examples[:15],
        "note": "Staging only. Production untouched.",
    }
    print("\n" + json.dumps(report, indent=2))
    db.close()


if __name__ == "__main__":
    main()
