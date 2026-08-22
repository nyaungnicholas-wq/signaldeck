<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-21T18:33:27) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 16 collapsed cross-section(s) of 40 day(s): 1d 2026-07-29 (13 distinct across 328 symbols), 1d 2026-07-31 (6 distinct across 328 symbols), 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 326 symbols), 1w 2026-07-29 (25 distinct across 326 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (8 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 327 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-08-21T18:33:27. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,649 | 44.1% | 55.5% | -11.4pp | 21 | [34.4%, 54.3%] |
| prequential-majority (1d) | all | 2,315 | 56.4% | 54.3% | +2.1pp | 18 | [37.5%, 73.6%] |
| directional-ensemble (1w) | all | 4,563 | 44.6% | 59.1% | -14.5pp | 19 | [34.9%, 54.7%] |
| prequential-majority (1w) | all | 4,202 | 60.0% | 59.3% | +0.6pp | 17 | [49.1%, 70.0%] |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 497 | 39.2% | 60.3% | -21.0pp | 16 | [26.3%, 53.9%] |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4413 [0.3441, 0.5432] and its null 0.5551 [0.3854, 0.7129] OVERLAP across [0.3854, 0.5432]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1138 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4458 [0.3487, 0.5472] and its null 0.5906 [0.4882, 0.6857] OVERLAP across [0.4882, 0.5472]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1449 is not resolved by this sample
- `prequential-majority (1w)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.3924 [0.2632, 0.5385] and its null 0.6026 [0.4549, 0.7337] OVERLAP across [0.4549, 0.5385]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.2103 is not resolved by this sample

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 186 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 7,285 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 170 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 7,368 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 170 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 7,368 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 7,387 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=37, divisor=481, corrected_alpha=0.00010395010395010396.

**Survivorship:** epoch 2026-07-24; unmeasured — no graded post-epoch symbols; measured effect +0.42pp (active-only 83.57% minus survivorship-clean 83.15%, n=74,829 clean vs 18,020 active, revalidation of 2026-08-21T10:20:16+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
