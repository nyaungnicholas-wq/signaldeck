<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-09-02T18:24:10) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 13 collapsed cross-section(s) of 55 day(s): 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-09-02T18:24:10. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,799 | 44.4% | 55.3% | -10.8pp | 25 | [34.8%, 54.6%] |
| prequential-majority (1d) | all | 2,497 | 55.8% | 53.8% | +2.0pp | 24 | [37.5%, 72.7%] |
| directional-ensemble (1w) | all | 5,380 | 45.3% | 56.7% | -11.4pp | 30 | withheld |
| prequential-majority (1w) | all | 4,938 | 57.7% | 57.1% | +0.5pp | 28 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 768 | 42.4% | 51.6% | -9.2pp | 27 | withheld |
| filingsdrift21 | all | 67 | withheld — no null | — | — | 6 | withheld |
| liquidity21 | all | 1,099 | 74.1% | 71.6% | +2.4pp | 5 | withheld |
| liquidity21#persist | all | 814 | withheld — no null | — | — | 4 | withheld |
| liquidity21-crypto | all | 44 | withheld — no null | — | — | 8 | withheld |
| liquidity21-crypto#persist | all | 23 | withheld — no null | — | — | 5 | withheld |
| trend21 | all | 1,107 | 77.9% | 77.6% | +0.3pp | 5 | withheld |
| trend21#persist | all | 825 | withheld — no null | — | — | 4 | withheld |
| trend21-crypto | all | 44 | withheld — no null | — | — | 8 | withheld |
| trend21-crypto#persist | all | 23 | withheld — no null | — | — | 5 | withheld |
| vol21 | all | 1,112 | 57.2% | 54.5% | +2.6pp | 4 | withheld |
| vol21#persist | all | 825 | withheld — no null | — | — | 3 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4444 [0.3475, 0.5458] and its null 0.5529 [0.3862, 0.7085] OVERLAP across [0.3862, 0.5458]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1084 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (5/10 credible days of 30, 25 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (4/10 credible days of 28, 24 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 27, 22 degenerate) — no interval, so no verdict
- `filingsdrift21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `liquidity21-crypto` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21-crypto#persist` — BENCHMARK (INSUFFICIENT 23/30)
- `trend21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21-crypto` — NO BASELINE — naive-persistence null not frozen for these calls
- `trend21-crypto#persist` — BENCHMARK (INSUFFICIENT 23/30)
- `vol21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `vol21#persist` — BENCHMARK — the frozen naive-persistence null itself

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 11,561 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=49, divisor=882, corrected_alpha=5.668934240362812e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.50pp (active-only 83.61% minus survivorship-clean 83.10%, n=75,654 clean vs 18,007 active, revalidation of 2026-09-01T10:20:09+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
