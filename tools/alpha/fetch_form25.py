"""Close the 2023-2026 delisting gap from SEC Form 25 filings.

fetch_delisted.py recovered 648 usable delistings from Alpaca's inactive asset
list, but 87% of them landed in 2021-2022 (the de-SPAC wave). Training on that
alone teaches "delisting == short-lived 2021 SPAC", which is a shortcut, not the
bankruptcy signal we want.

Form 25 / 25-NSE is the exchange's "Notification of Removal from Listing"
filing -- the official, free, complete record of US delistings. This tool reads
EDGAR's quarterly form index, extracts every Form 25, maps CIK to ticker, and
then pulls bars from Alpaca for any delisting we do not already hold.

Caveats recorded rather than smoothed over:
  * The Form 25 FILING date precedes actual removal by ~10 days. Where we have
    bars, the last bar is better evidence and is preferred.
  * Not every Form 25 is a company death -- share-class consolidations and
    voluntary exchange transfers file one too. Bars are the arbiter: a symbol
    still printing after the filing was not removed.
  * CIK->ticker uses SEC's current mapping, so companies that delisted and
    later changed identity may not resolve. Unresolved CIKs are reported.

Read-only with respect to the production database. Writes to the same staging
DB fetch_delisted.py produced.

Usage:
  python fetch_form25.py --staging <path> [--from-year 2023]
"""
import argparse
import gzip
import io
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

ENV = r"C:\Users\Nicholas_N\Desktop\claude code\stock-trader\.env"
UA = "SignalDeck Research (dimples.n3fam@gmail.com)"   # SEC requires a real contact
IDX = "https://www.sec.gov/Archives/edgar/full-index/{y}/QTR{q}/form.idx"
TICKERS = "https://www.sec.gov/files/company_tickers.json"
BARS = ("https://data.alpaca.markets/v2/stocks/{sym}/bars"
        "?timeframe=1Day&start=2020-01-01&end={end}&limit=10000"
        "&feed=iex&adjustment=all")
SEC_SLEEP = 0.15    # SEC asks for <=10 req/s; this is well under
ALPACA_SLEEP = 0.32


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


def sec_get(url, binary=False):
    req = urllib.request.Request(url, headers={
        "User-Agent": UA, "Accept-Encoding": "gzip", "Host": "www.sec.gov"})
    with urllib.request.urlopen(req, timeout=120) as r:
        data = r.read()
        if r.headers.get("Content-Encoding") == "gzip":
            data = gzip.decompress(data)
    return data if binary else data.decode("utf-8", "replace")


def cik_to_ticker():
    """SEC's official CIK -> ticker map."""
    raw = json.loads(sec_get(TICKERS))
    out = {}
    for row in raw.values():
        out.setdefault(int(row["cik_str"]), row["ticker"])
    return out


