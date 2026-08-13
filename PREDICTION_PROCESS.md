# The prediction process, and the gate every prediction must pass

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** SUPERSEDED — historical record; current status is `STRATEGY_DECK.md`
> **Scope:** How a prediction is produced and the gates it must pass before publication.
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
> Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **live accuracy · confidence-interval verdicts ·
> survivorship control · point-in-time data · structural forecast counts.**
>
> **FC1 CLOSED in P2 (2026-08-04).** Layer 6 used to state one live record while
> three other documents stated three others. No document types the record now:
> every one of them includes the same generated block from
> `partials/live_accuracy.md`, rendered from `data/accuracy_registry.json` by
> `tools/live_accuracy.py`, and CI fails on a superseded literal. See
> `proofs/P2_LIVE_RECORD_RECONCILIATION.md`.
>
> **Known defect remaining (FC3):** point 5 asserts survivorship control that
> `ALPHA_WORKFLOW.md` §B2 measured open. `proofs/P3A_SURVIVORSHIP_BACKFILL.md`
> re-measured it and closed 2019–2022, quantifying a 2023–2025 residual rather
> than declaring it shut. Read point 5 against that window.
>
> **FC8 CLOSED in P6 (2026-08-04).** Part 3 used to state one outstanding structural
> forecast count where the per-kind table summed to another. Neither is typed now;
> both come from the generated block, by the same mechanism that closed FC1.
>
> **Interval language, corrected twice.** Layer 6 originally claimed "the whole
> confidence interval below the null" and read that as significant negative skill,
> at a time when the platform withheld every interval for this claim (distinct-day
> counts 9/5/4/4 against the `min_distinct_blocks = 10` floor). P6 removed both the
> interval claim and the verdict. The 1d sample has since crossed the floor and the
> registry now publishes an interval for it, in the generated block — so the verdict
> is once again available, but from the registry, not from this prose. See
> `proofs/P6_GOVERNANCE_CLEANUP.md` §3 and `proofs/P10_FREEZE_LIFT.md` §3.
>
> Authority: `proofs/P0_FREEZE.md`, **lifted 2026-08-04** by `proofs/P10_FREEZE_LIFT.md`.

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
(tamper-evident against a third party without the signing key, and
not evidence against the operator, who holds both the key and the database — see
`proofs/P10_FREEZE_LIFT.md` §5). The resolver fills the forward return later.
`internal/modelhealth` grades hourly on five components (skill, calibration,
drift, freshness, stability) and returns a verdict that can stop emission.
`tools/accuracy_registry.py` re-grades daily against the majority-class
baseline on independent (symbol, horizon, UTC-day) observations.

### Layer 6 — What it does with a failure

This is the part most systems skip. The directional ensemble graded below its
own baseline. The record is not typed here — it is the generated block below,
and it is the same block every other document in this repository carries:

<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-12T17:53:20) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,376 | 41.7% | 55.1% | -13.4pp | 16 | [33.7%, 50.2%] |
| prequential-majority (1d) | all | 2,044 | 57.2% | 54.8% | +2.4pp | 13 | [40.0%, 72.8%] |
| directional-ensemble (1w) | all | 2,873 | 36.5% | 65.1% | -28.6pp | 11 | [29.0%, 44.7%] |
| prequential-majority (1w) | all | 2,202 | 68.9% | 67.7% | +1.2pp | 8 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 452 | 44.7% | 46.6% | -1.9pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 471 | 38.0% | 65.7% | -27.7pp | 10 | [28.4%, 48.6%] |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1w)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — FAILED — significantly worse than the naive baseline

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 162 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 4,146 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 107 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 4,178 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 107 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 4,178 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 4,192 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=21, divisor=273, corrected_alpha=0.00018315018315018315.

