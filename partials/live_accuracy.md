<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-12T17:53:20) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,376 | 41.7% | 55.1% | -13.4pp | 16 | [33.7%, 50.2%] |
| prequential-majority (1d) | all | 2,044 | 57.2% | 54.8% | +2.4pp | 13 | [40.0%, 72.8%] |
| directional-ensemble (1w) | all | 2,873 | 36.5% | 65.1% | -28.6pp | 11 | [29.0%, 44.7%] |
| prequential-majority (1w) | all | 2,202 | 68.9% | 67.7% | +1.2pp | 8 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 452 | 44.7% | 46.6% | -1.9pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 471 | 38.0% | 65.7% | -27.7pp | 10 | [28.4%, 48.6%] |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1w)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — FAILED — significantly worse than the naive baseline

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 162 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 4,146 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 107 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 4,178 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 107 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 4,178 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 4,192 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=21, divisor=273, corrected_alpha=0.00018315018315018315.

**Survivorship:** epoch 2026-07-24; unmeasured — no graded post-epoch symbols; measured effect +0.31pp (active-only 83.58% minus survivorship-clean 83.27%, n=75,118 clean vs 17,860 active, revalidation of 2026-08-12T10:20:18+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
