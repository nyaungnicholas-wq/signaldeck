# SignalDeck — Honest Plan to a Real Edge

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

### Phase 1 — Better data (COSTS ~$129/mo — your call, biggest single lever)
- Alpaca Algo Trader Plus ($99): real-time full-market SIP (code already requests it; `SIGNALDECK_ALPACA_FEED` is wired).
- Tiingo ($30): real fundamentals + **analyst estimate revisions** (the highest-edge free-ish feature there is).
- You cannot squeeze edge from thin, delayed, free data. This is the ceiling-raiser.

### Phase 2 — Real alpha features, OOS-gated (FREE, partly done)
- Add estimate-revision + surprise, insider clusters, options put/call + unusual flow, macro/credit/vol regime conditioning as features. The gate (lift>0 walk-forward) decides if any earns a live output. Never trust one; ensemble many.

### Phase 3 — Product: make the prediction tab LOW-FREQUENCY + high-conviction
- Instead of a daily direction % on everything, fire a call ONLY when multiple *proven* signals align AND risk/reward is favorable. Rare (2×/month), higher hit rate, positive expectancy. This is the ONLY honest path to a "high win rate" number.
- Alerts fire on **verifiable facts** (insider buy, short-vol 3σ, regime flip), scored forever — not on the 51% model.

### Phase 4 — Validate honestly (the guardrail)
- Walk-forward, cost-adjusted, N in the hundreds, pre-registered, multiple-comparison corrected. Ship a claim ONLY when accuracy's Wilson floor beats the naive baseline by a real margin. Keep the gate — it's the moat.

## Realistic timeline
- Weeks: reframe + features + data upgrade.
- **Months** of live resolutions before ANY edge claim is trustworthy (crypto accrues fastest, 24/7).
- If after all that the edge is still zero, the honest app will *say so* — and you'll have saved yourself from trading noise.