**Survivorship:** epoch 2026-07-24; unmeasured — no graded post-epoch symbols; measured effect +0.31pp (active-only 83.58% minus survivorship-clean 83.27%, n=75,118 clean vs 17,860 active, revalidation of 2026-08-12T10:20:18+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->

> No interval verdict is available: the platform withholds every interval for this claim (distinct-day blocks below `min_distinct_blocks = 10`).

The operational consequence is what stands: `modelhealth` set
`verdict: retired, emitting: false`. It is off. The structural predictors
(trend21, vol21, liquidity21) are correctly `PENDING`, with 0 resolvable before
**2026-08-07**, because a 21-day horizon cannot be graded sooner. The outstanding
forecast counts are not typed here: they change daily and were previously stated
two ways in this file. Read them per predictor from the generated block above,
which is regenerated from `data/accuracy_registry.json`.

### Why inversion is not a rescue

The registry's failure menu ("retire, invert, or relabel as experimental")
invites a tempting arithmetic mistake, so the arithmetic goes on record here —
as a RULE rather than as a number, because numbers typed into prose are what
produced FC1.

Inverting an accuracy `a` produces `1 − a`. The honest competing model was never
a coin flip; it is the constant majority-class guess at the null `p`. So an
inverted signal clears the bar only when `1 − a > p`, i.e. only when the
original sits **below `1 − p`**. Read the live table above: every directional
row sits above its own `1 − null`, which is the arithmetic statement that the
ensemble is not anti-predictive enough to be useful upside down. It is noise
around the base rate. No sign flip or relabeling of a below-null signal beats
the constant guess — relabeling changes the badge, not the record.

This is enforced in code, not just prose: `daemon/internal/api/modelhealth.go`
refuses `emitting: true` for any model key marked as an inverted or relabeled
variant (`-inverted`, `-relabeled`, `-flipped`) unless that variant has passed
the full canary re-admission gate (Part 2, point D — Wilson lower bound above
both the incumbent and the majority-class null, over ≥30 independent
observations spanning ≥14 days) and carries the gate's `readmitted: true`
record. A variant is a new model and re-enters through the same front door as
one. And no recalibration work applies either way: **calibration = 0 is the
correct score for a retired model** — a model that no longer emits has no
probabilities left to calibrate.

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
`canonicalFeatureKeys`. Tests: `TestNoLookahead_LaterDaysDontChangeEarlierFolds` (alphax),
`TestNoSameBarFill` / `TestNextBarFillTiming` (backtest), and
`TestEvaluateNoLookaheadAppendingFuture` (forecast) proving appending future
rows cannot change an earlier fold's predictions.

**5. Survivorship bias.** The discovery corpus is built from the universe that
EXISTED, not the one that survived: `hist-backfill` recomputes `research_weeks`
off `store.ResearchUniverse` (every name ever tracked, dead included), so a name
that left still contributes the weeks it actually traded, while the deep-fetch
set is bounded by `store.TradableAt` at run time. Until 2026-07-27 that pass
iterated the ACTIVE list, which meant a corpus spanning 2020→today was assembled
only from names still listed today and the era-survival gate was measured on it —
indistinguishable from "the rule worked on the 2022 names that survived to 2026".
Every `research_loop_runs` row now also records `corpus_coverage`: the worst week's
(symbols in the corpus / symbols that actually printed a daily bar), named in the
loop's summary line. It corrects no reported figure — it BOUNDS the bias a
Bonferroni-corrected, era-gated grid search cannot correct internally.
Known live limit, stated rather than hidden: names that died BEFORE this platform
ever tracked them are absent from numerator and denominator alike and no free data
source recovers them, which is why the capitulation-bounce expectancy
(+5.6%/trade) is flagged untrustworthy rather than shipped.
`TestDocumentedControlsHaveNonTestCallers` fails the build if any Go identifier
named in this section has no non-test caller — the lesson from the earlier
finding that `delisted_at` sat empty fleet-wide because nothing called
`MarkDelisted`, turned into an invariant instead of a comment.

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
baseline**, never 50%. This is why a directional accuracy in the high forties
reads as failure here: the null it is measured against is the majority-class
rate in the live table above, not a coin flip.

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

## Part 3 — Pre-registration of the 2026-08-07 structural grading

**Registered 2026-07-26.** As of that date the structural predictors carried
outstanding forecasts and **0 of 30 required observations had
resolved** — nothing below was written with any knowledge of the outcome. The
count is deliberately not restated here; it was previously given two ways in this
file, and the per-predictor figures are generated into the block in Part 1. That
is the entire value of this section: after 2026-08-07 it can only be checked,
never rewritten. The machine-readable twin of this section lives in the
hash-chained `prereg_records` table (`internal/prereg`, served at
`GET /api/prereg`). The chain head is NOT externally timestamped: as of
2026-07-27 no public anchors repository exists and `ops/anchor-publish.sh` has
never pushed, so a later edit is detectable only to someone who already trusts
the operator's copy of the chain — which is to say, it is a matter of trust.

### The hypotheses

Each kind's full band table, resolution rule, stated null, and known weakness
are frozen verbatim in the chain; the top-band claims being tested are:

| kind | question (abbreviated) | horizon | top-band claim | null to beat |
|---|---|---|---|---|
| trend21 | same side of SMA200 in 21 sessions | 21d | 94.6% (≥0.8) / 97.2% (≥0.9) | persistence, not 50% |
| vol21 | vol regime (elevated/calm) persists | 21d | 72.0% (≥0.9) | vol-regime persistence |
| liquidity21 | dollar-volume side vs 200d median | 21d | 87.6% (≥0.9) | persistence scores the SAME 0.876 |
| trend63 | same side of SMA200 in 63 sessions | 63d | 83.7% (≥0.9) | quarterly persistence |
| trend21-crypto | as trend21, crypto tables | 21d | 98.5% (≥0.5) | bear-heavy persistence |
| liquidity21-crypto | as liquidity21, crypto tables | 21d | 96.4% (≥0.9) | persistence scores 0.783 |

These are backtest numbers until graded, and the platform labels them so
everywhere they render.

### The grading code, pinned

- Grader: `tools/accuracy_registry.py`, registered at commit
  `04395a2e8fec1e558cd8cfe0c12a50dbc360cabb` (the last commit touching the
  grader on the registration date — checkable in git history independently of
  this document).
- The grader file's SHA-256 is measured at registration time and frozen into
  the chain record (kind `grading-protocol`, `GET /api/prereg`). Any later
  edit to the grader re-digests to a different protocol hash and the
  registrar appends an **AMENDMENT** record on its next pass — the grader
  cannot move under the frozen claims without leaving a chained trace.
- `TestGradingProtocolMatchesTheActualGrader` additionally fails the build if
  the refusal thresholds registered here stop matching the grader's source.
- Independence: one observation per (symbol, horizon, UTC day). 408 forecasts
  resolving on one day are ONE market observation.

### The refusal rule (decided now, not after seeing the sample)

- Fewer than **30 independent observations** → `INSUFFICIENT`: no verdict is
  claimed either way.
- Fewer than **`MIN_DISTINCT_BLOCKS = 10` distinct non-overlapping horizon
  blocks** (`call_day // horizon_days`, anchored at the first call day) → no
  interval is published at all, and **no interval means no verdict**. For
  horizon-1 directional rows a block *is* a UTC day, so those keep the
  `MIN_DISTINCT_DAYS = 10` gate; for a 21-day structural kind the block gate is
  the stricter one, requiring ~10 x 21 days of calls rather than 10 days. A
  sample spread over three non-overlapping windows has no measurable
  between-cluster variance; reading a verdict off its point estimate is the
  exact failure this gate exists to prevent. The chained grading protocol
  (`prereg.GradingProtocol`, chain kind `grading-protocol`) registers both
  floors, so the machine-readable record and this prose name the same gate.

### What each outcome will mean

With `[lo, hi]` the day-clustered 95% interval on live accuracy and `C` the
claim frozen in the chain (never the code's value on grading day):

| live record | verdict | consequence, committed now |
|---|---|---|
| `hi < C − 0.05` | **DECAYED** | the advertised band table is retired from display; the failure is recorded unfiltered in the registry (and would publish unfiltered once a public anchors repo exists — none does today) |
| `lo ≥ C − 0.05` | **HOLDING** | the claim may keep rendering, now citing the live record instead of the backtest |
| otherwise | **WIDE** | still experimental — no promotion, wait for more independent days |
| refusal rule fires | **INSUFFICIENT** | no verdict claimed; the gap is reported, not papered over |

A pre-registered negative is publishable evidence; a post-hoc one is not.
Whatever the registry prints on 2026-08-07 ships to the public repo exactly as
printed — DECAYED included.

---

## The standing rule

A predictor may be displayed as a forecast only when its live record, on
independent observations, beats the majority-class baseline with the whole
Wilson interval above that baseline. Until then it renders as experimental, or
it does not render. The directional ensemble failed that rule and is retired.
The structural predictors have not yet been tested by it; the first honest
evidence arrives **2026-08-07**, and the registry re-run on that date is the
event that decides whether the 70%+ claims survive contact with live data.
