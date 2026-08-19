# The differentiated tables are too sparse to be features, and the events say nothing

Three findings, in order of how much work each saves.

## 1. As cross-sectional features they are not merely weak, they are impossible

Measured against 2,655,783 stock symbol-days of daily bars:

| table | symbol-days covered | share of universe | span |
|---|---|---|---|
| `insider_trades` (by `filed_ts`) | 2,860 | **0.108%** | 2008-03-21 .. 2026-08-07, 621 symbols |
| `sentiment_features` | 41,880 | **1.577%** | 2012-04-17 .. 2026-08-11, 699 symbols |
| `filings` | 29,878 | **1.125%** | 1998-08-20 .. 2026-08-11, 907 symbols |
| `short_volume` | 26,070 | **0.982%** | 2026-05-20 .. 2026-08-10, 1040 symbols |

A cross-sectional model ranks roughly 300 names against each other on one day.
A feature present on 1.6% of rows exists for about five of them. You cannot rank
a cross-section on a column that 98% of the cross-section lacks, and no amount
of imputation fixes that — imputing the missing 98% means ranking on the
imputation.

So the originally proposed test, "does insider/sentiment data add IC over the
six price features", is not a test that can be run. It is reported here as
structurally impossible rather than as a negative result, because those are
different claims and only one of them is true.

`short_volume` and `filings` fail a second way regardless: 2026-05-20 and (in
usable density) 2026-02-05 onward give too little history to walk forward at
all.

## 2. As events, the effect is absent at 1d and 5d

`research/eventstudy.py` keys each event to the first trading day at or after
`filed_ts` — never `tx_ts`, which is when the trade happened and is not knowable
at decision time. Returns are market-relative: the day's median stock return at
the same horizon is subtracted, so a sample that happens to sit in a bull month
does not read as skill. Inference clusters by EVENT DAY, since many events share
a day and are not independent.

| arm | H | n events | n days | mean abnormal | 95% CI |
|---|---|---|---|---|---|
| insider_purchase | 1 | 189 | 137 | +27.59 bp | [−72.94, +128.11] |
| insider_purchase | 5 | 189 | 137 | −35.56 bp | [−232.04, +160.92] |
| insider_purchase | 21 | 167 | 127 | +126.74 bp | [−336.48, +589.96] |
| insider_sale | 1 | 1307 | 279 | −3.71 bp | [−41.66, +34.25] |
| insider_sale | 5 | 1302 | 276 | +46.63 bp | [−59.73, +152.98] |
| insider_sale | 21 | 1131 | 261 | +197.21 bp | [+0.60, +393.81] |

Every interval at 1 and 5 days contains zero. There is no detectable effect.

## 3. The one "significant" cell is refuted by its own placebo

`insider_sale` at 21 days is the single interval that excludes zero, at
+197.21 bp [+0.60, +393.81]. Its matched placebo — the same count of events
drawn at random from the same days — returns **+306.76 bp [+94.44, +519.07]**.

The random control is LARGER and MORE significant than the real signal. That
does not mean insider sales predict returns; it means the 21-day measurement is
biased and neither number can be believed. The placebo exists precisely to catch
this, and it caught it. Both 1-day placebos contain zero, so the bias is
specific to the long horizon — most likely the placebo drawing from all stocks
while insider events concentrate in a particular kind of name, which makes it an
unmatched control at the horizon where composition matters most.

Quoting the +197 bp without its placebo would have produced a publishable-looking
insider-trading result out of nothing.

## 4. Sentiment produced zero events

`sentiment_high` and `sentiment_low` fired on **0** events. The arm required a
symbol in the top or bottom decile of that day's sentiment cross-section with
`n_polar >= 3`; with roughly 699 symbols spread over 14 years, a given day holds
too few scored names for a within-day decile to mean anything, and the polarity
floor removes what remains. Reported as zero rather than quietly dropped.

## Status

No usable signal from the differentiated tables. Not "we tried and it was
weak" — as features they are uncomputable at this coverage, as events they are
flat where the measurement is trustworthy, and the one place they look
significant is where the measurement demonstrably is not.

```
python research/eventstudy.py --json ev.json
```
