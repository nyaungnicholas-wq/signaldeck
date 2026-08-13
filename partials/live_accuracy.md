<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-12T21:33:12) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,380 | 42.9% | 56.9% | -13.9pp | 14 | [33.0%, 53.5%] |
| prequential-majority (1d) | all | 2,051 | 58.1% | 55.7% | +2.4pp | 12 | withheld |
| directional-ensemble (1w) | all | 2,256 | 38.8% | 62.6% | -23.8pp | 11 | withheld |
| prequential-majority (1w) | all | 1,895 | 65.3% | 63.9% | +1.4pp | 9 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 454 | 45.2% | 48.6% | -3.4pp | 7 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 344 | 34.9% | 62.9% | -28.1pp | 9 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1d)` — INSUFFICIENT DAYS (9/10 credible days of 12, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (7/10 credible days of 11, 4 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (6/10 credible days of 9, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 7, 2 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (9/10 credible days of 9) — no interval, so no verdict

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 162 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 4,751 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 114 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 4,797 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 114 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 4,797 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 4,816 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=27, divisor=351, corrected_alpha=0.00014245014245014247.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 325/336 graded symbols (96.7%): 11 inactive symbol(s) with no delisted_at; measured effect +0.31pp (active-only 83.58% minus survivorship-clean 83.27%, n=75,118 clean vs 17,860 active, revalidation of 2026-08-12T10:20:18+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
