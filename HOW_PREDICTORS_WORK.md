# How every SignalDeck predictor works — inputs, math, gates, output

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** SUPERSEDED — historical record; current status is `STRATEGY_DECK.md`
> **Scope:** Per-predictor reference — inputs, math, gates, outputs.
> **Frozen claim classes:** FC3 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, statuses in `proofs/P10_FREEZE_LIFT.md` §4
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE as a record, NOT as a current statement — see `STRATEGY_DECK.md`

> **Backtest data: `pre-survivorship-fix`.** Every historical figure below was
> computed on the universe as it stood BEFORE the 2026-08-04 survivorship
> backfill (`proofs/P3A_SURVIVORSHIP_BACKFILL.md`) and the point-in-time
> universe rebuild (`proofs/P3B_PIT_UNIVERSE.md`). It has not been re-run on
> the repaired universe. Read the numbers as a record of what was measured
> then, not as what the repaired data would produce now.

> ## ⛔ SUPERSEDED — publishable as a record, not as current status
> **P0 freeze 2026-08-04, lifted 2026-08-04** (`proofs/P10_FREEZE_LIFT.md`). Remediation
> is complete. This document is publishable as a record of what was measured and when;
> it **must not be quoted as *current* status** — quote `STRATEGY_DECK.md` for that.
>
> Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **live accuracy · structural band tables ·
> survivorship control · point-in-time data.**
>
> Known defects: the Part C live record disagrees with `PREDICTION_PROCESS.md`,
> `CASE_STUDY.md` and `INSTITUTIONAL_GAP.md`; the §B1–B3 band tables (up to 97.2%)
> are backtests computed over a bar history measured survivor-seeded in
> `ALPHA_WORKFLOW.md` §B2, and §B5 already shows ~74% of trend21's conviction spread
> is barrier geometry and that liquidity21 scores **worse** than its Φ(z) null.
>
> **Part C reconciled in P2 (2026-08-04)** — every document now includes the same
> generated block (`proofs/P2_LIVE_RECORD_RECONCILIATION.md`). **PIT universe
> closed in P3B (2026-08-04)** — `universe_membership` holds 1,854,228 rows over
> 2,146 days (`proofs/P3B_PIT_UNIVERSE.md`); the band tables below were computed
> before that and have not been re-derived against it.
>
> Authority: `proofs/P0_FREEZE.md`, **lifted 2026-08-04** by `proofs/P10_FREEZE_LIFT.md`.

Read from the code on 2026-08-03. Companion to `PREDICTION_PROCESS.md` (which
covers the pipeline and the 18-point bias gate); this file is the per-predictor
arithmetic.

---

## 0. The two families

SignalDeck runs two completely separate prediction families. They do not share
math, horizons, or grading, and only one is currently live.

| family | predicts | horizon | status today |
|---|---|---|---|
| **Directional** (7 legs → ensemble → calibration) | P(price up) | 1h / 1d / 1w | **RETIRED — not emitting.** Every horizon graded below its prequential-majority null; figures in PART C |
| **Structural** (trend21, vol21, liquidity21, trend63, 2 crypto twins, filingsdrift21) | which *regime* persists, never price direction | 21d / 63d | **PENDING.** ~11.8k forecasts recorded, 0 resolved. First gradable **2026-08-07** |

---

# PART A — THE DIRECTIONAL FAMILY

Seven independent legs each produce a P(up). Legs that cannot prove
out-of-sample lift are **dropped, not down-weighted**. Survivors are blended
with learned weights, then calibrated, then recorded and graded.

## A1. Pressure Score (leg `pressure`)
`internal/signals/score.go`

A weighted vote of classical indicators, each normalised to [−1, +1].

**Components**

| component | formula | notes |
|---|---|---|
| `trend_sma` | (close>SMA200 ? +0.5 : −0.5) + (SMA50>SMA200 ? +0.25 : −0.25) + (close>SMA20 ? +0.25 : −0.25) | ties read "below" — equality is not bullish |
| `momentum_roc` | clamp(ROC(n) / scale) | 1h: n=60 min bars, scale 0.005 · 1d: n=5 days, scale 0.02 · 1w: n=20 days, scale 0.05 |
| `rsi` | clamp((RSI(14) − 50) / 25) | 50→0, 25/75→∓1 |
| `macd` | clamp(hist / (0.005 × close)) | MACD(12,26,9) histogram; ±1 = histogram ≥0.5% of price |
| `vwap_dist` | clamp((close − VWAP20) / ATR14) | dropped if ATR ≤ 0 |
| `rvol_confirm` | clamp((RVOL − 1)/2) × sign(momentum) | volume as confirmer; dropped if momentum missing |
| `imbalance` | clamp(signed book imbalance) | crypto only, requires fresh 1s snapshot |
| `vol_regime` | Bollinger-width percentile over 90 bars | **weight 0 — informational only.** Width does not predict direction |

