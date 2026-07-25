# The prediction process, and the gate every prediction must pass

Two things live in this file. First, exactly how a number gets from a bar to a
displayed forecast — the layers, in order. Second, the standing checklist that
each layer is answerable to: the eighteen ways a quant system silently produces
a confident wrong answer, each one either mapped to the code that already
prevents it or listed as an open gap with a concrete spec.

The checklist is not aspirational. Where a row says "implemented", a test or a
live measurement backs it, and the citation is the proof. Where a row says
"gap", nothing in the repo prevents that failure today.

---

## Part 1 — How a prediction is actually produced

### Layer 0 — Ingest and validate

Bars arrive from Alpaca (stocks, IEX free tier) and Kraken (crypto), plus
keyless FRED macro series and SEC EDGAR filings. Before anything models them:

- `dq-auditor` gates staleness against `internal/marketcal`, so a market
  holiday is not read as a dead feed.
- `internal/splitfix` detects corporate-action corruption; every predictor
  additionally refuses a window containing a one-day move beyond
  `maxSaneReturn = 0.65` rather than forecasting from a reverse-split artifact.
- Retention is tiered and archive-before-prune; daily bars are never pruned
  (`PruneBars` refuses `tf=1d` in code).

### Layer 1 — Features

`buildFeatureVector` (`internal/pipeline/predict.go`) assembles one vector per
(symbol, horizon) at prediction time and persists it to the `features` table —
featureVersion is now 10. The vector merges, each source gate-honoring and
absent-when-unavailable rather than zero-filled:

pressure components (trend/momentum/RSI/MACD/VWAP/RVOL/imbalance) · expectancy
state key · regime label · cross-sectional rank percentile · news sentiment and
`news_vol_z` · crypto microstructure from 1s snapshots · `vix_*` from FRED ·
cross-sectional alpha sources (put/call, COT, short interest, funding rate,
StockTwits, Wikipedia attention, TradingView reco) · candlestick and indicator
signals · trend geometry.

The persisted vector is the training set. `pred_raw`, `pred_cal`, `gbm_prob`
and `meanrev_prob` are excluded from `canonicalFeatureKeys` so no model can
learn a shortcut off its own output.

### Layer 2 — Legs

Each leg produces an independent P(up), and each must earn its place:

| leg | source | admission rule |
|---|---|---|
| pressure | composite score | dropped only on a *measured* negative grade (fail-safe: unmeasured stays) |
| expectancy | conditional forward-return table for the current state | needs n ≥ 5 in the state cell |
| forecast | walk-forward logistic | OOS lift > 0 |
| GBM | from-scratch gradient-boosted trees | OOS lift > 0, min 60 train rows |
| mean-reversion | inverted momentum | OOS lift > 0 **net of a 10bp cost** |
| sentiment | daily news aggregate | ≥ 3 headlines, ≤ 3 days old, scale capped at 0.15 |

An unproven leg is **dropped, not down-weighted** — a signal with no measured
out-of-sample lift carries no information and must not dilute the blend.

### Layer 3 — Blend

`ensemble.WeightedProbability` combines the surviving legs. Weights come from
`internal/adaptive` (6h worker): per-component hit-rate and IC computed per
regime cell from resolved outcomes, weights ∝ max(0, hitRate − 0.5), gated at
n ≥ 30 per cell with fallback regime → global → static prior. Per-symbol models
(`internal/symbolagent`) override with the symbol's own weights once it has 40
of its own resolved outcomes.

### Layer 4 — Calibration

Raw probability is mapped through an isotonic recalibration fit **prequentially**
— only on pairs already resolved when the point was made, so no point is graded
by a map trained on itself. Below 30 pairs, `Calibrate` returns the identity and
reports `calibrated=false`. Blocks are shrunk toward the base rate with a
pseudocount of 25, and a persisted per-symbol map is display-clamped to
[0.25, 0.75], because realized 1-day accuracy near 51% cannot support a
displayed 0.85.

### Layer 5 — Record and grade

Every prediction writes a row to `prediction_outcomes` with the probability
frozen at prediction time, and a hash-chained entry to `prediction_ledger`
(235k rows, tamper-evident). The resolver fills the forward return later.
`internal/modelhealth` grades hourly on five components (skill, calibration,
drift, freshness, stability) and returns a verdict that can stop emission.
`tools/accuracy_registry.py` re-grades daily against the majority-class
baseline on independent (symbol, horizon, UTC-day) observations.

