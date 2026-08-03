# Each leg, graded alone — 2026-08-02

The measurement nobody had published: every ensemble leg's OWN hit rate against
its own base rate, on the live resolved record. 132,156 joined
prediction/outcome pairs at horizon 1d, 29 distinct UTC days.

Method: `predictions.components` stores each leg's probability at the moment the
blend was computed, so each leg can be graded standalone by joining to
`prediction_outcomes.up`. A leg is graded ONLY on rows where it was present.
Intervals are day-clustered — the UTC day is the unit of resampling, never the
row.

## Result

| leg | present | precision | base rate | edge | days | ±2se |
|---|---:|---:|---:|---:|---:|---:|
| Pressure | 132,156 | 0.4801 | 0.5280 | **−0.0479** | 29 | 0.0604 |
| Expectancy | 126,917 | 0.4955 | 0.5238 | **−0.0282** | 29 | 0.0369 |
| Forecast | 123,335 | 0.5150 | 0.5228 | **−0.0078** | 29 | 0.0284 |
| Sentiment | 11,233 | 0.4263 | 0.5011 | **−0.0748** | 26 | 0.0591 |
| GBM | 37,097 | 0.4819 | 0.5413 | **−0.0593** | 23 | 0.0612 |
| MeanRev | 38,445 | 0.5941 | 0.5369 | +0.0572 | 26 | 0.0695 |
| — | | | | | | |
| BLEND (raw) | 132,156 | 0.4975 | 0.5280 | −0.0305 | 29 | 0.0399 |
| BLEND (calibrated) | 132,156 | 0.4832 | 0.5280 | −0.0448 | 29 | 0.0542 |

**Five of six legs are worse than their own base rate.** Not marginally — by 0.8
to 7.5 percentage points.

Leg availability, which bounds how much any leg could contribute:

| leg | present on |
|---|---|
| Pressure | 100.0% |
| Expectancy | 96.0% |
| Forecast | 93.3% |
| MeanRev | 29.1% |
| GBM | 28.1% |
| Sentiment | 8.5% |

## MeanRev looked like an edge. It is not.

+0.0572 was the only positive number in the table, so it was examined properly
before being believed. It does not survive.

**1. The day-clustered interval includes the baseline.** [0.5246, 0.6637] against
a base of 0.5369. The lower bound sits *below* the number it must beat.

**2. Split by call direction, the edge is exactly zero:**

| subset | n | precision | base | edge |
|---|---:|---:|---:|---:|
| UP call, prob 0.5–0.6 | 10,492 | 0.5966 | 0.5966 | **+0.0000** |
| UP call, prob 0.6–0.7 | 3,906 | 0.5709 | 0.5709 | **+0.0000** |
| UP call, prob 0.7–0.8 | 1,169 | 0.5261 | 0.5261 | **+0.0000** |
| DOWN call, prob 0.3–0.4 | 4,698 | 0.6748 | 0.6748 | **+0.0000** |
| DOWN call, prob 0.4–0.5 | 13,687 | 0.6276 | 0.6276 | **+0.0000** |

Precision equals the base rate identically, because within a subset where every
call points the same way, precision IS the share of that class. The aggregate
+0.0572 arises only from mixing the UP and DOWN subsets: the leg issues UP calls
on up-heavy days and DOWN calls on down-heavy days.

That is textbook **unskilled classification** — learning the class proportion
rather than the features. It is trap #1 in EIGHTY_PERCENT_SUPERPROMPT.md section
0, and it is why that document forbids reporting accuracy without its base rate.

**3. One band is catastrophically inverted.** UP calls at prob 0.8–0.9:
precision **0.2056** against a base rate of **0.7944** — an edge of **−0.5888**
across 1,138 rows and 13 days. The leg is most wrong exactly where it claims most
confidence. This is a real defect and is the most specific lead in the file.

## What this explains

The blend is a weighted mean over these six legs
(`ensemble.WeightedProbability`). Averaging one leg that tracks base rates with
five that are worse than chance cannot produce skill — and it doesn't: raw
0.4975, calibrated 0.4832, against a 0.5280 majority baseline.

Calibration makes it *worse* (0.4975 → 0.4832), which is what fitting an isotonic
map on anti-signal does: it sharpens confidently wrong predictions. That is the
mechanism behind the negative Brier skill and the downward-sloping reliability
curve, and it answers open item **F-5** — the map inverted because the input
carries anti-signal, not because the map is broken. **Do not flip the sign.**

## What must NOT be concluded from this

- **Not** "drop five legs and ship MeanRev." Its within-direction edge is zero
  and its interval includes the baseline.
- **Not** "invert the blend." Inversion yields 0.5168, still below the 0.5280
  majority baseline, with an IC confidence interval that includes zero.
- **Not** "the pipeline is broken." The pipeline is measuring correctly. What it
  measures is that there is no edge here.

## The honest next question

Every leg is a *directional* call on a *daily* horizon — the hardest, most
efficiently-arbitraged prediction in the space, where published out-of-sample
consensus is 54–58% and a 918-experiment controlled study found a mean of 50.08%.
This system is at 49.75%.

The productive move is not repairing these six legs. It is asking whether a
different target — a different horizon, a conditional subset, a
non-directional quantity such as realised volatility or a relative
cross-sectional ranking — carries signal that next-day direction does not.

Repro: `scratchpad/legs.py`, `scratchpad/meanrev.py` (method above; both
read-only against `data/signaldeck.db`).
