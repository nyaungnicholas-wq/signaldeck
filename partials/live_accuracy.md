<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-09T14:05:15) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 1,997 | 43.0% | 53.4% | -10.3pp | 14 | [33.6%, 53.0%] |
| prequential-majority (1d) | all | 1,665 | 55.6% | 52.6% | +3.0pp | 11 | [33.6%, 75.5%] |
| directional-ensemble (1w) | all | 1,597 | 40.6% | 60.1% | -19.5pp | 8 | withheld |
| prequential-majority (1w) | all | 926 | 65.7% | 62.7% | +2.9pp | 5 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 451 | 44.6% | 46.5% | -1.9pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 117 | 38.5% | 50.0% | -11.5pp | 7 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (5/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (7/10 distinct days) — no interval, so no verdict

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 142 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 3,565 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 86 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 3,596 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 86 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 3,596 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 3,608 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=18, divisor=234, corrected_alpha=0.00021367521367521368.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 327/336 graded symbols (97.3%): 9 inactive symbol(s) with no delisted_at; measured effect +0.24pp (active-only 83.51% minus survivorship-clean 83.27%, n=75,062 clean vs 17,879 active, revalidation of 2026-08-09T10:20:22+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
