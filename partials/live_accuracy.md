<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-26T18:38:32) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 13 collapsed cross-section(s) of 46 day(s): 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 326 symbols), 1w 2026-07-29 (25 distinct across 326 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-08-26T18:38:32. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,766 | 44.4% | 55.4% | -11.0pp | 24 | [34.8%, 54.3%] |
| prequential-majority (1d) | all | 2,432 | 56.1% | 54.1% | +2.0pp | 21 | [37.9%, 72.8%] |
| directional-ensemble (1w) | all | 4,794 | 44.8% | 58.4% | -13.5pp | 22 | withheld |
| prequential-majority (1w) | all | 4,433 | 59.2% | 58.6% | +0.6pp | 20 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 563 | 39.8% | 57.1% | -17.3pp | 19 | withheld |
| filingsdrift21 | all | 15 | — | — | — | 2 | withheld |
| liquidity21 | all | 275 | withheld — no null | — | — | 1 | withheld |
| liquidity21-crypto | all | 14 | — | — | — | 2 | withheld |
| trend21 | all | 275 | withheld — no null | — | — | 1 | withheld |
| trend21-crypto | all | 14 | — | — | — | 2 | withheld |
| vol21 | all | 278 | withheld — no null | — | — | 1 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4436 [0.3485, 0.5430] and its null 0.5537 [0.3896, 0.7069] OVERLAP across [0.3896, 0.5430]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1101 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (3/10 credible days of 22, 19 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (3/10 credible days of 20, 17 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (4/10 credible days of 19, 15 degenerate) — no interval, so no verdict
- `filingsdrift21` — PENDING (first grade 2026-08-14, 15/30 resolved)
- `liquidity21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21-crypto` — PENDING (first grade 2026-08-14, 14/30 resolved)
- `trend21` — NO BASELINE — naive-persistence null not frozen for these calls
- `trend21-crypto` — PENDING (first grade 2026-08-14, 14/30 resolved)
- `vol21` — NO BASELINE — naive-persistence null not frozen for these calls

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 8,762 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=42, divisor=546, corrected_alpha=9.157509157509158e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.42pp (active-only 83.56% minus survivorship-clean 83.14%, n=75,179 clean vs 18,025 active, revalidation of 2026-08-23T10:20:15+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
