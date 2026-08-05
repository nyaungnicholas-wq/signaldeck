# SignalDeck remediation — four reviews, combined and corrected
<!-- SUPERSEDED-SNAPSHOT -->
> ## 📛 SUPERSEDED LIVE RECORD — HISTORICAL
> **Marked 2026-08-04 by P2.** Any live accuracy, baseline or sample size quoted
> below is the record **as it stood when this document was written**, not the
> current one. It is kept because a dated record is evidence; it is labelled
> because four such records were once in circulation with nothing to tell them
> apart (FC1).
>
> **The one authoritative live record is `partials/live_accuracy.md`**, generated
> from `data/accuracy_registry.json` by `tools/live_accuracy.py`. Reconciliation:
> `proofs/P2_LIVE_RECORD_RECONCILIATION.md`.


Four independent remediation perspectives were supplied on 2026-08-03/04. This
document merges them, resolves the places where they contradict each other or
the code, corrects the ones that are wrong, adds two findings none of them
contained, and records what was actually built.

Reviews are referenced as **R1** (five numbered fixes), **R2** (systems
diagnosis, eight phases), **R3** (accept-and-pivot), **R4** (the long
diagnostic).

---

## Part 0 — The consensus core, adopted without change

All four agree on the following, the code already agrees, and none of it is
relitigated below:

1. Keep the directional ensemble **retired**. It is 6.6pp below its null.
2. **Do not invert, relabel, or flip it.** Inverting 48.1% yields 51.9%, still
   below the 54.6% null; the bar is 45.4%.
3. **Do not lower** the majority-class baseline, the Wilson gate, the
   Bonferroni divisor, or the independent-day floors.
4. **Do not tune on resolved live data** — anything fitted after seeing outcomes
   is a new hypothesis and re-enters through the canary.
5. **Rows are not observations.** Day-clustering is mandatory everywhere.
6. Structural predictors describe **regimes**, not direction, and are not a
   drop-in replacement for the retired family.

That consensus is correct and is the spine of everything below.

---

## Part 1 — Where the reviews are wrong, and what replaces them

### C1. R1 Fix 1 — right intent, wrong mechanism

> *"Hardcode all local loss functions to optimize explicitly against the rolling
> majority class rather than 0.5."*

**Do not do this.** Log-loss is a *proper scoring rule*: it is minimised by
reporting the true conditional probability. Replacing its reference point with
the majority class makes it improper — the model would no longer be incentivised
to report true probabilities, and every downstream consumer (calibration, Brier,
the EV engine) assumes properness. You would corrupt the probability to flatter
the accuracy.

The intent is right and reachable a different way. What R1 wants is for the
model to learn **deviation from drift**, not drift itself. That is an
**intercept offset**, the standard GLM construction:

```
p = sigma( logit(baseRate_t) + w.x )
```

with `baseRate_t` the **prequential** majority rate — computed strictly from
days before *t*. The loss stays log-loss and stays proper; `w` now carries only
excess-over-drift, which is exactly the quantity being graded.

Two further corrections to R1's version:
- **"trailing 50-day"** is an unregistered free parameter chosen by eye. Use the
  same `null_prequential` the registry already grades against, so training
  target and grading null are the same object. A different window would let the
  model beat a benchmark nobody scores it on.
- This changes what the model emits, so it is a **new hypothesis** and enters
  through the canary — not a hot edit to the retired model.

### C2. R1 Fix 2 — the premise is factually wrong

> *"Current flaw: the adaptive worker weights legs based on unclustered standard
> errors."*

It does not. `internal/adaptive/adaptive.go` already computes
`se = 0.5/sqrt(days)` under the no-skill null, with `MinCellDays = 20` and
`MinCellSamples = 30`. This was the H4/H5 remediation; the code comments record
the live case it fixed (a cell giving 93.6% of its weight to gbm on 232 rows).

