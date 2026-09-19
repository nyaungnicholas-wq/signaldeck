# NOT COMPLETE — sealed holdout graded once, both gates failed

Repo `signaldeck` @ `ada8af42f5b8`, branch `hmm-regime-and-pbo`. Graded 2026-08-31T08:21:07Z.
Result: [`out/SEAL_RESULT.json`](out/SEAL_RESULT.json). Raw transcript preserved.

## Verdict

| Gate | Threshold | Measured | Result |
|---|---|---|---|
| G1 return | excess ≥ **+10.0 pp** | **−38.72 pp** | **FAIL** by 48.7 pp |
| G2 Sharpe | net annualised ≥ **1.00** | **0.449** | **FAIL** by 0.55 |

Sealed block **2021-01-04 → 2026-08-28, 1,420 trading days**. One grading run. Not close on
either gate, and the failure is not a near miss that a small change would rescue.

## Full sealed measurements

| | `dt_sma150_invvol` | SPY buy-and-hold |
|---|---|---|
| Cumulative return | 82.66% | **121.39%** |
| CAGR | 11.28% | **15.15%** |
| Annualised vol | 21.92% | 16.73% |
| **Sharpe vs cash** | **0.449** | **0.732** |
| Max drawdown | **−28.50%** | −24.50% |
| ES 95% (daily) | −3.32% | −2.41% |
| Avg gross exposure | 2.15 | 1.00 |
| Max gross | 3.29 | 1.00 |
| Turnover / yr | 11.23 | 0.18 |
| Trade days | 65 | 1 |
| Beta vs SPY | 0.812 | 1.00 |

The strategy lost on **every** dimension simultaneously: less return, more volatility, worse
Sharpe, deeper drawdown, fatter left tail. It did not trade off risk for return — it gave up
both. There is no reading of this table on which the construction was preferable to holding SPY.

Independent recomputation (plain numpy, not via `metrics`) agreed on all eight quantities to
within 1e-6 — e.g. Sharpe 0.449035 vs 0.44903464817843936. The numbers are not a reporting
artefact.

## Cost sensitivity — the failure is not a cost assumption

| cost bps | financing bps | excess pp | Sharpe |
|---|---|---|---|
| 5 | 90 | −32.97 | 0.475 |
| **10** | **90** | **−38.72** | **0.449** |
| 20 | 90 | −49.69 | 0.398 |
| 40 | 90 | −69.66 | 0.295 |
| 10 | 150 | −46.19 | 0.415 |

Even at the most generous assumption tested — 5 bps, half the frozen cost — the strategy still
loses to SPY by 33 pp and reaches only Sharpe 0.475. Zero-cost would not save it.

## Why it failed — evidence, not narrative

