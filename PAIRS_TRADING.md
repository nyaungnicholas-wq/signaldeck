# Pairs Trading — and the resolution of H018 (CORR63)

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** FROZEN — AUTHORITATIVE
> **Scope:** Pairs-trading study and the resolution of H018 (CORR63). The DO NOT SHIP verdict is unaffected by the frozen classes.
> **Frozen claim classes:** FC2, FC3 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4
> **Authority:** `proofs/P0_FREEZE.md` (freeze) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** BLOCKED — internal use only

> ## ⚠ FROZEN — DO NOT DISTRIBUTE (P0 freeze, 2026-08-04)
> Under remediation. Do not publish or present any figure below. Do not attach capital.
> Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **backtest returns · intervals · Sharpe · survivorship.**
> Open defects in this file: the 2019→2026 universe is measured survivor-seeded
> (`ALPHA_WORKFLOW.md` §B2), and borrow cost is not modelled although every trade
> shorts one leg. Block count is stated three ways (26 / 24 / 26-of-26).
> The **DO NOT SHIP** verdict is unaffected by all three and stands.
> Authority and scope: `proofs/P0_FREEZE.md`.

**Date:** 2026-07-25
**Tool:** `tools/pairs_trading.py`
**Verdict: DO NOT SHIP.** H018's persistence is real and strong. It is also not harvestable.

---

## Why this test

H018 sat unshipped in the research ledger: *"63d SPY-correlation regime persists
(trailing-rank predictable)"*, 73.1% point estimate, quarter-clustered CI
[0.671, 0.795]. The lower bound straddled the 0.70 product bar, so it was parked
pending "more independent quarters."

More quarters would never have settled it. A correlation-rank persistence
statistic is not a decision anyone can act on, so no sample size turns it into a
product. Cointegration pairs trading is the rigorous, tradable form of the same
idea — correlation says two names move together, cointegration says their price
*spread* mean-reverts to a level you can bet on. If H018's persistence carries
economic content, pairs selected on trailing co-movement make money out of
sample. If it is shared market beta wearing a costume, they do not.

## Method

Walk-forward, 252-session formation window → next 63 sessions traded, stepping a
full 63 so no two trade windows share a day. 26 blocks, 2019→2026, 918
SIC-sectored symbols including names the platform no longer tracks.

The things that decide whether a pairs backtest is honest:

- **Frozen parameters.** Hedge ratio (OLS on log prices), spread mean and spread
  sd all come from the formation window. Recomputing them inside the trade
  window is the classic pairs lookahead and manufactures most published Sharpes.
- **Engle-Granger critical values** (−3.34 at 5%), not standard ADF values.
  Using −2.86 on a *fitted regression residual* is the most common way a pairs
  study finds cointegration that is not there.
- **Matched nulls.** The cointegration-selected arm is compared against random
  same-sector pairs and against the *least* cointegrated pairs, traded on
  identical rules. If selection carries no information the three agree.
- **Block bootstrap.** Each walk-forward block is one cluster. Trades opened in
  the same quarter share a regime; resampling individual trades would treat 900
  correlated outcomes as 900 independent ones.
- **Cost sweep**, not a single optimistic number, because pairs trading is
  cost-dominated and the break-even level is the finding.

## Results

Rules: enter |z| ≥ 2, exit |z| ≤ 0.5, stop |z| ≥ 4, force close at window end.
Top 20 pairs per block.

| Arm | trades | mean/trade (0 bps) | 95% CI (block) | win rate | Sharpe |
|---|---|---|---|---|---|
| **Cointegrated (selected)** | 938 | **+0.339%** | **[−0.164%, +0.791%]** | 44.1% | +0.70 |
| Random same-sector (null) | 570 | +0.321% | [−0.116%, +0.798%] | 43.7% | +0.39 |
| Least cointegrated (null) | 717 | +0.360% | [−1.151%, +1.975%] | 41.1% | +0.14 |

**Selection edge over random same-sector pairs: +0.017% per trade.** That is
nothing. The three arms are indistinguishable — the least-cointegrated arm has
the *highest* point estimate. Whatever return is there is generic sector mean
reversion available by picking pairs at random, and the CI contains zero at zero
cost, before a cent of friction.

