"""Fetch daily bars for DELISTED US equities into a staging DB.

The survivorship fix. The production universe holds 1,077 symbols of which only
21 ever stopped trading -- a survivor-seeded population. Alpaca's asset list
carries ~19k inactive securities and its bars endpoint still serves their
history, so the companies that died are recoverable for free.

Discipline (mirrors tools/backfill_delistings.py):
  * NEVER writes to the production database. Output is a standalone staging DB
    plus a JSON report, for review before any merge.
  * Records the last bar date as delisted_at evidence, rather than trusting an
    external claim.
  * TICKER-REUSE GUARD: a symbol whose last bar is recent is almost certainly a
    reassigned ticker, not a live delisting. Those are flagged, not merged.
    (Observed: SBNY, seized 2023-03, still prints bars into 2025.)

Usage:
  python fetch_delisted.py [--limit N] [--out PATH]
"""
import argparse
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

ENV = r"C:\Users\Nicholas_N\Desktop\claude code\stock-trader\.env"
ASSETS = "https://paper-api.alpaca.markets/v2/assets?status=inactive&asset_class=us_equity"
# adjustment MUST match daemon/internal/ingest/alpaca/client.go's barAdjustment.
# This requested "all" (split PLUS dividends) while the live backfill requested
# "split", so every symbol imported through this staging path carried a different
# price convention from everything else in the bars table -- and a symbol fed by
# both paths gets a seam that reads like a real move. The Go side has a test
# (TestAdjustmentModesAgree) that reads THIS file to keep the two in step.
BARS = ("https://data.alpaca.markets/v2/stocks/{sym}/bars"
        "?timeframe=1Day&start=2020-01-01&end={end}&limit=10000"
        "&feed=iex&adjustment=split")
EXCHANGES = {"NASDAQ", "NYSE", "AMEX", "ARCA", "BATS"}

# A symbol still printing bars this recently is a live ticker, not a delisting.
REUSE_GUARD_DAYS = 45
RATE_SLEEP = 0.32          # ~190 req/min, under Alpaca's 200/min free tier


def headers():
    kv = {}
    with open(ENV) as f:
        for line in f:
            line = line.strip()
            if "=" in line and not line.startswith("#"):
                k, v = line.split("=", 1)
                kv[k.strip()] = v.strip().strip("\"'")
    if not kv.get("ALPACA_KEY") or not kv.get("ALPACA_SECRET"):
        sys.exit("ALPACA_KEY / ALPACA_SECRET not found")
    return {"APCA-API-KEY-ID": kv["ALPACA_KEY"],
            "APCA-API-SECRET-KEY": kv["ALPACA_SECRET"]}