**Weights per horizon** (renormalised over whatever survives; if nothing
survives the horizon is omitted entirely):

- **1h**: momentum 0.30, RSI 0.15, MACD 0.15, VWAP 0.15, imbalance 0.25 (crypto only)
- **1d**: trend 0.35, momentum 0.20, RSI 0.15, MACD 0.15, VWAP 0.10, imbalance 0.05
- **1w**: trend 0.45, momentum 0.25, MACD 0.15, RSI 0.10, rvol_confirm 0.05

`Score = clamp(Σ normᵢ × wᵢ / Σwᵢ)` → leg probability `p = (score + 1) / 2`.

**Gate:** fail-safe and asymmetric. Dropped only on a *measured* negative
lift (`PressureLift ≤ 0`); an unmeasured leg is kept. A cold trainer never
blanks the platform's oldest leg.

## A2. Expectancy (leg `expectancy`)
`internal/expectancy/`

Not a model — a conditional lookup table. The current bar is mapped to a
discrete **state key** (trend vs SMA200/SMA50, RSI bucket, RVOL bucket,
momentum ROC bucket — 20 daily bars / 60 minute bars). The leg's probability is
the historical fraction of positive forward returns observed in that same state
cell.

- SMA200 used when ≥60 bars, else SMA50; below that the trend dimension is dropped.
- Daily walk capped at 500 bars, min 80; minute min 300 bars, sampled every 15 bars to decorrelate overlap.
- **Gate:** `minSamples = 5` — a state thinner than 5 observations is not emitted.
- Used as-is: it is already a P(up).

## A3. Logistic forecast (leg `forecast`)
`internal/forecast/forecast.go` — from-scratch logistic regression, no ML deps.

**8 features**, all past-only, computed at bar *i*:
`ret1, ret5, ret10` (pct returns), `(RSI14 − 50)/50`, `(close−SMA20)/SMA20`,
`(close−SMA50)/SMA50`, realized vol (stdev of last 20 log returns),
`volume / SMA20(volume)`.

**Fit:** batch gradient descent, `iters = 800`, `learningRate = 0.1`,
`l2Reg = 1e-3`, weights initialised to zero (fully deterministic, no RNG).
Warm-up = 50 bars. `minLabeledSamples = 150` (~19 rows per feature).

`P(up) = σ(w·x + b)`, `σ(z) = 1/(1+e^{−z})`.

**Gate:** purged, embargoed, expanding-window walk-forward. Purge width comes
from the data — the widest declared label horizon — plus one bar. Admitted only
if `Lift = Accuracy − BaseRate > 0`. An unpurgeable grade is withheld, not published.

## A4. GBM (leg `gbm`)
`internal/gbm/gbm.go` — from-scratch gradient-boosted trees on log-loss.

**Hyperparameters (fixed, not config, so grades stay comparable):**
`NEstimators = 60`, `MaxDepth = 3`, `LearningRate = 0.1`, `MinLeaf = 8`,
`L2 = 1.0` (ridge shrink on leaf values).

Prediction = `σ(bias + Σ 0.1 × treeₖ(x))` where bias is the initial log-odds.

**Gates:** `minTrainSamples = 60`, `minPerFold = 12`, embargo denominator 10,
same purged walk-forward as the logistic. Admitted only on OOS lift > 0.

## A5. Mean reversion (leg `meanrev`)
`internal/meanrev/meanrev.go`

Pure reflection of the momentum blend's own raw probability — no fitting:

```
pMR = 0.5 + strength × (0.5 − pRaw),   strength = 1.0
```

So a momentum call of 0.70 becomes a mean-reversion call of 0.30. When the
momentum lean is exactly 0.5 the leg says nothing.

**Gate — the strictest one:** graded walk-forward **net of a 10bp round-trip
cost**, against the best no-skill *constant* strategy (always-long or
always-short) on cost-net labels. A move too small to trade counts as a loss for
the signal. `minSamples = 60`. Dropped unless cost-net lift > 0.

## A6. Sentiment (leg `sentiment`)
`p = 0.5 + score × 0.15`, score ∈ [−1, +1] from the daily news aggregate.