The proposed formula is also backwards. R1 says to multiply row counts by
`sqrt(N_rows/day)`. The design effect for a clustered proportion is
`deff = 1 + (m-1)*rho`, and effective N **divides** by it. Multiplying row
counts inflates N — the opposite of the intended correction. The implemented
`0.5/sqrt(days)` is the conservative `rho = 1` limit and is already stricter
than any deff estimate.

**What survives:** R4's version of the same idea — *"replace row counts with
day-clustered evaluation everywhere"*. The adaptive layer is done; the audit is
for whatever else still counts rows. That is a real work item, not a rewrite.

### C3. R1 Fix 3 contradicts R1 Fix 5 — and Fix 5 wins

Fix 3 says modify `trend21` and `liquidity21` conviction now. Fix 5 says do not
force-patch, let the pre-registered window arrive untouched. **These cannot both
happen.** Conviction is the frozen claim; changing its definition retires every
pre-registered hypothesis and destroys the only externally-checkable evidence
the platform has.

Resolution, which delivers Fix 3's substance without breaking Fix 5:

| | action |
|---|---|
| **Now, additive** | Publish the geometry null alongside every structural forecast — `z`, `Phi(z)`, `edgeOverGeometry`. Changes no prediction, no conviction, no frozen number. **BUILT** (`internal/structregime/geometry.go`). |
| **Now, preregistered** | Register the velocity conviction — `Rank(d/dt distance)` instead of absolute distance — as a named **challenger**, recording forecasts it does not serve. |
| **On promotion** | It re-enters through the canary gate like any new model: Wilson lower bound above both the incumbent and the null, >=30 independent observations spanning >=14 days. |

R1's velocity idea is a good hypothesis. Shipping it as an edit would be
p-hacking with extra steps.

### C4. R1 Fix 4 — conflates cost with volatility, and desynchronises three modules

> *"tau_dynamic = max(10bps, 0.2 x ATR14%)"*

Two defects.

**First, tau is not a hurdle — it is the cost band.** `internal/distribution`
uses `tau = 10bps` to define the no-trade zone and, critically, `GradeBand`
scores a realized move inside +/-tau as a **NO-CALL**. The mean-reversion leg's
entire admission gate is a cost-net grade computed at 10bp. Redefining tau in
`ev.go` alone silently rebases the meanrev leg's historical lift and changes
which labels count as no-calls. Three modules would disagree about what tau
means.

**Second, volatility is not a cost.** A high-volatility asset does not pay a
wider spread because it is volatile; charging it one invents a fee it never
pays, and the EV number stops being an expectation of anything.

The economic intent — *don't fire when the expected move is inside local noise*
— is right, and belongs in a **separate hurdle**:

```
tau_cost   = max(10bps, measured round-trip cost)      # spread + fees + slippage, per symbol
hurdle     = k * sigma_h                                # sigma_h = horizon-scaled realized vol
admit iff  NetEV >= max(MinNetEV, hurdle)
```

Cost stays cost, noise stays noise, and `internal/distribution` keeps a single
tau. `k` is a free parameter, so it is preregistered and shadowed before it
gates a live decision.

### C5. R2's "16,022 label-less rows" is misdiagnosed — and the reality is worse

R2 lists this under data/label integrity and asks whether it contaminates
training tables. It does not: these are not training labels.

They are `regime_outcomes.naive_label` — the **frozen naive-persistence null**
for the structural family. Current state, measured 2026-08-04:

```
24,956 regime_outcomes rows
16,072 (64%) carry NULL naive_label
     0 rows resolved, across every kind
```

This is **not a bug**. It is a deliberate quarantine
(`prereg.NullQuarantineScope`): rows written before the 2026-07-27 write-path
guard cannot be given a call-time baseline afterwards, because *a persistence
label computed after the outcome is known is a hindsight baseline*. The platform
refuses to backfill them, and that refusal is correct — I checked whether
backfill was safe and it is not, for exactly the stated reason.