def get(url, h, tries=3):
    for i in range(tries):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers=h), timeout=60) as r:
                return json.load(r)
        except urllib.error.HTTPError as e:
            if e.code == 429:                      # rate limited: back off
                time.sleep(2 ** i)
                continue
            if e.code in (403, 404):
                return None
            raise
        except Exception:
            if i == tries - 1:
                return None
            time.sleep(1 + i)
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--limit", type=int, default=0, help="cap symbols (0 = all)")
    ap.add_argument("--out", default="delisted_staging.db")
    args = ap.parse_args()

    h = headers()
    today = datetime.now(timezone.utc)
    end = today.strftime("%Y-%m-%d")

    assets = get(ASSETS, h) or []
    cand = [a for a in assets
            if a.get("exchange") in EXCHANGES
            and a.get("symbol", "").isalpha() and len(a["symbol"]) <= 5]
    cand.sort(key=lambda a: a["symbol"])
    if args.limit:
        cand = cand[:args.limit]
    print(f"inactive assets: {len(assets):,} -> exchange-listed candidates: {len(cand):,}",
          flush=True)

    db = sqlite3.connect(args.out)
    db.executescript("""
        CREATE TABLE IF NOT EXISTS delisted_symbol(
            symbol TEXT PRIMARY KEY, name TEXT, exchange TEXT,
            first_bar TEXT, last_bar TEXT, n_bars INT, last_close REAL,
            reused INT DEFAULT 0);
        CREATE TABLE IF NOT EXISTS delisted_bar(
            symbol TEXT, ts INT, open REAL, high REAL, low REAL,
            close REAL, volume REAL, PRIMARY KEY(symbol, ts));
    """)

    kept = skipped = reused = padded = 0
    total_bars = 0
    t0 = time.time()

    for i, a in enumerate(cand, 1):
        sym = a["symbol"]
        d = get(BARS.format(sym=sym, end=end), h)
        time.sleep(RATE_SLEEP)
        bars = (d or {}).get("bars") or []
        # Drop vendor pads BEFORE anything else reads this list.
        #
        # The endpoint is queried with timeframe=1Day, so every bar here is daily,
        # and a daily bar with volume 0 and open=high=low=close is the vendor
        # fabricating a session its feed never saw. A US-listed equity that trades
        # zero shares produces no bar at all.
        #
        # Filtering HERE rather than at insert time fixes two things at once. The
        # pads never reach delisted_bar (sdmaint import-delisted copies that table
        # verbatim into production), AND bars[-1] below becomes the last REAL bar
        # — which matters because importdelisted.go derives delisted_at from it,
        # so a padded tail was inventing the delisting date. PPEM ended up stamped
        # 2026-06-08 on a series that was 97% synthetic.
        n_pads = sum(1 for b in bars
                     if b["v"] == 0 and b["o"] == b["h"] == b["l"] == b["c"])
        if n_pads:
            bars = [b for b in bars
                    if not (b["v"] == 0 and b["o"] == b["h"] == b["l"] == b["c"])]
            padded += n_pads
        if not bars:
            skipped += 1
        else:
            last = datetime.fromisoformat(bars[-1]["t"].replace("Z", "+00:00"))
            is_reused = (today - last).days < REUSE_GUARD_DAYS
            db.execute(
                "INSERT OR REPLACE INTO delisted_symbol VALUES(?,?,?,?,?,?,?,?)",
                (sym, a.get("name", ""), a["exchange"], bars[0]["t"][:10],
                 bars[-1]["t"][:10], len(bars), bars[-1]["c"], int(is_reused)))
            if is_reused:
                reused += 1                        # recorded, but not a delisting
            else:
                db.executemany(
                    "INSERT OR REPLACE INTO delisted_bar VALUES(?,?,?,?,?,?,?)",
                    [(sym, int(datetime.fromisoformat(
                        b["t"].replace("Z", "+00:00")).timestamp()),
                      b["o"], b["h"], b["l"], b["c"], b["v"]) for b in bars])
                kept += 1
                total_bars += len(bars)
        if i % 100 == 0:
            db.commit()
            print(f"  {i}/{len(cand)}  kept={kept} reused={reused} "
                  f"no-data={skipped} pads-dropped={padded:,} bars={total_bars:,} "
                  f"({time.time()-t0:.0f}s)", flush=True)

    db.commit()

    years = {}
    for (lb,) in db.execute(
            "SELECT last_bar FROM delisted_symbol WHERE reused=0"):
        years[lb[:4]] = years.get(lb[:4], 0) + 1

    report = {
        "generated_utc": today.isoformat(),
        "candidates": len(cand),
        "delisted_kept": kept,
        "ticker_reuse_flagged": reused,
        "no_data": skipped,
        "bars": total_bars,
        "delistings_by_year": dict(sorted(years.items())),
        "staging_db": args.out,
        "note": "Staging only. Nothing written to the production database.",
    }
    with open("delisted_report.json", "w") as f:
        json.dump(report, f, indent=2)

    print("\n" + json.dumps(report, indent=2))
    db.close()


if __name__ == "__main__":
    main()
