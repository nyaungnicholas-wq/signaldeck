"""SignalDeck DEMO stub daemon: synthetic data for recording the product on video.

SignalDeck's real prices, forecasts and accuracy figures derive from Alpaca market
data and may not be shown publicly. This stub answers the API routes the demo
script (signaldeck-owner-kit/cac-2026/DEMO_SCRIPT.md) walks through with the
response SHAPES of web/src/lib/api.ts and the daemon, and VALUES that are
generated from a fixed seed. It reads no database, no file and no network: every
number below comes from random.Random(SEED) or a constant written here.

Tickers are DEMOA..DEMOF. Checked 2026-10-23 against the live companies table
(11,162 SEC registrant tickers) and symbols table (2,950), read-only: no match,
and no ticker there starts with DEMO.

The public record is fictional too: predictors and claims are named demo-*, and
their verdicts, dates and thresholds are chosen here, not copied from the real
registry or chain (the selfcheck refuses real names paired with a verdict and the
real record's dates). Only the regime kinds vol21/trend21/liquidity21 keep their
names, because the web keys its panels on them; they carry no verdict.

Run with ops/demo-run.ps1, which also builds the web with NEXT_PUBLIC_DEMO_DATA=1
so every page carries the DEMO DATA banner. Standalone:
    python tools/demo/demo_daemon.py --port 8352 --registry <dir>/data/accuracy_registry.json
    python tools/demo/demo_daemon.py --selfcheck     (prints DEMO STUB SELFCHECK OK)
    python tools/demo/demo_daemon.py --dump FILE     (every GET route's JSON, for review)
"""

import argparse
import hashlib
import json
import random
import re
import sys
import threading
import time
from datetime import date, datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

SEED = 20261023
NOTE = "DEMO DATA: synthetic values generated from a fixed seed, not market data."
COMPANIES = {
    "DEMOA": "Demo Alpha Industries (fictional)",
    "DEMOB": "Demo Beta Logistics (fictional)",
    "DEMOC": "Demo Gamma Health (fictional)",
    "DEMOD": "Demo Delta Software (fictional)",
    "DEMOE": "Demo Epsilon Energy (fictional)",
    "DEMOF": "Demo Phi Retail (fictional)",
}
TICKERS = list(COMPANIES)
SIC = {"DEMOA": "3560", "DEMOB": "4210", "DEMOC": "8000", "DEMOD": "7372", "DEMOE": "1311", "DEMOF": "5331"}
GRADED_AT = "2026-10-23T06:00:00Z"
GRADER_SHA = hashlib.sha256(b"signaldeck-demo-grader").hexdigest()
# The synthetic record's own dates. Each was checked absent from the repository
# (git grep, 2026-10-25), so none is a date of the real record.
DEMO_FIRST_GRADABLE = "2026-04-13"
DEMO_RETIRED_ON = "2026-06-11"
CAVEAT = ("DEMO DATA: this accuracy is a synthetic stand-in for a backtest figure. "
          "It describes nothing real and is not a live measurement.")


def rng(*parts):
    return random.Random(":".join([str(SEED)] + [str(p) for p in parts]))


def sha(text):
    return hashlib.sha256(text.encode()).hexdigest()


def epoch(d, hour=20):
    return int(datetime(d.year, d.month, d.day, hour, tzinfo=timezone.utc).timestamp())


def sessions_back(n, end):
    """The last n weekdays up to and including end, oldest first."""
    out, d = [], end
    while len(out) < n:
        if d.weekday() < 5:
            out.append(d)
        d -= timedelta(days=1)
    return out[::-1]


def sessions_ahead(n, start):
    d, k = start, 0
    while k < n:
        d += timedelta(days=1)
        if d.weekday() < 5:
            k += 1
    return d


def last_session():
    d = datetime.now(timezone.utc).date()
    while d.weekday() >= 5:
        d -= timedelta(days=1)
    return d


def demo(obj):
    obj["demo"] = True
    return obj


# ---------------------------------------------------------------- market data

def bars(symbol, tf="1d", limit=250):
    limit = max(1, min(int(limit), 2000))
    r = rng("bars", symbol)
    price = r.uniform(20, 140)
    vol = r.uniform(0.012, 0.028)
    days = sessions_back(limit, last_session())
    out = []
    for i, d in enumerate(days):
        o = price
        c = max(1.0, o * (1 + r.gauss(0.0003, vol)))
        h = max(o, c) * (1 + abs(r.gauss(0, vol / 2)))
        lo = min(o, c) * (1 - abs(r.gauss(0, vol / 2)))
        ts = epoch(d) if tf == "1d" else epoch(last_session()) - (limit - i) * (3600 if tf == "1h" else 60)
        out.append({"ts": ts, "o": round(o, 2), "h": round(h, 2), "l": round(lo, 2), "c": round(c, 2),
                    "v": int(r.uniform(2e5, 4e6))})
        price = c
    return out