The consequence, which R2 did not draw: those 16,072 rows are **excluded from
every structural benchmark denominator**, and they are precisely the
earliest-recorded cohort — the first that would ever resolve. The structural
family's first resolvable evidence is evidence that carries no null.

### C6. Every review's "wait for 2026-08-07" rests on a date that cannot deliver

R1 Fix 5, R3 ("wait 4 days"), and R4 ("wait for the 2026-08-07 verdicts") all
take `firstGradableOn = 2026-08-07` at face value. So did my own earlier
summary. It is wrong, for two independent reasons.

**Reason 1 — the horizon is trading bars, not calendar days.** The resolver
indexes the bar array: `closes[t+horizonDays]` (`ResolveTrendAt`,
`ResolveLiquidityAt`, `ResolveVol21At`). 21 bars is ~30 calendar days. The
earliest call is 2026-07-18, so the earliest possible resolution is
**2026-08-17** — ten days *after* the advertised date. The 2026-08-07 figure is
what you get by adding 21 calendar days to the first call.

**Reason 2 — the block gate is far larger than the resolution wait.** A verdict
needs `MIN_DISTINCT_BLOCKS = 10` distinct non-overlapping horizon blocks, where
a block is `call_day // horizon_days`. Ten blocks of 21 days is 189 further days
of *calls*, and the last block must then resolve.

Measured by the new `tools/prereg_readiness.py`:

| kind | first call | earliest resolution | **first possible verdict** | vs claimed |
|---|---|---|---|---|
| trend21 | 2026-07-18 | 2026-08-17 | **2027-02-22** | short by 199 days |
| vol21 | 2026-07-18 | 2026-08-17 | **2027-02-22** | short by 199 days |
| liquidity21 | 2026-07-18 | 2026-08-17 | **2027-02-22** | short by 199 days |
| trend21-crypto | 2026-07-18 | 2026-08-17 | **2027-02-22** | short by 199 days |
| liquidity21-crypto | 2026-07-18 | 2026-08-17 | **2027-02-22** | short by 199 days |
| filingsdrift21 | 2026-07-22 | 2026-08-21 | **2027-02-26** | short by 203 days |
| trend63 | 2026-07-18 | 2026-10-15 | **2028-05-04** | short by 636 days |

Distinct blocks accrued so far: **2 of the 10 required**, for every kind.

**What will actually happen on 2026-08-07:** `tools/accuracy_registry.py` will
find zero resolved structural rows and return `INSUFFICIENT` for every kind.
That is the grader working correctly. It is not a verdict, and it must not be
reported as one.

This does not weaken the pre-registration — the frozen claims are intact and
still checkable. It corrects *when* they become checkable. The date discrepancy
itself belongs in the hash chain as an **amendment**; see Part 4.

### C7. R2's threshold point is right and pairs with C1

A fixed 0.50 decision threshold is not the operating point for an imbalanced
label. The three questions must be separated:

- **probability quality** → log loss, Brier skill (vs the base rate, not 0.5)
- **direction classification** → threshold chosen from prior data only
- **trade / abstain** → cost-adjusted EV, abstain when the edge is inside cost

With the C1 base-rate offset in place the natural threshold is the prequential
base rate, not 0.5. Same preregistration requirement: it changes decisions.

### C8. R3's "shorten the horizon to 1h" has no evidence behind it

The suggestion is that the null is closer to 50% intraday, so an edge is more
likely to show. Plausible — but the accuracy registry contains **no 1h rows at
all**. Only `1d` and `1w` are graded. The 1h horizon is not a fallback with a
weak record; it is a horizon with *no* record. It can be proposed, not adopted.

### C9. R2/R4's "unmeasured means shadow, not blend" — adopted in full

This is the single largest admission loophole and both reviews name it exactly.
The pressure leg is kept when its lift has never been measured; expectancy and
sentiment enter on count/freshness gates with no out-of-sample skill gate at
all.

The fail-safe exists for a real reason (a dead trainer must not blank the
platform), so the fix is a mode, not a deletion. **BUILT** — see Part 3.