### Layer 6 — What it does with a failure

This is the part most systems skip. The directional ensemble was graded at
**48.1% on 13,044 independent observations against a 54.6% baseline** — the
whole confidence interval below the null, which is significant *negative* skill,
not merely no edge. `modelhealth` set `verdict: retired, emitting: false`. It is
off. The structural predictors (trend21, vol21, liquidity21) are correctly
`PENDING` — 12,529 forecasts recorded, 0 resolvable before **2026-08-07**,
because a 21-day horizon cannot be graded sooner.

---

## Part 2 — The eighteen-point gate

### Implemented, with proof

**1. No predictive signal → search conditional, not universal.** The research
loop tests rules per regime cell (`internal/regimecond`, `internal/researchlab`,
`researchloop.go`), not "does RSI work". The alpha-discovery wave that produced
the structural predictors ran exactly this shape: 1-day direction was rejected
across seven independent tests, while vol-regime and trend-vs-SMA200 survived at
70–97%.

**3. Horizon mismatch.** Legs are graded and admitted per horizon (1h/1d/1w)
independently; a leg live on 1d can be absent on 1w. The embargo in `alphax` is
horizon-aware — a 1w label reaches 7 days forward and is purged accordingly.

**4. Feature leakage.** `alphax` uses purged walk-forward splits by UTC day with
a horizon-aware embargo (de Prado). Self-referential features are excluded from
`canonicalFeatureKeys`. Tests: `test_no_lookahead`, `test_fill_timing`, and a
GBM test proving appending future rows cannot change an earlier fold's
predictions.

**5. Survivorship bias.** `store.ResearchUniverse` / `TradableAt`. Known live
limit, stated rather than hidden: the tracked universe still under-represents
delisted names, which is why the capitulation-bounce expectancy (+5.6%/trade)
is flagged untrustworthy rather than shipped.

**6. Look-ahead.** Fills are `next_open` / `same_close`, never the bar that
generated the signal. Calibration is prequential. `byRegime` on `/track-record`
is deliberately **null** because regime-at-prediction-time is not persisted per
prediction, and using the current regime would be look-ahead.

**7. Distribution shift.** `modelhealth/drift.go` runs a two-sample KS test with
an n-adjusted critical value; drift is one of five health components. Regime
state and changes are tracked as first-class objects.

**8. Overfitting.** Walk-forward, non-overlapping forward windows (stepping by
the horizon so windows do not share days — overlapping daily samples inflate n
by ~63× and lie), quarter-block-clustered confidence intervals over ~30
independent quarters.

**9. Class imbalance.** Skill is measured against the **majority-class
baseline**, never 50%. This is why 48.1% reads as failure against a 54.6% null.

**11. Wrong objective.** `internal/moneymetrics` leads every money surface with
cost-adjusted expectancy, profit factor and payoff ratio; win rate is kept but
demoted. The 80%-win-rate-with-negative-expectancy case is a unit test.

**12. Data quality.** dq-auditor, freshness gates, split-corruption detection,
NaN guards on zero-volume days, `AlignByTs` timestamp intersection for
stock-vs-crypto comparisons.

**13. Feature drift.** Covered by the same KS drift monitor; the stability axis
was made non-inert in commit `9cd63b8`.

**14. Regime mixing.** Weights are learned per regime cell, not globally, with
an explicit fallback chain when a cell is thin.

**15. Ensemble weighting.** Weights are re-estimated every 6 hours from resolved
outcomes; a leg with no measured lift is dropped entirely and must re-earn
admission. This is the exact lesson from the earlier finding that removing one
component improved the ensemble.

**16. Execution slippage.** Commission and slippage on traded notional,
`slippage_log.jsonl` reconciled against broker-executed quantity, turnover and
an explicit capacity caveat on every backtest surface.

**17. Statistical significance.** Wilson intervals on proportions, Fisher-z on
IC, verdicts read from the **interval**, never the point estimate. `PENDING` and
`INSUFFICIENT(n/30)` are real verdicts.

**18. False discovery.** Bonferroni correction across the whole rule grid, plus
era-coverage (a rule must survive multiple market eras) and a counterfactual
check. A dry round is reported as the search working, not as a failure.

