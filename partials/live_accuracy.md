<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-10-01T02:20:59) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 667 | 52.8% | 57.4% | -4.6pp | 5 | withheld |
| prequential-majority (1d) | all | 667 | 65.8% | 57.4% | +8.4pp | 5 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 54 | 42.6% | 60.2% | -17.6pp | 2 | withheld |
| filingsdrift21 | all | 195 | withheld — no null | — | — | 24 | withheld |
| liquidity21 | all | 11,032 | 69.6% | 70.5% | -0.9pp | 26 | withheld |
| liquidity21#persist | all | 10,744 | withheld — no null | — | — | 25 | withheld |
| liquidity21-crypto | all | 233 | 49.4% | 44.8% | +4.5pp | 35 | withheld |
| liquidity21-crypto#persist | all | 212 | withheld — no null | — | — | 32 | withheld |
| trend21 | all | 11,103 | 78.4% | 78.8% | -0.4pp | 26 | withheld |
| trend21#persist | all | 10,819 | withheld — no null | — | — | 25 | withheld |
| trend21-crypto | all | 233 | 51.1% | 47.2% | +3.9pp | 35 | withheld |
| trend21-crypto#persist | all | 212 | withheld — no null | — | — | 32 | withheld |
| vol21 | all | 11,116 | 49.0% | 47.2% | +1.8pp | 25 | withheld |
| vol21#persist | all | 10,825 | withheld — no null | — | — | 24 | withheld |

Intervals are withheld this cycle, so **no pass/fail verdict is published from them**. A point estimate without an interval is not a result; treat every row above as a running tally.

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — INSUFFICIENT DAYS (5/10 credible days of 5) — no interval, so no verdict
- `prequential-majority (1d)` — INSUFFICIENT DAYS (5/10 credible days of 5) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (2/10 credible days of 2) — no interval, so no verdict
- `filingsdrift21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `liquidity21-crypto` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21-crypto` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `vol21` — INSUFFICIENT BLOCKS (2/10 non-overlapping horizon blocks) — no interval, so no verdict
- `vol21#persist` — BENCHMARK — the frozen naive-persistence null itself

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 27,776 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=72, divisor=1296, corrected_alpha=3.8580246913580246e-05.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 323/324 graded symbols (99.7%): 1 inactive symbol(s) with no delisted_at; measured effect +0.50pp (active-only 83.61% minus survivorship-clean 83.10%, n=75,654 clean vs 18,007 active, revalidation of 2026-09-01T10:20:09+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
