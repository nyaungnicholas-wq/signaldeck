<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-05T21:23:28) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 3,002 | 44.3% | 59.7% | -15.5pp | 11 | [33.4%, 55.7%] |
| prequential-majority (1d) | all | 2,302 | 60.5% | 57.3% | +3.2pp | 8 | withheld |
| directional-ensemble (1w) | all | 1,342 | 42.0% | 57.0% | -14.9pp | 6 | withheld |
| prequential-majority (1w) | all | 653 | 62.0% | 58.3% | +3.7pp | 3 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 318 | 51.3% | 59.0% | -7.7pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 83 | 37.3% | 46.4% | -9.0pp | 6 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1d)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (6/10 distinct days) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (3/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (6/10 distinct days) — no interval, so no verdict

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 98 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 4,192 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 80 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 4,217 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 80 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 4,217 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 4,234 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=12, divisor=156, corrected_alpha=0.0003205128205128205.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 326/335 graded symbols (97.3%): 9 inactive symbol(s) with no delisted_at; measured effect +0.36pp (active-only 83.58% minus survivorship-clean 83.22%, n=74,513 clean vs 17,876 active, revalidation of 2026-08-05T23:37:08+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
