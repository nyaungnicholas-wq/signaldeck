<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-10-02T14:05:27) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,065 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 6 | withheld (SD-30) |
| prequential-majority (1d) | all | 2,065 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 6 | withheld (SD-30) |
| directional-ensemble (1w) | all | 281 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 1 | withheld (SD-30) |
| prequential-majority (1w) | all | 281 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 1 | withheld (SD-30) |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 140 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 4 | withheld (SD-30) |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 21 | withheld (SD-30) | withheld (SD-30) | withheld (SD-30) | 1 | withheld (SD-30) |
| filingsdrift21 | all | 197 | withheld — no null | — | — | 25 | withheld |
| liquidity21 | all | 11,875 | 69.7% | 70.6% | -0.9pp | 27 | withheld |
| liquidity21#persist | all | 11,587 | withheld — no null | — | — | 26 | withheld |
| liquidity21-crypto | all | 247 | 49.4% | 45.6% | +3.8pp | 37 | withheld |
| liquidity21-crypto#persist | all | 226 | withheld — no null | — | — | 34 | withheld |
| trend21 | all | 11,957 | 78.3% | 78.7% | -0.3pp | 27 | withheld |
| trend21#persist | all | 11,673 | withheld — no null | — | — | 26 | withheld |
| trend21-crypto | all | 247 | 52.2% | 48.7% | +3.6pp | 37 | withheld |
| trend21-crypto#persist | all | 226 | withheld — no null | — | — | 34 | withheld |
| vol21 | all | 11,971 | 49.2% | 47.5% | +1.7pp | 27 | withheld |
| vol21#persist | all | 11,680 | withheld — no null | — | — | 26 | withheld |

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

- `trend63` — registered claim 70.0%, 28,910 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=74, divisor=1332, corrected_alpha=3.753753753753754e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.55pp (active-only 83.59% minus survivorship-clean 83.04%, n=76,506 clean vs 18,258 active, revalidation of 2026-10-01T10:20:22+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
