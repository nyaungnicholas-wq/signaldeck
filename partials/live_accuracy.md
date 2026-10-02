<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-10-01T14:05:18) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 1,052 | 50.5% | 59.3% | -8.8pp | 5 | withheld |
| prequential-majority (1d) | all | 1,052 | 64.6% | 59.3% | +5.3pp | 5 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 95 | 46.3% | 62.6% | -16.3pp | 3 | withheld |
| filingsdrift21 | all | 195 | withheld — no null | — | — | 24 | withheld |
| liquidity21 | all | 11,874 | 69.7% | 70.6% | -0.9pp | 27 | withheld |
| liquidity21#persist | all | 11,586 | withheld — no null | — | — | 26 | withheld |
| liquidity21-crypto | all | 240 | 49.2% | 45.2% | +4.0pp | 36 | withheld |
| liquidity21-crypto#persist | all | 219 | withheld — no null | — | — | 33 | withheld |
| trend21 | all | 11,954 | 78.3% | 78.6% | -0.3pp | 27 | withheld |
| trend21#persist | all | 11,670 | withheld — no null | — | — | 26 | withheld |
| trend21-crypto | all | 240 | 51.7% | 47.9% | +3.7pp | 36 | withheld |
| trend21-crypto#persist | all | 219 | withheld — no null | — | — | 33 | withheld |
| vol21 | all | 11,966 | 49.2% | 47.5% | +1.7pp | 27 | withheld |
| vol21#persist | all | 11,675 | withheld — no null | — | — | 26 | withheld |

Intervals are withheld this cycle, so **no pass/fail verdict is published from them**. A point estimate without an interval is not a result; treat every row above as a running tally.

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — INSUFFICIENT DAYS (3/10 credible days of 5, 2 degenerate) — no interval, so no verdict
- `prequential-majority (1d)` — INSUFFICIENT DAYS (3/10 credible days of 5, 2 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (3/10 credible days of 3) — no interval, so no verdict
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

- `trend63` — registered claim 70.0%, 28,627 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=73, divisor=1314, corrected_alpha=3.805175038051751e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.55pp (active-only 83.59% minus survivorship-clean 83.04%, n=76,506 clean vs 18,258 active, revalidation of 2026-10-01T10:20:22+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
