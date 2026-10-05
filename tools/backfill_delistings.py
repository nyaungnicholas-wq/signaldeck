#!/usr/bin/env python3
"""Backfill plan for symbols.delisted_at from a PUBLIC delistings source.

Why this exists
---------------
The survivorship wave (2026-07-24) gave the platform a delisted_at column and a
detector, but the detector can only see deaths that happen while the daemon is
watching. Everything that delisted BEFORE 2026 — the entire seeded history —
carries delisted_at=NULL, so store.TradableAt reconstructs a point-in-time
universe that is only point-in-time from the day we started looking. For deep
backtests that is still a survivor-seeded population wearing a survivorship-
free costume. This tool closes that gap with an external, public record of
delistings instead of our own observation window.

Sources (both public):
  * FMP delisted-companies endpoint (--source fmp, needs FMP_API_KEY):
      https://financialmodelingprep.com/api/v3/delisted-companies?page=N
  * A local CSV (--source csv --csv FILE): the FMP CSV export
    (symbol,companyName,exchange,ipoDate,delistedDate) or any two-column
    symbol,YYYY-MM-DD file such as a Nasdaq Trader symbol-directory diff.

Discipline, mirroring accuracy_registry.py:
  * Strictly READ-ONLY (sqlite mode=ro). The single-writer-connection doctrine
    lives in internal/store, so this script never touches the DB — it emits an
    UPDATE PLAN as JSON, and `sdmaint apply-delistings` applies it through
    store.MarkDelisted on the daemon's own writer connection.
  * Only symbols whose delisted_at is currently NULL/0 are planned; the guarded
    UPDATE in MarkDelisted makes application idempotent on top of that.
  * TICKER-REUSE GUARD: a symbol whose newest 1d bar prints AFTER the claimed
    delisting date (+ grace) is a conflict, not an update. Exchanges recycle
    tickers; stamping the old company's death onto the new company's row would
    delete a live name from every point-in-time universe built afterwards.
    Conflicts are reported, never silently applied.
  * Default cutoff --before 2026-01-01: post-2026 deaths are the live
    detector's jurisdiction (it has bar evidence; this source has none).

SURVIVORSHIP BOUND MODE (--survivorship-bound)
  The registry used to note the post-epoch attrition problem and stop there.
  This mode MEASURES it instead of declaring it unmeasurable: pull an external
  delistings record (SEC EDGAR Form 25 / 25-NSE "removal from listing" filings
  by default) for the window since the 2026-07-24 survivorship epoch, count
  tracked symbols that left the universe (external record UNIONed with the live
  detector's own delisted_at stamps) against the symbols actually graded, and
  publish a survivorship_bound field in data/accuracy_registry.json: the
  maximum accuracy inflation, in percentage points, if every dropped symbol had
  kept being graded and been wrong every single time. accuracy_registry.py
  prints the bound beside its design-effect disclosure.

Usage:
  python3 tools/backfill_delistings.py --source fmp --out plan.json
  python3 tools/backfill_delistings.py --source csv --csv delisted.csv --out plan.json
  # then, with the daemon stopped or via the maintenance window:
  ./bin/sdmaint apply-delistings -db data/signaldeck.db -plan plan.json

  # measure the post-epoch survivorship bound and stamp it into the registry:
  python3 tools/backfill_delistings.py --survivorship-bound            # EDGAR
  python3 tools/backfill_delistings.py --survivorship-bound --source csv --csv delisted.csv
"""
from __future__ import annotations

import argparse
import csv
import datetime as dt
import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                          "data", "signaldeck.db")

FMP_URL = "https://financialmodelingprep.com/api/v3/delisted-companies"
FMP_MAX_PAGES = 200          # hard stop; the endpoint pages ~100 rows at a time
FMP_PAGE_PAUSE_S = 0.25      # stay far under the free-tier rate limit

# A "delisted" name that keeps printing daily bars this long after its claimed
# death date is a ticker reuse or a relisting — a conflict, never an update.
REUSE_GRACE_DAYS = 7

# --- survivorship-bound mode ------------------------------------------------
# Mirrors accuracy_registry.SURVIVORSHIP_EPOCH: the day symbols.delisted_at
# started being recorded. Everything the registry grades starts here.
SURVIVORSHIP_EPOCH = "2026-07-24"
DEFAULT_REGISTRY = os.path.join(os.path.dirname(DEFAULT_DB), "accuracy_registry.json")

