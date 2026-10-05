"""Close the 2023-2026 delisting gap from SEC Form 25 filings.

fetch_delisted.py recovered 648 usable delistings from Alpaca's inactive asset
list, but 87% of them landed in 2021-2022 (the de-SPAC wave). Training on that
alone teaches "delisting == short-lived 2021 SPAC", which is a shortcut, not the
bankruptcy signal we want.

Form 25 / 25-NSE is the exchange's "Notification of Removal from Listing"
filing -- the official, free, complete record of US delistings. This tool reads
EDGAR's quarterly form index, extracts every Form 25, maps CIK to ticker, and
then pulls bars from Alpaca for any delisting we do not already hold.

WHY company_tickers.json ALONE COULD NEVER WORK
-----------------------------------------------
company_tickers.json is SEC's map of CURRENTLY-LISTED companies. A Form 25
filer is, by definition, a company being removed from listing -- so it is
absent from that file by construction. The first version of this tool used it
as the only resolver and lost 592 of 841 CIKs (70%). data.sec.gov's
submissions JSON does not rescue them either: measured on 20 sampled delisted
CIKs, its "tickers" array was empty 20/20 and "exchanges" empty 20/20, because
it is fed from the same current-listings table. "formerNames" carries prior
company NAMES, never a symbol; the Form 25 document itself carries issuer name,
CIK and file number but no ticker; and the XBRL companyconcept API returns 404
for dei:TradingSymbol (it does not serve string facts).

What does work is the issuer's own paperwork: EDGAR renders dei:TradingSymbol
onto the cover page (R1.htm) of every inline-XBRL filing, and a dead company's
last 8-K still names the symbol it was trading under. submissions JSON is used
only as an INDEX of those filings, never as a source of tickers. Measured over
all 445 unique CIKs in 2024 H1: 130 resolve from company_tickers.json, 250 more
from the R1 route, 65 residual -- and 43 of those 65 are fund/ETF trusts, 15
are debt/LP co-issuers and 5 are the exchanges themselves, none of which ever
had an equity ticker to find.

Caveats recorded rather than smoothed over:
  * The Form 25 FILING date precedes actual removal by ~10 days. Where we have
    bars, the last bar is better evidence and is preferred.
  * Not every Form 25 is a company death -- share-class consolidations and
    voluntary exchange transfers file one too. 150 of 378 resolved 2024-H1
    tickers are still listed today.
  * Alpaca pads a dead security with flat zero-volume carry-forward bars, so
    bars[-1] lies about the date of death. Only volume-bearing bars count.
  * R1.htm is a rendered artifact of EDGAR's viewer, not a documented API. It
    held on ~600 fetches with zero failures, but if SEC changes the markup
    SYM_RE stops matching. test_form25_resolve.py fails loudly if that happens.

Read-only with respect to the production database -- it is never opened. All
writes go to the staging DB passed via --staging, for review before any merge.

Usage:
  python fetch_form25.py --staging <path> [--from-year 2023] [--to-year 2026]
"""
import argparse
import gzip
import json
import os
import re
import sqlite3
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

ENV = r"C:\Users\Nicholas_N\Desktop\claude code\stock-trader\.env"
UA = "SignalDeck Research (dimples.n3fam@gmail.com)"   # SEC requires a real contact
IDX = "https://www.sec.gov/Archives/edgar/full-index/{y}/QTR{q}/form.idx"
TICKERS = "https://www.sec.gov/files/company_tickers.json"
SUBS = "https://data.sec.gov/submissions/CIK{cik:010d}.json"
R1 = "https://www.sec.gov/Archives/edgar/data/{cik}/{acc}/R1.htm"
# adjustment MUST match daemon/internal/ingest/alpaca/client.go's barAdjustment.
# "all" is split PLUS dividends; the live backfill requests "split". Two
# conventions in one bars column give a symbol fed by both paths a seam that
# reads exactly like a real move. TestAdjustmentModesAgree reads every fetcher
# in this directory, so a new one cannot drift either.
BARS = ("https://data.alpaca.markets/v2/stocks/{sym}/bars"
        "?timeframe=1Day&start=2020-01-01&end={end}&limit=10000"
        "&feed=iex&adjustment=split")
SEC_SLEEP = 0.15    # SEC asks for <=10 req/s; this is well under
ALPACA_SLEEP = 0.32
CACHE = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".cache_edgar")

