<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-09-03T14:05:50) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 13 collapsed cross-section(s) of 58 day(s): 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-09-03T14:05:50. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,862 | 44.7% | 55.0% | -10.4pp | 27 | [35.1%, 54.7%] |
| prequential-majority (1d) | all | 2,533 | 55.8% | 53.9% | +2.0pp | 25 | [37.8%, 72.5%] |
| directional-ensemble (1w) | all | 5,520 | 45.4% | 56.1% | -10.7pp | 31 | withheld |
| prequential-majority (1w) | all | 5,159 | 56.7% | 56.1% | +0.5pp | 29 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 816 | 42.6% | 50.2% | -7.5pp | 28 | withheld |
| filingsdrift21 | all | 68 | withheld — no null | — | — | 6 | withheld |
| liquidity21 | all | 1,390 | 72.9% | 70.9% | +2.1pp | 6 | withheld |
| liquidity21#persist | all | 1,105 | withheld — no null | — | — | 5 | withheld |
| liquidity21-crypto | all | 51 | 92.2% | 86.7% | +5.5pp | 9 | withheld |
| liquidity21-crypto#persist | all | 30 | withheld — no null | — | — | 6 | withheld |
| trend21 | all | 1,399 | 78.6% | 78.5% | +0.0pp | 6 | withheld |
| trend21#persist | all | 1,117 | withheld — no null | — | — | 5 | withheld |
| trend21-crypto | all | 51 | 51.0% | 26.7% | +24.3pp | 9 | withheld |
| trend21-crypto#persist | all | 30 | withheld — no null | — | — | 6 | withheld |
| vol21 | all | 1,405 | 54.9% | 52.9% | +2.1pp | 5 | withheld |
| vol21#persist | all | 1,118 | withheld — no null | — | — | 4 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4465 [0.3505, 0.5467] and its null 0.5505 [0.3866, 0.7041] OVERLAP across [0.3866, 0.5467]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1039 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (5/10 credible days of 31, 26 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (5/10 credible days of 29, 24 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 28, 23 degenerate) — no interval, so no verdict
- `filingsdrift21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `liquidity21-crypto` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21-crypto` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `vol21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `vol21#persist` — BENCHMARK — the frozen naive-persistence null itself

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 12,614 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=50, divisor=900, corrected_alpha=5.555555555555556e-05.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 329/1044 graded symbols (31.5%): 715 inactive symbol(s) with no delisted_at; measured effect +0.50pp (active-only 83.61% minus survivorship-clean 83.10%, n=75,654 clean vs 18,007 active, revalidation of 2026-09-01T10:20:09+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
