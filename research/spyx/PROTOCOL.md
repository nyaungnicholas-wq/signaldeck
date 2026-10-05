# SPYX research protocol — frozen 2026-08-31

Target under test (set by Nicholas, not by me):

| Gate | Definition | Threshold |
|---|---|---|
| G1 return | strategy net cumulative return − SPY cumulative total return, over the sealed window | ≥ +10.0 pp |
| G2 Sharpe | annualised mean daily excess return over the **risk-free rate**, ÷ daily excess-return stdev | ≥ 1.00 |

Both must hold **on the same sealed window, after costs**. Either one alone is not a pass.

## 1. Chronological splits — FROZEN

| Block | Dates | Purpose |
|---|---|---|
| DEV | 1993-01-29 → 2015-12-31 | all signal design, all parameter choice |
| VAL | 2016-01-01 → 2020-11-30 | chronological walk-forward; finalist selection |
| EMBARGO | 2020-12-01 → 2020-12-31 | ~21 trading days, no data used from here at all |
| SEAL | 2021-01-01 → 2026-08-28 | graded **exactly once** |

The sealed block is ~1,420 trading days, well above the 252-day floor, and contains a full
bear market (2022), a two-year bull (2023–24) and 2025–26. A candidate cannot pass it by
surviving only one regime.

Multi-sleeve strategies are bounded by the latest instrument inception (DBC, 2006-02), so
their effective DEV is shorter than SPY's. Every reported result names its own `n_days`.

## 2. Seal mechanism

`evalcore.load()` raises `PermissionError` for any request with `end >= 2021-01-01` unless
the environment variable `SPYX_SEAL=OPEN` is set. The guard fires before any file or network
access — it cannot be defeated by a cache hit. `run_panel.py --window seal` refuses and exits
2 when the seal is closed.

The seal is opened once, after the finalist manifest is written and its hash recorded. If the
finalist fails, the result stands as recorded. **The revealed block is never reused as
out-of-sample.**

## 3. Benchmark and baselines

Primary benchmark: **SPY buy-and-hold**, total return, over exactly the sealed dates, run
through the identical cost model. Prices are `auto_adjust=True`, so dividends are inside both
sides — verified by `integrity.py` check C1, which compares adjusted against unadjusted growth.

Secondary baselines, all reported: cash at the T-bill rate; SPY at constant 2× (the naive
leverage control — a candidate that merely levers SPY must be visible as such); equal-weight
sleeves; the diversified trend construction at 1×.

## 4. Cost model — FROZEN

One convention, `evalcore.cost_returns()`, used by every backtest. This closes audit finding
F4 (three engines, three cost conventions).

- **Trading cost** 10 bps charged on absolute weight traded, `Σ|w_t − w_{t−1}|`.
- **Financing** borrowed capital pays `risk-free + 90 bps` annual, charged daily. Idle cash
  earns the risk-free rate. Gross exposure above 1.0 is therefore never free.
- **Risk-free** ^IRX 13-week T-bill, converted to a daily simple rate.
- **Excluded and disclosed:** taxes; market impact beyond the flat 10 bps; borrow cost for
  shorting (no candidate shorts); queue position and partial fills. These are liquid ETFs at
  monthly turnover, so the flat spread is the material term — but it is an assumption, not a
  measurement, and a cost-sensitivity sweep is reported alongside the headline.

## 5. Rules of engagement

1. Signals are lagged one day. `strategies.py` shifts every signal and has a regression test
   that perturbs the final price and asserts no earlier weight moves.
2. Preprocessing, parameter choice and selection use DEV and VAL only.
3. Every candidate evaluated is appended to `out/trials.jsonl`. The configuration count is
   reported with the final result — an unreported search is a lie about the p-value.
4. Multiple testing is priced: Deflated Sharpe Ratio and PBO across the dev/val candidate set.
5. Leverage and exposure limits are frozen in the manifest before the seal opens.
6. Rejected: any result that depends on one instrument, one short interval, fewer than ~30
   position changes, or gross exposure outside the frozen limit.

## 6. Contamination disclosure — read this before believing any seal result

The sealed block is **not virgin to the analyst.** On 2026-08-30 a prior session measured
full-window results spanning 1993/2006 → 2026-08-30 — which *includes* these sealed dates —
for SPY buy-and-hold, SPY 200-day trend, the diversified trend construction at several
leverages, and eight pre-registered premia. Those numbers are on the record in
`stock-trader/experiments/RESULTS_premia.md`.

What that means precisely:

- I have seen **full-period aggregates that contain the sealed dates**. I have not seen the
  sealed block's isolated performance, and no split-sample result was reported at this
  boundary.
- So this is a **sub-period robustness test under partial contamination**, materially weaker
  than a true holdout. It must never be described as virgin out-of-sample discovery.
- The strongest available mitigation is applied: the finalist is chosen only from evidence
  generated inside DEV and VAL in this session, and the seal is opened exactly once.
- The only way to obtain a genuinely virgin holdout here is forward time. That is the
  standing recommendation regardless of how the seal grades.

## 7. Prior findings this protocol must not re-litigate

From the repo's own record and settled memory:

- SignalDeck's cross-sectional equity signal has IC ≈ 0.02 which flips sign by sub-period;
  0 of 27 configs cleared the pre-set bar on a fund-free universe. It is not a candidate here.
- Sharpe is scale-invariant and financing drag makes leverage strictly worse for Sharpe:
  0.75 → 0.72 → 0.70 → 0.67 at 1×/2×/3×/5×. **G1 and G2 therefore pull in opposite
  directions**, and that tension is the central difficulty of this target, not an incidental
  detail.
- Full-sample Sharpe vs cash for the best construction found to date is 0.75. The gate is
  1.00. Passing requires either a genuinely better construction or a favourable window — and
  the second of those is luck, which a single sealed window cannot distinguish from skill.