# Cover-page forms that carry dei:TradingSymbol. Fund forms (485BPOS, N-CSR,
# 497) are excluded ON PURPOSE: a fund trust files one Form 25 per ETF share
# class, and its cover page names one arbitrary member fund -- Exchange Listed
# Funds Trust resolved to "TDSB", which is not the delisted security.
COVER_FORMS = {"8-K", "8-K/A", "10-K", "10-K/A", "10-Q", "10-Q/A",
               "20-F", "20-F/A", "40-F", "6-K", "10-KT", "10-QT"}
R1_BUDGET = 10          # 4 was not enough: Sonic Foundry needed the 7th filing back

SYM_RE = re.compile(
    r"defref_dei_TradingSymbol[^>]*>\s*Trading Symbol\s*</a>\s*</td>\s*"
    r"<td[^>]*>\s*([A-Za-z0-9.\-/]+)", re.S)
EXCH_RE = re.compile(
    r"defref_dei_SecurityExchangeName[^>]*>[^<]*</a>\s*</td>\s*"
    r"<td[^>]*>\s*([A-Za-z ]+?)\s*<", re.S)
# A suspended registrant's later covers say "N/A" -- Agile Therapeutics' three
# most recent covers do, and only the 2024-03-25 8-K still says AGRX.
NO_SYMBOL = {"NONE", "N", "NA", "N/A"}


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
    # No Host header: it was hardcoded to www.sec.gov, which broke every
    # data.sec.gov call. urllib derives the right one from the URL.
    req = urllib.request.Request(url, headers={
        "User-Agent": UA, "Accept-Encoding": "gzip"})
    with urllib.request.urlopen(req, timeout=120) as r:
        data = r.read()
        if r.headers.get("Content-Encoding") == "gzip":
            data = gzip.decompress(data)
    return data if binary else data.decode("utf-8", "replace")


def cache_read(name):
    """EDGAR history is append-only, so a cached past response never goes
    stale. The one exception -- the in-progress quarter's form.idx, which grows
    daily -- is never written here; see form25_filings()."""
    p = os.path.join(CACHE, name)
    if not os.path.exists(p):
        return None
    with open(p, encoding="utf-8") as f:
        return f.read()


def cache_write(name, text):
    os.makedirs(CACHE, exist_ok=True)
    with open(os.path.join(CACHE, name), "w", encoding="utf-8") as f:
        f.write(text)


def cik_to_ticker():
    """SEC's official CIK -> ticker map, plus the set of tickers listed TODAY.

    The second return value is the ticker-reuse guard and it is free: it is the
    same file, already parsed.
    """
    raw = json.loads(sec_get(TICKERS))
    out, listed = {}, set()
    for row in raw.values():
        out.setdefault(int(row["cik_str"]), row["ticker"])
        listed.add(row["ticker"].upper())
    return out, listed


def submissions(cik):
    """Raw submissions JSON text, disk-cached so a re-run costs SEC nothing."""
    name = f"sub_{cik}.json"
    text = cache_read(name)
    if text is None:
        text = sec_get(SUBS.format(cik=cik))
        cache_write(name, text)
        time.sleep(SEC_SLEEP)
    return text


def ticker_from_edgar(cik, filed):
    """Recover a DELISTED issuer's ticker from its own last cover page.

    See the module docstring for why every cheaper route fails. submissions is
    used purely as an index of filings; the ticker comes from dei:TradingSymbol
    as EDGAR renders it into R1.htm.

    Only filings FILED ON OR BEFORE the Form 25 are considered. A company that
    drops to OTC re-tickers, and the newest cover carries the NEW symbol: Casa
    Systems' latest cover says CASSQ, but CASA is the Nasdaq listing whose bars
    we want. Newest-first over the whole history disagreed with newest-before-
    Form-25 on 3 of 20 sampled CIKs, and was wrong on all 3.

    Returns (ticker, exchange) or (None, None). Costs 1 + <=R1_BUDGET requests,
    zero on a cache hit.
    """
    key = f"res_{cik}_{filed}.json"
    hit = cache_read(key)
    if hit is not None:
        d = json.loads(hit)
        return d["t"], d["x"]

    try:
        r = json.loads(submissions(cik))["filings"]["recent"]
        cand = sorted(
            ((r["filingDate"][i], r["form"][i], r["accessionNumber"][i])
             for i in range(len(r["form"]))
             if r["isInlineXBRL"][i] == 1
             and r["form"][i] in COVER_FORMS
             and r["filingDate"][i] <= filed),
            reverse=True)
    except Exception:
        return None, None       # network/parse failure: not cached, retry next run

    t = x = None
    fetch_failed = False
    for _date, _form, acc in cand[:R1_BUDGET]:
        try:
            html = sec_get(R1.format(cik=cik, acc=acc.replace("-", "")))
        except Exception:
            # A fetch that never returned proves nothing about this CIK. SEC
            # throttles with 403, which lands here, so treating this as "no
            # ticker" would let one rate-limit burst write a permanent negative
            # for every CIK in it — and the cache never expires, so those CIKs
            # would be silently lost from every future run with nothing in the
            # report to show it. Remember it and refuse to cache below.
            fetch_failed = True
            time.sleep(SEC_SLEEP)
            continue
        time.sleep(SEC_SLEEP)
        m = SYM_RE.search(html)
        if m and m.group(1).upper() not in NO_SYMBOL:
            e = EXCH_RE.search(html)
            t, x = m.group(1).upper(), (e.group(1).strip() if e else "")
            break
    # Cache a HIT always (it is immutable — a filed cover page cannot change),
    # and a MISS only when every candidate was actually read and none named a
    # ticker. That is a structural fact about the filings; a transport failure
    # is not.
    if t is not None or not fetch_failed:
        cache_write(key, json.dumps({"t": t, "x": x}))
    return t, x


