<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-22T14:05:24) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 15 collapsed cross-section(s) of 41 day(s): 1d 2026-07-31 (6 distinct across 328 symbols), 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 326 symbols), 1w 2026-07-29 (25 distinct across 326 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-08-22T14:05:24. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,670 | 44.1% | 55.5% | -11.4pp | 21 | [34.5%, 54.2%] |
| prequential-majority (1d) | all | 2,341 | 56.4% | 54.3% | +2.1pp | 19 | [37.7%, 73.5%] |
| directional-ensemble (1w) | all | 4,653 | 44.7% | 58.7% | -14.0pp | 20 | [35.1%, 54.7%] |
| prequential-majority (1w) | all | 4,292 | 59.5% | 58.9% | +0.6pp | 18 | [48.7%, 69.5%] |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 522 | 39.7% | 58.7% | -19.1pp | 17 | [26.8%, 54.2%] |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4412 [0.3446, 0.5425] and its null 0.5552 [0.3865, 0.7121] OVERLAP across [0.3865, 0.5425]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1140 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4468 [0.3507, 0.5471] and its null 0.5867 [0.4850, 0.6816] OVERLAP across [0.4850, 0.5471]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1399 is not resolved by this sample
- `prequential-majority (1w)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.3966 [0.2677, 0.5416] and its null 0.5872 [0.4204, 0.7360] OVERLAP across [0.4204, 0.5416]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1906 is not resolved by this sample

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 186 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 7,913 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 177 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 8,005 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 177 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 8,005 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 8,024 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=38, divisor=494, corrected_alpha=0.00010121457489878543.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 328/651 graded symbols (50.4%): 323 inactive symbol(s) with no delisted_at; measured effect +0.42pp (active-only 83.56% minus survivorship-clean 83.14%, n=75,179 clean vs 18,025 active, revalidation of 2026-08-22T10:20:15+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
