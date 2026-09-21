# SignalDeck — Honest Plan to a Real Edge

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** AUTHORITATIVE — freeze lifted 2026-08-04
> **Scope:** Plan and ablation record for reaching a measurable edge.
> **Frozen claim classes:** FC3 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, statuses in `proofs/P10_FREEZE_LIFT.md` §4
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE — caveats are the frozen classes above

> **Backtest data: `pre-survivorship-fix`.** Every historical figure below was
> computed on the universe as it stood BEFORE the 2026-08-04 survivorship
> backfill (`proofs/P3A_SURVIVORSHIP_BACKFILL.md`) and the point-in-time
> universe rebuild (`proofs/P3B_PIT_UNIVERSE.md`). It has not been re-run on
> the repaired universe. Read the numbers as a record of what was measured
> then, not as what the repaired data would produce now.

> ## ⚠ P0 freeze (2026-08-04) — LIFTED 2026-08-04 by `proofs/P10_FREEZE_LIFT.md`
> Remediation complete; freeze lifted. **Do not attach capital.** Figures below are historical unless generated.
> Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **live accuracy · intervals · survivorship · point-in-time data.**
> The 2026-07-27 ablation tables are computed over a bar history measured
> survivor-seeded in `ALPHA_WORKFLOW.md` §B2 and are frozen pending P3.
> Authority: `proofs/P0_FREEZE.md`, **lifted 2026-08-04** by `proofs/P10_FREEZE_LIFT.md`.

## The target, reset to reality
- **80% directional win rate is not achievable by anyone.** World-class quant funds run 52–56% and get rich on it via leverage + scale + risk management.
- The number that matters is **cost-adjusted EXPECTANCY** (win% × avg win − loss% × avg loss − fees/slippage/taxes), not win rate. You can hit 80% win rate with negative expectancy (win +1% often, lose −10% rarely) — that loses money.
- **A GOOD, honest destination:** 53–56% directional accuracy on longer horizons, OR a rare high-conviction alert stream (~2×/month) at 58–65% hit rate with *positive* cost-adjusted expectancy. That is genuinely valuable. 80% is not on the menu — for us or TradingView.

