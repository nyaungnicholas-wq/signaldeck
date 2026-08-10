# Root cause: the cross-section collapse

**Date:** 2026-08-08. **Scope:** Gate 1, narrowed to *why the cross-section
flattens*. **Status:** root cause identified for both collapses; one is ACTIVE.

Reproduce: `go run ./cmd/forecastmon --days 30`

---

## Summary

There are **two separate collapses with different mechanisms**, which is why
neither previous fix ended it. One is in the calibrator; the other is upstream in
the ensemble and is happening right now.

| | Collapse A | Collapse B |
|---|---|---|
| when | 2026-07-27 → 08-04 (and 07-12 → 07-15) | 2026-08-07 → now |
| where | calibration | ensemble leg admission |
| raw score | **healthy** (174–188 distinct / 329) | **collapsed** (32 distinct / 329) |
| published | collapsed (5–13 distinct) | — (unresolved) |
| status | recovered 08-05 | **ACTIVE** |

The decisive query was one line: count distinct `raw_prob` vs distinct `cal_prob`
per day. On 08-04 raw carried 188 distinct values and calibrated carried 12.
On 08-07 raw itself carried 32.

## Collapse A — one global isotonic map for the whole universe

`predict.go` selects calibration per symbol:

```go
cal := raw
if usePersonalCal {          // per-symbol isotonic
    cal = personalCal(raw)
} else if fn, ok := globalCal[h]; ok {
    cal = fn(raw)            // ONE fleet-wide map
}
```

Measured on the live database: **all 2,950 `symbol_models` rows for 1d are
`tier='static'`, `fitted=0`, with zero knots.** The personal branch is therefore
never taken, and every symbol is calibrated by the same fleet-wide map.

Isotonic regression (`poolAdjacentViolators`) is a **step function**. A map with
K blocks can emit at most K distinct values — for the entire universe. When the
raw score carries little monotone signal, PAV pools nearly everything, K falls to
5–13, and 329 symbols receive 5–13 distinct probabilities. The cross-sectional
ranking is destroyed downstream of a model that had produced one.

Corroborating detail: on 2026-07-25/26 the calibrated range is exactly
[0.250, 0.750], which is `persistedCalLo`/`persistedCalHi` — a hard display
clamp, confirming a persisted map was in force.

**This was already known.** `CalibrateRanking` (`c17d8fe`, 2026-08-01) exists for
precisely this defect; its comment records the measurement — *"collapsed seven
distinct per-symbol crypto probabilities to a single 0.4635 and 322 stocks to six
distinct values"*. The published record recovers on 08-05, the day `e346809` and
`e081d4b` landed. On 08-05 `cal_prob == raw_prob` exactly: the map was refused
and the identity used, which is the intended honest failure.

So the mechanism is understood and the guard works. What did not exist was
anything that **noticed** during the eight days it was failing.

## Collapse B — every leg failed its admission bar (ACTIVE)

The current collapse is upstream of calibration, so no calibration fix touches
it. The ensemble's own admitted-leg count fell:

| day | rows | distinct raw | 0 legs | 1 leg | 2+ legs |
|---|---|---|---|---|---|
| 2026-08-06 | 14,032 | 1,475 | 24 | 820 | **13,188** |
| 2026-08-07 | 3,043 | **78** | 557 | 2,008 | 478 |
| 2026-08-08 | 677 | **31** | 36 | 537 | 104 |

With one admitted leg the blend has nothing to vary across symbols, so `raw_prob`
degenerates mechanically. `GBMProb` disappears from the component set on 08-07.

The trainers say why, in their own run details:

```
gbm-trainer         ok   trained 0 model legs over 329 symbols
                         (0 GBM + 0 mean-rev with OOS edge);
                         feature-health retired 31 input(s)
pressure-trainer    ok   0 symbol-horizons graded, 0 benched (OOS lift<=0)
expectancy-trainer  ok   fleet AUC 0.4303 over 317 symbols / 8220 symbol-days
                         (ANTI-PREDICTIVE - benched fleet-wide)
```

`gbm-trainer` has **never** trained a non-zero leg count in the retained run
window.

**The admission gates are not broken. They are correct.** Expectancy at fleet AUC
0.4303 is anti-predictive; pressure has OOS lift ≤ 0; no GBM leg clears its edge
bar. Each is correctly refused. The collapse is the honest consequence of a
system whose legs do not predict — and it is independent evidence for the
inversion seen in the bucket table, arrived at by a different route.

Macro inputs were checked and exonerated: `macro_series` is fresh to 08-06/08-07
for every series that feeds the retired features.

## The actual defect

Not the collapse. **The silence.**

Every one of those trainers files status **`ok`** while admitting zero legs.
"trained 0 model legs" is recorded as a success. Nothing anywhere compared the
count of admitted legs to the previous day, and nothing compared distinct
published probabilities to symbol count — so a fleet that had stopped
discriminating reported a healthy run every hour for eight days, and then again
starting 08-07.

The system kept **publishing a forecast** the whole time, assembled from whatever
single leg survived.

## Fixed

- `forecastmon` now checks the **raw** side (`ForecastDayStatsRaw`), so a
  collapse is caught the day it is emitted rather than a horizon later. The
  resolved-side view could not see 08-07 at all.
- Raw and published are reported separately, which is what distinguishes
  Collapse A from Collapse B without a query.
- `AdmittedLegHistogram` records the leg count per day, so the mechanism behind a
  raw collapse is diagnosable rather than inferred.
- A raw collapse is a hard worker error → `/api/health` degraded within a day,
  with no alert transport required.

## Not fixed — decisions for the operator

1. **A forecast is still published when the blend runs on one leg or none.**
   The honest behaviour is to withhold, exactly as `globalCalibration` withholds
   below its evidence floor. That is a product decision, not a bug fix.
2. **Trainers report `ok` when they admit nothing.** They should return
   `workers.ErrDegraded` — "completed without delivering" is what that error
   exists to say, and it is already used elsewhere for exactly this.
3. **Per-symbol calibration has never fitted** (0 of 2,950). Either it needs the
   corpus to reach its floor, or the floor is unreachable in practice and the
   tier is dead weight.
4. **Nothing gates publication on the monitor.** `/accuracy` and the registry
   will still publish figures computed over collapsed days.

None of these is applied here: each changes what the product emits, and this
session's rule is that fixes land one at a time and attributably.