def form25_filings(from_year, to_year):
    """Every Form 25 / 25-NSE in EDGAR's quarterly index, streamed."""
    now = datetime.now(timezone.utc)
    seen = []
    for y in range(from_year, to_year + 1):
        for q in (1, 2, 3, 4):
            if y == now.year and q > (now.month - 1) // 3 + 1:
                continue
            try:
                text = sec_get(IDX.format(y=y, q=q))
            except urllib.error.HTTPError as e:
                print(f"  {y} QTR{q}: HTTP {e.code}, skipped", flush=True)
                continue
            time.sleep(SEC_SLEEP)
            n = 0
            for line in text.splitlines():
                if not (line.startswith("25 ") or line.startswith("25-NSE ")):
                    continue
                # fixed-width-ish: form, company, cik, date, path
                parts = line.split()
                if len(parts) < 4:
                    continue
                date = parts[-2]
                cik = parts[-3]
                if not cik.isdigit() or len(date) != 10:
                    continue
                company = " ".join(parts[1:-3])
                seen.append((int(cik), company, date))
                n += 1
            print(f"  {y} QTR{q}: {n} Form 25 filings", flush=True)
    return seen


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--staging", required=True)
    ap.add_argument("--from-year", type=int, default=2023)
    ap.add_argument("--to-year", type=int, default=2026)
    args = ap.parse_args()

    print("fetching SEC CIK->ticker map...", flush=True)
    c2t = cik_to_ticker()
    print(f"  {len(c2t):,} CIKs mapped", flush=True)

    print(f"scanning EDGAR form index {args.from_year}-{args.to_year}...", flush=True)
    filings = form25_filings(args.from_year, args.to_year)
    print(f"total Form 25 filings: {len(filings):,}", flush=True)

    # earliest filing per CIK is the delisting event
    by_cik = {}
    for cik, company, date in filings:
        if cik not in by_cik or date < by_cik[cik][1]:
            by_cik[cik] = (company, date)

    resolved, unresolved = {}, 0
    for cik, (company, date) in by_cik.items():
        t = c2t.get(cik)
        if t:
            resolved[t.upper()] = (company, date)
        else:
            unresolved += 1
    print(f"resolved to tickers: {len(resolved):,}  unresolved CIKs: {unresolved:,}")

    db = sqlite3.connect(args.staging)
    db.execute("""CREATE TABLE IF NOT EXISTS form25(
        symbol TEXT PRIMARY KEY, company TEXT, filed TEXT,
        had_bars INT DEFAULT 0, n_bars INT DEFAULT 0, last_bar TEXT)""")
    have = {r[0] for r in db.execute("SELECT symbol FROM delisted_symbol")}
    missing = sorted(set(resolved) - have)
    print(f"already in staging: {len(set(resolved) & have):,}   "
          f"NEW to fetch: {len(missing):,}", flush=True)

    h = alpaca_headers()
    end = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    added = added_bars = nodata = 0

    for i, sym in enumerate(missing, 1):
        company, filed = resolved[sym]
        bars = []
        try:
            req = urllib.request.Request(BARS.format(sym=sym, end=end), headers=h)
            with urllib.request.urlopen(req, timeout=60) as r:
                bars = (json.load(r).get("bars") or [])
        except Exception:
            pass
        time.sleep(ALPACA_SLEEP)

        if bars:
            last = bars[-1]["t"][:10]
            # A symbol still printing well after its Form 25 was not removed --
            # share-class change, transfer, or a reused ticker. Record, skip.
            still_trading = last > filed and (
                datetime.fromisoformat(last) - datetime.fromisoformat(filed)).days > 30
            db.execute("INSERT OR REPLACE INTO form25 VALUES(?,?,?,?,?,?)",
                       (sym, company, filed, 1, len(bars), last))
            if not still_trading and len(bars) >= 60:
                db.executemany(
                    "INSERT OR REPLACE INTO delisted_bar VALUES(?,?,?,?,?,?,?)",
                    [(sym, int(datetime.fromisoformat(
                        b["t"].replace("Z", "+00:00")).timestamp()),
                      b["o"], b["h"], b["l"], b["c"], b["v"]) for b in bars])
                db.execute(
                    "INSERT OR REPLACE INTO delisted_symbol VALUES(?,?,?,?,?,?,?,?)",
                    (sym, company, "SEC-FORM25", bars[0]["t"][:10], last,
                     len(bars), bars[-1]["c"], 0))
                added += 1
                added_bars += len(bars)
        else:
            db.execute("INSERT OR REPLACE INTO form25 VALUES(?,?,?,?,?,?)",
                       (sym, company, filed, 0, 0, ""))
            nodata += 1

        if i % 100 == 0:
            db.commit()
            print(f"  {i}/{len(missing)} added={added} no-data={nodata} "
                  f"bars={added_bars:,}", flush=True)

    db.commit()
    years = {}
    for (lb,) in db.execute(
            "SELECT last_bar FROM delisted_symbol WHERE reused=0 AND n_bars>=60"):
        years[lb[:4]] = years.get(lb[:4], 0) + 1

    report = {
        "form25_filings": len(filings),
        "resolved_tickers": len(resolved),
        "unresolved_ciks": unresolved,
        "new_fetched": len(missing),
        "added_delistings": added,
        "added_bars": added_bars,
        "no_alpaca_data": nodata,
        "delistings_by_year_after_merge": dict(sorted(years.items())),
        "note": "Staging only. Production database untouched.",
    }
    with open("form25_report.json", "w") as f:
        json.dump(report, f, indent=2)
    print("\n" + json.dumps(report, indent=2))
    db.close()


if __name__ == "__main__":
    main()