## "Can't you just copy other people / TradingView's AI?"
- **As FEATURES behind the OOS gate: yes, and we already do some** (TradingView reco, insider Form 4, short-volume, put/call, COT are ingested; tv_reco + insider + short-vol now feed the model). That is how Danelfin/Zacks work.
- **As gospel / mirroring TV's AI signals: no.** Public technical signals are the *most* arbitraged — if they worked, the edge is gone by the time you see it. TradingView "AI" indicators are curve-fit backtests advertising overfit win rates; copying them inherits their *negative live* edge (the same trap we just fixed here). Copying overfitting is not edge.
- **The honest version of "copy others":** aggregate MANY weak external signals (analyst estimate revisions = the #1 documented anomaly, insider clusters, options flow, short dynamics) as inputs to a model validated out-of-sample — never trust any single one.

## The plan (phased, honest)

### Phase 0 — Reframe the target (FREE, do first)
- Stop predicting 1-day direction (it's ~efficient → 51% base rate, we score 48%). Predict what's actually forecastable:
  - **Cross-sectional relative rank** (which names beat peers) — `alphax` already does this and is now ungated (small +lift).
  - **5–20 day horizon** (momentum + estimate revisions have persistent, documented edge).
  - **Volatility / regime** (genuinely predictable — you *can* be honestly calibrated on "will realized vol exceed baseline?").
- Change the headline metric everywhere from "win rate" to **edge-vs-naive-baseline** (already done) + **cost-adjusted expectancy**.

### Phase 1 — Better data (RE-ASSESSED 2026-09-20: $0, not a lever)
- Historical daily bars are already full-consolidated SIP on Alpaca's free tier; the graded forecast tier runs on complete data.
- Algo Trader Plus ($99/mo) purchases only the real-time 16-minute SIP tail, which a separate IEX poller covers and the next deep pass heals; this is deliberately not taken.
- Tiingo was never integrated (no code, no config) and is dropped; fundamentals come from public EDGAR filings.

### Phase 2 — Real alpha features, OOS-gated (FREE, partly done)
- Add estimate-revision + surprise, insider clusters, options put/call + unusual flow, macro/credit/vol regime conditioning as features. The gate (lift>0 walk-forward) decides if any earns a live output. Never trust one; ensemble many.

### Phase 3 — Product: make the prediction tab LOW-FREQUENCY + high-conviction
- Instead of a daily direction % on everything, fire a call ONLY when multiple *proven* signals align AND risk/reward is favorable. Rare (2×/month), higher hit rate, positive expectancy. This is the ONLY honest path to a "high win rate" number.
- Alerts fire on **verifiable facts** (insider buy, short-vol 3σ, regime flip), scored forever — not on the 51% model.

### Phase 4 — Validate honestly (the guardrail)
- Walk-forward, cost-adjusted, N in the hundreds, pre-registered, multiple-comparison corrected. Ship a claim ONLY when accuracy's Wilson floor beats the naive baseline by a real margin. Keep the gate — it's the moat.

## Ablation (2026-07-27): "the edge was not the shortcut" — now a measured claim

A7 removed four model-output / label-derived keys from model-leg training
(`forecast_prob`, `forecast_lift`, `expectancy_hit_rate`, `n_used` — 232k/232k/239k/248k
live feature rows each at the time). The removal was made on principle; this section
measures what it cost. Harness: `daemon/cmd/selfref-ablation` retrains every leg twice on
ONE frozen snapshot (`VACUUM INTO` copy of the live DB, 2026-07-27 00:34 UTC, snapshot
sha256 `7e5c205e…8ae635`), once on the shipped post-A7 layout (**without**) and once with
the four keys restored under aliases that bypass `gbm.SelfReferentialKey` (**with**) —
identical rows, folds, purge and hyperparameters otherwise, so the arms differ in nothing
but those four features (and their derived `__has` presence bits).

**Result: zero admission-gate decisions change.** Every leg that clears the lift>0 gate
without the shortcut features is the same leg, at the same lift, that clears it with them.

### Per-symbol GBM leg · featureVersion 10 · 1d (the live admission-gate population; 27,490 labeled rows, dataset hash `686c087ad63207cd`)

37 legs graded in both arms (per-leg purged walk-forward, 5 folds, OOS N=10,057 per arm):

| arm | graded | admitted (lift>0) | mean lift | median lift | N-weighted lift |
|---|---|---|---|---|---|
| with shortcuts (pre-A7) | 37 | **2** (5.4%) | −0.3737 | −0.3383 | −0.3725 |
| without (shipped) | 37 | **2** (5.4%) | −0.3998 | −0.4737 | −0.3987 |

Per-leg OOS lift (admitted = lift>0; **gate flips: 0 of 37**):

| symbol | OOS-N | with (pre-A7) | without | Δ | flip? |
|---|---|---|---|---|---|
| WULF | 264 | **+0.0076** | **+0.0076** | +0.0000 | no |
| BTC/USD | 290 | **+0.0069** | **+0.0069** | +0.0000 | no |
| ADA/USD | 291 | +0.0000 | +0.0000 | +0.0000 | no |
| HOOD | 270 | +0.0000 | +0.0000 | +0.0000 | no |
| TSLA | 265 | +0.0000 | +0.0000 | +0.0000 | no |
| SPCX | 265 | +0.0000 | +0.0000 | +0.0000 | no |
| TSLL | 265 | +0.0000 | +0.0000 | +0.0000 | no |
| SOFI | 266 | −0.0000 | −0.0000 | +0.0000 | no |
| SPY | 265 | −0.0000 | −0.0000 | +0.0000 | no |
| NOK | 268 | −0.0000 | −0.0000 | +0.0000 | no |
| DRAM | 271 | −0.1070 | −0.0406 | −0.0664 | no |
| DOGE/USD | 286 | −0.2413 | −0.1923 | −0.0490 | no |
| XRP/USD | 283 | −0.1449 | −0.2862 | +0.1413 | no |
| NU | 268 | −0.6604 | −0.3284 | −0.3321 | no |
| T | 265 | −0.3321 | −0.3321 | +0.0000 | no |
| AAL | 275 | −0.3345 | −0.3345 | +0.0000 | no |
| COIN | 272 | −0.6765 | −0.3456 | −0.3309 | no |
| LINK/USD | 285 | −0.1614 | −0.4035 | +0.2421 | no |
| RIVN | 266 | −0.4323 | −0.4737 | +0.0414 | no |
| SOL/USD | 284 | −0.5246 | −0.4894 | −0.0352 | no |
| CLRO | 272 | −0.5588 | −0.5588 | +0.0000 | no |
| ETH/USD | 285 | −0.5509 | −0.5684 | +0.0175 | no |
| HYG | 270 | −0.6519 | −0.6370 | −0.0148 | no |
| AAPL | 275 | −0.6473 | −0.6473 | +0.0000 | no |
| AMD | 273 | −0.6557 | −0.6557 | +0.0000 | no |
| TQQQ | 265 | −0.6642 | −0.6642 | +0.0000 | no |
| SNDK | 266 | −0.6654 | −0.6654 | +0.0000 | no |
| SOXL | 266 | −0.5677 | −0.6654 | +0.0977 | no |
| SOXS | 266 | −0.5677 | −0.6654 | +0.0977 | no |
| INTC | 269 | −0.2937 | −0.6654 | +0.3717 | no |
| CRNX | 272 | −0.6654 | −0.6654 | +0.0000 | no |
| APP | 272 | −0.6654 | −0.6654 | +0.0000 | no |
| BITO | 272 | −0.3272 | −0.6765 | +0.3493 | no |
| LQD | 269 | −0.3383 | −0.6840 | +0.3457 | no |
| NVDA | 268 | −0.7015 | −0.7015 | +0.0000 | no |
| QQQ | 267 | −0.8015 | −0.8914 | +0.0899 | no |
| SNXX | 266 | −0.9023 | −0.9023 | +0.0000 | no |

The two admitted legs (WULF, BTC/USD) carry **identical lift to four decimals with and
without the shortcuts** — the shortcut features contributed nothing to the only legs with
measured edge. The paired mean Δlift of +0.0261 (shortcuts flattering the average) lives
entirely in deeply-gated-out legs (−0.29 to −0.90 lift), where "less bad" still means
"nowhere near the blend".

### Pooled cross-sectional alphax leg · v3+ pool · 1d (50,000 pooled rows, dataset hash `ed439e23dbeb02ab`)

| arm | OOS lift | accuracy | base rate | AUC | N | gate |
|---|---|---|---|---|---|---|
| with shortcuts (pre-A7) | −0.0045 | 0.4957 | 0.5001 | 0.4947 | 3,807 | GATED OUT |
| without (shipped) | −0.0084 | 0.4917 | 0.5001 | 0.4909 | 3,807 | GATED OUT |

Shortcuts flattered pooled lift by +0.0039 — still below zero, gate unchanged.

### Legs the grader refused in BOTH arms (identically)

GBM v8 (both horizons), GBM v10 1w, alphax 1w pool: too little per-symbol/per-day history
for purged walk-forward grading — with or without the shortcuts. This matches production
(no GBM 1w legs exist in `model_forecasts`). The mean-reversion and linear forecast legs
never read the feature store through the exclusion predicate (meanrev consumes
`pressure_score` only), so the ablation cannot move them by construction.

**Reproduce:** `cd daemon && go run ./cmd/selfref-ablation -db ../data/signaldeck.db
-snapshot /tmp/ablate.db -versions 8,10`. The per-dataset row hashes above
(sha256 over `symbol|ts|up|fwd_return` of the exact labeled rows consumed) pin the inputs;
training is deterministic given them. Guard against regression: the census test in
`daemon/internal/gbm/selfref_test.go` fails the suite the day any newly-logged feature key
is not explicitly classified as observation or model output.

## Realistic timeline
- Weeks: reframe + features + data upgrade.
- **Months** of live resolutions before ANY edge claim is trustworthy (crypto accrues fastest, 24/7).
- If after all that the edge is still zero, the honest app will *say so* — and you'll have saved yourself from trading noise.
