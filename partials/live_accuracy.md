<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-10-21T17:33:35) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,423 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 18 | withheld (SD-30) |
| prequential-majority (1d) | all | 1,792 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 16 | withheld (SD-30) |
| directional-ensemble (1w) | all | 2,279 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 12 | withheld (SD-30) |
| prequential-majority (1w) | all | 365 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 4 | withheld (SD-30) |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 724 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 12 | withheld (SD-30) |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 1,334 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 11 | withheld (SD-30) |
| filingsdrift21 | all | 210 | withheld — no null | — | — | 33 | withheld |
| liquidity21 | all | 21,483 | 70.2% | 70.8% | -0.6pp | 40 | withheld |
| liquidity21#persist | all | 21,195 | withheld — no null | — | — | 39 | withheld |
| liquidity21-crypto | all | 366 | 65.0% | 64.1% | +1.0pp | 54 | withheld |
| liquidity21-crypto#persist | all | 345 | withheld — no null | — | — | 51 | withheld |
| trend21 | all | 21,649 | 79.6% | 79.8% | -0.2pp | 40 | withheld |
| trend21#persist | all | 21,365 | withheld — no null | — | — | 39 | withheld |
| trend21-crypto | all | 366 | 62.3% | 60.9% | +1.4pp | 54 | withheld |
| trend21-crypto#persist | all | 345 | withheld — no null | — | — | 51 | withheld |
| vol21 | all | 21,691 | 53.2% | 53.0% | +0.2pp | 40 | withheld |
| vol21#persist | all | 21,400 | withheld — no null | — | — | 39 | withheld |

**Directional 1d/1w figures withheld: label partly realised at issue (SD-30); a corrected label grades from 2026-10-04 and figures return only after that window clears its evidence floors.** These rows are scored against a label that is mostly realised when the call is issued, so their accuracy, null, skill, interval and verdict are not published; n and distinct days describe the sample only.

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `filingsdrift21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21` — INSUFFICIENT BLOCKS (3/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `liquidity21-crypto` — INSUFFICIENT BLOCKS (3/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21` — INSUFFICIENT BLOCKS (3/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21-crypto` — INSUFFICIENT BLOCKS (3/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `vol21` — INSUFFICIENT BLOCKS (4/10 non-overlapping horizon blocks) — no interval, so no verdict
- `vol21#persist` — BENCHMARK — the frozen naive-persistence null itself

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 38,016 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=94, divisor=1692, corrected_alpha=2.955082742316785e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.55pp (active-only 83.59% minus survivorship-clean 83.04%, n=76,506 clean vs 18,258 active, revalidation of 2026-10-01T10:20:22+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