# The web keys its regime panels on these three kind names, so they stay; every
# read, accuracy and date under them is seeded, and none carries a verdict.
REGIMES = {"vol21": ("calm", "elevated"), "trend21": ("uptrend", "downtrend"), "liquidity21": ("liquid", "thin")}
REGIME_WORD = {"vol21": "volatility", "trend21": "trend", "liquidity21": "liquidity"}
# Draw version of the regime reads (v3 review B3-3, 2026-10-25): the first draw showed DEMOA's
# conviction 91.0% and its quarterly banded accuracy 78.4%, both figures in the real public
# record. LEAD_CONVICTION is DEMOA's (it leads every kind so the demo's first stock has a read).
# Under "r721" (and 0.88) none of the scene-7 regime figures (the watchlist's DEMOA, DEMOB and
# DEMOC chips, DEMOA's conviction, banded accuracy and n) occurs in the public record outside
# .sql, .json and .csv files; the selfcheck refuses the two known real ones (PUBLIC_FIGURES).
RDRAW = "r721"
LEAD_CONVICTION = 0.88


def regime_rows(kind):
    rows = []
    for i, t in enumerate(TICKERS):
        r = rng("regime", kind, t, RDRAW)
        conv = round(r.uniform(0.45, 0.95), 2)
        tier = "high" if conv >= 0.8 else "medium" if conv >= 0.6 else "low"
        rows.append({
            "symbol": t, "market": "stocks", "ts": epoch(last_session()), "kind": kind, "horizonDays": 21,
            "regime": r.choice(REGIMES[kind]), "conviction": conv,
            "historicalAccuracy": round(r.uniform(0.58, 0.79), 3), "evidence": "backtest",
            "firstGradableOn": DEMO_FIRST_GRADABLE, "evidenceCaveat": CAVEAT, "tier": tier,
            "rank": i + 1, "n": r.randint(400, 3000),
        })
    # DEMOA leads every kind so the demo's first stock has a read to show.
    rows[0]["conviction"], rows[0]["tier"] = LEAD_CONVICTION, "high"
    if kind == "vol21":
        rows[0]["regime"] = "calm"
    return rows


def regimes(q):
    want = [s for s in (q.get("symbols", [""])[0].split(",")) if s]
    out = {}
    for kind in REGIMES:
        rows = regime_rows(kind)
        out[kind] = [x for x in rows if not want or x["symbol"] in want]
    body = {
        "forecasts": out,
        "kinds": {k: {"what": f"Demo {REGIME_WORD[k]} regime: which of two states a demo ticker is in over 21 sessions.",
                      "accuracyTiers": {"high": "synthetic 0.7-0.8", "medium": "synthetic 0.6-0.7", "low": "synthetic below 0.6"},
                      "caveat": CAVEAT, "tradeability": "Demo: no tradeability is claimed."} for k in REGIMES},
        "methodology": "Demo: each read is a seeded random draw standing in for a backtested regime classifier.",
        "whyHonest": "Demo: the accuracy shown is synthetic and labelled as such.",
    }
    if want:
        body["kindStats"] = {k: {"count": len(TICKERS), "measured": len(TICKERS),
                                 "meanHistoricalAccuracy": round(sum(x["historicalAccuracy"] for x in regime_rows(k)) / len(TICKERS), 3)}
                             for k in REGIMES}
    return demo(body)


def vol_regime(_q):
    rows = []
    for i, t in enumerate(TICKERS):
        r = rng("vol63", t, RDRAW)
        conv = round(r.uniform(0.5, 0.95), 2)
        rows.append({"symbol": t, "market": "stocks", "ts": epoch(last_session()), "regime": r.choice(("calm", "elevated")),
                     "conviction": conv, "historicalAccuracy": round(r.uniform(0.6, 0.78), 3), "evidence": "backtest",
                     "evidenceCaveat": CAVEAT, "tier": "high" if conv >= 0.8 else "medium", "rank": i + 1,
                     "n": r.randint(300, 1500)})
    return demo({"forecasts": rows, "highConviction": sum(1 for x in rows if x["tier"] == "high"),
                 "what": "Demo quarterly volatility regime (synthetic).",
                 "whyHonest": "Demo: synthetic values, labelled as such.",
                 "accuracyTiers": {"high": "synthetic", "medium": "synthetic"}, "caveat": CAVEAT,
                 "tradeability": "Demo: no tradeability is claimed."})


# ------------------------------------------------------------ public record

# Fictional predictors, decoupled from the real registry: no real name, verdict
# pattern or date. One row per state the pages can show, in an order that puts a
# RETIRED and an INSUFFICIENT box first on /accuracy. Figures come from the seed.
PREDICTORS = [
    # predictor, horizon, variant, family, status, retired
    ("demo-model-a", "1d", "", "direction", "RETIRED", True),
    ("demo-regime-c", "", "", "structure", "INSUFFICIENT", False),
    ("demo-model-b", "1d", "", "direction", "OK", False),
    ("demo-model-b", "1w", "", "direction", "REFUSED", False),
    ("demo-regime-d", "", "", "structure", "INSUFFICIENT", False),
    ("demo-benchmark", "1d", "", "benchmark", "NO_BASELINE", False),
]
REASONS = {
    "RETIRED": f"demo: retired by the pre-registered rule on {DEMO_RETIRED_ON} (synthetic record)",
    "OK": "demo: the interval lies above the baseline (synthetic record)",
    "REFUSED": "demo: grading window refused (synthetic record)",
    "INSUFFICIENT": "demo: too few independent days for an interval (synthetic record)",
    "NO_BASELINE": "demo: no registered baseline to grade against (synthetic record)",
}


