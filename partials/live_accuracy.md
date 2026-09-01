<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-31T16:25:02) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 14 collapsed cross-section(s) of 52 day(s): 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-08-31T16:25:02. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,768 | 44.4% | 55.4% | -11.0pp | 24 | [34.6%, 54.6%] |
| prequential-majority (1d) | all | 2,439 | 56.2% | 54.2% | +2.0pp | 22 | [37.6%, 73.2%] |
| directional-ensemble (1w) | all | 5,062 | 45.1% | 58.0% | -12.8pp | 28 | withheld |
| prequential-majority (1w) | all | 4,701 | 58.7% | 58.1% | +0.6pp | 26 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 645 | 40.9% | 55.6% | -14.7pp | 24 | withheld |
| filingsdrift21 | all | 44 | withheld — no null | — | — | 4 | withheld |
| liquidity21 | all | 814 | 74.8% | 72.1% | +2.7pp | 4 | withheld |
| liquidity21#persist | all | 531 | withheld — no null | — | — | 3 | withheld |
| liquidity21-crypto | all | 31 | withheld — no null | — | — | 6 | withheld |
| liquidity21-crypto#persist | all | 10 | withheld — no null | — | — | 3 | withheld |
| trend21 | all | 819 | 77.8% | 77.2% | +0.6pp | 4 | withheld |
| trend21#persist | all | 539 | withheld — no null | — | — | 3 | withheld |
| trend21-crypto | all | 31 | withheld — no null | — | — | 6 | withheld |
| trend21-crypto#persist | all | 10 | withheld — no null | — | — | 3 | withheld |
| vol21 | all | 825 | 59.6% | 57.8% | +1.9pp | 3 | withheld |
| vol21#persist | all | 540 | withheld — no null | — | — | 2 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4436 [0.3463, 0.5455] and its null 0.5536 [0.3858, 0.7101] OVERLAP across [0.3858, 0.5455]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1100 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (4/10 credible days of 28, 24 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (4/10 credible days of 26, 22 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (4/10 credible days of 24, 20 degenerate) — no interval, so no verdict
- `filingsdrift21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `liquidity21-crypto` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21-crypto#persist` — BENCHMARK (INSUFFICIENT 10/30)
- `trend21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21-crypto` — NO BASELINE — naive-persistence null not frozen for these calls
- `trend21-crypto#persist` — BENCHMARK (INSUFFICIENT 10/30)
- `vol21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `vol21#persist` — BENCHMARK — the frozen naive-persistence null itself

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 10,344 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=46, divisor=828, corrected_alpha=6.0386473429951696e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.42pp (active-only 83.56% minus survivorship-clean 83.14%, n=75,179 clean vs 18,025 active, revalidation of 2026-08-23T10:20:15+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