### The six remaining gaps — BUILT 2026-07-25

All six were closed in one wave. Each is a pure engine with its own tests, a
worker, and a read surface that carries its own caveat.

**A. Distributional targets (point 2, label noise) — `internal/distribution`,
`return-distribution-runner` (6h), `GET /api/return-forecast`.** The binary
up/down target is replaced by a cost-aware return distribution: P(move clears
+τ), P(clears −τ), P(inside the band — the no-trade zone the old target could
not express), expected return, σ, and the 10/50/90 quantiles, with τ = 10bps to
match the mean-reversion leg's cost. The distribution is conditioned on the
**volatility regime**, because that is the axis this platform has validated
skill on — the center carries no edge and the payload says so; the width is
what rests on a real predictor. Conditioning is graded walk-forward with
**pinball loss against its own climatology**, so it has to beat the
unconditional forecast to claim anything, exactly like an OOS lift gate.
`GradeBand` completes the label-noise fix: a realized move inside ±τ is a
NO-CALL, scored as neither right nor wrong.

**B. Feature redundancy (point 10) — `internal/featureredundancy`,
`feature-redundancy-runner` (24h), `GET /api/feature-redundancy`.** Pairwise-
complete correlations over `canonicalFeatureKeys`, single-linkage clustering at
|ρ| ≥ 0.9, one representative per cluster elected by |IC|, and the honest
`effectiveCount` published next to `fieldCount`. A pair with too few shared
observations is left UNMERGED rather than assumed independent. Nothing is
deleted — the report is input to a decision, and it states in its own payload
that representative election reads labels and is therefore not a validation.

**C. Multi-source price validation (point 12) — `internal/pricecheck`,
`price-validator` (24h, opt-in), `GET /api/price-validation`.** Compares stored
daily closes against an independent source on the intersection of trading days.
A one-sided offset on nearly every day is labeled an adjustment-convention
difference, *not* corruption — conflating the two is how an alert gets ignored.
Disagreement lowers confidence and raises a `dq_event`; it never overwrites a
bar. The second provider's prices are compared and discarded: only derived
statistics are stored, which keeps this inside the no-redistribution rule.
Enable with `SIGNALDECK_PRICE_VALIDATION_URL` (a `{symbol}` template returning
daily-bar CSV).

**D. Canary rollout (point 7) — `internal/canary`, `canary-runner` (1h),
`GET /api/canary`.** A new version no longer inherits production. Its Wilson
lower bound must clear both the incumbent's live accuracy and the naive
majority-class baseline, over ≥30 independent observations spanning ≥14 days,
before it may serve; an interval entirely below the incumbent ends the trial.
Everything else HOLDS, with the challenger recording forecasts it does not
serve. Version identity is the feature-vector version, because a changed vector
IS a changed model — so this needed no new plumbing.

**E. Dataset versioning (point 4/18) — `internal/datasetver`,
`dataset-version-runner` (24h), `GET /api/dataset-versions`.** A pinned
canonical SHA-256 per (symbol, timeframe) slice. Appending new bars is
recognized as an extension and is not flagged; a rewrite *inside* a recorded
range raises a `dataset_revised` dq event, because any claim measured on that
slice has stopped being reproducible.

**F. Nightly bias regression (point 6) — `ops/nightly-bias.sh`, launchd
`com.signaldeck.bias`, 02:40 daily.** Re-runs all 16 bias-discipline packages
and *counts* the named no-lookahead/purge/embargo tests, so deleting an
invariant fails as loudly as breaking one. Then it checks the same assumptions
against the live database read-only: outcomes resolved before their own
timestamp, the pooling-inflation factor, skill published below its sample floor,
canary promotions below their gates, and retired models still emitting. First
run: PASS — 20 invariant tests, 151,924 raw outcomes → 19,128 independent
(inflation 7.9×), live invariants clean.

---

## The standing rule

A predictor may be displayed as a forecast only when its live record, on
independent observations, beats the majority-class baseline with the whole
Wilson interval above that baseline. Until then it renders as experimental, or
it does not render. The directional ensemble failed that rule and is retired.
The structural predictors have not yet been tested by it; the first honest
evidence arrives **2026-08-07**, and the registry re-run on that date is the
event that decides whether the 70%+ claims survive contact with live data.
