# 2026-07-27 — Null transition complete: hindsight column dropped, verdicts graded against the prequential null only

## What changed

`tools/accuracy_registry.py` completed the dual-null transition it promised
one cycle ago:

- **Removed** the hindsight null (`max(base, 1-base)` over the finished
  window) from verdict logic, row output, the printed summary, and the JSON
  `null_policy`. No row publishes a `null_hindsight` column anymore.
- **Kept** the walk-forward prequential null as the only baseline: each day's
  constant guess is the majority class over days strictly *before* it (a coin
  flip on day one or a tied prior), graded through the same day-clustered
  interval machinery as the model it benchmarks.
- `null_method` on directional rows is now `prequential-majority
  (walk-forward)`; `null_acc` equals `null_prequential` by construction.

The transition-cycle design published both nulls side by side with the
stricter (higher) driving the verdict, precisely so this switch would be
attributable to the null definition alone — never to data drift. Because the
prequential null is ≤ the hindsight null by construction, verdicts could only
soften across the switch, never sharpen. The disclosure predicted **zero
verdict changes**. This regrade proves it.

## Regrade procedure

Both grades ran against the **committed reproducibility snapshot** (`repro/`,
manifest hashes verified before grading — a tampered snapshot refuses to
grade):

```
# before: dual-null code (verdict vs max(hindsight, prequential))
python3 tools/accuracy_registry.py --snapshot repro --json before.json
# after: prequential-only code (this change)
python3 tools/accuracy_registry.py --snapshot repro --json after.json
```

Snapshot identity: `repro/MANIFEST.json` generated 2026-07-27T01:06:37+00:00
at git commit `d14686bf6006835e94878762705b99024a366904`; manifest file
sha256 `00bbde71cc70412805d57dda342b63185cc5d4b4cc3320d4715c1279ec6452f6`.

## Before/after verdict diff — 0 changes across all 8 rows

| Predictor | Before verdict (dual-null) | After verdict (prequential-only) | Changed |
|---|---|---|---|
| directional-ensemble (1d) | INSUFFICIENT (18/30) | INSUFFICIENT (18/30) | no |
| directional-ensemble (1d, high conviction) | INSUFFICIENT (8/30) | INSUFFICIENT (8/30) | no |
| liquidity21 | PENDING (first grade 2026-08-14, 0/30 resolved) | PENDING (first grade 2026-08-14, 0/30 resolved) | no |
| liquidity21-crypto | PENDING (first grade 2026-08-14, 0/30 resolved) | PENDING (first grade 2026-08-14, 0/30 resolved) | no |
| trend21 | PENDING (first grade 2026-08-14, 0/30 resolved) | PENDING (first grade 2026-08-14, 0/30 resolved) | no |
| trend21-crypto | PENDING (first grade 2026-08-14, 0/30 resolved) | PENDING (first grade 2026-08-14, 0/30 resolved) | no |
| trend63 | PENDING (first grade 2026-09-25, 0/30 resolved) | PENDING (first grade 2026-09-25, 0/30 resolved) | no |
| vol21 | PENDING (first grade 2026-08-14, 0/30 resolved) | PENDING (first grade 2026-08-14, 0/30 resolved) | no |

What actually moved — the null values on the two directional rows, exactly as
designed (structural rows never carried a directional null):

| Predictor | null_hindsight (before) | null_prequential | null used before | null used after |
|---|---|---|---|---|
| directional-ensemble (1d) | 0.7778 | 0.5833 | 0.7778 (max) | 0.5833 (prequential) |
| directional-ensemble (1d, high conviction) | 0.7500 | 0.6250 | 0.7500 (max) | 0.6250 (prequential) |

Neither row currently reaches the verdict stage (both are below the
30-observation floor), so the stricter-vs-fairer null had no verdict to move
— and once these rows do graduate past the floor, every future verdict will
have been graded against the prequential null from the start of its record.

## Downstream consumers

`web/src/app/accuracy/page.tsx` and `ops/accuracy-registry.sh` both compute
the display null as the stricter of the published columns while filtering
nulls/absent values, so a missing `null_hindsight` degrades to
prequential-only automatically. No consumer change was required.

## Tests

`tools/test_accuracy_registry.py` updated: the end-to-end prequential test
now asserts `null_method == "prequential-majority (walk-forward)"`,
`null_acc == null_prequential`, and that **no** row (directional or
structural) publishes a `null_hindsight` key. Full suite: 29 tests, all
passing (`python3 -m unittest discover -s tools -p 'test_*.py'`).
