# The inversion: there is nothing to exploit, and one real bug beside it

**Date:** 2026-08-08. **Question:** are the legs anti-predictive because of a
data-path defect, or genuinely?

**Answer: neither.** The pressure leg is **indistinguishable from chance** on the
statistic it is actually used for. The 0.36 figure that fired the fleet veto is an
artifact of the estimator, not a measurement of backwardness. Separately, a real
data-integrity bug was found: **33% of stock labels are duplicated across
weekends.**

---

## What was ruled out, with evidence

| hypothesis | test | result |
|---|---|---|
| labels misaligned | recomputed `fwd_return` from bars for 4,000 resolved rows | **4,000/4,000 exact, 0 direction disagreements** |
| `pressure_score` sign flipped at source | corr with PRIOR-day return | **+0.1379** — correctly tracks the recent move |
| AUC computed wrong | read both implementations | `clusterstat.RankAUC` and `pressure.aucRank` are **byte-identical** correct Mann-Whitney |
| market simply mean-reverts | daily cross-sectional autocorrelation, 31 days | **mean +0.27**, 14/31 negative — not stable, does not explain it |

The label path is clean. The sign convention is correct. No sign-flip is
warranted, and the session's rule forbidding one was right.

## The finding: the leg is noise, not backwards

The fleet veto reads an **n-weighted mean of per-symbol AUCs**, each estimated on
~30 noisy observations. That is a different and worse-behaved statistic than the
one the leg is actually asked for — the leg ranks symbols *against each other on
one day*, so the relevant measure is the **within-day cross-sectional AUC**:

```
within-day cross-sectional AUC, stocks, weekday rows only,
one row per symbol-day, 16 days

  mean daily AUC          0.4636
  95% CI (day-clustered)  [0.4113, 0.5158]   <-- CONTAINS 0.5
  days above 0.5          5/16
```

| statistic | value | what it says |
|---|---|---|
| n-weighted mean of per-symbol AUCs (drives the veto) | **0.3614** | strongly backwards |
| pooled AUC, all rows | 0.4676 | slightly backwards |
| pooled AUC, clean rows | 0.4780 | slightly backwards |
| **within-day cross-sectional, day-clustered** | **0.4636 [0.411, 0.516]** | **chance** |

An AUC of 0.36 would be genuinely exploitable inverted. An AUC of 0.464 with a CI
straddling 0.5 is not exploitable in either direction. **Flipping this sign would
be fitting noise on 16 days** — the exact overfitting the session prohibits.

## The real bug: weekend label duplication

`base = BarAtOrBefore(p.Ts)` and `fwd = BarAtOrAfter(base.Ts + 86400)`. For a
prediction made on Saturday the base bar is Friday's and the forward bar is
Monday's — identical to Friday's own prediction. Friday, Saturday and Sunday all
grade against **one** Friday→Monday move.

```
consecutive symbol-day pairs  15,917
IDENTICAL label                5,229   (32.9%)

  crypto (24/7 bars)   0.0%
  stocks              33.2%

by day-of-week (stocks)
  Sun 84.7%   Mon 27.1%   Tue 24.7%   Wed 0.6%   Thu 17.3%   Fri 0.8%   Sat 64.9%
```

`md.TradingDay()` is a pure calendar shift `(ts - offset) / 86400`. It merges the
after-close tail onto its session, which is what it was written for, but it never
maps a weekend onto the preceding session. So Saturday and Sunday receive their
own day indices and the dedup that exists specifically to enforce independence
counts them as **three independent observations of one market move**.

This survived the 2026-08-05 dedup fix because that fix keyed on the
PREDICTION's calendar day. The correct independence unit is the **settled move**,
identified by the base bar.

**Impact.** Roughly 1.5x inflation of n on the stock record, and a day-clustered
design effect that understates the true clustering because it clusters on the
wrong key. It does NOT explain the inversion — removing the duplicated rows moves
the pooled AUC by **+0.0104**.

## Not fixed here

The fix is to make the independence unit the settling bar rather than the
prediction's calendar day. That changes the denominator of every published
statistic — n, effective n, every CI, every verdict in the accuracy registry —
so it is deliberately not applied in the same pass that discovered it. It needs
its own change, its own before/after on the frozen holdout, and a decision about
restating existing figures.

## What this means for the starving legs

The legs are not being wrongly benched by a broken measurement, and they are not
secretly informative upside-down. On the evidence available they carry no
directional signal at 1d, and the honest output remains **no directional product
yet**.

The veto's estimator is worth changing regardless — an n-weighted mean of
per-symbol AUCs on ~30 observations each is a noisy statistic to gate on, and it
reported 0.36 where the day-clustered cross-sectional measure reports 0.46 with a
CI containing chance. That is a real difference in what the fleet is being told.