# SEC EDGAR: Form 25 / 25-NSE = notification of removal from listing, filed
# per issuer; the quarterly form index plus the CIK->ticker map turns those
# filings into a ticker-level delistings record with no API key.
EDGAR_FORM_IDX_URL = "https://www.sec.gov/Archives/edgar/full-index/{year}/QTR{qtr}/form.idx"
EDGAR_TICKER_MAP_URL = "https://www.sec.gov/files/company_tickers.json"
# SEC fair-access policy wants a descriptive UA with a contact.
EDGAR_UA = "signaldeck-survivorship-audit/1.0 (" + \
    os.environ.get("EDGAR_CONTACT", "ops@signaldeck.local") + ")"
EDGAR_FORMS = {"25", "25/A", "25-NSE", "25-NSE/A"}


def parse_date(s: str) -> int | None:
    """YYYY-MM-DD → unix seconds at 00:00 UTC, or None if unparseable."""
    s = (s or "").strip()
    for fmt in ("%Y-%m-%d", "%m/%d/%Y"):
        try:
            d = dt.datetime.strptime(s, fmt).replace(tzinfo=dt.timezone.utc)
            return int(d.timestamp())
        except ValueError:
            continue
    return None


def fetch_fmp(api_key: str) -> list[dict]:
    """Page through the FMP delisted-companies endpoint. Returns raw rows."""
    rows: list[dict] = []
    for page in range(FMP_MAX_PAGES):
        q = urllib.parse.urlencode({"page": page, "apikey": api_key})
        req = urllib.request.Request(f"{FMP_URL}?{q}",
                                     headers={"User-Agent": "signaldeck-backfill/1.0"})
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                batch = json.load(resp)
        except (urllib.error.URLError, json.JSONDecodeError) as e:
            print(f"FMP page {page} failed: {e}", file=sys.stderr)
            break
        if not isinstance(batch, list) or not batch:
            break
        rows.extend(batch)
        time.sleep(FMP_PAGE_PAUSE_S)
    return rows


def _edgar_get(url: str) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": EDGAR_UA})
    with urllib.request.urlopen(req, timeout=60) as resp:
        return resp.read()


