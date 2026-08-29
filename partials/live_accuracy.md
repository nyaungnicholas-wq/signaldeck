<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-28T19:23:56) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 14 collapsed cross-section(s) of 49 day(s): 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-08-28T19:23:56. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,768 | 44.4% | 55.4% | -11.0pp | 24 | [34.7%, 54.5%] |
| prequential-majority (1d) | all | 2,439 | 56.2% | 54.2% | +2.0pp | 22 | [37.7%, 73.1%] |
| directional-ensemble (1w) | all | 5,056 | 45.2% | 58.0% | -12.8pp | 25 | withheld |
| prequential-majority (1w) | all | 4,633 | 58.8% | 58.3% | +0.6pp | 22 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 647 | 41.1% | 55.7% | -14.6pp | 22 | withheld |
| filingsdrift21 | all | 32 | withheld — no null | — | — | 3 | withheld |
| liquidity21 | all | 683 | 76.1% | 73.4% | +2.8pp | 3 | withheld |
| liquidity21#persist | all | 402 | withheld — no null | — | — | 2 | withheld |
| liquidity21-crypto | all | 28 | — | — | — | 4 | withheld |
| liquidity21-crypto#persist | all | 7 | withheld — no null | — | — | 1 | withheld |
| trend21 | all | 685 | 77.4% | 77.0% | +0.3pp | 3 | withheld |
| trend21#persist | all | 405 | withheld — no null | — | — | 2 | withheld |
| trend21-crypto | all | 28 | — | — | — | 4 | withheld |
| trend21-crypto#persist | all | 7 | withheld — no null | — | — | 1 | withheld |
| vol21 | all | 690 | 60.3% | 58.3% | +2.0pp | 2 | withheld |
| vol21#persist | all | 405 | withheld — no null | — | — | 1 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4436 [0.3465, 0.5452] and its null 0.5536 [0.3862, 0.7098] OVERLAP across [0.3862, 0.5452]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1100 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (4/10 credible days of 25, 21 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (4/10 credible days of 22, 18 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (4/10 credible days of 22, 18 degenerate) — no interval, so no verdict
- `filingsdrift21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `liquidity21-crypto` — PENDING (first grade 2026-08-14, 28/30 resolved)
- `liquidity21-crypto#persist` — BENCHMARK (INSUFFICIENT 7/30)
- `trend21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21-crypto` — PENDING (first grade 2026-08-14, 28/30 resolved)
- `trend21-crypto#persist` — BENCHMARK (INSUFFICIENT 7/30)
- `vol21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `vol21#persist` — BENCHMARK — the frozen naive-persistence null itself

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 10,343 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=44, divisor=792, corrected_alpha=6.313131313131313e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.42pp (active-only 83.56% minus survivorship-clean 83.14%, n=75,179 clean vs 18,025 active, revalidation of 2026-08-23T10:20:15+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