**1. Beta rose exactly when it should not have.** Beta vs SPY was 0.422 over DEV+VAL and
**0.812** in the sealed block. The construction stopped being the low-beta diversified book it
was selected for and became a high-beta, higher-volatility one. Sealed vol 21.9% against SPY's
16.7% — it was *more* volatile than the benchmark, having been vol-matched to it on DEV+VAL
(20.16% vs SPY's own realised vol). The vol match did not hold out of sample.

**2. Trade count collapsed.** 65 trade days in 1,420 sealed sessions, against 170 in 3,731
DEV+VAL sessions — the per-day rate roughly halved (0.046/day → 0.037/day) while annual
turnover *rose* from 8.9 to 11.2. Fewer, larger rebalances: the sleeves moved in and out of
trend together rather than independently, which is diversification failing at precisely the
moment it is supposed to pay.

**3. The diversifying sleeves stopped diversifying.** 2021–2026 contained a simultaneous bond
and equity drawdown. TLT, IEF, VNQ and GLD are the sleeves that make the 1/N-with-cash
construction work; when they fall with equities, the book holds correlated risk at 2.15×
average gross, which is the worst possible configuration.

**4. Financing was a real headwind, as predicted before the seal opened.** DEV+VAL ran on
near-zero cash; the sealed block ran 4–5%. At 2.15× average gross the borrowed portion pays
rf + 90 bps continuously. The 150 bps sensitivity row costs a further 7.5 pp of excess,
confirming the channel is live and material.

The pre-seal prediction — recorded in this file before grading — was "G1 looks reachable, G2 is
the binding gate." **That was wrong.** G1 failed worse than G2 relative to its threshold. The
error was assuming the return shortfall could only come from insufficient leverage, when it in
fact came from the strategy underperforming its benchmark outright at 2.15× gross.

## Uncertainty — and why it does not rescue this result

A Sharpe on 1,420 observations has SE ≈ 0.42, so 0.449 carries a 95% interval of roughly
**[−0.37, 1.27]**. The gate value 1.00 is inside that band. So this single window does **not**
establish that the true Sharpe is below 1.00.

That cuts honestly in both directions and it does not save the strategy, because:

- G1 does not depend on that noise in the same way. A −38.72 pp cumulative shortfall over 5.6
  years is a large, directly observed quantity, not a marginal statistic.
- The strategy was beaten by the benchmark on return *and* volatility *and* drawdown
  simultaneously. That joint pattern is not what an unlucky draw from a superior process
  typically looks like.

Both statements are reported because the protocol requires the uncertainty regardless of which
way it points.

## What the development evidence said beforehand, for the record

| | DEV 2006–15 | VAL 2016–20 | **SEAL 2021–26** |
|---|---|---|---|
| Sharpe | 0.752 | 1.015 | **0.449** |
| Excess vs SPY | +227.99 pp | +47.99 pp | **−38.72 pp** |
| Beta | — | — | 0.812 (vs 0.422 in-sample) |

PBO 0.057 and Deflated Sharpe 0.9955 both looked reassuring in-sample. **They were not
predictive here.** That is the single most useful lesson in this file: PBO measured across 8
near-identical configurations tests only whether the *SMA choice* generalises. It cannot detect
that the whole rule family depends on a correlation structure that changed. Low PBO is not
evidence of out-of-sample validity when the candidate set is narrow — and this is a measured
instance of that failure, not a theoretical caveat.

## Protocol compliance

- Finalist frozen **before** any sealed data was read; manifest hashes `bec0f3fb…` /
  `ef5bfd30…` verified by the grader at run time and matching after.
- Seal opened once. `evalcore.load()` refused all pre-grading access (`audit_finalist.py` A1).
- Adversarial audit **11/11** pre-seal, including causality-by-truncation 25/25 and an exact
  accounting identity.
- **No tuning against the revealed block.** The revealed holdout is now spent and must never be
  reused as out-of-sample.
- The first grading run computed the identical verdict but crashed writing the JSON (numpy bool
  not serialisable), leaving a truncated file. That partial file is **preserved** as
  `out/SEAL_RESULT.partial-crash-01.json`; the serialiser was fixed and the run repeated. No
  strategy, metric, data or window code changed — the frozen hashes still match, and the
  recomputed numbers are identical to the crashed run's printed output.

## Next valid research step

1. **Do not iterate on this rule family against this block.** It is revealed. Any further work
   on trend-following ETF sleeves needs a *new future* holdout.
2. **The honest reading of the whole exercise:** across every *causal* construction tested
   here, the best Sharpe measured in any window is 0.90, and the only one that reached 1.015
   did so in a single validation window and then delivered 0.449 when it mattered.
3. **Structural conclusion — CORRECTED, see the ceiling section below.** Long/flat trend on
   nine long-biased ETFs did not produce an uncorrelated return stream when bonds and
   equities fell together. But the earlier, stronger claim — that no Sharpe ≥ 1.00 strategy
   exists in this instrument set — is **refuted by measurement**: the hindsight ceiling on the
   same nine sleeves is 1.230. The binding constraint is the *rule*, not the universe.
4. **The only uncontaminated evidence available here is forward time.** A pre-registered
   forward test remains the sole route to a genuinely virgin holdout.
5. **Production baseline — gap now closed, see below.**

## Production strategy re-measured (`prod_baseline.py` → `out/prod_*.json`)

`stock-trader` V7 PUSH-20 (`apply_daily_champion` + `run_rotation_backtest`), its daily equity
curve bridged into this protocol's metrics and benchmark. Development windows only; the seal
was not reopened.

| Window | years | Strategy CAGR | SPY CAGR | **excess/yr** | cumulative pp | Sharpe | SPY Sharpe | Max DD | Beta |
|---|---|---|---|---|---|---|---|---|---|
| DEV 1993→2015 | 22.9 | 7.87% | 8.96% | **−1.09 pp/yr** | −147.47 | 0.365 | 0.412 | −42.9% | 0.505 |
| VAL 2016→2020 | 4.9 | 20.98% | 14.94% | **+6.04 pp/yr** | +56.41 | 0.820 | 0.770 | −35.8% | 1.104 |
| DEV+VAL 1993→2020 | 27.8 | 9.86% | 9.91% | **−0.04 pp/yr** | −15.01 | 0.456 | 0.471 | −42.9% | 0.611 |

**READ THE ANNUALISED COLUMN, NOT THE CUMULATIVE ONE.** The −147.47 pp figure is not a
catastrophe, it is compounding: a −1.09 pp/yr gap over 22.9 years compounds to −147.48 pp,
matching the reported value to two decimals. Cumulative percentage points are not comparable
across windows of different length, and quoting them alone invites exactly this misreading.
An earlier draft of this file did misread it.

**What the numbers actually say.** Over the full 27.8 years this strategy returns 9.86% CAGR
against SPY's 9.91% — a four-basis-point annual difference, which is a dead heat, not
underperformance. Sharpe 0.456 vs 0.471 is likewise a tie. It achieves that at **−42.9% max
drawdown against SPY's −55.2%**, with beta 0.611. So: roughly SPY-equivalent return, materially
shallower drawdown, lower beta.

**The fold spread is still real and still the caution.** −1.09 pp/yr over 1993–2015 against
+6.04 pp/yr over 2016–2020 is a wide swing, and the published headline (CAGR 19.8%, Sharpe
0.83) starts in 2006 — a date that excludes the weaker stretch. Neither gate passes in any
window. But "dead heat with a shallower drawdown" is a materially different claim from
"underperforms", and the evidence supports the former.

**Caveat, stated rather than assumed away:** this engine was designed and tuned on a 2006+
window, and its universe coverage in the 1990s is thinner, so the DEV figure may partly reflect
a degraded early universe rather than pure strategy weakness. The comparison is also not a
perfect like-for-like — the strategy side carries the engine's internal cost model while the
benchmark side carries this protocol's. Both caveats are recorded in `out/prod_*.json`.

## Hindsight ceiling — a correction to my own conclusion

Run after grading, on DEV+VAL only (`ceiling.py` → `out/ceiling.json`). It computes the best
Sharpe obtainable by a long-only *static* allocation whose weights were optimised **with full
hindsight on the same data**. No causal rule can beat that, so it is an upper bound.

| Universe | Rows | SPY Sharpe | Equal-weight | **Hindsight ceiling** |
|---|---|---|---|---|
| sleeves9 (2006-02-06→) | 3,731 | 0.499 | 0.568 | **1.230** |
| sectors, equity only (1998-12-22→) | 5,521 | 0.358 | 0.417 | **0.488** |
| broad25 (2013-07-18→) | 1,857 | 0.756 | 0.713 | 1.486 |

Optimal sleeve weights with perfect foresight: **IEF 55.8%, QQQ 26.3%, TLT 9.9%, GLD 8.0%,
SPY 0.0%.**

**This refutes a claim made earlier in this file.** I wrote that the evidence did not support
the existence of a Sharpe ≥ 1.00 strategy in this instrument set. That was an overreach: a
static allocation over the very same nine sleeves clears 1.00 on 2006–2020. The instrument set
is not the binding constraint — the rule is.

What the ceiling actually shows:

1. **The available Sharpe is a bond bet.** IEF + TLT are two-thirds of the optimal book and
   SPY gets exactly zero weight. That Sharpe is the 2006–2020 bond bull market. The sealed
   block is where that regime reversed — the same mechanism that killed the finalist, seen
   from the other side.
2. **Equity selection contributes nothing.** Nine equity sectors cap at 0.488 *with hindsight*.
   All of the achievable Sharpe comes from equity/bond diversification. This rules out a whole
   family of candidate directions before any effort is spent on them.
3. **`broad25`'s 1.486 is not trustworthy.** Its sample is only 1,857 rows starting
   2013-07-18 (MTUM/QUAL inceptions bind it) — short and almost entirely inside the bond bull.
   The `sleeves9` figure over 3,731 rows is the meaningful one.

The sharpened research question is therefore *not* "does an edge exist here" but: **can a
causal rule find a bond-heavy risk-parity allocation without hindsight, and survive that
allocation's regime reversing?** 2021–2026 punished exactly that reversal.

## DATA-LINEAGE DEFECT — found after grading, and it outlives this experiment

The regression suite (`test_spyx.py`) failed audit check **A7 reproduce_manifest_metrics**
immediately after being written, with discrepancies of 2–6 × 10⁻⁶ against the frozen manifest.
That is not a code regression. **The data underneath changed.**

### What happened

`evalcore.load()` selects, among cached parquet files, the one with the **widest** date range
that covers the request. The cache is a shared, mutable directory that other processes also
write to. So which file a backtest reads depends on what has run before it:

| Time | Process | File it created for TLT |
|---|---|---|
| 00:40 | dev-window probe | `TLT_1993-01-29_2015-12-31.parquet` |
| 00:45 | `candidates.py` — **finalist selected here** | `TLT_1993-01-29_2020-11-30.parquet` |
| 01:20 | `seal_run.py` — **finalist graded here** | `TLT_1993-01-29_2026-08-28.parquet` ← now widest, so it wins every later load |
| 01:39 | `prod_baseline.py` (the stock-trader engine writes to the same cache) | `TLT_1991-12-06_2020-11-30.parquet` |

The 01:20 files are a **fresh vendor download with revised adjustment factors**. Measured
against the previously-selected snapshot over 6,060 overlapping days:

- TLT: max relative difference **2.077e-06**, mean 3.169e-07, differing on 5,334 of 6,060 days
- IEF: max relative difference **2.032e-06**, mean 3.126e-07, same 5,334 days

TLT, IEF, GLD and DBC were all re-downloaded, because no cached file started early enough to
cover `DEV_START` — their existing files began 1996-01-01.

### Does this invalidate the graded result? No.

- `seal_run.py` created and used one consistent snapshot throughout its own run, so the graded
  numbers are internally coherent, and `SEAL_RESULT.json` records the digests it ran under.
- The margins are 48.7 pp on G1 and 0.55 on G2. A 2 × 10⁻⁶ relative price perturbation cannot
  move either. The verdict is robust.
- Config ranking is likewise safe: Sharpe gaps between the 8 candidates are ~0.1, five orders
  of magnitude larger than the perturbation.

**But the finalist was selected on one data snapshot and graded on another**, and nothing in
the protocol noticed. That is the defect, and it would matter for any result with a thin margin.

### Two of my own claims this corrects

1. I wrote that the grading run "needs no network" because cache coverage through 2026-08-30
   was confirmed. **Wrong** — I checked only the *end* date. Four sleeves had no file starting
   early enough, so `seal_run.py` hit the network and wrote new files.
2. A7's failure is the regression suite working correctly on its first run. It should not be
   silenced, and its tolerance must not be loosened to make it pass.

### Root-cause fix — IMPLEMENTED

`evalcore.load()` now resolves data through an explicit pin at `out/data_manifest.json`. The
first resolution of a ticker records the exact filename; every later load uses that file. A
request the pinned file cannot serve raises a **loud** `ValueError` naming both ranges and
telling the caller to re-pin deliberately — it never silently swaps to different data. Verified:
pinning SPY to a deliberately too-narrow file makes a dev-window load refuse rather than serve.

All ten series are pinned (9 sleeves + `^IRX`).

**This changed `evalcore.py`'s digest, `ef5bfd309f9d9ea0…` → `d0dee48079c14eee…`.** The graded
record is unaffected: `out/SEAL_RESULT.json` independently records that the seal ran under
`ef5bfd309f9d9ea0…`, so what was graded, and under what code, remains verifiable. The manifest's
`frozen_hashes` now describes the pre-fix code and should be treated as historical.

### Known failing check — do not silence it

`test_spyx.py` reports **A7 reproduce_manifest_metrics FAIL** (3 pass, 1 fail). This is correct
behaviour, not a defect: the manifest's dev/val metrics were computed on the 00:45 snapshot and
cannot be reproduced on the pinned 01:20 data. The discrepancies are 2–6 × 10⁻⁶.

It is deliberately **not** fixed by loosening the tolerance, and deliberately not fixed by
re-running `candidates.py` — regenerating a frozen manifest after grading is exactly the kind of
quiet rewrite that makes a protocol untrustworthy. A7 must pass on the next clean run from
scratch under pinned data; if it does not, something is genuinely wrong.

## Post-mortem: the ceiling on the spent block, and what actually went wrong

Run after grading with `ceiling.py --window seal`. The seal is spent; this **selects no strategy
and grades nothing**. It characterises what the period rewarded, to test whether the "bond bull
reversed" explanation is correct rather than merely plausible.

Hindsight-optimal long-only static allocation, nine sleeves, both windows:

| Sleeve | DEV+VAL 2006–2020 | SEAL 2021–2026 |
|---|---|---|
| IEF | **55.8%** | **0.0%** |
| TLT | 9.9% | **0.0%** |
| QQQ | 26.3% | 14.3% |
| GLD | 8.0% | **29.3%** |
| DBC | 0.0% | **30.6%** |
| SPY | **0.0%** | 25.8% |
| **Ceiling Sharpe** | **1.230** | **1.045** |

Bonds go from **two-thirds of the optimal book to exactly zero**. Commodities plus gold go from
8% to 59.9%. SPY goes from zero to a quarter. Overlap between the two optimal vectors (sum of
elementwise minima) is **0.223** — about 78% of the allocation inverted.

### This reframes the failure

1. **The Sharpe was available in the sealed window too.** The ceiling there is 1.045, above the
   1.00 gate. The finalist scored 0.449 not because the period was unrewarding, but because it
   was holding the wrong assets.
2. **So the target was never a rule-tuning problem.** It is a *regime-prediction* problem. Both
   windows contain a portfolio clearing Sharpe 1.00; the portfolios are nearly orthogonal. No
   amount of tuning an SMA length on 2006–2020 data can discover that DBC and GLD would be the
   2021–2026 winners while IEF and TLT would be worthless.
3. **This is the mechanism behind the beta shift.** With bonds contributing nothing, a
   trend-following book that keeps allocating to them holds dead weight and its equity sleeves
   dominate — which is exactly the observed 0.422 → 0.812 beta.
4. **The `sectors` universe also clears 1.00 in the sealed window (1.074, led by XLE 35.7%)**,
   against 0.488 in DEV. Even the "equity selection contributes nothing" conclusion is
   window-specific — it was true of 2006–2020 and false of 2021–2026, driven by energy.

The honest research question this leaves is narrow and hard: **can any causal rule detect an
allocation regime change of this magnitude near enough to real time to act on it?** Trend
following is the standard attempt, and on this evidence it was too slow — it rebalanced monthly
on a 150-day filter while the leadership rotated underneath it.

## Standing caveats

Taxes excluded. Market impact beyond a flat spread not modelled; no borrow cost (nothing
shorts); no latency, rejected-order or partial-fill modelling — monthly ETF rebalances at this
size make queue effects immaterial, but that is an assumption. Universe is
survivorship-selected. Multi-sleeve history begins 2006-02-06 (DBC inception). The sealed block
was analysed in aggregate by a prior session, so this is a **contaminated sub-period test, not
virgin out-of-sample** — see PROTOCOL.md §6.

No live-money order was placed by any of this. Nothing here authorises live trading, and a
failed backtest certainly does not.