def label(p, h, v):
    return p + (f" ({h}{', ' + v if v else ''})" if h else "")


# Draw version of the record figures (review R4, 2026-10-25): the first draws showed
# 46.1% accuracy and 2,479 resolved forecasts, both figures in the real public record.
# Under "v13" no record figure (accuracy, baseline, skill, count) equals one there in an
# accuracy or sample context; the selfcheck refuses the known real ones (PUBLIC_FIGURES).
DRAW = "v13"


def predictor_figures(p, h, st):
    """n, accuracy, baseline, skill, days and interval: only RETIRED (below) and OK (above) rows are graded."""
    r = rng("acc", p, h, DRAW)
    graded = st in ("RETIRED", "OK")
    n = r.randint(1500, 6000) if graded else r.randint(60, 400)
    null = round(r.uniform(0.51, 0.55), 3)
    days = r.randint(40, 70) if graded else r.randint(8, 30)
    if not graded:
        return n, None, null, None, days, None
    acc = round(null + r.uniform(0.02, 0.035) if st == "OK" else null - r.uniform(0.03, 0.06), 3)
    ci = [round(acc - 0.012, 3), round(acc + 0.012, 3)] if st == "OK" else [round(acc - 0.02, 3), round(acc + 0.015, 3)]
    return n, acc, null, round(acc - null, 3), days, ci


def accuracy(_q):
    rows = []
    for p, h, v, fam, st, ret in PREDICTORS:
        n, acc, null, skill, days, _ci = predictor_figures(p, h, st)
        rows.append({"predictor": p, "horizon": h, "variant": v, "family": fam, "publication_status": st,
                     "retired": ret, "retirement_sticky": ret, "reasons": [REASONS[st]], "evidence_refs": [],
                     "live_n": n, "live_acc": acc, "null_acc": null if acc is not None else None, "skill": skill,
                     "effective_n": round(n / 7) if acc is not None else None, "distinct_days": days,
                     "ci_method": "day-clustered bootstrap", "note": NOTE})
    return demo({"status": "OK", "graded_at": GRADED_AT, "grader_sha256": GRADER_SHA, "rows": rows,
                 "withheld_horizons": []})


def registry():
    """The file /accuracy reads from <cwd>/../data/accuracy_registry.json, bound to accuracy() above."""
    rows = []
    for p, h, v, fam, st, _ret in PREDICTORS:
        if fam not in ("direction", "structure"):
            continue
        n, acc, null, skill, days, ci = predictor_figures(p, h, st)
        r = rng("reg", p, h)
        row = {"predictor": label(p, h, v), "family": fam, "band": "all",
               "claimed": round(r.uniform(0.6, 0.75), 3), "live_n": n, "live_acc": acc, "ci": ci,
               "ci_method": "day-clustered bootstrap", "distinct_days": days,
               "effective_n": round(n / 7) if acc is not None else None,
               "null_hindsight": null if acc is not None else None, "null_prequential": null if acc is not None else None,
               "null_acc": null if acc is not None else None, "skill": skill, "note": NOTE}
        row["verdict"] = {
            "RETIRED": "FAILED - the interval lies below the baseline (synthetic)",
            "OK": "VALIDATED - the interval lies above the baseline (synthetic)",
            "REFUSED": "REFUSED - the grading window is degenerate, so nothing is graded (synthetic)",
        }.get(st) if fam == "direction" else f"PENDING - backtested claim, first graded live on {DEMO_FIRST_GRADABLE} (synthetic)"
        rows.append(row)
    return {"generated": GRADED_AT, "graded_at": GRADED_AT, "grader_sha256": GRADER_SHA, "min_independent_n": 30,
            "survivorship_epoch": DEMO_FIRST_GRADABLE, "null_policy": "max(hindsight, prequential)", "calibration": None,
            "rows": rows, "demo": True}


# Fictional claims with synthetic dates and thresholds (none from the real chain).
PREREG = [
    ("demo-grading-protocol", "2026-03-04", None, {"grader": "demo grader (synthetic record)", "maxAlpha": 0.05,
        "refusal": "Withhold rather than publish when the grading window is degenerate."}),
    ("demo-model-a", "2026-03-05", True, {"model": "demo-model-a", "question": "Will a demo ticker close higher one session ahead?",
        "horizons": ["1d"], "baseline": "always call the majority direction (synthetic)"}),
    ("demo-model-b", "2026-03-11", True, {"model": "demo-model-b", "question": "Will a demo ticker close higher one session and one week ahead?",
        "horizons": ["1d", "1w"], "baseline": "always call the majority direction (synthetic)"}),
    ("demo-regime-c", "2026-03-23", True, {"kind": "demo-regime-c", "question": "Will a demo ticker stay on its side of its 50-day average for 15 sessions?",
        "horizonDays": 15, "resolution": "Compare the close 15 sessions later with the 50-day average at that bar.",
        "bands": [{"minConviction": 0.0, "claimedAccuracy": 0.63}, {"minConviction": 0.75, "claimedAccuracy": 0.69}],
        "knownWeakness": "Demo record: no real history behind it."}),
    ("demo-regime-d", "2026-03-23", True, {"kind": "demo-regime-d", "question": "Will a demo ticker's trading volume stay in its band for 15 sessions?",
        "horizonDays": 15, "resolution": "Compare median volume over the next 15 sessions with the band at the call.",
        "bands": [{"minConviction": 0.0, "claimedAccuracy": 0.61}]}),
    ("demo-vol-forecast", "2026-03-30", True, {"model": "demo volatility forecast (synthetic record)", "loss": "QLIKE",
        "baselines": ["random walk", "EWMA"], "minDistinctDays": 60}),
    ("demo-retire-rule", "2026-03-30", None, {"model": "demo-model-a", "minIndependentN": 150, "minDistinctDays": 20,
        "criterion": "the day-clustered interval lies wholly below the baseline", "action": "stop publishing and record the retirement"}),
]


