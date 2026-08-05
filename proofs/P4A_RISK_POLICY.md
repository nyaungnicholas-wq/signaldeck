# P4A — Risk policy: proof artifact

**Date:** 2026-08-04 · **Phase:** P4A · **Artifact:** `RISK_POLICY.md`

## What was asked

Write an actual risk policy covering position-level rules, portfolio-level
rules, a drawdown ladder, and a kill switch. Mark it SPEC ONLY / NOT IN FORCE
until implemented. Ensure no document claims it is live unless code exists.

## What was found first

The phase brief asserted the platform had "no stop-loss, no binding sizing, no
max drawdown, no correlation cap, no real kill switch." **Two of those five were
wrong, and saying so is part of the deliverable** — a policy written against a
false inventory would have re-specified controls that already bind and missed
the ones that do not.

Measured against the code before any change:

| Brief's claim | Measured reality |
|---|---|
| no stop-loss | **Correct.** No stop, no take-profit, no time stop. The only exit is the `cal_prob` flip through the flat threshold |
| no binding sizing | **Wrong.** `riskgate.Evaluate` sizes every entry at quarter Kelly (`KellyFraction` 0.25) or equal-slice, caps it at 10% of equity, and refuses below a 0.5% minimum ticket. The refusal path was live: `if !gate.Allow { refused++; continue }` |
| no max drawdown | **Wrong.** `MaxDrawdown` 0.20 and `MaxDailyLoss` 0.05 were both enforced, both with an explicit UNARMED state when unmeasurable |
| no correlation cap | **Correct, and worse than stated.** `CorrToBook` was *measured* in `paperev.go` and then never read by any decision. A dead input is more misleading than a missing one, because it looks like coverage |
| no real kill switch | **Correct.** No halt control was wired to anything |

## What was built

`RISK_POLICY.md`, ~3,250 words. Every rule carries **[IN FORCE — paper]** or
**[SPEC ONLY — NOT IN FORCE]**. Count: **11 in force, 8 spec only.**

Two limits were added to `riskgate` as part of this phase so the policy could
mark them in force rather than aspirational:

- **`MaxCorrToBook` 0.80** — refuses a candidate correlated at or above the cap
  to the book it would join. This makes the previously dead `CorrToBook`
  measurement load-bearing. Refuses, never trims: a smaller slice of the same
  exposure is the same mistake in a less honest package.
- **`MaxGrossExposure` 1.00** — no leverage. Exhausted gross refuses at
  admission; partial gross trims to headroom. Arms only when *every* open
  position could be marked, because an understated gross hands out headroom the
  book may not have.

Both follow the package's existing unarmed-when-unmeasured doctrine.

## Honesty controls in the document

- A banner stating the policy governs a simulated book and that no order has
  ever been placed from this repository.
- §3's ladder table shows the requested rungs (−5% halve, −8% suspend, −12%
  flatten) as SPEC ONLY beside the two rungs that actually fire (−20%, −5%
  daily), with the sentence "the specified ladder is strictly tighter than the
  enforced one at every rung. The gap is real and this document does not
  minimise it."
- §2.3 discloses that the sector cap only covers *classified* names, because
  `riskSector` returns `""` for most of the broad universe.
- §4.4 states that the kill switch stops new risk and does **not** liquidate,
  which is why the −12% "flatten and halt" rung cannot be in force.
- §6 lists six preconditions before any of this could govern real money.

## Verification

The document is gated by a machine check requiring 38 substantive phrases
(every limit name, every section heading, the fail-closed language, the status
markers) and **banning** the string `stock-trader/trader/risk_gate.py` and the
phrases "live trading is enabled" / "this policy is live".

```
OK RISK_POLICY.md (3252 words)
```

Code backing the newly in-force rules:

```bash
cd daemon && go test ./internal/riskgate/ -count=1
```

```
ok  github.com/nyaungnicholas-wq/signaldeck/internal/riskgate  0.346s
```

New tests added in this phase: `TestCorrelationCapRefusesRatherThanTrims`,
`TestUnmeasuredCorrelationLeavesTheCapUnarmed`,
`TestCorrelationRefusalCarriesNoSizing`,
`TestGrossExposureCapRefusesFullAndTrimsPartial`,
`TestUnmeasuredGrossLeavesTheCapUnarmed`, `TestNewCapsNeverBlockAnExit`,
`TestWithDefaultsFillsTheNewLimits`.

## Done-when

| Criterion | Status |
|---|---|
| The policy exists | ✅ `RISK_POLICY.md` |
| Marked SPEC ONLY / NOT IN FORCE until implemented | ✅ per-rule markers, 8 of 19 rules are SPEC ONLY |
| No doc claims it is live unless code exists | ✅ machine-checked ban list; `INSTITUTIONAL_GAP.md` and `STRATEGY_DECK.md` reconciled in P4C |

## What this phase did NOT do

The three position-level barriers (§1.2–§1.4) are **specified, not built**. That
is deliberate and the reason is recorded in the policy: on daily bars an
intrabar barrier cannot be filled honestly, because the bar does not say whether
the low preceded the high. Shipping a stop against an undisclosed path
assumption would have produced a *worse* artifact than shipping none — a risk
control reported at a price that was never available. The correct implementation
fills at the next open or ingests intraday bars, and neither is in scope here.
