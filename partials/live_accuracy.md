<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-04T20:34:19) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (research-loop liveness check failed (exit 1) — a narrated grid search left no verifiable judgment record, or a pre-registered forecast kind has never frozen a forecast and gave no refusal; the grader was not run), and the last successful grade is 17.5h old. The numbers below are that last successful grade, taken at 2026-08-04T20:34:19. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,911 | 43.1% | 56.8% | -13.7pp | 10 | [31.1%, 55.9%] |
| prequential-majority (1d) | all | 2,298 | 59.8% | 56.6% | +3.2pp | 7 | withheld |
| directional-ensemble (1w) | all | 938 | 45.6% | 52.2% | -6.6pp | 5 | withheld |
| prequential-majority (1w) | all | 332 | 57.2% | 49.8% | +7.4pp | 2 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 300 | 55.3% | 60.0% | -4.7pp | 6 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 62 | 45.2% | 44.4% | +0.8pp | 4 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1d)` — INSUFFICIENT DAYS (7/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (5/10 distinct days) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (2/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (6/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (4/10 distinct days) — no interval, so no verdict

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 77 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 3,624 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 66 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 3,649 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 66 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 3,649 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 3,665 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=9, divisor=117, corrected_alpha=0.00042735042735042735.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 326/335 graded symbols (97.3%): 9 inactive symbol(s) with no delisted_at; measured effect +0.53pp (active-only 83.58% minus survivorship-clean 83.05%, n=57,490 clean vs 17,876 active, revalidation of 2026-08-05T03:27:32+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