The timing is free: the ensemble is already retired, so tightening admission
costs nothing today and cannot be blamed for a regression later.

### C10. R2 Phase 5's out-of-fold stack is correct but out of order

Stacking on out-of-fold base predictions is the right architecture. But a stack
cannot manufacture lift from legs that individually have none — it can only
reweight what is there. R2's own Phase 2 (per-leg live lift audit) must land
first, and all four reviews call for it. Build the audit, read it, then decide
whether a stack has anything to stack.

---

## Part 2 — Two findings the reviews did not contain

1. **The pre-registered gradable date is unreachable by ~7 months** (C6). Every
   review's sequencing depends on it.
2. **64% of structural outcome rows carry no null and are the first to resolve**
   (C5). The first structural evidence to arrive is un-benchmarkable by
   construction.

Both are now instrumented rather than remembered:
`python tools/prereg_readiness.py` prints them and **exits 1** while any claimed
date is unreachable, so CI sees it.

---

## Part 3 — What was built

All changes are additive or behind an explicit opt-in. No frozen claim, no
published number, and no live prediction moved.

| # | change | file | check |
|---|---|---|---|
| 1 | **Geometry null as a first-class output** — `Phi`, `GeometryFrom`, `TrendGeometry`, `LiquidityGeometry`, `VolGeometry`, `EdgeOverGeometry`. The zero-free-parameter benchmark conviction has to beat. Refuses (`OK=false`) rather than inventing a null when sigma is unusable. | `internal/structregime/geometry.go` | `geometry_test.go` — CDF pinned at six known points, monotone, refusal cases, and a regression test asserting `PredictTrend` still matches the frozen band table |
| 2 | **Readiness instrument** — computes the real first-verdict date per kind from the block gate and the trading-day horizon; reports the null-quarantine; exits 1 when a claimed date is unreachable. | `tools/prereg_readiness.py` | `--self-check` (asserts the epoch-day arithmetic and that the earliest resolution already postdates the claimed date) |
| 3 | **Conviction reads the interval, not the point estimate** — `WinRateLB` added; the ceiling is set by the lower bound. Without an interval, HIGH is unreachable and the cap says so. | `internal/composite/conviction.go` | `conviction_lowerbound_test.go` — includes the live 1d high-conviction tier as a test case (55.2% point / 0.44 bound must read LOW), plus a monotonicity property |
| 4 | **Fail-closed admission** — `RequireMeasuredLegs` makes every leg (pressure, expectancy, sentiment included) prove a measured positive lift; `AdmittedProbability` returns `ok=false` when nothing is admitted, so a caller emits *no forecast* instead of a 0.5 that reads like one. | `internal/ensemble/ensemble.go` | `admission_test.go` |
| 5 | **Expectancy evidence floors** — `minSamples` 5 -> 30, new `minDistinctDays = 5`, and hit rates shrunk toward the horizon's pooled base rate with pseudocount 25 so a thin cell can never publish a saturated 0.0/1.0. | `internal/expectancy/expectancy.go` | `hardening_test.go` — includes a 1200-minute-bar case spanning <=2 UTC days that must emit nothing |

**A latent inconsistency surfaced by change 5, fixed with it.** The walk floors
`minDailyBars = 80` and `minMinuteBars = 300` could no longer produce
`minSamples = 30`: with `walkStart = 60`, 80 daily bars yield 20 samples, and
300 minute bars at `minuteStep = 15` yield 12. The walk gate would admit a
symbol whose every state cell was then dropped for thinness — a silent
contradiction between two floors. Raised to `95` and `600` respectively, derived
in the code comment rather than picked:

```
daily:  walkStart + minSamples                    = 60 + 30            =  90 -> 95
minute: walkStart + minSamples*minuteStep + 60fwd = 60 + 450 + 60      = 570 -> 600
```

This makes the expectancy leg strictly harder to populate, which is the
direction all four reviews want.

