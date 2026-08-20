<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-19T14:05:46) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,538 | 43.9% | 55.9% | -12.0pp | 18 | [33.9%, 54.5%] |
| prequential-majority (1d) | all | 2,209 | 56.9% | 54.7% | +2.2pp | 16 | [37.3%, 74.6%] |
| directional-ensemble (1w) | all | 3,572 | 43.3% | 61.9% | -18.6pp | 17 | [31.9%, 55.5%] |
| prequential-majority (1w) | all | 3,211 | 63.4% | 62.6% | +0.8pp | 15 | [54.7%, 71.3%] |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 454 | 45.2% | 48.6% | -3.4pp | 7 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 420 | 37.9% | 61.3% | -23.5pp | 14 | [25.0%, 52.7%] |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4393 [0.3391, 0.5447] and its null 0.5589 [0.3831, 0.7211] OVERLAP across [0.3831, 0.5447]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1196 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4331 [0.3191, 0.5546] and its null 0.6190 [0.5264, 0.7037] OVERLAP across [0.5264, 0.5546]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1859 is not resolved by this sample
- `prequential-majority (1w)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 7, 2 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.3786 [0.2498, 0.5271] and its null 0.6131 [0.4582, 0.7480] OVERLAP across [0.4582, 0.5271]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.2345 is not resolved by this sample

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 181 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 6,389 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 156 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 6,460 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 156 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 6,460 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 6,480 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=35, divisor=455, corrected_alpha=0.00010989010989010989.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 325/383 graded symbols (84.9%): 58 inactive symbol(s) with no delisted_at; measured effect +0.27pp (active-only 83.58% minus survivorship-clean 83.31%, n=75,821 clean vs 18,090 active, revalidation of 2026-08-19T10:20:18+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
