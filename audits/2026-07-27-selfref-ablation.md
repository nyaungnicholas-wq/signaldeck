# Audit note — self-reference ablation (closes reaudit finding A7)

**Date:** 2026-07-27 (runs at 00:34 and 00:37 UTC)
**Finding closed:** A7 from [2026-07-26-reaudit.md](2026-07-26-reaudit.md) — four
model-output / label-derived feature keys reached model-leg training sets
(double-counting / shortcut learning; point-in-time was preserved, so not look-ahead).
**Claim this note makes citable:** removing the shortcut features changed **zero**
admission-gate decisions — the (absence of) edge was never the shortcut.

## Methodology

**Keys ablated** (exactly the four A7 removed via `gbm.SelfReferentialKey`):

| key | nature | live rows at audit time |
|---|---|---|
| `forecast_prob` | walk-forward logistic leg's own output | 228,596 |
| `forecast_lift` | label-derived accuracy statistic of that leg | 228,596 |
| `expectancy_hit_rate` | expectancy leg's output | 235,441 |
| `n_used` | encodes which legs cleared their gates | 244,752 |

**Harness:** `daemon/cmd/selfref-ablation`. It retrains the feature-store model
legs twice on ONE frozen snapshot — a `VACUUM INTO` copy of the live DB taken
from a read-only connection — so both arms consume byte-identical inputs:

- **without (shipped):** the post-A7 layout, four keys excluded by
  `gbm.SelfReferentialKey`.
- **with (pre-A7):** the four keys restored by renaming (`k` → `k__preA7`) in a
  copy of each row's vector, so they bypass the shared exclusion predicate while
  production code stays untouched. The arms therefore differ in nothing but the
  availability of those four features (and their derived `__has` presence bits).

Legs covered: the per-symbol GBM leg (mirrors `pipeline.GBMTrainer`:
version-pinned rows, 5,000-row cap, `WithLabelSpan` purge, 5 folds,
`gbm.Defaults`) and the pooled cross-sectional alphax leg (mirrors
`pipeline.AlphaXTrainer`: v3+ pool, 50k cap, day-purged Evaluate with the
horizon-aware embargo). The mean-reversion and linear forecast legs never read
the feature store through the exclusion predicate, so the ablation cannot move
them by construction.

**Reproduce:** `cd daemon && go run ./cmd/selfref-ablation -db ../data/signaldeck.db -snapshot /tmp/ablate.db`.
Per-dataset row hashes (sha256 over `symbol|ts|up|fwd_return` of the exact
labeled rows consumed) pin the inputs; training is deterministic given them.

## Result

Raw outputs: [2026-07-27-selfref-ablation-full.txt](2026-07-27-selfref-ablation-full.txt)
(all legs) and [2026-07-27-selfref-ablation-perleg.txt](2026-07-27-selfref-ablation-perleg.txt)
(per-symbol breakdown).

### GBM leg · featureVersion 10 · 1d — the live admission-gate population

27,490 labeled rows, dataset hash `686c087ad63207cd`; 37 legs graded per arm,
OOS N = 10,057 per arm:

| arm | graded | admitted (lift>0) | mean lift | median lift | N-weighted lift |
|---|---|---|---|---|---|
| with shortcuts (pre-A7) | 37 | **2** (5.4%) | −0.3737 | −0.3383 | −0.3725 |
| without (shipped) | 37 | **2** (5.4%) | −0.3998 | −0.4737 | −0.3987 |

Paired across the 37 legs, mean Δlift (with − without) = **+0.0261**: the
shortcut features flattered mean OOS lift by ~0.026, but per the per-leg table
**0 of 37 legs flipped the lift>0 admission gate** in either direction. The two
admitted legs (WULF +0.0076, BTC/USD +0.0069) have Δ = 0.0000 exactly.

### Pooled alphax leg · v3+ · 1d

50,000 pooled rows, dataset hash `ed439e23dbeb02ab`, N = 3,807:

| arm | OOS lift | accuracy | base rate | AUC | gate |
|---|---|---|---|---|---|
| with shortcuts (pre-A7) | −0.0045 | 0.4957 | 0.5001 | 0.4947 | GATED OUT |
| without (shipped) | −0.0084 | 0.4917 | 0.5001 | 0.4909 | GATED OUT |

Δlift = +0.0039 — shortcuts flattered pooled lift slightly; still below zero,
gate unchanged.

### Legs refused identically in both arms

GBM v8 (1d and 1w), GBM v10 1w, and the alphax 1w pool were refused by the
grader in BOTH arms (too little per-symbol / per-day history for purged
walk-forward folds), matching production, where no GBM 1w legs exist in
`model_forecasts`.

## Provenance

- Full run (00:34 UTC): snapshot sha256
  `7e5c205ee295ee274dc146ef18f0dc57c4bda423cb4b78a7aa05dfea9c8ae635`.
- Per-leg run (00:37 UTC): reused the snapshot file; it prints sha256
  `247c9d1339990575184304d060f03941628c02e60f5ad9aa794efe2900de83b2` (the file's
  bytes changed between runs — SQLite header/WAL touch on open). The
  **per-dataset row hashes are identical across both runs**
  (`686c087ad63207cd`, `ed439e23dbeb02ab`, etc.), which is the binding input pin.
- Regression guard: the census test in `daemon/internal/gbm/selfref_test.go`
  fails the suite the day any newly-logged feature key is not explicitly
  classified as observation or model output.

## Conclusion

A7's removal of the four self-referential keys was made on principle; this
ablation measures what it cost. The shortcut features inflated measured OOS
lift slightly (+0.026 mean on the GBM v10 1d population, +0.004 pooled alphax)
but changed no admission decision anywhere: every leg admitted without them is
the same leg, at approximately the same lift, admitted with them, and every
gated-out or refused leg stays gated out or refused. **The shortcut features
were not the signal**, and the shipped post-A7 configuration loses nothing that
the admission gate would have accepted.
