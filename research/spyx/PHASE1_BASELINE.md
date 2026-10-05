# Phase 1 — baseline reproduction and integrity audit

Repo `signaldeck` @ `ada8af4`, branch `hmm-regime-and-pbo`. Run 2026-08-31.
Interpreter `signaldeck/.venv/Scripts/python.exe` (pandas 3.0.5, yfinance 1.5.2).
Prices from `stock-trader/data/_cache/*.parquet`, fetched `auto_adjust=True` (total return).

## What was reproduced, and what changed when it was

All prior published figures for these constructions were computed with a Sharpe **against
zero**. Recomputed against the actual risk-free rate (^IRX 13-week bill, daily simple), every
one of them falls:

| Series | Window | Sharpe vs zero (prior) | Sharpe vs cash (here) |
|---|---|---|---|
| SPY buy-and-hold | 1993–2015 | 0.55 | **0.41** |
| SPY buy-and-hold | 2006–2015 | 0.64–0.65 (recorded) | **0.38** |

The prior ledger `stock-trader/experiments/RESULTS_premia.md` lists SPY at Sharpe 0.64 and
the best construction at 0.72–0.75. Those are not comparable to a 1.00 gate defined against
the risk-free rate. Everything below uses the vs-cash convention.

## Baseline panel (measured, not recalled)

Cost model throughout: 10 bps on |Δw|, financing at rf + 90 bps annual on borrowed capital,
idle cash earns rf. Single convention, `evalcore.cost_returns` — this closes audit finding F4.

**DEV, joint sample 2006-02-06 → 2015-12-31, 2,494 rows**

| name | cum% | cagr% | vol% | sharpe | maxdd% | avgGross | excess pp | gates |
|---|---|---|---|---|---|---|---|---|
| spy_bh | 97.51 | 7.12 | 20.74 | 0.38 | −55.19 | 1.00 | 0.00 | `--` |
| cash | 11.03 | 1.06 | 0.11 | — | 0.00 | 0.00 | −86.48 | `--` |
| spy_trend200 | 64.06 | 5.13 | 11.22 | 0.41 | −21.35 | 0.68 | −33.45 | `--` |
| spy_2x | 109.74 | 7.77 | 41.48 | 0.36 | −84.22 | 2.00 | +12.23 | `R-` |
| divtrend_1x | 66.39 | 5.28 | 8.04 | 0.55 | −12.96 | 0.63 | −31.12 | `--` |
| divtrend_2x | 125.43 | 8.56 | 16.09 | 0.53 | −25.22 | 1.27 | +27.92 | `R-` |
| divtrend_3x | 181.74 | 11.03 | 24.13 | 0.51 | −36.12 | 1.90 | +84.23 | `R-` |
| divtrend_5x | 259.89 | 13.81 | 40.22 | 0.50 | −53.96 | 3.17 | +162.38 | `R-` |
| divtrend_invvol_1x | 70.16 | 5.52 | 6.49 | **0.70** | −7.80 | 0.63 | −27.36 | `--` |
| divtrend_invvol_3x | 222.31 | 12.55 | 19.47 | 0.65 | −24.15 | 1.90 | +124.80 | `R-` |

**VAL, 2016-01-04 → 2020-11-30, 1,237 rows**

| name | cum% | cagr% | vol% | sharpe | maxdd% | avgGross | excess pp | gates |
|---|---|---|---|---|---|---|---|---|
| spy_bh | 97.33 | 14.85 | 18.99 | 0.77 | −33.72 | 1.00 | 0.00 | `--` |
| spy_trend200 | 56.18 | 9.51 | 11.57 | 0.75 | −17.83 | 0.74 | −41.15 | `--` |
| spy_2x | 194.00 | 24.57 | 37.97 | 0.74 | −58.83 | 2.00 | +96.66 | `R-` |
| divtrend_1x | 35.17 | 6.33 | 5.73 | **0.90** | −8.64 | 0.58 | −62.16 | `--` |
| divtrend_2x | 67.35 | 11.06 | 11.47 | 0.87 | −16.74 | 1.17 | −29.99 | `--` |
| divtrend_3x | 102.17 | 15.42 | 17.21 | 0.85 | −24.26 | 1.75 | +4.83 | `--` |
| divtrend_5x | 180.15 | 23.35 | 28.68 | 0.84 | −37.66 | 2.92 | +82.81 | `R-` |
| divtrend_invvol_1x | 29.19 | 5.36 | 4.88 | 0.86 | −7.31 | 0.58 | −68.14 | `--` |
| divtrend_invvol_3x | 78.91 | 12.58 | 14.65 | 0.81 | −20.79 | 1.75 | −18.43 | `--` |

