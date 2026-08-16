# dirfix — directional-ensemble investigation (2026-08-15)

## Read this before trusting any CSV in here

The universe changed mid-investigation. `extract.py` now excludes funds, and
**artifacts produced before that are not comparable to ones produced after.**

| artifact | universe | trustworthy? |
|---|---|---|
| `results_F/G/H/I.csv` | contaminated — includes ETFs and leveraged inverse products | accuracy conclusions hold; any P&L reading does not |
| `sharpe_sweep.csv` | contaminated | superseded |
| `ablation.csv`, `best_config.json`, `HOLDOUT.json` | fund-free | current |
| `paper_ledger.jsonl` | fund-free (first tranche VOIDED, see below) | current |

`market='stocks'` does NOT exclude ETFs in this database. LQD, TLT, SPY and
leveraged inverse products (TSLZ −2x TSLA, MSTZ −2x MSTR, SOXS −3x semis) are
all filed as stocks. Measured before the fix, they were **52.7% of long picks
and 51.2% of short picks**. Shorting a leveraged inverse ETF harvests
daily-rebalance *decay*, not a forecast, at 20–100%/yr borrow against the 3%
charged. That was roughly half the apparent edge.

The fund filter is on NAME, deliberately not on `fundamentals` presence: that
table covers only currently-subscribed live names, and using it cut delisted
symbols from 1,868 to **1** — trading fund contamination for a far worse
survivorship bias. Bare `Shares`/`Trust`/`Index` are excluded from the pattern
because they match ADRs, foreign "Ordinary Shares" issuers and REITs
(QTS REALTY TRUST), which are real companies.

## Where it landed

Fund-free, **0 of 27 configs clear the pre-set bar** (t_NW ≥ 2, maxDD ≥ −35%),
against 2 of 27 contaminated. Best available: search Sharpe **0.72, t_NW 1.66
(not significant)** over 1,045 days; holdout 2.39. The search period is 3× longer
and more representative. Beta flipped +0.47 → −0.65 once funds were removed, so
what remains is substantially a short-market bet.

**The tradeable edge on operating companies is not established.** Every time a
measurement artifact was removed the edge shrank — tranche estimate ~0.5 →
portfolio sim 1.06 → padding bug fixed 1.11 → funds excluded 0.72. That is the
signature of a result that was mostly artifact.

## The forward ledger is the only thing left that cannot be re-run

`paper.py open|grade|report|void`. Positions are written once with entry prices,
before outcomes exist, and graded only when the exit bar arrives. The first
tranche (2026-08-14) was VOIDED — not deleted — because it was picked on the
contaminated panel; its rows keep their prices and carry a `void_reason`.
Reopened fund-free the same day, 21-day hold, first result ~2026-09-14.
`report()` counts only CLOSED rows, so a void can never flatter the record.

Regenerate everything: `python extract.py && python seal_holdout.py` then
`cache_preds.py`, `ablate.py`, `pick_best.py`, `holdout_port.py`.
Parquet is gitignored (2.2GB) and rebuilds from the DB.
