# HAR RV forward test: where the registered rule does not decide (2026-10-01)

The live grader (`daemon/internal/rvgrade`, wired into `GET /api/vol-forecast/record`)
implements the registration in `daemon/internal/volprereg/volprereg.go`
(chain kind `rv-forecast-test-registration`, prereg seq 105, `specDigest`
`f44e22b2…f9e8`, which matches the compiled `Registration().Hash()`). It does not change
the spec, any threshold, horizon, loss or minimum.

Where the registered text admits more than one reading, the grader does **not** pick one.
It evaluates every combination and publishes a verdict only when they all agree. If they
disagree, the record stays `ACCRUING` and `verdictReason` names the competing outcomes.
That hold ends when the owner resolves the items below. Each resolution should be written
as an amendment record, never as an edit to the filed spec.

## Open: these change the verdict

| # | Registered text | Readings evaluated | Where it lives |
|---|---|---|---|
| A1 | "Bonferroni-corrected across family size 24 and the looks already spent" | divisor **26** (24 + 2), **48** (24 × 2, `prereg.MultiplicityRule`'s family × looks), **72** (24 × (1 + 2), `researchx.Divisor`'s grid × (1 + prior searches)) | `rvgrade.Divisors()` |
| A2 | "the headline DM p-value" after "the Harvey-Leybourne-Newbold small-sample correction" | two-sided p from **Student-t, T−1 df** (HLN's own recommendation) and from **N(0,1)** | `Cell.PStudent`, `Cell.PNormal` |
| A3 | "the headline clears its bar but the RV^CC control does not" / "the RV^CC control holds" | control must clear the **same corrected bar** with HAR's sign, the **uncorrected 0.05** with HAR's sign, or **HAR's sign alone** | `Decide`, `controlBar` |
| A4 | NO SKILL is "p at or above 0.05"; BEATS needs "the sign in HAR's favour" | **gap.** HAR *significantly worse* than EWMA matches no registered outcome. The grader reports `UNREGISTERED: significant in EWMA's favour` and holds at ACCRUING. It does not map this onto NO SKILL. | `Decide` |
| A5 | "fewer than 60 distinct live trading days → INSUFFICIENT" | Implemented as a **rolling** grade over every live day at each rebuild, which is the literal reading. The alternative is a single grade frozen at the first 60 days. A rolling verdict can change from day to day, and the rule charges nothing for repeated live looks (optional stopping). | endpoint |
| A6 | "fewer than 60 distinct live trading days → INSUFFICIENT" | It is unsaid whether the floor also binds the **RV^CC control**. Missing or zero-move closes can leave the control with fewer days than the headline. The grader will not rule BEATS vs ARTIFACT off a control under 60 days: it holds at `UNDEFINED: RV^CC control under the evidence floor`. The reference refuses its control below 30 days. | `Decide` |

18 combinations of A1 × A2 × A3 are published in `horizons[0].readings`. Measured
sensitivity: for a headline t of −3.2 over 80 days, Student-t p ≈ 0.0020 and normal
p ≈ 0.0014, which straddle 0.05/26 = 0.00192. A1 and A2 matter exactly in that band.

## Decided by the literal text (recorded, not open)

- **The control cell** is MSE on RV^CC, HAR vs EWMA(0.94), at horizon 1. It is the
  headline's counterpart, and the Control field fixes the loss: "Squared error only".
  Sessions where the close did not move are excluded and counted (`ccZeroExcluded`).
  Windows with a missing or non-positive close are counted separately (`ccMissing`).
- **Two-sided p plus a separate sign condition.** A one-sided p would make the
  registered "with the sign in HAR's favour" clause redundant.
- **The 30-symbol floor applies per cell.** A day counts in a cell only if at least 30
  forecasts have that cell's loss defined. All RV^GK cells count the same days. The
  control can drop a day that the headline keeps.
- **Start rule.** `created_ts` is whole seconds, so "strictly after" is
  `created_ts > registration.ts`. A forecast frozen inside the registration's own second
  is excluded. Today this is a no-op: the first forecast was frozen 4,691 s after the
  record.
- **Horizon 5 is labelled `SECONDARY`, not given a verdict.** The Headline field says the
  other 23 cells "cannot produce the verdict", so a horizon-5 badge carrying one of the
  four outcomes would claim something the registration forbids. Below its own floor,
  horizon 5 reads `INSUFFICIENT`.
- **Test statistics are published only at or above the 60-day floor.** Below it, a
  t-statistic is a look the registration did not budget for.

## Known limits of the live record (cannot be fixed without amending or hindsight)

- **The flat-22 null is not frozen** in `rv_forecasts`. The Nulls field registers three
  nulls, but the table stores only `null_rw` and `null_ewma`. Rebuilding the third after
  the outcome would be hindsight, which the Nulls field itself forbids. Live, the grader
  computes 12 of the 24 cells: per horizon, QLIKE and MSE vs RW and EWMA on RV^GK, plus
  MSE vs RW and EWMA on RV^CC. QLIKE on RV^CC is excluded by the Control field.
- **HLN factor at h = 5.** Both implementations (Go `harrv.DieboldMariano`, Python
  `diebold_mariano`) apply sqrt((n−1)/n). That is the exact HLN factor only at h = 1. The
  horizon-5 cells are secondary and are left as-is so parity holds. The verdict is
  unaffected.
- **The bootstrap interval** ("reported ALONGSIDE … NOT the decision rule") is not in the
  live record. The Python reference uses Mersenne Twister, which Go cannot reproduce for
  parity. It never decides the verdict.
- **RV^CC is computed at grade time** from the current daily bars. RV^GK was frozen at
  resolution. A later bar revision moves the control's outcome but not the headline's.
  There is no wild-move guard on RV^CC, which matches `cc_series` in the reference.

- **Rows enter a cell only when both losses are defined.** The reference averages each
  model over its own valid rows instead. The two are identical on live data, because the
  store refuses non-positive forecasts and nulls. "30 symbols on a day" is counted as
  loss-defined forecasts in the cell, not as all resolved forecasts that day.
- **Resolver forming-bar risk (pre-existing, `pipeline/rvforecast.go` RVOutcomeWorker).**
  The resolver does not check `DailyBarSettled` on the last bar of the outcome window. A
  pass during market hours could therefore freeze an h=1 RV^GK outcome from a partial
  session, while `CCTarget` later reads the settled bar. Not changed here. It is a
  resolver fix, not grading.

## Adjacent copy that a verdict will contradict (not changed here)

- `RVRecordCaveat` opens "LIVE RECORD, ACCRUING. This is not a claim of skill." It stays
  on the response even under BEATS THE NULLS.
- `meanQlike`, `vsEwma`, `vsRandomWalk`, `n` and `ungradable` come from
  `store.RVLiveRecord`, which has no start-rule filter. This is a no-op today, because no
  forecast predates the registration. They are also pooled row means, not the day-clustered
  statistic. On a pass, step 9's `riskHeadline.ts` prints `vsEwma`. The registered number
  is `grade.headline.meanDiff`.
- Step 9's pass sentence says the "next-day **and next-week**" forecast beat its
  baselines. The registration grades only horizon 1.

## Parity

`tools/rv_grader_parity.py --write` generates `daemon/internal/rvgrade/testdata/` from
`tools/rv_forecast_backtest.py` (`aggregate`, `diebold_mariano`, `cc_series`, and the
tail of `control_section`). `--check` proves the committed files are that module's
output. `TestParityWithPythonReference` asserts that the Go grader matches them to 1e-9
relative on mean differential, t, day count and HAC lag for both horizons, on the four
shared cells, and to 1e-15 on every RV^CC window.
