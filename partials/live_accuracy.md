<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-25T18:50:28) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 14 collapsed cross-section(s) of 43 day(s): 1d 2026-07-31 (6 distinct across 328 symbols), 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 326 symbols), 1w 2026-07-29 (25 distinct across 326 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-08-25T18:50:28. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,695 | 44.1% | 55.5% | -11.4pp | 22 | [34.5%, 54.2%] |
| prequential-majority (1d) | all | 2,366 | 56.4% | 54.3% | +2.1pp | 20 | [37.8%, 73.4%] |
| directional-ensemble (1w) | all | 4,724 | 44.8% | 58.5% | -13.7pp | 21 | withheld |
| prequential-majority (1w) | all | 4,363 | 59.3% | 58.7% | +0.6pp | 19 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 541 | 39.9% | 57.9% | -18.0pp | 18 | withheld |
| filingsdrift21 | all | 10 | — | — | — | 1 | withheld |
| liquidity21 | all | 121 | 72.7% | — | — | 1 | withheld |
| liquidity21-crypto | all | 7 | — | — | — | 1 | withheld |
| trend21 | all | 121 | 73.6% | — | — | 1 | withheld |
| trend21-crypto | all | 7 | — | — | — | 1 | withheld |
| vol21 | all | 123 | 61.8% | — | — | 1 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4412 [0.3451, 0.5419] and its null 0.5553 [0.3875, 0.7114] OVERLAP across [0.3875, 0.5419]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1141 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (3/10 credible days of 21, 18 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (3/10 credible days of 19, 16 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (4/10 credible days of 18, 14 degenerate) — no interval, so no verdict
- `filingsdrift21` — PENDING (first grade 2026-08-14, 10/30 resolved)
- `liquidity21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21-crypto` — PENDING (first grade 2026-08-14, 7/30 resolved)
- `trend21` — NO BASELINE — naive-persistence null not frozen for these calls
- `trend21-crypto` — PENDING (first grade 2026-08-14, 7/30 resolved)
- `vol21` — NO BASELINE — naive-persistence null not frozen for these calls

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 8,289 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=41, divisor=533, corrected_alpha=9.380863039399625e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.42pp (active-only 83.56% minus survivorship-clean 83.14%, n=75,179 clean vs 18,025 active, revalidation of 2026-08-23T10:20:15+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