### Three things this establishes

1. **No construction has ever cleared the Sharpe gate in any window.** The maximum observed
   is 0.90 (divtrend_1x, VAL). The gate is 1.00.
2. **The gates trade against each other.** Leverage buys the return gate and costs Sharpe.
   The cost is small in VAL (0.90 → 0.84 at 5×) only because 2016–2020 had near-zero cash
   rates. The sealed block ran 4–5% cash, so financing drag there will be materially larger.
3. **The same rule scores 0.55 in DEV and 0.90 in VAL.** Sharpe estimated on a single
   ~5-year window carries a standard error near 0.4. The 1.00 threshold sits inside that
   noise, so one sealed window cannot separate skill from a favourable draw. This is a
   property of the target, not a defect in the strategy, and it will be reported with the
   result either way.

## Data-integrity audit — `integrity.py`, HIGH=0 MED=4 INFO=5

- **C1 dividends present.** SPY adjusted growth 9.92%/yr vs unadjusted 7.89%/yr, a 2.02 pp
  gap. Dividends are inside both sides of the comparison. This was the single most likely way
  the entire result could have been wrong; it is clean.
- **C10 look-ahead probe.** Annualised return at lag 0/1/2/5 days = 7.64 / 8.20 / 7.61 /
  8.11%. Lag-0 is *not* elevated — contamination collapses under lag; this degrades gently
  and non-monotonically, which is noise, not leakage.
- **C6, 4× MED, all the same event.** One 7-calendar-day gap at 2001-09-17 in SPY, EFA, QQQ
  and IWM — the post-9/11 exchange closure. Only the four ETFs that existed in 2001 show it.
  Genuine.
- **C8 risk-free.** 7 sessions with a negative 13-week bill yield, all March 2020 (min
  −0.10%/yr). Real market history, not a feed error. *This was initially graded HIGH by an
  over-strict check I wrote; the innocent explanation was tested and the rule corrected to
  fire only below −1%/yr.* Range over the window: −0.10% to 6.22%, mean 2.32%.
- **C7 / C9 survivorship — unfixable, disclosed.** Instrument inceptions: SPY 1993-01-29,
  QQQ 1999-03-10, IWM 2000-05-26, EFA 2001-08-27, TLT/IEF 2002-07-30, GLD 2004-11-18,
  VNQ 2004-09-29, **DBC 2006-02-06**. The joint sample retains 3,731 rows against 7,011 in
  the longest single series. All nine instruments were chosen in the present and all nine
  still exist; no computation in this repo can remove that bias.

### The correction that matters most

Prior work described the diversified-trend result as resting on "2006-2026 only, 20 years"
while quoting SPY's 33-year series alongside it. Measured: **DBC's 2006-02-06 inception binds
the joint panel**, so the multi-sleeve construction never had access to more than 2006-onward
regardless of SPY's history. The DEV block for any 9-sleeve candidate is 2006–2015 — ten
years, one financial crisis — not twenty-three.

## Artefacts

| File | Role |
|---|---|
| `evalcore.py` | single source of truth: loading, cost model, metrics, seal guard |
| `strategies.py` | weight builders; every signal shifted 1 day, with a look-ahead regression test |
| `integrity.py` | the audit above, writes `out/integrity.json` |
| `PROTOCOL.md` | frozen splits, gates, cost model, contamination disclosure |

Seal status at time of writing: **CLOSED**. `evalcore.load()` refuses any request crossing
2021-01-01; verified by the self-check, which asserts `PermissionError` is raised.
