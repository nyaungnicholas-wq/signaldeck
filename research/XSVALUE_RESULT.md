# The signal has information and no tradeable value

Result of the grid pre-registered in `XSVALUE_PREREG.md`. All four cells are
reported, as promised. All four FAIL the criterion declared in advance.

## The grid

| cell | config | IC (95% CI) | gross spread | turnover | net @10bp/side |
|---|---|---|---|---|---|
| A | hold=1, all symbols | **+0.0506** [+0.0412, +0.0600] | +18.75 bp [+5.0, +32.5] | 0.668 | +5.39 bp |
| B | hold=5, all symbols | **+0.0755** [+0.0646, +0.0863] | −33.16 bp | 0.091 | −34.97 bp |
| C | hold=1, top-1000 liquid | **+0.0291** [+0.0166, +0.0416] | −3.18 bp | 0.573 | −14.63 bp |
| D | hold=5, top-1000 liquid | **+0.0386** [+0.0260, +0.0512] | −1.45 bp [−35.7, +32.8] | 0.068 | −2.80 bp |

Success required net spread positive with a CI excluding zero at 10 bps per
side, in a majority of walk-forward years. Cell A is the only positive net
number, and it does not survive its own interval: gross CI [+5.0, +32.5] minus
2 × 0.668 × 10 = 13.4 bp of cost gives a net interval of roughly [−8.4, +19.1],
which contains zero. The other three are negative outright.

## What is actually true

**The information is real.** Mean daily rank IC is positive in every cell, with
a CI excluding zero in every cell, ranging +0.029 to +0.076. An IC near 0.05 is
not a rounding artifact; it is the range real cross-sectional books operate in.
The +1.83pp hit rate in `XSDIRECTION_FINDING.md` was not a mirage.

**It does not convert into return.** A positive rank correlation says the
ordering carries information. It does not say the extremes pay, and here they do
not: the top-minus-bottom decile mean is unstable in sign and, after costs,
never reliably positive. Hit rate ignores magnitude, and this is what that
looks like when you finally measure the magnitude.

**Turnover is the executioner in cell A.** 0.668 means two thirds of the book
changes daily. At 10 bps per side that is 13.4 bp of cost against 18.75 bp of
gross edge. The signal is not wrong; it is too fast to keep what it finds.

**Liquidity kills it.** Restricting to the top 1000 names by dollar volume drops
IC from +0.051 to +0.029 and the gross spread from +18.75 bp to −3.18 bp. What
edge exists lives disproportionately where it cannot be traded.

## An anomaly this document will not paper over

Cell B reports the HIGHEST IC (+0.0755) and a strongly NEGATIVE spread
(−33.16 bp), with per-year spreads swinging +136, −1, −163, −58, −114 bp against
a steady positive IC. A stable positive rank correlation cannot produce a
sign-flipping decile mean if both measure the same thing well.

The likely explanation is not a broken alignment — label and forward return both
read `close[i+hold]` — but the estimator: IC is a rank statistic and robust to
outliers, while the decile spread is a MEAN of raw returns and is not. Five-day
returns across a microcap-heavy universe have tails that a mean cannot survive.
Cell C is consistent with that reading: in liquid names, where the tails are
thinner, the spread collapses toward zero.

That is a hypothesis, not a demonstration. It is not being asserted as a
finding, and the number is not being quoted as a market fact. Resolving it needs
a winsorized or dollar-neutral weighted spread, which is the next measurement,
not this one.

## Status

The cross-sectional signal is REAL and NOT TRADEABLE on this evidence. No
feature was added, no hyperparameter tuned, no year dropped, and no cell was
withheld. The honest ceiling of the current model is an IC around 0.05 that
costs consume.

The next question worth asking is not "how do we raise the lift". It is whether
a slower, liquidity-constrained, outlier-robust construction retains any of the
IC — and cells C and D say the answer is probably not much.

Reproduce:

```
python research/xsvalue.py --json A.json
python research/xsvalue.py --hold 5 --json B.json
python research/xsvalue.py --liquid-top 1000 --json C.json
python research/xsvalue.py --hold 5 --liquid-top 1000 --json D.json
```
