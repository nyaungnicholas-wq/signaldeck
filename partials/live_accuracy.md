<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-09-30T18:04:11) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 603 | 52.2% | 51.5% | +0.7pp | 19 | [42.3%, 62.0%] |
| prequential-majority (1d) | all | 603 | 47.3% | 51.5% | -4.2pp | 19 | [36.6%, 58.2%] |
| directional-ensemble (1w) | all | 7,137 | 48.3% | 55.2% | -7.0pp | 42 | withheld |
| prequential-majority (1w) | all | 7,126 | 43.9% | 55.2% | -11.3pp | 42 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 1 | 0.0% | 50.0% | -50.0pp | 1 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 1,110 | 48.6% | 60.6% | -12.1pp | 37 | withheld |
| filingsdrift21 | all | 191 | withheld — no null | — | — | 23 | withheld |
| liquidity21 | all | 10,756 | 69.5% | 70.4% | -0.9pp | 25 | withheld |
| liquidity21#persist | all | 10,468 | withheld — no null | — | — | 24 | withheld |
| liquidity21-crypto | all | 226 | 49.6% | 44.4% | +5.2pp | 34 | withheld |
| liquidity21-crypto#persist | all | 205 | withheld — no null | — | — | 31 | withheld |
| trend21 | all | 10,823 | 78.4% | 78.7% | -0.3pp | 25 | withheld |
| trend21#persist | all | 10,539 | withheld — no null | — | — | 24 | withheld |
| trend21-crypto | all | 226 | 50.4% | 46.3% | +4.1pp | 34 | withheld |
| trend21-crypto#persist | all | 205 | withheld — no null | — | — | 31 | withheld |
| vol21 | all | 10,837 | 48.9% | 46.6% | +2.2pp | 24 | withheld |
| vol21#persist | all | 10,546 | withheld — no null | — | — | 23 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — NO SKILL — indistinguishable from baseline
  - **verdict not supported by its own interval** — accuracy 0.5224 [0.4229, 0.6201] and its null 0.5149 [0.4097, 0.6189] OVERLAP across [0.4229, 0.6189]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of +0.0075 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (7/10 credible days of 42, 35 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (7/10 credible days of 42, 35 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT (1/30)
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (7/10 credible days of 37, 30 degenerate) — no interval, so no verdict
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

- `trend63` — registered claim 70.0%, 26,927 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=70, divisor=1260, corrected_alpha=3.968253968253968e-05.

**Survivorship:** epoch 2026-07-24; measured effect +0.50pp (active-only 83.61% minus survivorship-clean 83.10%, n=75,654 clean vs 18,007 active, revalidation of 2026-09-01T10:20:09+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->