def fetch_edgar_form25(since: dt.date) -> list[dict]:
    """Form 25 / 25-NSE filings since `since`, as FMP-shaped rows.

    A failed download exits instead of returning [] — in bound mode an empty
    list means "zero delistings", and a network error must never be allowed to
    impersonate that claim. Genuinely-empty windows are a legitimate result.

    Known limit, acceptable for the short post-epoch window this mode covers:
    company_tickers.json eventually drops long-dead issuers, so a CIK filing
    Form 25 years ago may no longer map to a ticker. Recent filers still do.
    """
    try:
        tick_raw = json.loads(_edgar_get(EDGAR_TICKER_MAP_URL))
    except (urllib.error.URLError, json.JSONDecodeError) as e:
        sys.exit(f"EDGAR ticker map fetch failed: {e}")
    cik_to_syms: dict[int, list[str]] = {}
    for ent in tick_raw.values():
        cik_to_syms.setdefault(int(ent["cik_str"]), []).append(ent["ticker"].upper())

    rows: list[dict] = []
    today = dt.date.today()
    y, q = since.year, (since.month - 1) // 3 + 1
    while (y, q) <= (today.year, (today.month - 1) // 3 + 1):
        url = EDGAR_FORM_IDX_URL.format(year=y, qtr=q)
        try:
            idx = _edgar_get(url).decode("latin-1")
        except urllib.error.URLError as e:
            sys.exit(f"EDGAR form index fetch failed ({url}): {e} — refusing to "
                     "report a partial window as the whole record")
        in_body = False
        for line in idx.splitlines():
            if not in_body:
                in_body = line.startswith("---")
                continue
            # Company names hold spaces; the tail is fixed: ... CIK DATE FILE.
            parts = line.split()
            if len(parts) < 5 or parts[0] not in EDGAR_FORMS:
                continue
            cik, date_filed = parts[-3], parts[-2]
            if date_filed < since.isoformat():
                continue
            try:
                syms = cik_to_syms.get(int(cik), [])
            except ValueError:
                continue
            name = " ".join(parts[1:-3])
            for s in syms:  # one CIK can carry several share classes
                rows.append({"symbol": s, "companyName": name,
                             "delistedDate": date_filed})
        q += 1
        if q == 5:
            y, q = y + 1, 1
        time.sleep(FMP_PAGE_PAUSE_S)
    return rows


def measure_survivorship_bound(db_path: str, source_rows: list[dict],
                               source_name: str, since: str,
                               registry_path: str) -> dict:
    """Bound the accuracy inflation post-epoch attrition could be hiding.

    Grading only ever sees symbols that survived long enough to be graded. The
    worst case is that every tracked symbol that left the universe since the
    epoch would have kept being graded at the average per-symbol rate and been
    wrong every single time; the bound is how many percentage points headline
    accuracy would fall under that assumption. Dropped symbols that were partly
    graded before dying get a full missing quota anyway — a bound is allowed to
    overstate, never to understate.

    The graded record is counted with the exact independence filters
    tools/accuracy_registry.py uses (dedup to one row per symbol/horizon/UTC-day,
    resolved with prob and up present), so the bound speaks about the same
    numbers the registry publishes.
    """
    since_ts = parse_date(since)
    if since_ts is None:
        sys.exit(f"bad --since date: {since!r}")
    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        tracked = {sym.upper(): delisted for sym, delisted in con.execute(
            "SELECT symbol, COALESCE(delisted_at, 0) FROM symbols WHERE market = 'stocks'")}
        n_dir, h_dir = con.execute("""
            WITH dedup AS (
              SELECT prob, up,
                     ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, ts/86400
                                        ORDER BY ts DESC) rn
              FROM prediction_outcomes
              WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL
                AND ts >= ?
                -- the tracked prequential-majority benchmark ("<horizon>#pm")
                -- shadows every ensemble row; counting it here would double
                -- the denominator and quietly shrink the bound
                AND horizon NOT LIKE '%#pm')
            SELECT COUNT(*),
                   COALESCE(SUM(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END), 0)
            FROM dedup WHERE rn = 1""", (since_ts,)).fetchone()
        n_reg, h_reg = con.execute(
            """SELECT COUNT(*), COALESCE(SUM(CASE WHEN correct = 1 THEN 1 ELSE 0 END), 0)
               FROM regime_outcomes WHERE resolved_at IS NOT NULL AND ts >= ?""",
            (since_ts,)).fetchone()
        graded_symbols = con.execute("""
            SELECT COUNT(*) FROM (
              SELECT symbol_id FROM prediction_outcomes
              WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL
                AND ts >= ?
              UNION
              SELECT symbol_id FROM regime_outcomes
              WHERE resolved_at IS NOT NULL AND ts >= ?)""",
            (since_ts, since_ts)).fetchone()[0]
    finally:
        con.close()

    # Symbols that left the universe: the live detector's own delisted_at stamps
    # UNIONed with the external record. Double-listing is impossible (set union);
    # an external claim on a still-live name only widens the bound — safe side.
    dropped_db = {s for s, d in tracked.items() if d >= since_ts}
    dropped_ext: set[str] = set()
    ext_since = 0
    for row in source_rows:
        ts = parse_date(row.get("delistedDate") or "")
        if ts is None or ts < since_ts:
            continue
        ext_since += 1
        sym = (row.get("symbol") or "").upper()
        if sym in tracked:
            dropped_ext.add(sym)
    dropped = dropped_db | dropped_ext

    n = n_dir + n_reg
    hits = h_dir + h_reg
    bound: dict = {
        "as_of": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "epoch": since,
        "source": source_name,
        "source_delistings_since_epoch": ext_since,
        "symbols_graded": graded_symbols,
        "graded_observations": n,
        "graded_hits": hits,
        "symbols_dropped": len(dropped),
        "dropped_from_db_detector": sorted(dropped_db),
        "dropped_from_external_source": sorted(dropped_ext),
        "method": ("worst case: every dropped symbol keeps being graded at the "
                   "average per-symbol rate (n/symbols_graded observations each) "
                   "and is wrong every time; bound_pp = 100 * (hits/n - "
                   "hits/(n + dropped * n/symbols_graded)). Overstates for "
                   "symbols partly graded before delisting — bounds may "
                   "overstate, never understate."),
    }
    if n <= 0 or graded_symbols <= 0:
        bound["bound_pp"] = None
        bound["reason"] = "no graded post-epoch observations yet — nothing to bound"
    else:
        acc = hits / n
        missing = len(dropped) * (n / graded_symbols)
        worst = hits / (n + missing)
        bound["live_acc"] = acc
        bound["worst_case_acc"] = worst
        bound["bound_pp"] = (acc - worst) * 100.0

    reg: dict = {}
    if os.path.exists(registry_path):
        with open(registry_path, encoding="utf-8") as f:
            reg = json.load(f)
    reg["survivorship_bound"] = bound
    with open(registry_path, "w", encoding="utf-8") as f:
        json.dump(reg, f, indent=1)
    return bound


def load_csv(path: str) -> list[dict]:
    """Load an FMP-export CSV or a bare symbol,date file into FMP-shaped rows."""
    rows: list[dict] = []
    with open(path, newline="", encoding="utf-8-sig") as f:
        sample = f.read(4096)
        f.seek(0)
        has_header = csv.Sniffer().has_header(sample) if sample.strip() else False
        reader = csv.reader(f)
        header: list[str] | None = None
        for i, rec in enumerate(reader):
            if not rec or not rec[0].strip():
                continue
            if i == 0 and has_header:
                header = [c.strip().lower() for c in rec]
                continue
            if header and "symbol" in header:
                m = dict(zip(header, rec))
                rows.append({"symbol": m.get("symbol", "").strip(),
                             "companyName": m.get("companyname", "").strip(),
                             "delistedDate": (m.get("delisteddate")
                                              or m.get("date") or "").strip()})
            else:  # bare symbol,date
                rows.append({"symbol": rec[0].strip(), "companyName": "",
                             "delistedDate": rec[1].strip() if len(rec) > 1 else ""})
    return rows


def build_plan(db_path: str, source_rows: list[dict], source_name: str,
               before_ts: int) -> dict:
    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    try:
        symbols = {r["symbol"].upper(): r for r in con.execute(
            """SELECT sy.id, sy.symbol, sy.name, sy.active,
                      COALESCE(sy.delisted_at, 0) AS delisted_at,
                      COALESCE(MAX(b.ts), 0)      AS last_bar_ts
               FROM symbols sy
               LEFT JOIN bars b ON b.symbol_id = sy.id AND b.tf = '1d'
               WHERE sy.market = 'stocks'
               GROUP BY sy.id""")}
    finally:
        con.close()

    # Newest claimed date wins when a source lists the same ticker twice
    # (recycled tickers appear once per corpse in FMP's history).
    best: dict[str, tuple[int, dict]] = {}
    unparsed = 0
    for row in source_rows:
        sym = (row.get("symbol") or "").upper()
        ts = parse_date(row.get("delistedDate") or "")
        if not sym or ts is None:
            unparsed += 1
            continue
        if sym not in best or ts > best[sym][0]:
            best[sym] = (ts, row)

    updates, conflicts, skipped_set, skipped_late = [], [], 0, 0
    grace = REUSE_GRACE_DAYS * 86400
    for sym, (ts, row) in sorted(best.items()):
        rec = symbols.get(sym)
        if rec is None:
            continue                      # not a name this platform tracks
        if ts >= before_ts:
            skipped_late += 1             # live detector's jurisdiction
            continue
        if rec["delisted_at"]:
            skipped_set += 1              # already marked; nothing to plan
            continue
        entry = {"symbol_id": rec["id"], "symbol": rec["symbol"],
                 "delisted_at": ts,
                 "date": dt.datetime.fromtimestamp(ts, dt.timezone.utc).strftime("%Y-%m-%d"),
                 "source_name": row.get("companyName", "")}
        if rec["last_bar_ts"] > ts + grace:
            entry["last_bar"] = dt.datetime.fromtimestamp(
                rec["last_bar_ts"], dt.timezone.utc).strftime("%Y-%m-%d")
            conflicts.append(entry)       # ticker reuse / relisting — never apply
        else:
            updates.append(entry)

    return {
        "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "db": os.path.abspath(db_path),
        "source": source_name,
        "before": dt.datetime.fromtimestamp(before_ts, dt.timezone.utc).strftime("%Y-%m-%d"),
        "summary": {
            "source_rows": len(source_rows),
            "source_unparsed": unparsed,
            "matched_updates": len(updates),
            "conflicts_ticker_reuse": len(conflicts),
            "skipped_already_marked": skipped_set,
            "skipped_after_cutoff": skipped_late,
        },
        "updates": updates,
        "conflicts": conflicts,
    }


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--source", choices=("fmp", "csv", "edgar"), default=None,
                    help="delistings source (default: fmp for the backfill plan, "
                         "edgar for --survivorship-bound)")
    ap.add_argument("--csv", help="local delistings CSV (required with --source csv)")
    ap.add_argument("--api-key", default=os.environ.get("FMP_API_KEY", ""),
                    help="FMP API key (default: $FMP_API_KEY)")
    ap.add_argument("--before", default="2026-01-01",
                    help="only plan delistings strictly before this UTC date")
    ap.add_argument("--out", help="write the plan JSON here (default: stdout)")
    ap.add_argument("--survivorship-bound", action="store_true",
                    help="measure the post-epoch survivorship bound instead of "
                         "emitting a backfill plan, and stamp it into --registry")
    ap.add_argument("--since", default=SURVIVORSHIP_EPOCH,
                    help="survivorship epoch (bound mode only)")
    ap.add_argument("--registry", default=DEFAULT_REGISTRY,
                    help="accuracy registry JSON to update (bound mode only)")
    args = ap.parse_args()

    before_ts = parse_date(args.before)
    if before_ts is None:
        print(f"bad --before date: {args.before!r}", file=sys.stderr)
        return 2

    source = args.source or ("edgar" if args.survivorship_bound else "fmp")
    if source == "edgar":
        if not args.survivorship_bound:
            print("the edgar source only covers the post-epoch window — "
                  "use it with --survivorship-bound", file=sys.stderr)
            return 2
        since_d = dt.date.fromisoformat(args.since)
        rows = fetch_edgar_form25(since_d)
        source_name = "edgar:form-25"
    elif source == "fmp":
        if not args.api_key:
            print("FMP source needs --api-key or $FMP_API_KEY "
                  "(or use --source csv with a downloaded list)", file=sys.stderr)
            return 2
        rows = fetch_fmp(args.api_key)
        source_name = "fmp:delisted-companies"
    else:
        if not args.csv:
            print("--source csv requires --csv FILE", file=sys.stderr)
            return 2
        rows = load_csv(args.csv)
        source_name = f"csv:{os.path.basename(args.csv)}"

    if args.survivorship_bound:
        # An empty external list over a days-long window is a legitimate "no
        # delistings" result here (fetch failures exit before this point), so
        # no empty-rows refusal: the DB detector's own stamps still count.
        bound = measure_survivorship_bound(args.db, rows, source_name,
                                           args.since, args.registry)
        print(json.dumps(bound, indent=2))
        if bound.get("bound_pp") is not None:
            print(f"survivorship bound: {bound['symbols_dropped']} symbol(s) left "
                  f"the universe since {args.since} vs {bound['symbols_graded']:,} "
                  f"graded -> at most {bound['bound_pp']:.2f} pp accuracy inflation. "
                  f"Written to {args.registry}", file=sys.stderr)
        else:
            print(f"survivorship bound not computable: {bound.get('reason')}. "
                  f"Written to {args.registry}", file=sys.stderr)
        return 0

    if not rows:
        print("source returned no rows — refusing to emit an empty plan",
              file=sys.stderr)
        return 1

    plan = build_plan(args.db, rows, source_name, before_ts)
    text = json.dumps(plan, indent=2)
    if args.out:
        with open(args.out, "w", encoding="utf-8") as f:
            f.write(text + "\n")
    else:
        print(text)

    s = plan["summary"]
    print(f"plan: {s['matched_updates']} updates, "
          f"{s['conflicts_ticker_reuse']} ticker-reuse conflicts (excluded), "
          f"{s['skipped_already_marked']} already marked, "
          f"{s['skipped_after_cutoff']} after cutoff. "
          f"Apply with: ./bin/sdmaint apply-delistings -plan <plan.json>",
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
