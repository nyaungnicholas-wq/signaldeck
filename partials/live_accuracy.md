<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-20T14:05:46) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 15 collapsed cross-section(s) of 38 day(s): 1d 2026-07-31 (6 distinct across 328 symbols), 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (34 distinct across 327 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 326 symbols), 1w 2026-07-29 (25 distinct across 326 symbols), 1w 2026-07-31 (33 distinct across 326 symbols), 1w 2026-08-01 (16 distinct across 327 symbols), 1w 2026-08-02 (13 distinct across 327 symbols), 1w 2026-08-03 (8 distinct across 327 symbols), 1w 2026-08-04 (7 distinct across 326 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-08-20T14:05:46. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,617 | 44.0% | 55.6% | -11.6pp | 20 | [34.2%, 54.2%] |
| prequential-majority (1d) | all | 2,288 | 56.6% | 54.4% | +2.2pp | 18 | [37.5%, 73.9%] |
| directional-ensemble (1w) | all | 4,325 | 44.3% | 59.3% | -14.9pp | 18 | [34.4%, 54.7%] |
| prequential-majority (1w) | all | 3,964 | 60.2% | 59.6% | +0.7pp | 16 | [49.4%, 70.1%] |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 490 | 38.8% | 60.7% | -21.9pp | 16 | [26.5%, 52.7%] |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4398 [0.3423, 0.5423] and its null 0.5562 [0.3849, 0.7150] OVERLAP across [0.3849, 0.5423]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1164 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4432 [0.3444, 0.5468] and its null 0.5926 [0.4901, 0.6876] OVERLAP across [0.4901, 0.5468]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1494 is not resolved by this sample
- `prequential-majority (1w)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.3878 [0.2646, 0.5272] and its null 0.6071 [0.4667, 0.7319] OVERLAP across [0.4667, 0.5272]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.2194 is not resolved by this sample

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 182 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 7,277 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 163 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 7,363 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 163 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 7,363 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 7,383 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=36, divisor=468, corrected_alpha=0.00010683760683760684.

**Survivorship:** epoch 2026-07-24; unmeasured — no graded post-epoch symbols; measured effect +0.40pp (active-only 83.56% minus survivorship-clean 83.16%, n=74,848 clean vs 18,014 active, revalidation of 2026-08-20T10:20:23+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
