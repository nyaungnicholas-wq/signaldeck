# A +1pp lift that is one bad year, not an edge

A cross-sectional directional model beats the naive prequential baseline out-of-sample by +1.01pp overall, 95% CI [+0.22, +1.38] pp over 1,153 test days. The CI excludes zero. The lift is entirely 2022. A noise control with randomly permuted labels also beats the baseline in 2022 by +2.35pp. In 2023-2026 the model is at or below zero every year. This is one regime observation, not an edge.

## The question

Does a cross-sectional directional model beat the naive baseline out-of-sample on 8 years of daily bars?

## Method

- Labels derived from `bars(tf='1d')`, stocks only, NOT `prediction_outcomes` (which holds only 39 distinct days). Label up = 1 if next available daily close > today's close.
- 2,468,457 observations, 1,847 distinct days, 2,870 symbols, 2018-07-26..2026-08-11.
- Six features, each ranked cross-sectionally within the day to [-0.5,+0.5]: r1, r5, r21, r63, vol21 (21d stdev of returns), dvol (log volume vs its 21d mean). All computed strictly at or before day t.
- Logistic regression, plain gradient descent, small L2.
- Walk-forward by calendar year, train on everything strictly before the test year minus a 10-trading-day embargo.
- Baseline: PREQUENTIAL majority — the side that was the majority in TRAINING, applied as a committed rule. NOT `max(mean(y_test),1-mean(y_test))`, which picks the winning side after seeing the test labels.
- 95% CI clusters on DAY (t distribution, n_days-1 df), not on row.

## Headline result

Overall accuracy 0.5164 vs baseline 0.5063, lift +1.01pp, 95% CI [+0.22, +1.38] pp over 1,153 test days. The CI excludes zero.

## Why that is not an edge

A noise control was run: identical pipeline with labels randomly permuted WITHIN each day. Per-year lift, model vs that noise control:

| Year | Model | Noise | Difference |
|------|-------|-------|------------|
| 2022 | +4.65 | +2.35 | +2.29 |
| 2023 | -0.66 | +0.00 | -0.66 |
| 2024 | -0.01 | +0.00 | -0.01 |
| 2025 | -0.33 | +0.00 | -0.33 |
| 2026 | -0.02 | +0.00 | -0.02 |
| overall | +1.01 | +0.62 | |

1. The entire overall lift is 2022. In 2023-2026 the model is at or below zero every year.
2. A model with RANDOMISED labels also "beats" the baseline in 2022, by +2.35pp. Half the apparent 2022 gain requires no information at all.
3. The mechanism: the prequential baseline is a COMMITTED directional bet ("always up", learned from 2018-2021). 2022 fell, so always-up scored 0.4614. Any model that sometimes says "down" hedges that bet and gains — skill is not required. In 2023-2026 the noise model collapses to always-up and ties the baseline at EXACTLY +0.00, four folds to the digit.
4. That exact 0.00 in four folds is also the leakage evidence: a harness with lookahead could not tie a committed baseline to the digit. The as-of discipline holds.
5. So the honest reading is one regime observation, not an edge. One year is one regime; the repo's own standards (day-clustering, Bonferroni, regime-survival) reject a single-year result, and the other four years are negative.

## A consequence for the gates

Any "beats-naive" test scored against a committed prequential baseline is winnable by a coin flip in a year the regime turns. A noise model scored +0.62pp overall with a day-clustered CI of [+0.16, +0.86] that EXCLUDES ZERO. A gate that a random model passes is not measuring skill. Recommend that beats-naive results carry a noise-control arm, or that the baseline be evaluated per-regime.

## What this does not say

It does not say no directional edge can exist. It says these six standard cross-sectional features, on this universe, at 1 day, do not have one, which is consistent with the 615 hypotheses the eighty-loop has tested with zero survivors and with the 2026-08-08 finding that the ensemble legs are noise rather than backwards.

## Reproduce

`research/xsdirection.py`

```
python research/xsdirection.py --json out.json
python research/xsdirection.py --shuffle-labels --json noise.json
```

NO DIRECTIONAL PRODUCT.

## The target is wrong, not the modelling

Volatility is predictable in a way daily direction is not, and this repository
already contains a model that beats its baseline out of sample. `go run
./cmd/hmmbakeoff`, measured 2026-08-10 over 400 symbols and 305,328 labelled
bars, grading each label on the NEXT day's absolute move, normalised per symbol
so the score cannot come from separating quiet symbols from wild ones:

| labeller | separation (highest/lowest label) |
|---|---|
| HMM (`internal/hmmregime`, fitted out-of-sample) | **1.39x** |
| trailing 20d realized-vol tercile (naive baseline) | 1.25x |
| incumbent rule-based (`internal/regime`) | 1.14x |

The HMM beats both the naive baseline and the incumbent, on a 60/40 split whose
parameters never see the test window, with every label computed from bars[0..i]
and the target strictly forward of it.

Three things this does NOT mean. It is a separation ratio, not an accuracy, so
it is not comparable to the 55% directional null. It says nothing about
direction — a volatility regime is not a directional forecast, and grading it as
one is the error this document exists to avoid. And the bakeoff reports no
confidence interval and clusters on nothing, so "1.39x beats 1.25x" is a point
estimate on 305,328 observations, not a verdict; it needs day- or
symbol-clustered inference before it earns one.

What it does mean is that the honest route to a product that beats its baseline
runs through volatility, not through daily direction.

