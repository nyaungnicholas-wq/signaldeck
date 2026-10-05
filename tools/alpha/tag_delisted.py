"""Filter and classify the delisted staging set so its skew is usable, not hidden.

fetch_delisted.py recovered 1,072 delistings but 87% of the usable ones died in
2021-2022 -- the de-SPAC wave. A model trained on that learns "delisting ==
short-lived 2021 SPAC", a shortcut that will not generalize to the ordinary
bankruptcies and buyouts we actually want it to anticipate.

The SEC route to broader coverage is closed: Form 25 records that a delisting
happened but not which ticker it happened to (company_tickers.json drops
delisted names, per-CIK submissions return the post-delisting OTC ticker, and
the filing document carries no symbol). So the skew cannot be fixed by adding
data. It can only be labelled.

SPACs have a tell: a blank-check shell holds its IPO proceeds in trust and
trades pinned near $10 until it merges or liquidates. Median close in a tight
band around $10 with low dispersion is a far better discriminator than the
company name, which is often just "... Acquisition Corp" and often not.

Emits `cohort` on every symbol so downstream work can hold a cohort out rather
than silently blending them.
"""
import argparse
import json
import sqlite3
import statistics

MIN_BARS = 60          # below this there is not enough path to label anything
SPAC_LO, SPAC_HI = 9.0, 11.0
SPAC_MAX_CV = 0.12     # coefficient of variation: SPACs barely move pre-merger


def classify(closes, name):
    """Return a cohort label from price behaviour, falling back to the name."""
    med = statistics.median(closes)
    sd = statistics.pstdev(closes) if len(closes) > 1 else 0.0
    cv = sd / med if med else 1.0

    if SPAC_LO <= med <= SPAC_HI and cv <= SPAC_MAX_CV:
        return "spac_shell"
    # Died cheap and volatile: the bankruptcy / collapse cohort we care about.
    if closes[-1] < 1.0 or (closes[-1] < 0.2 * max(closes)):
        return "collapse"
    n = (name or "").lower()
    if "acquisition corp" in n or "blank check" in n:
        return "spac_named"
    return "ordinary"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--staging", required=True)
    args = ap.parse_args()

    db = sqlite3.connect(args.staging)
    db.execute("ALTER TABLE delisted_symbol ADD COLUMN cohort TEXT")     \
        if "cohort" not in [r[1] for r in db.execute(
            "PRAGMA table_info(delisted_symbol)")] else None

    rows = list(db.execute(
        "SELECT symbol,name,n_bars FROM delisted_symbol WHERE reused=0"))
    counts, dropped = {}, 0

    for sym, name, n_bars in rows:
        if n_bars < MIN_BARS:
            db.execute("UPDATE delisted_symbol SET cohort='too_short' WHERE symbol=?",
                       (sym,))
            db.execute("DELETE FROM delisted_bar WHERE symbol=?", (sym,))
            dropped += 1
            counts["too_short"] = counts.get("too_short", 0) + 1
            continue
        closes = [r[0] for r in db.execute(
            "SELECT close FROM delisted_bar WHERE symbol=? ORDER BY ts", (sym,))]
        if not closes:
            continue
        c = classify(closes, name)
        db.execute("UPDATE delisted_symbol SET cohort=? WHERE symbol=?", (c, sym))
        counts[c] = counts.get(c, 0) + 1

    db.commit()

    usable = db.execute(
        "SELECT COUNT(*),SUM(n_bars) FROM delisted_symbol "
        "WHERE reused=0 AND cohort NOT IN ('too_short')").fetchone()
    by_year = {}
    for (lb, coh) in db.execute(
            "SELECT last_bar,cohort FROM delisted_symbol "
            "WHERE reused=0 AND cohort NOT IN ('too_short')"):
        by_year.setdefault(lb[:4], {}).setdefault(coh, 0)
        by_year[lb[:4]][coh] += 1

    report = {
        "cohorts": dict(sorted(counts.items(), key=lambda x: -x[1])),
        "dropped_too_short": dropped,
        "usable_symbols": usable[0],
        "usable_bars": usable[1],
        "by_year_cohort": {y: dict(sorted(v.items())) for y, v in sorted(by_year.items())},
        "non_spac_usable": sum(
            n for c, n in counts.items() if c in ("collapse", "ordinary")),
        "note": ("cohort='spac_shell'/'spac_named' should be held out or "
                 "down-weighted; 'collapse' is the bankruptcy signal."),
    }
    with open("delisted_cohorts.json", "w") as f:
        json.dump(report, f, indent=2)
    print(json.dumps(report, indent=2))
    db.close()


if __name__ == "__main__":
    main()