def trades_after_delisting(bars, filed, grace_days=30):
    """True when this ticker was still really trading long after its Form 25.

    The second half of the ticker-reuse guard, and the half that catches funds.
    A delisted security's tape stops within days of the filing; a ticker that is
    still printing VOLUME a month later is a different security wearing the same
    symbol. Grace is 30 days so a settlement tail, a late final print, or a
    filing that precedes the actual last trade cannot trip it -- the five
    measured leaks ran 589-755 days past their filing, nowhere near the edge.

    Volume-bearing bars only: Alpaca pads dead securities with flat zero-volume
    carry-forward rows, and those would make every delisting look like it never
    stopped trading.
    """
    live = live_bars(bars)
    if not live:
        return False
    try:
        filed_d = datetime.fromisoformat(filed).date()
        last_d = datetime.fromisoformat(live[-1]["t"][:10]).date()
    except (ValueError, TypeError, KeyError):
        # An unparseable date must not silently disarm the guard, but it also
        # must not invent a reuse. Refusing to judge is the honest answer; the
        # first guard still applies.
        return False
    return (last_d - filed_d).days > grace_days


def has_spliced_gap(bars, max_gap_days=90, max_step=0.5):
    """True when the series jumps across a long hole — two securities, one row.

    The third guard, for the case the other two structurally cannot see: a
    ticker recycled onto a company that ALSO later delisted. Its last bar sits
    near its own Form 25, so trades_after_delisting() is silent, and neither
    issuer is listed today, so company_tickers.json is silent. What gives it
    away is inside the series: a months-long hole with a different price on the
    far side. Measured on the 2023-2026 population, 10 of 1,231 kept symbols —
    RDUS is Radius Global Infrastructure at 9.42 followed by Radius Recycling at
    30.83 (+227%), ALTM is Altus Midstream at 62.34 followed by Arcadium Lithium
    at 6.80 (-89%).

    BOTH conditions are required, because the gap alone is not evidence. 108 of
    1,231 have a >90d hole and most are SPAC units that simply did not trade for
    a year — MLACU sits at 10.46 before its 562-day gap and 10.00 after. One
    thinly-traded security, not two, and excluding it would throw away a real
    delisting. The price step is what separates them.

    Cause-agnostic on purpose: a >50% step across a >90-day hole is untrustworthy
    whether it is ticker reuse, an unadjusted split, or a vendor error. None of
    the three belongs in a return series.
    """
    live = live_bars(bars)
    for a, b in zip(live, live[1:]):
        try:
            gap = (datetime.fromisoformat(b["t"][:10]).date()
                   - datetime.fromisoformat(a["t"][:10]).date()).days
        except (ValueError, TypeError, KeyError):
            continue
        if gap > max_gap_days and a.get("c") and abs(b["c"] / a["c"] - 1) > max_step:
            return True
    return False


def live_bars(bars):
    """Alpaca carries a dead security forward with flat, zero-volume bars --
    SBNY prints close=70 v=0 for 201 sessions after Signature Bank was seized,
    two years past its last real trade -- so bars[-1] is not the last trade and
    a naive last-bar rule mis-dates or discards the delisting. 75 of 191
    recoverable 2024-H1 delistings (39%) turn on this. The padding must not
    reach the training set either, so this list is what gets stored.

    A bar must also carry a PRICE. Alpaca returns volume-bearing bars with OHLC
    all zero for some pre-merger SPAC shells -- LeddarTech (LDTC) has 373 of
    them from 2021-03 to 2023-12, with real volume and close=0.0, and its
    genuine series only starts at 6.08 on 2023-12-22. A zero close is not a
    trade at zero; it is an absent price, and it detonates any return
    computation that divides by the previous close.
    """
    return [b for b in bars if b["v"] > 0 and b.get("c", 0) > 0]