Cost sweep, cointegrated arm:

| cost | mean/trade | Sharpe | CI excludes zero? |
|---|---|---|---|
| 0.0 bps/side | +0.339% | +0.70 | no |
| 2.5 bps/side | +0.289% | +0.59 | no |
| 5.0 bps/side | +0.239% | +0.49 | no |
| 10.0 bps/side | +0.139% | +0.28 | no |

Two more things that would each be disqualifying on their own:

- **Size, not frequency.** 414 winners averaging +4.24% against 524 losers
  averaging −2.75%. 48% of trades exit on the stop. A strategy that loses more
  often than it wins and relies on outlier convergences has a fat left tail that
  938 trades across 26 quarters cannot characterize.
- **Concentration.** 15–16 of 24 trading blocks positive, with the P&L living in
  2020Q1, 2022Q3 and 2023Q4. That is a regime bet, not a strategy.

## The mechanism finding — this is the real result

| | Spearman rho, formation rank → forward rank | 95% CI |
|---|---|---|
| **Correlation** rank | **+0.725** | [+0.706, +0.747] |
| **Cointegration** (ADF) rank | **−0.004** | [−0.014, +0.011] |

Correlation rank persists hard: 26 of 26 blocks positive. Cointegration rank
persists *not at all* — a pair that tests beautifully cointegrated over 252
sessions is exactly as likely as any other pair to test cointegrated over the
next 63.

That single contrast explains everything, including why H018 was stuck:

> **The persistent thing is shared market beta.** Every pair in a sector already
> has it, so it confers no advantage in choosing *which* pair to trade — and a
> dollar-neutral spread position is constructed precisely to cancel it out. The
> component that a spread trade actually monetizes, the stationary idiosyncratic
> residual, is the component with zero persistence.

This is also why the beta-regime variant failed at 64.1% while the correlation
variant reached 73.1%. Correlation persistence and beta persistence are the same
underlying fact measured two ways; correlation just measures it with less noise.
H018 was never close to a product. It was a well-estimated measurement of market
beta.

Binary framings of forward co-movement persistence, for completeness — these
corroborate the mechanism but are **not** restatements of the ledgered 73.1%,
which measured SPY-correlation tiering rather than pair co-movement:

| framing | result | null |
|---|---|---|
| stays above forward median | 97.5% | 50% |
| stays in top tercile | 93.5% | 33% |
| stays in top decile | 61.1% | 10% |

## Limitations, stated plainly

- **Delisting risk is not tested.** `delisted_at` is NULL for every symbol and
  only ~18 of 746 inactive names ever stop printing bars. Including inactive
  names removes *watchlist-selection* bias, but the trade that matters most to a
  pairs book — the leg that goes to zero and never converges — is essentially
  absent. Zero legs died mid-trade across 938 trades. Tail risk is understated
  by an unknown amount, which only makes a do-not-ship verdict safer.
- **Daily closes only.** Real pairs desks trade intraday; a 63-day horizon on
  daily bars is the slow end of the strategy family.
- **Sharpe is portfolio-level with idle capital** — flat days are included.
- A 4-sector smoke test showed a much stronger result (+0.860%/trade, CI
  excluding zero) that the full 918-symbol universe did not survive. The
  small-sample version was the flattering one. Recorded here because it is
  exactly the result that would have shipped had the run stopped early.

## Ledger

H018 gains a second evidence row under tag `2026-07-25 pairs-test` in
`daemon/internal/pipeline/researchledger.go`. The seeded prior and the original
evidence are untouched, per the file's own discipline. The open question is
rewritten from "needs more quarters" to the resolution: the persistence is real
and unharvestable, and the surviving question is whether any correlation-regime
surface can be built that is not just shared market beta.

## Reproduce

```bash
python3 tools/pairs_trading.py --json scratchpad/pairs_results.json
```

Roughly 45 minutes for the four-level cost sweep. `--quick` runs 4 sectors for a
smoke test — and, as above, do not trust its numbers.