def prereg_chain():
    recs, prev = [], ""
    for i, (kind, day, early, spec) in enumerate(PREREG, start=1):
        spec_hash = sha(json.dumps(spec, sort_keys=True, separators=(",", ":")))
        entry = sha(f"{prev}|{i}|{kind}|{spec_hash}")
        d = date.fromisoformat(day)
        recs.append({"seq": i, "ts": epoch(d, 12), "kind": kind, "registeredOn": day, "beforeFirstGradable": early,
                     "specHash": spec_hash, "prevHash": prev, "entryHash": entry, "note": "demo record", "spec": spec})
        prev = entry
    return recs


def prereg(_q):
    return demo({"records": prereg_chain(), "chainVerified": True, "registeredBefore": True,
                 "firstGradableOn": DEMO_FIRST_GRADABLE, "liveResolved": 412,
                 "whatThisIs": "Demo: the claim each predictor made, frozen and hash-chained before its forecasts resolved. Every record here is synthetic.",
                 "howToUseIt": "Recompute each specHash from its spec and each entryHash from the previous one.",
                 "whyChained": "Changing any old record changes every hash after it.",
                 "appendOnly": "An amended claim is a new record, never an update."})


LEDGER_N = 3214


def ledger_head():
    h = ""
    for i in range(1, LEDGER_N + 1):
        h = sha(f"{h}|demo-entry|{i}")
    return h


HEAD = ledger_head()


def ledger_verify(_q):
    proven = LEDGER_N - 137
    return demo({"intact": True, "count": LEDGER_N, "head": HEAD, "incremental": False,
                 "intactMeans": "Demo: the synthetic rows are internally consistent. That is not proof of when they were written.",
                 "computedAt": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                 "tamperEvidence": {"detectsOperatorRegeneration": False, "localAnchorsReproduce": True,
                                    "anteriorityScope": "Demo: constrains anyone without the signing key, not the operator.",
                                    "externalWitness": {"verified": False, "reason": "demo: no external receipt"},
                                    "localAnchorReproducesThroughSeq": proven, "provenAnteriorThroughSeq": proven,
                                    "provenAnteriorThroughCount": proven, "provenAnteriorAsOf": epoch(last_session(), 6),
                                    "anchorCount": 12, "anchorCheckMode": "recomputed", "storedHeadComparison": False,
                                    "payloadRecomputed": True, "failingAnchors": 0, "firstFailingSeq": None,
                                    "claim": "Demo: tamper evidence over a synthetic chain."}})


def track_record(q):
    h = q.get("horizon", ["1d"])[0]
    r = rng("track", h)
    ind = r.randint(1200, 2400)
    return demo({"horizon": h, "rawN": ind * 21, "independentN": ind, "minIndependentN": 3000, "distinctDays": 23,
                 "minDistinctDays": 60, "gated": True, "gateReason": "sample", "live": True,
                 "trackLabel": "live - probability frozen at prediction time (demo)",
                 "note": f"Demo: only {ind} deduplicated symbol-day resolutions over 23 days (need 3000 over 60). The grade is withheld until the record is statistically real.",
                 "winRate": None, "brier": None, "ic": None, "reliability": [], "byMarket": None, "byRegime": None,
                 "coverage": {h: {"resolved": ind, "total": ind + 300}},
                 "ledger": {"intact": True, "count": LEDGER_N, "head": HEAD},
                 "paper": {"available": False, "note": "Demo: no paper book."}, "gate": {"threshold": 3000}})


def vol_record(_q):
    hs = []
    for h in (1, 5):
        r = rng("volrec", h, DRAW)
        hs.append({"horizon": h, "n": r.randint(1500, 2600), "distinctDays": 23 if h == 1 else 19,
                   "ungradable": r.randint(10, 60), "sufficient": False, "verdict": "INSUFFICIENT",
                   "verdictReason": "Demo: fewer than 60 distinct trading days, so no verdict is given.",
                   "meanQlike": {"har": None, "rw": None, "ewma": None}, "vsEwma": None, "vsRandomWalk": None,
                   "lowerIsBetter": True})
    return demo({"asOf": epoch(last_session()), "evidence": "live", "minDistinctDays": 60, "horizons": hs,
                 "caveat": "LIVE RECORD, ACCRUING (demo data): the counts below are synthetic. No verdict is given before 60 trading days."})


# ------------------------------------------------------------- member state

STATE_LOCK = threading.Lock()
STATE = {"watch": ["DEMOB", "DEMOC"], "calls": None}


