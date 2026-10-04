<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-10-03T14:05:18) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,065 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 6 | withheld (SD-30) |
| prequential-majority (1d) | all | 2,065 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 6 | withheld (SD-30) |
| directional-ensemble (1w) | all | 288 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 2 | withheld (SD-30) |
| prequential-majority (1w) | all | 288 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 2 | withheld (SD-30) |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 140 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 4 | withheld (SD-30) |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 21 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 1 | withheld (SD-30) |
| filingsdrift21 | all | 199 | withheld — no null | — | — | 26 | withheld |
| liquidity21 | all | 12,151 | 69.8% | 70.7% | -0.9pp | 28 | withheld |
| liquidity21#persist | all | 11,863 | withheld — no null | — | — | 27 | withheld |
| liquidity21-crypto | all | 254 | 50.0% | 46.8% | +3.2pp | 38 | withheld |
| liquidity21-crypto#persist | all | 233 | withheld — no null | — | — | 35 | withheld |
| trend21 | all | 12,239 | 78.3% | 78.6% | -0.3pp | 28 | withheld |
| trend21#persist | all | 11,955 | withheld — no null | — | — | 27 | withheld |
| trend21-crypto | all | 254 | 52.8% | 49.4% | +3.4pp | 38 | withheld |
| trend21-crypto#persist | all | 233 | withheld — no null | — | — | 35 | withheld |
| vol21 | all | 12,252 | 49.5% | 48.0% | +1.4pp | 28 | withheld |
| vol21#persist | all | 11,961 | withheld — no null | — | — | 27 | withheld |

**Directional 1d/1w figures withheld: label partly realised at issue (SD-30); a corrected label is pending a preregistration decision.** These rows are scored against a label that is mostly realised when the call is issued, so their accuracy, null, skill, interval and verdict are not published; n and distinct days describe the sample only.

Intervals are withheld this cycle, so **no pass/fail verdict is published from them**. A point estimate without an interval is not a result; treat every row above as a running tally.

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `filingsdrift21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `liquidity21-crypto` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21-crypto` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `vol21` — INSUFFICIENT BLOCKS (3/10 non-overlapping horizon blocks) — no interval, so no verdict
- `vol21#persist` — BENCHMARK — the frozen naive-persistence null itself

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 28,914 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=75, divisor=1350, corrected_alpha=3.7037037037037037e-05.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 324/1015 graded symbols (31.9%): 691 inactive symbol(s) with no delisted_at; measured effect +0.55pp (active-only 83.59% minus survivorship-clean 83.04%, n=76,506 clean vs 18,258 active, revalidation of 2026-10-01T10:20:22+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
