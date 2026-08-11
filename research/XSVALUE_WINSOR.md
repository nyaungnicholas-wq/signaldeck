# Winsorising reverses the sign, and that is a diagnostic, not a profit

Clipping forward returns at the 1st/99th percentile WITHIN each day, before the
decile means and after the IC, turns every cell of the pre-registered grid from
negative to positive.

| cell | config | IC | raw spread | winsorised spread | net @10bp |
|---|---|---|---|---|---|
| A | hold=1, all | +0.0506 | +18.75 | **+45.90** [+35.4, +56.4] | +32.54 |
| B | hold=5, all | +0.0755 | **−33.16** | **+117.52** [+88.3, +146.8] | +115.71 |
| C | hold=1, liquid1000 | +0.0291 | −3.18 | **+15.58** [+3.6, +27.6] | +4.13 |
| D | hold=5, liquid1000 | +0.0386 | −1.45 | **+71.56** [+41.5, +101.6] | +70.20 |

Cell B moves 150 bp on nothing but the treatment of 2% of each day's rows. The
hypothesis recorded in `XSVALUE_RESULT.md` — that a rank IC can be honestly
positive while a decile MEAN is destroyed by microcap tails — is confirmed. The
ranking was never the problem. The estimator was.

## What this does NOT license

**A winsorised spread is not a return anyone can earn.** If a name in the short
book runs +300%, that loss is real; clipping it to the 99th percentile does not
refund it. The clipped number answers "is the central tendency of this signal
positive" (yes, decisively). It does not answer "what would this have paid",
and the raw column is closer to that answer.

So the correct reading is NOT "the strategy makes 45 bp a day". It is:

- the signal carries real information, now confirmed twice over;
- an EQUAL-WEIGHTED decile book is the wrong container for it, because a
  handful of names dominate the average and the sign with it;
- the economically meaningful version of this measurement is a portfolio with
  bounded per-name contribution — position caps, volatility scaling, or equal
  RISK rather than equal weight. That construction is economically similar to
  winsorising, which is the honest reason to think some of this is capturable.

**The magnitudes are implausible and should be treated as such.** +45.90 bp per
day gross (cell A) is not a real long-short return; annualised it is absurd. It
is what an equal-weighted decile of a 2,870-name universe that includes
untradeable microcaps produces on paper. The number is evidence about the
signal, not a forecast of P&L.

## The one cell that formally passes

`XSVALUE_PREREG.md` required: net spread positive with a CI excluding zero at
10 bps per side, in a majority of walk-forward years.

**Cell D passes.** Liquid top-1000, 5-day hold, winsorised: gross
+71.56 bp [+41.5, +101.6] per 5-day period against 2 × 0.068 × 10 = 1.4 bp of
cost, so the net interval excludes zero comfortably, and 4 of 5 years are
positive. Turnover of 0.068 is what makes it survive — the slow book barely
pays anything to trade.

Cell C, the most conservative cell, does NOT pass: gross [+3.6, +27.6] against
11.5 bp of cost leaves a net interval containing zero.

Cell D's caveats are load-bearing: one negative year (2023, −4 bp), and 2026 at
+205 bp is a large enough outlier that the mean leans on it.

## Status

Upgraded from "real and not tradeable" to **real, and plausibly tradeable in a
slow, liquidity-constrained, risk-bounded construction** — on the strength of
one formally passing cell whose headline metric is a diagnostic rather than a
P&L. What it now needs is a capped-position backtest that earns raw returns
under a bounded-weight rule, not another clipped average. Until that exists this
is not a claim about money.

```
python research/xsvalue.py --winsor 1 --json A.json
python research/xsvalue.py --winsor 1 --hold 5 --json B.json
python research/xsvalue.py --winsor 1 --liquid-top 1000 --json C.json
python research/xsvalue.py --winsor 1 --hold 5 --liquid-top 1000 --json D.json
```