def symbol_info(t):
    return {"id": 9000 + TICKERS.index(t), "symbol": t, "market": "stocks", "name": COMPANIES[t], "active": True,
            "addedAt": epoch(date(2026, 10, 1))}


def initial_calls():
    today = last_session()
    calls = []
    for i, (t, d, back, outcome) in enumerate([("DEMOB", "up", 30, "hit"), ("DEMOC", "down", 26, "miss"),
                                               ("DEMOE", "up", 21, "hit"), ("DEMOD", "up", 16, "miss"),
                                               ("DEMOF", "down", 12, "hit"), ("DEMOB", "up", 9, None)], start=1):
        made = today - timedelta(days=back)
        res = sessions_ahead(5, made)
        calls.append({"id": i, "symbol": t, "market": "stocks", "call": d, "horizon": 5, "note": "",
                      "createdTs": epoch(made, 15), "status": "void" if outcome is None else "resolved",
                      **({"outcome": outcome} if outcome else {}), "resolveDate": res.isoformat(), "canWithdraw": False})
    return calls


def journal_body():
    calls = STATE["calls"]
    res = [c for c in calls if c["status"] == "resolved"]
    stats = {"resolved": len(res), "callDays": len(res), "hits": sum(c.get("outcome") == "hit" for c in res),
             "misses": sum(c.get("outcome") == "miss" for c in res),
             "open": sum(c["status"] == "open" for c in calls), "void": sum(c["status"] == "void" for c in calls),
             "withdrawn": sum(c["status"] == "withdrawn" for c in calls), "hitRate": None, "ciLow": None,
             "ciHigh": None, "baselineUpRate": None, "withheld": True, "minN": 20}
    return demo({"calls": sorted(calls, key=lambda c: -c["createdTs"]), "stats": stats,
                 "caps": {"todayLeft": 5, "openLeft": 20 - stats["open"]}})


def companies(q):
    term = q.get("q", [""])[0].strip().lower()
    limit = int(q.get("limit", ["20"])[0])
    rows = []
    for t, name in COMPANIES.items():
        if term and term not in t.lower() and term not in name.lower():
            continue
        r = rng("co", t)
        rows.append({"ticker": t, "name": name, "exchange": "DEMO", "cik": 9000000 + TICKERS.index(t),
                     "sic": SIC[t], "sicDesc": "Demo sector", "tracked": True, "price": None, "dayChangePct": None,
                     "volume": None, "mcap": None, "sharesOutstanding": r.randint(20, 400) * 1_000_000, "float": None})
    return demo({"companies": rows[:limit], "total": len(rows), "limit": limit, "offset": 0,
                 "trackedCount": len(TICKERS), "unknownMcapExcluded": 0, "directoryCount": len(TICKERS),
                 "lastSyncTs": epoch(last_session(), 6), "sectors": [], "exchanges": [], "note": NOTE,
                 "mcapNote": NOTE, "asOf": epoch(last_session(), 6)})


def sym(q):
    t = q.get("symbol", ["DEMOA"])[0].upper()
    return t if t in COMPANIES else "DEMOA"


def filings_for(t, limit=8):
    r = rng("filings", t)
    out, d = [], last_session()
    for i, (form, lab) in enumerate([("8-K", "8-K - current report (demo)"), ("4", "Form 4 - insider transaction (demo)"),
                                     ("10-Q", "10-Q - quarterly report (demo)"), ("8-K", "8-K - earnings release (demo)"),
                                     ("10-K", "10-K - annual report (demo)")]):
        d -= timedelta(days=r.randint(6, 40))
        out.append({"id": f"0009{TICKERS.index(t)}-26-{i:06d}", "symbolId": 9000 + TICKERS.index(t), "symbol": t,
                    "form": form, "filedTs": epoch(d, 16), "title": f"{COMPANIES[t]} {form}", "url": "#demo-filing",
                    "label": lab})
    return out[:limit]


def insiders_for(t):
    r = rng("ins", t)
    out, d = [], last_session()
    for i, title in enumerate(["Chief Executive Officer (demo)", "Director (demo)", "Chief Financial Officer (demo)"]):
        d -= timedelta(days=r.randint(5, 30))
        code = r.choice("PSA")
        shares = r.randint(1, 40) * 500
        price = round(r.uniform(20, 120), 2)
        out.append({"accession": f"demo-{t}-{i}", "symbolId": 9000 + TICKERS.index(t), "symbol": t,
                    "insider": f"Demo Insider {chr(65 + i)}", "title": title, "code": code,
                    "codeLabel": {"P": "open-market purchase", "S": "open-market sale", "A": "grant or award"}[code],
                    "openMarket": code in "PS", "shares": shares, "price": price, "value": round(shares * price, 2),
                    "txTs": epoch(d, 15), "filedTs": epoch(d + timedelta(days=2), 15)})
    return out


def holders_for(t):
    r = rng("13f", t)
    return [{"cik": f"90000{i}", "manager": f"Example Capital {chr(65 + i)} (fictional)", "period": "2026-06-30",
             "symbolId": 9000 + TICKERS.index(t), "symbol": t, "cusip": f"DEMO{i}0000", "name": COMPANIES[t],
             "value": r.randint(5, 900) * 100_000, "shares": r.randint(10, 900) * 1000} for i in range(4)]