**Gate:** ≥3 headlines, ≤3 days old. The 0.15 scale is deliberate — maximal
sentiment moves the leg only 0.15 from a coin flip, so a weak noisy signal can
never dominate the blend.

## A7. Cross-sectional alpha (leg `alphax`)
`internal/alphax/` — pooled model of **P(this symbol beats the same-day universe
median forward return)**. A *relative* probability, deliberately blended into a
directional blend as a tilt. Purged walk-forward by UTC day with horizon-aware
embargo (de Prado). Dropped unless OOS lift > 0.

## A8. The blend
`internal/ensemble/ensemble.go` + `internal/adaptive/adaptive.go`

`RawProbability` = weighted average of surviving legs. Weights come from the
adaptive worker (every 6h), per **regime cell**, and this is the most careful
piece of math in the system:

1. **Standard error over DAYS, not rows.** `se = 0.5/√days` under the no-skill
   null p=0.5. Rows repeat one call across a day's symbols — 12.1 rows per
   symbol-day measured, so row-based SEs understate noise by ~√12.
2. **Empirical-Bayes shrinkage** toward no-skill (James-Stein / Efron-Morris):
   `τ² = max(0, mean(edge²) − mean(se²))`, then `shrunk = edge × τ²/(τ² + se²)`.
   If the spread is fully explained by noise, τ² = 0 and every edge shrinks to
   exactly zero.
3. **Bonferroni lower bound.** A leg earns weight only if
   `edge − z(α/tests) × se > 0`, α = 0.05, tests = number of (cell, leg)
   candidates.

`weight = shrunkEdge / Σ shrunkEdge` among survivors, per cell.
**Floors:** `MinCellSamples = 30` rows AND `MinCellDays = 20` distinct UTC days.
Fallback chain: regime cell → global (`all`) → static equal prior. Per-symbol
models override once a symbol has 40 of its own resolved outcomes.

## A9. Calibration
Isotonic (PAV) map fit **prequentially** — only on pairs already resolved when
the point was made, so no prediction is graded by a map trained on itself.

- `MinCalibrationPairs = 30`, `MinCalibrationDays = 20`; below that `Calibrate`
  returns identity and reports `calibrated = false`.
- Blocks shrunk toward the base rate with pseudocount `25.0`.
- **Re-monotonised after shrinking** — the 2026-07-26 review found 495 of 928
  persisted live maps had non-monotone knots because shrinkage reordered what
  PAV had just ordered. Do not remove that second pass.
- Persisted per-symbol maps display-clamped to **[0.25, 0.75]** — realized 1-day
  accuracy near 51% cannot support a displayed 0.85.

## A10. Conviction (the second axis)
`internal/composite/conviction.go` — deliberately a small ordinal
(low/moderate/high), never a false-precise number.

The **ceiling** is set by the model's MEASURED realized accuracy, never by the
per-symbol calibrated probability:
`strongWinRate = 0.58` → HIGH ceiling · `modestWinRate = 0.53` → MODERATE ·
below → LOW.

Then each of these **lowers the band one notch** (floored at low):
- **Overconfidence:** `|calProb − 0.5| ≥ 0.20` while win rate < `0.60`. A large
  edge the realized accuracy can't support *reduces* conviction.
- `|edge| < 0.02` — the symbol's own lean is inside coin-flip range.
- Prediction older than `26h`.
- Fewer than 2 legs blended.
- Factors disagree (bullish and bearish tiles both present, neither ≥2× the other).

## A11. Confidence score
`internal/confidence/confidence.go`

```
confidence = 0.50×edge + 0.25×calibration + 0.25×sample
  edge        = clamp01(Lift / 0.10),  ×0.5 if Lift < its own half-width,  0 if Lift ≤ 0
  calibration = clamp01(1 − CalibrationErr / 0.10)
  sample      = clamp01(EffectiveN / 300)      ← day-clustered effective N, not rows
```
If calibration was not measured, its weight is redistributed:
`(0.50×edge + 0.25×sample) / 0.75`. `MinEpisodes = 30` for the downside
(adverse-excursion) measurement.

## A12. EV decision engine
`internal/ev/ev.go` — pure, no I/O.

```
extra  = max(0, roundTripCostFrac − τ)
NetEV  = DistEV − extra
```
where `DistEV` is the expected return from the cost-aware return distribution
and τ = 10bps is the no-trade band. NetEV is *unmeasurable* (not zero) if either
the distribution or the cost is missing — the engine refuses with a named reason.

**Refusal envelope** (all env-overridable):
`MinNetEV = 0.0` · `MaxPInside = 0.75` (mass inside the cost band) ·
`MaxTailP90 = 0.10` (90th-pct adverse excursion) · `MaxRank = 10`.

