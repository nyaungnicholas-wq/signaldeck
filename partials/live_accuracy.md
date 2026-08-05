<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-03T23:06:30) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (grader exited 1), and the last successful grade is 15.0h old. The numbers below are that last successful grade, taken at 2026-08-03T23:06:30. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,257 | 46.3% | 52.9% | -6.6pp | 9 | withheld |
| prequential-majority (1d) | all | 1,644 | 55.5% | 51.1% | +4.4pp | 6 | withheld |
| directional-ensemble (1w) | all | 912 | 47.8% | 50.2% | -2.4pp | 4 | withheld |
| prequential-majority (1w) | all | 7 | 28.6% | 50.0% | -21.4pp | 1 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 297 | 55.2% | 60.3% | -5.1pp | 5 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 63 | 47.6% | 46.8% | +0.8pp | 4 | withheld |

Intervals are withheld this cycle, so **no pass/fail verdict is published from them**. A point estimate without an interval is not a result; treat every row above as a running tally.

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `prequential-majority (1d)` — INSUFFICIENT DAYS (6/10 distinct days) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT (7/30)

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 67 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 2,903 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 60 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 2,918 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 60 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 2,917 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 2,928 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=8, divisor=104, corrected_alpha=0.0004807692307692308.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 328/335 graded symbols (97.9%): 7 inactive symbol(s) with no delisted_at.

<!-- END GENERATED live_accuracy -->