def fundamentals(q):
    t = sym(q)
    r = rng("fund", t)
    hist_metric = q.get("history", [None])[0]
    quarters = [date(2024 + (k // 4), 3 * (k % 4) + 1, 1) for k in range(10)]
    series = {"Revenues": [r.randint(80, 900) * 1_000_000 for _ in quarters],
              "EPS": [round(r.uniform(-0.4, 2.2), 2) for _ in quarters]}
    sid = 9000 + TICKERS.index(t)
    metrics = [{"symbolId": sid, "symbol": t, "metric": m, "value": v[-1], "asOf": epoch(quarters[-1], 0),
                "fetchedAt": epoch(last_session(), 6)} for m, v in series.items()]
    metrics.append({"symbolId": sid, "symbol": t, "metric": "SharesOutstanding", "value": r.randint(20, 400) * 1_000_000,
                    "asOf": epoch(quarters[-1], 0), "fetchedAt": epoch(last_session(), 6)})
    body = {"symbol": t, "metrics": metrics, "note": NOTE}
    if hist_metric in series:
        body["historyMetric"] = hist_metric
        body["history"] = [{"symbolId": sid, "symbol": t, "metric": hist_metric, "value": v, "asOf": epoch(d, 0),
                            "fetchedAt": epoch(last_session(), 6)} for d, v in zip(quarters, series[hist_metric])]
    return demo(body)


def company_profile(q):
    t = sym(q)
    return demo({"symbol": t, "name": COMPANIES[t], "market": "stocks",
                 "company": {"cik": 9000000 + TICKERS.index(t), "sic": SIC[t], "sicDesc": "Demo sector",
                             "exchange": "DEMO", "name": COMPANIES[t]},
                 "peers": [{"ticker": p, "name": COMPANIES[p]} for p in TICKERS if p != t][:3],
                 "insiders": [{k: x[k] for k in ("insider", "title", "code", "shares", "value")} | {"ts": x["txTs"]}
                              for x in insiders_for(t)],
                 "holders": [{"manager": h["manager"], "value": h["value"], "shares": h["shares"]} for h in holders_for(t)],
                 "filings": [{k: f[k] for k in ("form", "title", "label", "filedTs", "url")} for f in filings_for(t)],
                 "fundamentals": [], "profile": f"{COMPANIES[t]} is a fictional company that exists only in SignalDeck's demo data.",
                 "note": NOTE})


def symbol_agent(q):
    t = sym(q)
    r = rng("agent", t)
    return demo({"symbol": t, "market": "stocks", "horizon": q.get("horizon", ["1d"])[0], "available": True,
                 "tier": "global", "personal": False, "nSamples": r.randint(20, 90), "threshold": 120,
                 "personality": "Demo: still learning from its own outcomes, so the global model is in force (synthetic).",
                 "skill": [{"component": c, "hitRate": round(r.uniform(0.45, 0.55), 3), "hasHR": True,
                            "ic": round(r.uniform(-0.05, 0.05), 3), "hasIC": True, "n": r.randint(20, 90)}
                           for c in ("pressure", "expectancy", "forecast")],
                 "activeWeights": {}, "updatedTs": epoch(last_session(), 6)})


# ----------------------------------------------------------------- routing

ME = {"id": 7, "username": "demo-member", "isAdmin": False, "member": True, "memberFinra": False, "demo": True}


def watchlist(_q):
    with STATE_LOCK:
        return [symbol_info(t) for t in STATE["watch"]]


GET = {
    "/api/auth/me": lambda q: dict(ME),
    "/api/health": lambda q: demo({"version": "demo-stub", "uptimeS": int(time.time() - STARTED), "alpaca": False,
                                   "openSignup": True, "signupPaused": False}),
    "/api/accuracy": accuracy,
    "/api/prereg": prereg,
    "/api/ledger/verify": ledger_verify,
    "/api/track-record": track_record,
    "/api/vol-forecast/record": vol_record,
    "/api/vol-forecast/latest": lambda q: demo({"available": False, "reason": "Demo: the record has not passed, so no forecast is served."}),
    "/api/regimes": regimes,
    "/api/vol-regime": vol_regime,
    "/api/watchlist": watchlist,
    "/api/companies": companies,
    "/api/bars": lambda q: bars(sym(q), q.get("tf", ["1d"])[0], q.get("limit", ["250"])[0]),
    "/api/journal": lambda q: journal_body(),
    "/api/journal/symbols": lambda q: demo({"symbols": [{"symbol": c["ticker"], "name": c["name"]} for c in companies(q)["companies"]]}),
    "/api/company/profile": company_profile,
    "/api/fundamentals": fundamentals,
    "/api/filings": lambda q: demo({"filings": filings_for(sym(q), int(q.get("limit", ["8"])[0])), "count": 5,
                                    "form": "", "symbol": sym(q), "note": NOTE}),
    "/api/insiders": lambda q: demo({"trades": insiders_for(sym(q)), "count": 3, "symbol": sym(q), "note": NOTE}),
    "/api/institutions": lambda q: demo({"holdings": holders_for(sym(q)), "count": 4, "symbol": sym(q), "note": NOTE}),
    "/api/dilution": lambda q: demo({"symbol": sym(q), "derived": True, "level": "low",
                                     "reasons": ["demo: synthetic share count steady"], "updatedTs": epoch(last_session(), 6), "note": NOTE}),
    "/api/congress": lambda q: demo({"trades": [], "count": 0, "symbol": sym(q), "recent90d": 0,
                                     "lagNote": "Disclosures lag trades by 30-45 days by law.", "note": NOTE, "source": None}),
    "/api/symbol-agent": symbol_agent,
    "/api/ask": lambda q: demo({"available": False, "reason": "not part of the demo"}),
    "/api/alert-prefs": lambda q: demo({"emailDigest": False, "emailVerified": True, "telegramLinked": False,
                                        "telegramAvailable": False, "mailAvailable": False}),
}


def post(path, body):
    with STATE_LOCK:
        if path in ("/api/auth/login", "/api/auth/google", "/api/auth/verify"):
            return 200, dict(ME)
        if path == "/api/auth/logout":
            return 200, demo({"ok": True})
        if path in ("/api/watch", "/api/unwatch"):
            t = str(body.get("symbol", "")).upper()
            if t not in COMPANIES:
                return 400, demo({"error": "demo stub: only the fictional DEMO tickers exist here"})
            if path == "/api/watch" and t not in STATE["watch"]:
                STATE["watch"].append(t)
            if path == "/api/unwatch" and t in STATE["watch"]:
                STATE["watch"].remove(t)
            return 200, demo(symbol_info(t))
        if path == "/api/journal":
            t = str(body.get("symbol", "")).upper()
            if t not in COMPANIES or body.get("call") not in ("up", "down"):
                return 400, demo({"error": "demo stub: pick a DEMO ticker and up or down"})
            h = int(body.get("horizon", 5))
            STATE["calls"].append({"id": len(STATE["calls"]) + 1, "symbol": t, "market": "stocks", "call": body["call"],
                                   "horizon": h, "note": str(body.get("note", ""))[:280], "createdTs": int(time.time()),
                                   "status": "open", "resolveDate": sessions_ahead(h, last_session()).isoformat(),
                                   "canWithdraw": True})
            return 200, journal_body()
        if path == "/api/journal/withdraw":
            for c in STATE["calls"]:
                if c["id"] == body.get("id") and c["status"] == "open":
                    c["status"], c["canWithdraw"] = "withdrawn", False
            return 200, journal_body()
    return 404, demo({"error": f"demo stub: {path} is not part of the demo"})


def answer(method, raw_path, body=None):
    u = urlparse(raw_path)
    q = parse_qs(u.query)
    if method == "POST":
        return post(u.path, body or {})
    fn = GET.get(u.path)
    if fn is None:
        return 404, demo({"error": f"demo stub: {u.path} is not part of the demo"})
    return 200, fn(q)


class Handler(BaseHTTPRequestHandler):
    def _send(self, status, obj):
        data = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("content-type", "application/json")
        self.send_header("cache-control", "no-store")
        self.send_header("x-signaldeck-demo", "synthetic")
        self.send_header("content-length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        self._send(*answer("GET", self.path))

    def _body(self):
        # The web proxy streams request bodies, so they arrive chunked with no length.
        if "chunked" in (self.headers.get("transfer-encoding") or "").lower():
            data = b""
            while True:
                size = int(self.rfile.readline().split(b";")[0].strip() or b"0", 16)
                if size == 0:
                    self.rfile.readline()
                    return data
                data += self.rfile.read(size)
                self.rfile.readline()
        return self.rfile.read(int(self.headers.get("content-length") or 0))

    def do_POST(self):
        try:
            body = json.loads(self._body() or b"{}")
        except ValueError:
            body = {}
        self._send(*answer("POST", self.path, body if isinstance(body, dict) else {}))

    def log_message(self, fmt, *args):
        sys.stderr.write("demo-stub %s\n" % (fmt % args))


STARTED = time.time()
STATE["calls"] = initial_calls()


def all_gets():
    paths = [p for p in GET] + ["/api/regimes?symbols=DEMOA", "/api/bars?symbol=DEMOA&market=stocks&tf=1d&limit=5",
                                "/api/fundamentals?symbol=DEMOA&history=EPS", "/api/companies?q=demo&limit=8",
                                "/api/journal/symbols?q=DEMO"]
    return {p: answer("GET", p) for p in paths}


# The real record this stub must never echo: predictor and claim names, and the
# dates of real verdicts and windows (retirement and survivorship epoch 2026-07-24,
# first structural call 2026-07-18, collapsed cross-sections 2026-07-27..08-06,
# registered first-gradable date and grading epoch 2026-08-07, and later dates).
REAL_NAMES = {"flagship", "directional-ensemble", "gapfill5", "vol21", "vol63", "trend21", "trend63", "liquidity21",
              "liquidity21-crypto", "trend21-crypto", "filingsdrift21", "market-benchmark", "prequential-majority",
              "vol-forecast-har", "auto-retire-rule", "grading-protocol"}
REAL_DATES = ("2026-07-18", "2026-07-24", "2026-07-27", "2026-08-04", "2026-08-05", "2026-08-06", "2026-08-07",
              "2026-08-17", "2026-09-25")
VERDICT_KEYS = {"publication_status", "verdict", "retired", "retirement_sticky", "reasons"}
# Accuracy, skill and sample figures of the real public record (git grep origin/public-launch,
# 2026-10-25): the 1w directional accuracy 46.1% and always-up 54.5% over n 9,118, the retired
# model's 2,479 graded rows at -13.0pp, and a 1d leg's -3.6pp; and (v3 review B3-3) trend21's
# moderate-band accuracy 91.0% and the gap-fill rate 78.4%.
PUBLIC_FIGURES = {"46.1%", "54.5%", "9,118", "2,479", "13.0pp", "3.6pp", "91.0%", "78.4%"}


def walk(o):
    if isinstance(o, dict):
        yield o
        for v in o.values():
            yield from walk(v)
    elif isinstance(o, list):
        for v in o:
            yield from walk(v)


def selfcheck():
    out = all_gets()
    for p, (status, body) in out.items():
        assert status == 200, (p, status)
        if isinstance(body, dict):
            assert body.get("demo") is True, f"{p} lacks demo:true"
    again = all_gets()
    strip = lambda d: json.dumps({k: v for k, v in d.items() if k not in ("/api/health", "/api/ledger/verify")}, sort_keys=True)
    assert strip(out) == strip(again), "not deterministic"
    text = json.dumps(out)
    tickers = set(re.findall(r'"(?:symbol|ticker)": "([^"]+)"', text))
    assert tickers and tickers <= set(TICKERS), f"non-demo ticker in output: {tickers - set(TICKERS)}"
    recs = prereg_chain()
    prev = ""
    for r in recs:
        assert r["specHash"] == sha(json.dumps(r["spec"], sort_keys=True, separators=(",", ":")))
        assert r["prevHash"] == prev and r["entryHash"] == sha(f"{prev}|{r['seq']}|{r['kind']}|{r['specHash']}")
        prev = r["entryHash"]
    acc, reg = accuracy({}), registry()
    assert acc["graded_at"] == reg["graded_at"] and acc["grader_sha256"] == reg["grader_sha256"]
    labels = {label(x["predictor"], x["horizon"], x["variant"]) for x in acc["rows"]}
    assert all(r["predictor"] in labels for r in reg["rows"]), "registry row without a published verdict"
    assert post("/api/watch", {"symbol": "AAPL"})[0] == 400, "a real ticker must be refused"
    # Decoupled from the real record (review G4, 2026-10-25): no real predictor or
    # claim name carries a verdict, the record's names are all fictional, and no
    # date of a real verdict or grading window appears in any route's JSON.
    everything = {p: b for p, (_s, b) in out.items()} | {"registry": reg}
    for d in walk(everything):
        name = str(d.get("predictor", d.get("kind", ""))).split(" (")[0]
        paired = VERDICT_KEYS & d.keys() if name in REAL_NAMES else set()
        assert not paired, f"real predictor {name} paired with {sorted(paired)}"
    names = {str(x.get("predictor", x.get("kind"))).split(" (")[0] for x in acc["rows"] + reg["rows"] + recs}
    assert all(n.startswith("demo-") for n in names), f"non-fictional name in the record: {sorted(names)}"
    text = json.dumps(everything)
    found = [d for d in REAL_DATES if d in text] + [w for w in ("RiskMetrics",) if w in text]
    assert not found, f"real record date or name in the output: {found}"
    shown = set()  # the record figures as the pages print them (review R4)
    for x in acc["rows"] + vol_record({})["horizons"]:
        if x.get("live_acc") is not None:
            shown |= {f"{x['live_acc'] * 100:.1f}%", f"{x['null_acc'] * 100:.1f}%", f"{abs(x['skill']) * 100:.1f}pp"}
        shown.add(f"{x.get('live_n', x.get('n')):,}")
    for x in [x for k in REGIMES for x in regime_rows(k)] + vol_regime({})["forecasts"]:  # regime reads (B3-3)
        shown |= {f"{x['conviction'] * 100:.1f}%", f"{x['historicalAccuracy'] * 100:.1f}%", f"{x['n']:,}"}
    shown |= {f"{LEDGER_N:,}", f"{LEDGER_N - 137:,}"}
    clash = sorted(shown & PUBLIC_FIGURES)
    assert not clash, f"a record figure equals one in the real public record: {clash}"
    print(f"DEMO STUB SELFCHECK OK ({len(out)} routes, tickers {sorted(tickers)}, record names {sorted(names)})")


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--port", type=int, default=8352)
    ap.add_argument("--registry", help="write the synthetic accuracy_registry.json here before serving")
    ap.add_argument("--selfcheck", action="store_true")
    ap.add_argument("--dump", help="write every GET route's JSON to this file and exit")
    a = ap.parse_args()
    if a.selfcheck:
        selfcheck()
        return
    if a.dump:
        with open(a.dump, "w", encoding="utf-8") as f:
            json.dump({p: b for p, (_s, b) in all_gets().items()} | {"registry": registry()}, f, indent=1)
        print(f"wrote {a.dump}")
        return
    if a.registry:
        with open(a.registry, "w", encoding="utf-8") as f:
            json.dump(registry(), f, indent=1)
    srv = ThreadingHTTPServer(("127.0.0.1", a.port), Handler)
    print(f"demo stub on http://127.0.0.1:{a.port} (synthetic data only)", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
