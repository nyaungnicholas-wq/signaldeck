<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-15T17:08:35) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,479 | 43.4% | 56.4% | -13.0pp | 16 | [33.5%, 53.9%] |
| prequential-majority (1d) | all | 2,150 | 57.5% | 55.2% | +2.3pp | 14 | [37.5%, 75.3%] |
| directional-ensemble (1w) | all | 2,911 | 41.4% | 62.7% | -21.3pp | 14 | withheld |
| prequential-majority (1w) | all | 2,544 | 64.9% | 63.8% | +1.1pp | 11 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 454 | 45.2% | 48.6% | -3.4pp | 7 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 352 | 35.2% | 62.9% | -27.7pp | 11 | [26.1%, 45.6%] |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4340 [0.3346, 0.5391] and its null 0.5637 [0.3857, 0.7267] OVERLAP across [0.3857, 0.5391]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1297 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (9/10 credible days of 14, 5 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (8/10 credible days of 11, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 7, 2 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — FAILED — significantly worse than the naive baseline

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 168 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 5,485 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 128 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 5,538 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 128 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 5,538 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 5,558 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=31, divisor=403, corrected_alpha=0.00012406947890818859.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 325/378 graded symbols (86.0%): 53 inactive symbol(s) with no delisted_at; measured effect +0.27pp (active-only 83.58% minus survivorship-clean 83.31%, n=75,733 clean vs 18,083 active, revalidation of 2026-08-16T10:20:21+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