## A13. Model health — the kill switch
`internal/modelhealth/modelhealth.go`, hourly.

```
overall = 0.40×skill + 0.20×calibration + 0.20×drift + 0.10×freshness + 0.10×stability

skill       = clamp01(0.5 + (accuracy − baseline)/0.10)     ← baseline = majority class, NOT 50%
calibration = clamp01(1 − calErr/0.20),  ×0.5 if Brier skill < 0
drift       = clamp01(0.5 + (recentAcc − accuracy)/0.10),   0.5 if recentN < 30
freshness   = clamp01(1 − ageDays/90)
stability   = clamp01(1 − featureDriftPct)
```

Verdicts: `< 0.35` retired (emitting **false**) · `< 0.55` degraded ·
`< 0.70` watch · else healthy. Below **30 independent observations** →
provisional, no verdict claimed either way.

**Overriding rule:** `edge < 0` retires the model outright regardless of every
other component. That is the rule that caught the directional ensemble.

---

# PART B — THE STRUCTURAL FAMILY

`internal/structregime/`. These predict **which regime persists**, never price
direction. Shared constants: `window = 200` (trailing distribution),
`minHistory = 260` bars, `horizon = 21` trading days, `ewmaLambda = 0.94`
(RiskMetrics), `maxSaneReturn = 0.65` (refuses any window containing a
split-artifact move).

## B1. trend21
**Question:** will the stock still be on its current side of the 200-day SMA in
21 sessions?

```
cur   = close/SMA200 − 1                    → regime = uptrend if cur>0 else downtrend
absd  = |close/SMA200 − 1| for every bar
conv  = percentile of today's |cur| within the trailing 200-bar absd window
```
Backtested accuracy by band: `<0.5 → 73.1%` · `0.5–0.8 → 90.0%` ·
`0.8–0.9 → 94.6%` · `≥0.9 → 97.2%` (cumulative all-decisions 83.3%).

## B2. liquidity21
**Question:** will mean daily dollar volume over the next 21 sessions be above or
below its trailing 200d median?

```
dv    = log(close × volume)
m     = 21-bar rolling mean of dv
rank  = percentile of m[today] in the trailing 200-bar window of m
conv  = 2 × |rank − 0.5|                    → regime = active if rank>0.5 else quiet
```
Bands: `<0.5 → 59.5%` · `0.5–0.8 → 73.9%` · `0.8–0.9 → 80.1%` · `≥0.9 → 87.6%`.

## B3. vol21
**Question:** will next-21d realized vol sit above or below its trailing median?

```
ev    = EWMA volatility of returns, λ = 0.94
rank  = percentile of ev[today] in the trailing 200-bar window
conv  = 2 × |rank − 0.5|                    → regime = elevated if rank>0.5 else calm
```
Bands: `<0.5 → 55.8%` · `0.5–0.8 → 64.3%` · `0.8–0.9 → 66.8%` · `≥0.9 → 72.0%`.

## B4. trend63 / trend21-crypto / liquidity21-crypto
Same code paths as B1/B2 with the horizon or the asset tables swapped; only
`Kind`, `HistoricalAccuracy` and `Tradeability` are overwritten.

## B5. The geometry caveat (2026-08-03) — the most important thing here

Both trend21 and liquidity21 ask "does a value stay on the same side of a slow
line?" and define conviction as distance from that line. A value far from a
line needs a large move to cross it — so **conviction predicts correctness for a
reason that is arithmetic, not market**.

Control for barrier distance in units the horizon can actually move,
`z = |value − reference| / σ(21d change)`, against `Φ(z)` — the driftless
random-walk probability of ending on the starting side, zero free parameters:

| kind | result |
|---|---|
| **trend21** (57,158 samples, 1,305 days) | tracks Φ(z) within ~2pp in every z band. The +24.5pp conviction spread falls to +6.3pp inside z bands — **~74% of it is just distance**. Hansen SPA: conviction adds nothing over z (p=0.204); z adds over conviction (p<0.001) |
| **liquidity21** (64,847 samples) | **worse than the null.** 71.1% vs Φ(z)'s 76.4%. The +28.1pp spread *inverts* to −5.1pp inside z bands, because log dollar volume mean-reverts and crosses its median more often than a random walk. Brier: z 0.1542 vs conviction 0.1630 |
| **vol21** (64,305 samples) | **the exception.** 51% of the +15.7pp spread survives inside z bands (+7.9pp) and neither side dominates (SPA p=0.073 / p=0.200). But size it honestly: the pure geometric rule scores 61.62% and the full model 62.16% — the entire edge is **+0.54pp**, and SPA cannot establish it at 5% |

