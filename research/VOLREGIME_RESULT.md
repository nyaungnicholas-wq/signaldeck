# The HMM volatility win does not survive inference

`cmd/hmmbakeoff` reports the HMM separating next-day absolute moves 1.39x
against 1.25x for a trailing-vol tercile — a point estimate with no interval and
no clustering. `research/volregime.py` re-measures it with both. The win does not
hold, and the reason is instructive twice over.

| labeller | separation | 95% CI (by symbol) | within-day diff | 95% CI (by day) |
|---|---|---|---|---|
| HMM | 1.996 | [1.460, 2.533] | **−0.6158** | [−0.6378, −0.5939] |
| tercile | 1.314 | [1.270, 1.357] | +0.1735 | [+0.1300, +0.2169] |
| random | **1.065** | [1.060, 1.070] | +0.0014 | [−0.0037, +0.0065] |

400 symbols, 305,328 out-of-sample test bars, fitted on the first 60% of each
symbol and graded on the last 40%, per-symbol normalised.

## Failure 1: the separation metric is upward-biased

A random labeller separates nothing. This one scores **1.065, with a CI that
excludes 1.00.**

The cause is selection: "separation" is the highest-mean label divided by the
lowest-mean label, and *which* label is highest is decided after seeing the
outcomes. Picking the max of several noisy group means and dividing by the min
is biased upward by construction, and the bias grows with the number of labels —
which is also why the 3-label tercile cannot be compared like-for-like against
the 2-state HMM.

This is the same error as grading against a baseline chosen with hindsight,
caught earlier in `XSDIRECTION_FINDING.md`. Any separation figure quoted from
this family of estimators, including the 1.39x in `cmd/hmmbakeoff` and the 1.996
above, inherits it.

## Failure 2: the HMM's pooled and day-clustered answers disagree in SIGN

Pooled, the HMM looks strong at 1.996. Clustered by day — comparing turbulent
against calm bars *within the same day*, which is the only comparison that
controls for market-wide volatility — it is **−0.6158, and the interval is
nowhere near zero.**

Bars the HMM calls turbulent have SMALLER forward moves than bars it calls calm,
day for day. The tercile baseline gets this right, at +0.1735. So the HMM's
pooled advantage is not within-day discrimination; it is something the pooling
is picking up across days or symbols that survives the per-symbol normalisation.

A labeller whose within-day sign is backwards is not a volatility model that is
merely weak. Either the state-to-name mapping ("higher-mean state = turbulent")
inverts for a large share of symbols, or the pooled statistic is measuring
regime persistence across time rather than discrimination within it.

## What this means

**The HMM must not be wired into a product on this evidence.** The prior note
that it "beats its baseline 1.39x vs 1.14x" rests on an estimator whose random
control fails, and the one measurement that controls for the obvious confound
points the other way.

The trailing-vol tercile is the only labeller here that behaves coherently:
modest separation, correct within-day sign, both intervals tight. If a
volatility product ships, that is the incumbent to beat, and it has not been
beaten.

Owed before this question is reopened: a separation estimator that scores 1.00
on random labels — fix the label-selection bias, or replace the ratio with the
within-day difference, which the random control already passes — and a
resolution of the sign contradiction. Neither is done here.

```
go run ./cmd/hmmbakeoff
python research/volregime.py --json vol.json
```