Why the day floor matters most for `1h`: daily bars are one per day, so
`distinct days == samples` for 1d/1w. The minute walk samples every 15 bars, so
a single session can manufacture hundreds of rows carrying one day of
information. That is where row-inflation actually lived.

---

### One deliberate live consequence of change 3, stated rather than hidden

The four production call sites that build `ConvictionInputs`
(`api/composite.go` x2, `api/desk.go` x2) supply `WinRate` and **do not supply
`WinRateLB`** — nothing on the platform currently computes a day-clustered
Wilson lower bound for the live win rate. Under the new contract that means
**conviction now caps at MODERATE everywhere in production**, with the reason
rendered in the drivers ("no confidence interval was supplied").

That is the fail-safe working, not a regression: HIGH conviction was previously
being granted off a point estimate, which is the exact defect the live
high-conviction tier exposed. It also propagates one step further —
`internal/recommendation` maps HIGH to `Buy`/`Avoid`, so the strongest actions
are unreachable until a real interval is plumbed. Given the directional
ensemble is retired and not emitting, nothing user-facing changes today.

**Follow-up (not done here):** plumb a day-clustered Wilson lower bound into
those four call sites. It should come from the same grading surface the
registry uses, so the ceiling and the published record cannot disagree.

## Part 4 — Preregistered challengers (built as hypotheses, not edits)

Each is a new model and enters through the canary gate. None may serve until its
Wilson lower bound clears both the incumbent and the majority-class null over
>=30 independent observations spanning >=14 days.

| challenger | what it changes | from |
|---|---|---|
| **base-rate-offset direction** | `sigma(logit(baseRate_t) + w.x)`, prequential offset, log-loss unchanged | C1 (R1 Fix 1, corrected) |
| **velocity conviction** | `Rank(d/dt distance)` for trend21/liquidity21 | C3 (R1 Fix 3, corrected) |
| **split-tau EV** | measured per-symbol cost band + `k * sigma_h` hurdle | C4 (R1 Fix 4, corrected) |
| **base-rate decision threshold** | classify at the prequential base rate, not 0.5 | C7 (R2) |
| **out-of-fold stack** | meta-model on unseen base predictions | C10 (R2), *after* the leg audit |

### The gradability amendment — FILED 2026-08-04

`firstGradableOn` was wrong (C6). The correction is now on the chain.

```
seq       37
kind      gradability-correction
prevHash  c06945bbb9ac4173115b9260bfb18bd422607b0f7b47c65421f55e25cdafb23f
entryHash 33802e0a57e9ac0bdc7b18e6e06b883db98523adda69617d658e1e03ac31afba
```

**The frozen constant was NOT edited.** `prereg.FirstGradableOn` still reads
`"2026-08-07"`, because the platform's own doctrine — stated in
`internal/mcp/tools.go` — is that a hash-chained commitment is never rewritten.
The correction is appended, so a reader sees both the original commitment and
the correction in order, which is the entire point of an append-only log. The
record's `kind` is deliberately its own (`gradability-correction`) rather than an
amendment to a structural kind: amending `trend21` would change that claim's
spec hash and read as *the claim moved*, and no claim moved.

Filed by `daemon/cmd/prereg-amend` (dry-run by default; `-commit` writes), which:

- **verifies the whole chain before appending** and refuses to write onto a
  broken one;
- **refuses to file at all if any structural forecast has resolved**, because the
  record asserts it predates every outcome and that must be true, not assumed;
- **measures the state it reports at commit time** rather than transcribing it —
  the daemon ingests continuously and a hardcoded count is stale within minutes;
- opens with `_txlock=immediate` so the write lock is taken **before** the chain
  head is read. `store.AppendPrereg` uses the store's deferred-transaction
  connection, where head-read and insert are not atomic against another writer —
  a concurrent registrar pass could produce two records sharing one `prev_hash`,
  a fork being exactly what a tamper-evident log exists to make impossible. The
  daemon (PID 58456 at the time) kept running throughout; nothing was stopped.