**Deliberate non-fix:** `PredictVol21` ranks EWMA(0.94) while `ResolveVol21At`
grades against flat 21d realized vol; the signs disagree 13.1% of the time.
Matching them would score the *same* model at 71.65% instead of 62.16% — a
+9.50pp inflation, 17× the entire disputed edge, bought by grading a forecast
against a barrier built from its own estimator. The mismatch is the conservative
setup and stays.

---

# PART C — WHAT THE LIVE RECORD SAYS TODAY

This block is generated from `data/accuracy_registry.json`. It is the same block
every other document in this repository includes, and nothing below is typed by
hand — that is the whole point of it (FC1; see
`proofs/P2_LIVE_RECORD_RECONCILIATION.md`).

<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (registry `REFUSED` since 2026-09-09T16:49:48) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **GRADING REFUSED — no accuracy figures are published.** Reason: publication gate: the graded window contains 23 collapsed cross-section(s) of 83 day(s): 1d 2026-07-27 (6 distinct across 330 symbols), 1d 2026-07-28 (8 distinct across 330 symbols), 1d 2026-07-29 (13 distinct across 328 symbols), 1d 2026-07-31 (6 distinct across 328 symbols), 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-17 (148 distinct across 1032 symbols), 1w 2026-07-18 (100 distinct across 1032 symbols), 1w 2026-07-21 (18 distinct across 317 symbols), 1w 2026-07-22 (29 distinct across 317 symbols), 1w 2026-07-23 (3 distinct across 67 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.. The grade computed at 2026-09-09T16:48:38 (0.0h old) is withheld, not lost: it is retained inside the registry under `stale_last_registry` for the historical record and is deliberately not reprinted here, because a number the publication gate refused to stand behind is not a live number. The in-app `/accuracy` page and `/api/accuracy` apply the same gate from the same registry.

<!-- END GENERATED live_accuracy -->

Every interval is `withheld` — the distinct-day counts are below the
`min_distinct_blocks = 10` gate, so **no interval means no verdict**.

Structural predictors, all `live_n = 0`:

| predictor | forecasts recorded | frozen claim | DB avg conviction-implied |
|---|---|---|---|
| trend21 | 2,918 | 0.731 | 0.816 |
| vol21 | 2,928 | 0.558 | 0.622 |
| liquidity21 | 2,903 | 0.595 | 0.702 |
| trend63 | 2,917 | 0.700 | 0.743 |
| trend21-crypto | 60 | 0.934 | 0.935 |
| liquidity21-crypto | 60 | 0.795 | 0.937 |
| filingsdrift21 | 67 | 0.500 | 0.500 |

**Multiplicity in force right now:** family_size 13 × looks 8 = divisor **104**,
corrected α = 0.00048, CI z = **3.491**. Bonferroni corrects for both the rows
published per cycle *and* the number of grading looks taken over the same
accruing rows — and both counters are monotone (folded with `max()` against the
chain), so re-cutting a snapshot cannot refund a look already spent.

**Inversion is not a rescue** and the arithmetic is on record as a rule rather
than a number: inverting accuracy `a` gives `1 − a`, so an inverted signal clears
a majority-class null `p` only when the original sits **below `1 − p`**. Every
directional row in the generated live table sits above its own `1 − null`.
`internal/api/modelhealth.go` refuses `emitting: true` for any `-inverted`,
`-relabeled` or `-flipped` model key unless it passes the full canary
re-admission gate.

---

# PART D — THE STANDING RULE

> A predictor may be displayed as a forecast only when its live record, on
> independent observations, beats the majority-class baseline with the **whole
> Wilson interval** above that baseline. Until then it renders as experimental,
> or it does not render.

The directional ensemble failed that rule and is retired. The structural
predictors have not yet been tested by it. The first honest evidence arrives
**2026-08-07** — four days from now — when `tools/accuracy_registry.py` re-runs
against resolved 21-day forecasts. Verdict rules, frozen in the hash-chained
prereg table before any of it resolved:

| live record vs frozen claim C | verdict | committed consequence |
|---|---|---|
| `hi < C − 0.05` | **DECAYED** | the band table is retired from display |
| `lo ≥ C − 0.05` | **HOLDING** | the claim may keep rendering, citing live instead of backtest |
| otherwise | **WIDE** | still experimental, no promotion |
| <30 independent obs or <10 distinct blocks | **INSUFFICIENT** | no verdict claimed |
