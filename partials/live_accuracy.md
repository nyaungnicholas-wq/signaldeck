<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-18T18:25:41) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,518 | 43.7% | 56.1% | -12.4pp | 17 | [33.7%, 54.3%] |
| prequential-majority (1d) | all | 2,189 | 57.1% | 54.9% | +2.3pp | 15 | [37.4%, 74.9%] |
| directional-ensemble (1w) | all | 3,249 | 42.3% | 62.4% | -20.1pp | 16 | [30.5%, 55.0%] |
| prequential-majority (1w) | all | 2,888 | 64.1% | 63.2% | +0.9pp | 14 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 454 | 45.2% | 48.6% | -3.4pp | 7 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 374 | 35.8% | 62.7% | -26.9pp | 13 | [26.0%, 47.0%] |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4373 [0.3372, 0.5427] and its null 0.5610 [0.3842, 0.7235] OVERLAP across [0.3842, 0.5427]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1237 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4229 [0.3050, 0.5503] and its null 0.6236 [0.5233, 0.7143] OVERLAP across [0.5233, 0.5503]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.2007 is not resolved by this sample
- `prequential-majority (1w)` — INSUFFICIENT DAYS (9/10 credible days of 14, 5 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 7, 2 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — FAILED — significantly worse than the naive baseline

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 177 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 5,935 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 149 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 5,997 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 149 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 5,997 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 6,019 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=34, divisor=442, corrected_alpha=0.00011312217194570136.

**Survivorship:** epoch 2026-07-24; unmeasured — no graded post-epoch symbols; measured effect +0.36pp (active-only 83.67% minus survivorship-clean 83.31%, n=75,792 clean vs 18,084 active, revalidation of 2026-08-18T10:20:23+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