Verified afterwards by independent recomputation (Python, not the tool that wrote
it): 37 records, every entry hash and `prev_hash` link recomputes, **all 36
pre-existing entry hashes unchanged**, no duplicate `prev_hash`. A snapshot of
the prior 36 rows is at
`data/backups/prereg_records.pre-amendment-20260804.json`.

---

### The derived date now includes the block gate

`store.EarliestGradeableAt` answered "when does the first forecast RESOLVE" while
being served as `earliestGradeableOn` — the field the MCP surface calls
"authoritative for scheduling". Resolution is one data point; the grader refuses
to publish an interval below `MIN_DISTINCT_BLOCKS`. So the derived date
understated the wait by seven months, which is the same shape of error as the
frozen date, one level down.

Added `store.EarliestVerdictAt` and surfaced it as `earliestVerdictOn`:

| field | question it answers | live value |
|---|---|---|
| `prereg.FirstGradableOn` | what was committed to | 2026-08-07 (frozen, unedited) |
| `earliestGradeableOn` | when the first forecast resolves | 2026-08-17 |
| **`earliestVerdictOn`** | **when a claim is first checkable** | **2027-02-13** |

Three fields, because they are three different questions and collapsing them is
what produced the original error. The existing field's meaning is unchanged, and
`TestEarliestGradeableAtUnchanged` pins it so the fix cannot silently redefine
what it was asked to sit beside.

Two details that matter:

- **Quarantined rows do not start the block clock.** A row with a NULL
  `naive_label` cannot enter a benchmark denominator, so counting it would
  repeat the exact defect — a date ignoring one of its own determinants. The
  live first-eligible day is 2026-07-27, the quarantine cutoff, not 2026-07-18.
- **`tools/structural_liveness.py` deliberately does NOT gain this.** Its
  question is whether a DUE row went ungraded, which genuinely keys off
  resolution. The file's header says the two implementations must move together;
  they still agree on the resolution formula, and the comment now records that
  they answer different questions rather than having drifted.

The Go side uses the block-ALIGNED form `(firstBlock + N-1) * horizon`, matching
the grader's `day // horizon_days` bucketing; `prereg_readiness.py` and the
seq-37 record use the conservative `firstCall + (N-1) * horizon`. They differ by
up to `horizon-1` days (2027-02-13 vs 2027-02-22). Both are far past
2026-08-07, and each states which form it used, so the gap is documented rather
than discovered.

## Part 5 — Not done, and why

- **No rebuild of the directional family.** R3 is right that this is the wrong
  place to spend effort, and R2's own ordering puts the per-leg audit first.
- **No change to any structural predictor.** C3.
- **No inversion, no relabeling, no gate loosening.** Part 0.
- **No `tau` change in `internal/distribution` or `ev.go`.** C4 — it would
  silently rebase the mean-reversion leg's admission grade.
- ~~The per-leg live lift audit is specified but not built.~~ **BUILT — see
  Part 7.** The claim that it needed an export "that does not exist yet" was
  wrong: `predictions.components` has carried every leg's probability and its
  admission lift for all 324,741 predictions. Only the weights and the fallback
  tier were genuinely missing, and those are now recorded too.

## Part 7 — The per-leg live lift audit (BUILT, and it found the culprit)

I said this needed a diagnostic export that did not exist. **That was wrong.**
`predictions.components` has stored every leg's probability and its admission
lift for all 324,741 predictions since the beginning. Only the *weights* and the
*fallback tier* were genuinely missing.

**Built:**

| | |
|---|---|
| `tools/leg_audit.py` | grades each leg separately: prequential-majority null, day-clustered interval, Brier skill, log loss, AUC, pairwise correlation, leave-one-out contribution. Verdicts read off the **interval**, never the point estimate. `--self-check` gates it; exits 1 on any `HURT`. |
| `predictions.weights` / `predictions.basis` | the weight map actually used and which tier supplied it (`personal` / `regime` / `static`). Without these, "weighted by measured skill" and "fell back to a static prior" are indistinguishable after the fact. Additive migration; legacy rows read `''` = *not recorded*, never *equal weights*. |

