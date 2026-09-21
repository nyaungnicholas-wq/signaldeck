<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-09-20T23:50:57) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 603 | 52.2% | 51.5% | +0.7pp | 19 | [42.4%, 61.9%] |
| prequential-majority (1d) | all | 603 | 47.3% | 51.5% | -4.2pp | 19 | [36.7%, 58.1%] |
| directional-ensemble (1w) | all | 4,515 | 49.7% | 53.4% | -3.8pp | 33 | withheld |
| prequential-majority (1w) | all | 4,515 | 45.1% | 53.4% | -8.3pp | 33 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 1 | 0.0% | 50.0% | -50.0pp | 1 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 798 | 51.1% | 59.8% | -8.6pp | 30 | withheld |
| filingsdrift21 | all | 177 | withheld — no null | — | — | 18 | withheld |
| liquidity21 | all | 6,963 | 68.5% | 68.8% | -0.3pp | 18 | withheld |
| liquidity21#persist | all | 6,675 | withheld — no null | — | — | 17 | withheld |
| liquidity21-crypto | all | 170 | 52.9% | 46.3% | +6.6pp | 26 | withheld |
| liquidity21-crypto#persist | all | 149 | withheld — no null | — | — | 23 | withheld |
| trend21 | all | 7,003 | 78.7% | 78.9% | -0.2pp | 18 | withheld |
| trend21#persist | all | 6,719 | withheld — no null | — | — | 17 | withheld |
| trend21-crypto | all | 170 | 38.8% | 31.5% | +7.3pp | 26 | withheld |
| trend21-crypto#persist | all | 149 | withheld — no null | — | — | 23 | withheld |
| vol21 | all | 7,026 | 48.3% | 46.9% | +1.3pp | 17 | withheld |
| vol21#persist | all | 6,735 | withheld — no null | — | — | 16 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — NO SKILL — indistinguishable from baseline
  - **verdict not supported by its own interval** — accuracy 0.5224 [0.4236, 0.6194] and its null 0.5149 [0.4105, 0.6181] OVERLAP across [0.4236, 0.6181]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of +0.0075 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (6/10 credible days of 33, 27 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (6/10 credible days of 33, 27 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT (1/30)
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (6/10 credible days of 30, 24 degenerate) — no interval, so no verdict
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

- `trend63` — registered claim 70.0%, 20,933 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=61, divisor=1098, corrected_alpha=4.553734061930783e-05.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 2221/2943 graded symbols (75.5%): 722 inactive symbol(s) with no delisted_at; measured effect +0.50pp (active-only 83.61% minus survivorship-clean 83.10%, n=75,654 clean vs 18,007 active, revalidation of 2026-09-01T10:20:09+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
