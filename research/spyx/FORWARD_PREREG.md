# Forward-test pre-registration — the only uncontaminated holdout left

Registered 2026-08-31, repo `signaldeck` @ `ada8af42f5b8`. **Not yet started.** This file is a
commitment device, not a result.

## Why this exists

The 2021-01-01 → 2026-08-28 block was graded once on 2026-08-31 and is **spent**. Every other
window in the available data is disqualified:

| Window | Why it cannot serve as a holdout |
|---|---|
| 2006–2015 | used as DEV; all signal design and parameter choice happened here |
| 2016–2020 | used as VAL; finalist selection happened here |
| 2020-12 | embargo |
| 2021–2026 | **revealed** by the single grading run |

There is no remaining historical window that is uncontaminated. Additionally, the benchmark
statistics for every one of those windows have now been observed, so even switching to a new
instrument universe over old dates would be testing against a benchmark whose outcome is
known. **Forward time is the only source of virgin evidence.**

This is why the target cannot be re-attempted tonight, and why re-running anything against
2021–2026 would be dishonest rather than merely weak.

## Registered hypothesis

Stated before any forward data exists:

> A long/flat trend construction on nine liquid ETF sleeves, levered to benchmark risk, does
> **not** achieve a net annualised Sharpe ≥ 1.00 nor beat SPY by ≥ 10 pp out of sample.

Note the direction. Given the graded evidence — sealed Sharpe 0.449, excess −38.72 pp, beta
rising 0.422 → 0.812 — the registered expectation is **failure**, and the forward test is
registered to see whether that failure replicates rather than to hunt for a pass.

## Frozen specification

- **Strategy under test:** `dt_sma150_invvol` exactly as graded — 9 sleeves
  (SPY EFA QQQ IWM TLT IEF GLD DBC VNQ), each held when above its own 150-day SMA,
  inverse-volatility weighted within the in-trend set, monthly rebalance, signal applied from
  the next session, leverage 3.29.
- **Code hashes:** `strategies.py` `bec0f3fb07048f9a…`, `evalcore.py` `ef5bfd309f9d9ea0…`.
  A grading run must refuse if either differs.
- **Costs:** 10 bps on |Δw|, financing at rf + 90 bps annual, idle cash at rf. Unchanged.
- **Benchmark:** SPY buy-and-hold, total return, identical dates, identical cost model.
- **Start:** the first trading session after this file is committed.
- **Minimum duration before grading:** 252 trading days. Grading earlier is prohibited —
  a shorter window would have a Sharpe standard error above 1.0 and could not distinguish
  any hypothesis from any other.
- **Gates:** unchanged — excess ≥ +10.0 pp cumulative AND net annualised Sharpe ≥ 1.00.
- **Runs allowed:** one.

## What would change my mind

The registered expectation is failure. It would be overturned only by the forward test
clearing **both** gates on ≥252 sessions with no code change. Anything less — clearing one
gate, clearing both on a shorter window, or clearing them after any modification — does not
count and must be reported as a failure to replicate.

## What this cannot fix

A forward test removes contamination. It does **not** remove:

- **Survivorship** in the instrument set; all nine ETFs were selected in the present.
- **The structural argument.** Nine long-biased ETFs cannot produce an uncorrelated return
  stream when bonds and equities fall together, which is exactly what 2021–2026 delivered.
  Reaching Sharpe ≈ 1 in managed futures requires 50–100 markets including FX and rates,
  traded both long and short. No amount of forward data changes what this universe can express.
- **The multiple-testing debt** inherited from prior sessions, which PBO over 8 near-identical
  configurations did not price — and which, measured against the sealed outcome, it visibly
  failed to detect.

## Honest expected value

Low. The graded evidence is not a near miss: the strategy lost to SPY on return, volatility,
Sharpe and drawdown simultaneously, and survived no cost assumption tested. The most probable
outcome of this forward test is that it confirms the failure.

Registering it anyway is the point — the alternative is quietly abandoning a refuted rule and
later rediscovering it, which is how the 1,414 non-replicating configurations in this repo's
history came to exist.

## Live trading

Not authorised by this file, by the graded result, or by any outcome of the forward test.
Execution stays disabled.