### The finding

**The pressure leg is what hurt the ensemble — and it was in the blend on the
"unmeasured means allowed" fail-safe.**

| horizon | leg | acc | null | lift | leave-one-out | verdict |
|---|---|---|---|---|---|---|
| 1w | **pressure** | 0.4800 | 0.5052 | **−0.0253** | **−0.0194** | **HURT** |
| 1w | **sentiment** | 0.4320 | 0.5018 | **−0.0699** | **−0.0254** | **HURT** |
| 1w | expectancy | 0.5236 | 0.5052 | +0.0184 | +0.0133 | HELPED |
| 1d | **pressure** | 0.4753 | 0.4434 | +0.0319 | **−0.0323** | NO MEASURED EDGE |
| 1d | meanrev | 0.6180 | 0.4437 | +0.1743 | **+0.0416** | HELPED |
| 1d | forecast | 0.5250 | 0.4434 | +0.0816 | +0.0046 | HELPED |
| 1d | alphax | 0.4756 | 0.4391 | +0.0365 | +0.0057 | INSUFFICIENT DAYS (9/10) |

Read the **leave-one-out** column, not the accuracy column. Pressure at 1d has a
*positive* standalone lift and is still the most harmful leg in the blend:
removing it moves the blend from 0.4850 to 0.5173. A leg can look fine alone and
still contribute nothing but noise once the legs it duplicates are present.

**Pressure was unmeasured on 9,491 of 15,991 rows at 1d and 13,355 of 13,911 at
1w** — it was admitted by the cold-start fail-safe almost every time, never by
evidence. That is exactly the loophole R2 and R4 both named, and the audit now
puts a measured cost on it. `RequireMeasuredLegs` (Part 3, change 4) closes it.

The inverse case is as sharp: **mean-reversion has the strictest gate on the
platform** (cost-net walk-forward) **and is the best leg by a wide margin**
(+0.1743 lift, +0.0416 leave-one-out, the only positive Brier skill anywhere in
the table) — and it was admitted on just 58 of 746 rows. The gate that made a leg
prove itself is the gate that found the good one.

### Two corrections to the reviews, from the measurement

- **R2 and R3 both assert the legs are near-duplicates** ("one signal with seven
  correlated measurements"). Measured, they are not. The largest |rho| is
  pressure|meanrev at **−0.425**, which is arithmetic — meanrev is defined as the
  inversion of the momentum blend. Next is expectancy|forecast at +0.395. And
  `pressure|forecast` is **+0.021**, essentially independent. Collinearity is a
  real concern in principle and a small one here; it is not what broke the
  ensemble.
- **Direction and calibration failed differently.** Brier skill is negative for
  every leg but meanrev, including legs whose directional accuracy beats the
  null. There is some ranking signal; the probabilities attached to it are worse
  than just forecasting the base rate. Any rebuild should be graded on a proper
  score, not accuracy alone — which is R2's Phase 7, now with evidence behind it.

### Caveat carried on the report itself

This population (15,991 independent rows over 31 days at 1d) is larger and less
filtered than the accuracy registry's (2,257 over 9 days), because the registry
also applies survivorship filtering and its own multiplicity correction. These
are **diagnostics, not verdicts** — the tool prints that in its own footer, and a
leg that looks good here still has to pass the registry's gates.

## Part 6 — What to do on 2026-08-07

Run the registry exactly as pre-registered. Expect `INSUFFICIENT` for every
structural kind, and report it as what it is: the refusal rule firing, on
schedule, because zero forecasts have resolved. Then run
`python tools/prereg_readiness.py`, which will print 2027-02-22 as the real
date, and put that date in the calendar instead.

The system is not failing. It is measuring honestly and the measurement takes
longer than the calendar note said it would.