def form25_filings(from_year, to_year):
    """Every Form 25 / 25-NSE in EDGAR's quarterly index, streamed."""
    now = datetime.now(timezone.utc)
    cur_q = (now.month - 1) // 3 + 1
    seen = []
    for y in range(from_year, to_year + 1):
        for q in (1, 2, 3, 4):
            if y == now.year and q > cur_q:
                continue
            # 2024 QTR1's index alone is 57,767,881 bytes; a 2023-2026 run
            # pulls 15 of them. Past quarters are immutable, so cache them --
            # this is the single largest cost in the tool. The in-progress
            # quarter grows daily and is always re-fetched.
            name = f"form_{y}_Q{q}.idx"
            text = None if (y == now.year and q == cur_q) else cache_read(name)
            if text is None:
                try:
                    text = sec_get(IDX.format(y=y, q=q))
                except urllib.error.HTTPError as e:
                    print(f"  {y} QTR{q}: HTTP {e.code}, skipped", flush=True)
                    continue
                if not (y == now.year and q == cur_q):
                    cache_write(name, text)
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
    c2t, listed_today = cik_to_ticker()
    print(f"  {len(c2t):,} CIKs mapped, {len(listed_today):,} tickers listed today",
          flush=True)

    print(f"scanning EDGAR form index {args.from_year}-{args.to_year}...", flush=True)
    filings = form25_filings(args.from_year, args.to_year)
    print(f"total Form 25 filings: {len(filings):,}", flush=True)

    # earliest filing per CIK is the delisting event
    by_cik = {}
    for cik, company, date in filings:
        if cik not in by_cik or date < by_cik[cik][1]:
            by_cik[cik] = (company, date)

    print(f"resolving {len(by_cik):,} CIKs...", flush=True)
    resolved, via_map, via_edgar, unresolved = {}, 0, 0, 0
    t0 = time.time()
    for i, (cik, (company, date)) in enumerate(sorted(by_cik.items()), 1):
        t = c2t.get(cik)                      # free, but only ever hits survivors
        if t:
            via_map += 1
        else:
            t = ticker_from_edgar(cik, date)[0]
            via_edgar += 1 if t else 0
        if t:
            resolved[t.upper()] = (company, date)
        else:
            unresolved += 1
        if i % 50 == 0:
            print(f"  {i}/{len(by_cik)} map={via_map} edgar={via_edgar} "
                  f"unresolved={unresolved} ({time.time()-t0:.0f}s)", flush=True)
    print(f"resolved to tickers: {len(resolved):,} "
          f"(company_tickers {via_map:,} + EDGAR R1 {via_edgar:,})  "
          f"unresolved CIKs: {unresolved:,}", flush=True)

    db = sqlite3.connect(args.staging)
    # form25 is a staging REPORT table, rebuilt each run -- it gained a verdict
    # column, and a stale row from a previous schema would be unreadable.
    # delisted_symbol / delisted_bar accumulate and are only created if this is
    # a fresh staging DB (normally fetch_delisted.py made them first).
    db.executescript("""
        DROP TABLE IF EXISTS form25;
        CREATE TABLE form25(
            symbol TEXT PRIMARY KEY, company TEXT, filed TEXT,
            had_bars INT DEFAULT 0, n_bars INT DEFAULT 0, last_bar TEXT,
            verdict TEXT);
        CREATE TABLE IF NOT EXISTS delisted_symbol(
            symbol TEXT PRIMARY KEY, name TEXT, exchange TEXT,
            first_bar TEXT, last_bar TEXT, n_bars INT, last_close REAL,
            reused INT DEFAULT 0);
        CREATE TABLE IF NOT EXISTS delisted_bar(
            symbol TEXT, ts INT, open REAL, high REAL, low REAL,
            close REAL, volume REAL, PRIMARY KEY(symbol, ts));
    """)
    have = {r[0] for r in db.execute("SELECT symbol FROM delisted_symbol")}
    missing = sorted(set(resolved) - have)
    print(f"already in staging: {len(set(resolved) & have):,}   "
          f"NEW to fetch: {len(missing):,}", flush=True)

    h = alpaca_headers()
    end = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    added = added_bars = nodata = still_listed = thin = spliced = 0

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

        if not bars:
            db.execute("INSERT OR REPLACE INTO form25 VALUES(?,?,?,?,?,?,?)",
                       (sym, company, filed, 0, 0, "", "no-bars"))
            nodata += 1
        elif sym in listed_today or trades_after_delisting(bars, filed):
            # TWO reuse guards, because either one alone leaks.
            #
            # company_tickers.json catches a ticker recycled onto another
            # REGISTRANT (DOC: Physicians Realty delisted 2024-03, Healthpeak
            # took the symbol). It also covers the common non-death case, a
            # Form 25 for warrants/units/notes or an exchange change, which is
            # 150 of 378 resolved 2024-H1 tickers.
            #
            # But that file is a REGISTRANT->ticker map: it contains no ETFs and
            # no closed-end funds. Measured, 6,208 of Alpaca's 13,324 active
            # tradable symbols are absent from it -- and funds are the dominant
            # reuser of a freed 3-4 letter ticker. Relying on it alone let five
            # symbols through whose series splice a dead company onto a live
            # ETF: HLTH ran Cue Health to close=0.0502 on 2024-06-05, then a
            # 778-day gap, then close=25.40 (Tema Healthcare AI ETF) -- a single
            # step of +50,498% written straight into the training set. CONX
            # +149% over 565 days, EGLE -55% over 512, GRIN +79% over 354.
            #
            # So the second guard is the bars themselves, and it is the one the
            # earlier version of this tool had. An earlier note here claimed no
            # bar threshold could separate reuse from a clean delisting because
            # "the largest inter-bar gap in a clean delisting was 14 days and
            # DOC's recycle shows a gap of 4". That is true of the GAP but false
            # of the test that matters: what separates them is trading that
            # continues WELL PAST THE FORM 25 DATE. All five leaks print 589-755
            # days after their filing; a genuine delisting stops within days of
            # it. DOC is still caught by the first guard, which is what that
            # guard is for.
            db.execute("INSERT OR REPLACE INTO form25 VALUES(?,?,?,?,?,?,?)",
                       (sym, company, filed, 1, len(bars), "", "still-listed"))
            still_listed += 1
        else:
            live = live_bars(bars)
            if len(live) < 60:
                db.execute("INSERT OR REPLACE INTO form25 VALUES(?,?,?,?,?,?,?)",
                           (sym, company, filed, 1, len(live), "", "too-few-bars"))
                thin += 1
            elif has_spliced_gap(bars):
                # Two securities on one ticker where BOTH eventually delisted,
                # so neither of the other guards can see it. Recorded with its
                # own verdict rather than folded into still-listed: these are
                # real delistings we are declining to import because the series
                # cannot be trusted, and that is a different fact.
                db.execute("INSERT OR REPLACE INTO form25 VALUES(?,?,?,?,?,?,?)",
                           (sym, company, filed, 1, len(live), live[-1]["t"][:10],
                            "spliced-series"))
                spliced += 1
            else:
                last = live[-1]["t"][:10]
                db.execute("INSERT OR REPLACE INTO form25 VALUES(?,?,?,?,?,?,?)",
                           (sym, company, filed, 1, len(live), last, "kept"))
                db.executemany(
                    "INSERT OR REPLACE INTO delisted_bar VALUES(?,?,?,?,?,?,?)",
                    [(sym, int(datetime.fromisoformat(
                        b["t"].replace("Z", "+00:00")).timestamp()),
                      b["o"], b["h"], b["l"], b["c"], b["v"]) for b in live])
                db.execute(
                    "INSERT OR REPLACE INTO delisted_symbol VALUES(?,?,?,?,?,?,?,?)",
                    (sym, company, "SEC-FORM25", live[0]["t"][:10], last,
                     len(live), live[-1]["c"], 0))
                added += 1
                added_bars += len(live)

        if i % 100 == 0:
            db.commit()
            print(f"  {i}/{len(missing)} added={added} still-listed={still_listed} "
                  f"thin={thin} no-data={nodata} bars={added_bars:,}", flush=True)

    db.commit()
    years = {}
    for (lb,) in db.execute(
            "SELECT last_bar FROM delisted_symbol WHERE reused=0 AND n_bars>=60"):
        years[lb[:4]] = years.get(lb[:4], 0) + 1

    report = {
        "form25_filings": len(filings),
        "unique_ciks": len(by_cik),
        "resolved_tickers": len(resolved),
        "resolved_via_company_tickers": via_map,
        "resolved_via_edgar_r1": via_edgar,
        "unresolved_ciks": unresolved,
        "new_fetched": len(missing),
        "added_delistings": added,
        "added_bars": added_bars,
        "skipped_still_listed": still_listed,
        "skipped_too_few_bars": thin,
        "skipped_spliced_series": spliced,
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
