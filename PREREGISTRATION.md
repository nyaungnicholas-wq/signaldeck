# PREREGISTRATION — the 2026-08-14 structural grading protocol

**Status: FROZEN 2026-07-26.** The SHA-256 digest of this file is registered in the
pre-registration hash chain (`prereg_records`, kind `prereg-document`). The chain head
has **NOT** been published anywhere outside this machine: as of 2026-07-27 no public
anchors repository exists, so the registration date is asserted by the operator and
cannot be checked by anyone else. `ops/anchor-publish.sh` is written and tested but
has never pushed. If a chain head is not in a third party's git history before
2026-08-14, the six structural claims below are permanently unverifiable AS
PRE-REGISTERED, whatever is built afterwards. Any
edit to this file after registration changes its digest, which appends an AMENDMENT
record to the chain on the registrar's next pass. An amendment dated before
2026-08-14 is a visible correction; one dated after is a disqualifying rewrite.

This document is the human-readable statement of the protocol. The machine-readable
claims and grading rules it describes are themselves individually hash-chained (see
`daemon/internal/prereg/prereg.go` and `GET /api/prereg`); where prose and chain
disagree, the chain governs.

---

## 1. What is being graded, and when

Six structural regime predictors carry outstanding live forecasts whose advertised
accuracies are, today, **backtest numbers only** — zero live observations have
resolved. The platform displays them as claims, not records, everywhere.

The claim graded per predictor is the outstanding-forecast-weighted mean of its
frozen band table: each forecast is stamped at call time with the frozen
`historical_accuracy` for its conviction band, and the registry grades
`AVG(historical_accuracy)` over all forecasts since the survivorship epoch. Stamped
rows are immune to later edits of the band tables. As of registration those
headline claims and first verdict-eligible dates are:

| Predictor | Headline claim (forecast-weighted) | First grade eligible |
|---|---:|---|
| liquidity21 | 71.4% | 2026-08-14 |
| liquidity21-crypto | 94.6% | 2026-08-14 |
| trend21 | 81.8% | 2026-08-14 |
| trend21-crypto | 93.6% | 2026-08-14 |
| vol21 | 62.0% | 2026-08-14 |
| trend63 | 74.4% | 2026-09-25 |

The per-band claim tables, the exact yes/no question each predictor answers, the
resolution rule, the baseline, and each predictor's already-measured weaknesses are
frozen verbatim in the chain (one record per kind, registered 2026-07-26, before
any forecast resolved). The chain's `FirstGradableOn` constant (2026-08-07) marks
the earliest date any single forecast can resolve; **2026-08-14** is the earliest
date the minimum-evidence gates below can be satisfied, and therefore the earliest
date a verdict can exist. Everything in this protocol is committed before that date.

## 2. Claim freeze

- The claims graded are the ones **frozen in the chain on 2026-07-26**, not
  whatever the code says on grading day. The grader reads at-call-time stamped
  accuracies, so a post-registration edit to the band tables cannot move the target.
- The grader itself is pinned: `tools/accuracy_registry.py` is registered in the
  chain under kind `grading-protocol` with its commit and content SHA-256. An edited
  grader re-digests to a different hash and appends an AMENDMENT automatically.
- Amendments append; they never overwrite. The original record survives
  byte-for-byte and both are readable in order.

**2026-07-27 — the grader now READS the target instead of recomputing it.**
Until this change `tools/accuracy_registry.py` set `C` to
`AVG(historical_accuracy)` over `regime_outcomes`, i.e. it rebuilt the target at
grading time out of the same table the outcomes live in. That number is weighted
by the realized conviction mix, so it could drift after outcomes were visible
without breaking a single hash — which made the chain decorative on the one code
path that emits verdicts. `C` is now read from the newest chained
`prereg_records` row per kind, taking the min-conviction (floor 0.0) band, which
is the all-decisions universe §6 fixes as the grading universe. Every emitted row
carries `claimed_source` and the record's `spec_hash`; the old recomputed average
ships beside it as `claimed_db_avg`, labelled a diagnostic, so drift between the
two stays visible. Snapshots now export `prereg_claims.csv` so an outside grade
reads the same frozen target rather than recomputing one.

For trend21 this moves `C` from 0.8177 to the registered 0.731 — a permissive
move, and it is landed deliberately together with the persistence null
(restrictive) and while every structural row is still `PENDING`, so no verdict
exists that either change could have been chosen to flip. The justification is
fidelity to the freeze, not the direction of the number.

## 3. Choice of null

The null for every structural kind is **persistence — the regime simply
continued — not a 50% coin.** SMA200 side, volatility regime, and liquidity
regime are all sticky states; a naive "nothing changes" rule already scores far
above 50% (for liquidity21 the persistence null equals the top-band claim of
0.876, and for liquidity21-crypto naive persistence scores 0.783 with 98%
agreement). Each spec's frozen `baseline` field states the null for that kind, and
the registry publishes `null_prequential` (the persistence null measured on the
same live window being graded) beside every row; the stricter (higher) null drives
any skill statement. Accuracy at or below persistence means the predictor adds
nothing, whatever the headline number says.

## 4. Minimum-evidence gates (min-n / min-days)

Verdicts come from the interval, never the point estimate:

- **Independence unit:** one observation per (symbol, horizon, UTC day). 408
  forecasts resolving on one day are ONE market observation, however many symbols
  they cover. Intervals are day-clustered (Wilson on the effective sample,
  matching `clusterstat` in the Go daemon).
- **min-n gate:** fewer than **30 independent observations** → `INSUFFICIENT`,
  no verdict claimed in either direction.
- **min-days gate:** fewer than **10 distinct UTC days** among resolved outcomes →
  no interval is published at all, and no interval means no verdict.

## 5. Verdict map (fixed before any outcome exists)

With `[lo, hi]` the day-clustered 95% interval on live accuracy and `C` the frozen
claim:

- `hi < C − 0.05` → **DECAYED** — the advertised table is retired from display and
  the failure publishes unfiltered.
- `lo ≥ C − 0.05` → **HOLDING** — the claim may keep rendering, now citing the
  live record.
- otherwise → **WIDE** — still experimental, no promotion.

Every verdict, including DECAYED, ships to the public anchor repository. A
pre-registered negative is publishable evidence; a post-hoc one is not.

## 6. No post-hoc slicing

The grading universe is fixed now: **every forecast of a registered kind recorded
since the survivorship epoch (2026-07-24), band "all", no exclusions.** The
following are ruled out in advance:

- No subsetting after the fact by symbol, sector, date range, market regime, or
  conviction band to find a slice where the claim held.
- No swapping the resolution rule, horizon, or accuracy metric for one that reads
  better on the realized data.
- No moving the survivorship epoch, the 0.05 tolerance, the min-n/min-days gates,
  or the interval construction after outcomes are visible.
- No quiet deletion: unresolved-by-design cases (ties, degenerate windows) are NOT
  graded rather than guessed, exactly as each frozen resolution rule states — and
  that rule cannot be reinterpreted post hoc.

Any analysis outside this universe is exploratory, must be labeled as such, and
cannot generate a verdict.

## 7. External anchoring and verification

`ops/anchor-publish.sh` is written to publish the verified chain head
(`prereg.log`), this document, and the full registry JSON to a public git
repository on every run. A digest in a third party's git history is the one
record the operator cannot rewrite after the fact — and **as of 2026-07-27 no
such record exists**: the repository has not been created, the script has never
pushed, and nothing here has been externally timestamped. Step 3 below is
therefore not currently performable by anyone; steps 1 and 2 only prove internal
consistency, which an operator holding the database can manufacture.

To verify independently:

1. `shasum -a 256 PREREGISTRATION.md` — must equal `specHash` of the
   `prereg-document` record in `GET /api/prereg`.
2. Recompute the chain: `entry_hash = sha256(prev_hash ‖ 0x1E ‖
   "ts=…|kind=…|specHash=…|note=…")` for every record; the head must match the
   newest `prereg seq=… head=…` line in the public repo's `prereg.log`.
3. Confirm the git commit dates of those published lines precede 2026-08-14.

## 8. AMENDMENT 2026-07-27 — naive-persistence null for the structural predictors

**What changed.** Until today the grader passed NO baseline for structural kinds
(`tools/accuracy_registry.py` hard-coded `null_acc = None`), so a structural
verdict could only compare live accuracy to its own frozen claim — HOLDING,
WIDE, or DECAYED. None of those can say "this predictor has no skill". Six of
the eight registered predictors were therefore unfalsifiable in the direction
that matters, while `internal/structregime`'s own liquidity caveat states that
**a naive persistence rule scores the SAME accuracy** — i.e. a HOLDING verdict
could have been reporting pure stickiness.

As of **2026-07-27** the regime-outcome worker freezes, at call time and beside
every call, the naive-persistence null: the "nothing changes" label the CURRENT
state already carries at the call bar (`regime_outcomes.naive_label`, computed
by `structregime.NaiveTrendAt` / `NaiveLiquidityAt` / `NaiveVol21At`). It is
resolved against the realized label by the same resolver as the call and is
never recomputed after the outcome is known. The registry publishes it as a
first-class `<kind>#persist` row through the identical clustered-CI path and
reads the structural verdict against it (VALIDATED / NO SKILL / FAILED); the
frozen-claim comparison is retained beside it as `claim_verdict`.

**Disclosure.** No such null existed between 2026-07-26 (this document's
registration) and 2026-07-27. Calls frozen in that interval carry
`naive_label = NULL`; they are excluded from the benchmark denominator, never
scored as null misses, and their kind grades **NO BASELINE** rather than a skill
verdict until enough post-amendment calls resolve.

**Why this is admissible under section 6.** It lands while all six structural
kinds are still PENDING at 0/30 resolved — before any structural outcome has
been graded, so no realized data could have informed it. It only makes verdicts
HARDER to pass: adding a baseline can lower reported skill and can never raise
it. Nothing about the grading universe, the horizon, the resolution rule, the
0.05 tolerance, or the min-n/min-days gates changed. Had this waited until after
2026-08-14 it would have been a post-hoc analysis choice that section 6 forbids.

## 9. AMENDMENT 2026-07-27 — structural grading clusters on non-overlapping horizon BLOCKS

**What changed.** `regime_outcomes` freezes one call per (symbol, kind, UTC-day)
at `horizon_days = 21`, so twenty-one consecutive call days share at least 17 of
their 21 forward sessions: they are **one** independent forward window, not 21.
Until today `clustered_ci()` / `design_effect()` clustered on the CALL DAY only —
there was no stride, block, or overlap term anywhere in the grader — so the
between-cluster variance was measured across clusters that are near-copies of
each other, and the effective n was inflated by roughly the horizon length.

As of **2026-07-27** structural grading folds per-day tallies into integer
horizon blocks (`block = utc_day // horizon_days`, `horizon_blocks()`) BEFORE
the design effect and the effective-N Wilson interval are computed
(`clustered_ci_blocks()`), so the cluster unit is a non-overlapping forward
window. This is the standard already used by `internal/volregime` and the PAIRS
study (non-overlapping 63d windows, quarter-block CIs) and already named as a
failure mode in PREDICTION_PROCESS.md point 8; it was simply absent from the one
live grader that will publish a structural verdict.

**Gate change.** The structural minimum-evidence gate in section 4 changes from
**≥10 distinct UTC days** to **≥10 non-overlapping horizon blocks**
(`MIN_DISTINCT_BLOCKS = 10`). Below that no interval is published and the
verdict is `INSUFFICIENT BLOCKS (k/10 non-overlapping horizon blocks)`. Every
structural row now publishes `distinct_blocks`, `block_span` and `horizon_days`
beside `distinct_days` in the registry JSON, PENDING rows included.

**Scope.** Directional grading is unaffected: at horizon 1 a block IS a day, the
`MIN_DISTINCT_DAYS = 10` constant is unchanged, and the auto-retire rule's
frozen digest still pins it. Nothing else about the universe, horizon,
resolution rule, claim freeze, null, or the 0.05 tolerance changed.

**Why this is admissible under section 6.** It lands while every structural row
is still PENDING at 0/30 resolved, so no realized structural outcome could have
informed it. It cannot raise any published number: accuracy is a point estimate
this change does not touch, effective n can only shrink, intervals can only
widen, and the only verdicts it can create are INSUFFICIENT ones — a would-be
HOLDING or VALIDATED can become INSUFFICIENT, never the reverse. Its cost is
real and accepted: at 21-day blocks, ten independent windows means the first
structural verdict cannot arrive before roughly 2027-01, well past the
2026-08-14 date the old day gate would have allowed.

## 10. AMENDMENT 2026-07-27 — the unmatched-null quarantine manifest

**What happened.** The naive-persistence null of section 8 arrived with a
write-path guard: a post-epoch structural row with no frozen baseline is refused
outright, and the outcome worker refuses to run at all while any such row exists.
That refusal was correct and is unchanged. But 1,157 rows already existed on the
wrong side of it. They were produced by a silent failure in the freeze path: the
snapshot used `INSERT OR IGNORE` on the per-(symbol, kind, UTC-day) dedup index,
so when a row for that day was already stored WITHOUT a baseline, a later freeze
that DID carry one was discarded and reported as ordinary deduplication. Nothing
downstream could distinguish the two, which is why the count reached 1,157 before
anyone measured it.

**What is NOT being done.** Those rows are not backfilled. A persistence label
computed today for a call frozen weeks ago is a hindsight baseline, not a null,
and it would silently improve the very comparison the null exists to make honest.
They are not relabelled, not deleted, and not graded. The amendment epoch
(`store.NullAmendmentEpoch`, 2026-07-27T00:00:00Z) is NOT advanced — moving it
would retroactively excuse an arbitrary window and is the same evasion wearing a
timestamp.

**What is being done.** The affected set is frozen ONCE, exactly as it stood,
into `regime_outcome_quarantine`, with a manifest digest
(`regime_outcome_quarantine_manifest`) computed over its exact membership. That
digest is appended to the pre-registration hash chain under the kind
`null-quarantine-manifest`, recording the row count and the explicit fact that
the set is **not growable**. The daemon recomputes the digest on every tick
before trusting the exemption; the unmatched-null invariant is redefined as
post-epoch NULL-baseline rows *outside* the manifest. So a row that goes
unmatched after the freeze still fails the guard, and any attempt to extend or
edit the exempt set fails verification in the daemon and appends a visible
AMENDMENT to this chain.

**Effect on the numbers: none.** Quarantined rows keep their NULL
`naive_label`, keep grading as **NO BASELINE**, and stay excluded from every
structural benchmark denominator — the same treatment section 8 already
disclosed for pre-amendment rows. The exemption excuses them from one thing
only: the startup invariant. It cannot lift, widen, or create any accuracy
figure. Its entire purpose is to let the guard remain absolutely strict for
every future row instead of failing forever on a fact about the past.

**Mechanism fix.** The dedup collision is no longer silent. When the stored row
has a NULL baseline and the incoming freeze carries one, the store returns a
distinct typed error (`store.ErrNaiveLabelDropped`), the worker records a DQ
event and counts it, and the row is left untouched rather than repaired. A test
constructs exactly that collision. This converts an unfalsifiable write-path
failure into a loud one at the moment it happens, which is the part of this
amendment that actually improves the method.

## 11. AMENDMENT 2026-07-27 — the blind final era for the discovery grid

**What was wrong.** Every gate in `researchx.Discover` — the Bonferroni-corrected
Wilson bound, `RegimeSurvival`, `FragileThreshold`, `Counterfactual` — was
computed on one pooled 2020–2026 sample, and the era decomposition then
partitioned *that same sample* post hoc. A rule reaching `shadow` had therefore
never been evaluated on a single observation the search did not see. Era
survival is a real robustness check, but it is an in-sample one: the grid
selects the rule knowing the eras it will later be judged on.

**The commitment.** `researchx.PreregHoldoutEra = "y2026"` (2026-01-01.., matching
`histfeat.EraY2026`; 15,173 observations / 28 weeks in the corpus as of this
amendment) is frozen HERE, before the next loop run. `Discover` splits the
corpus by WEEK — a week takes the era of its latest observation, the same
convention `gradeArm` already uses — runs the entire 48-rule grid and all four
existing gates on the observations OUTSIDE that era, and only then grades the
blind era. A candidate that cleared every in-sample gate must clear the SAME
corrected Wilson bound against a null measured on the blind era's own matched
observations (floored at 0.5, as everywhere else) over at least
`MinHoldoutWeeks` week-trials. Otherwise it is ledgered with `Survives=false`
and `RejectedBy="holdout"`, appears in `research_loop_judgments` like every
other kill, and carries the holdout weeks, bound and null that killed it.
Insufficient held-out history is a rejection, not a waiver.

**What did NOT change.** The grid is untouched: no new rules, no new atoms, no
new feature families, no threshold moved, `MaxAlpha` unchanged, the divisor
still grid × (1 + prior searches). This is not a search for a new predictor
because the current ones show no edge — the search space is byte-for-byte the
same.

**Why this is admissible under section 6.** The gate is one-directional by
construction. It adds a rejection and removes none, so a candidate can only
lose `Survives`, never gain it, and the in-sample corpus it is fitted on is
strictly SMALLER than before (2020–2025 instead of 2020–2026), which makes
every existing gate harder rather than easier. It touches no published accuracy
figure. `TestHoldoutNeverPromotes` asserts the subset property directly, and
`TestHoldoutEraRejectsAnEdgeThatDoesNotRepeat` shows a rule that pooling would
have promoted being killed by the blind era. The expected effect is fewer
survivors and a later first `shadow`, and that cost is accepted.

**Chaining.** This file's SHA-256 is registered on the pre-registration chain
under the kind `prereg-document`; editing it appends a visible AMENDMENT record
on the next `prereg-registrar` pass, so the holdout era cannot be retargeted
after a result is seen without leaving a chain entry.

## 12. AMENDMENT 2026-07-27 — the published interval's z is derived from a registered family-wise and sequential divisor

**What changed.** Every interval this registry publishes was computed from a
hard-coded `z = 1.96` (`wilson()`, `wilson_eff()` in
`tools/accuracy_registry.py`) and reported as "95% CI". That number was never
this surface's operating error rate, for two reasons that were both unpriced:

* **Family.** One grading cycle publishes ~10 rows at once — each directional
  horizon, its prequential-majority benchmark, every structural kind and its
  persistence twin. The chance that at least one of ten nominal-95% intervals
  excludes its null by luck alone is ~40%, not 5%.
* **Looks.** `ops/com.signaldeck.accuracy.plist` re-grades the same accruing
  rows every day. Re-testing a growing sample and reading the verdict off
  whichever look happens to cross the bar is optional stopping, and only the
  FAILED direction had a frozen stopping rule (the auto-retire rule, §
  `auto-retire-rule`); nothing bounded the VALIDATED or HOLDING directions.

As of **2026-07-27** the z is derived, not written down:
`divisor = family_size × looks`, `corrected_alpha = MaxAlpha / divisor`,
`z = probit(1 − corrected_alpha/2)`, floored at the uncorrected two-sided z.
`MaxAlpha = 0.05`. `family_size` is the number of rows the cycle actually
publishes, measured from those rows. `looks` is the monotone count of
`grading-look` records on the pre-registration chain — appended by the
registrar once per distinct registry `graded_at`, carrying its own counter, and
folded with `max()` over the record count, those counters, and the looks the
last published registry already declared, exactly the way
`ResearchLoop.priorSearches` folds its meta, `worker_runs` and durable sources.
Log rotation, a truncated chain, or a re-cut snapshot therefore cannot refund a
look. `researchx.DiscoverConfig.Divisor()` already priced the grid and the prior
searches inside discovery; this applies the same discipline to the surface that
actually publishes verdicts.

**Registration.** `MaxAlpha` and the divisor rule are frozen on the chain in the
`grading-protocol` record (`prereg.Protocol.MaxAlpha`,
`prereg.Protocol.MultiplicityRule`) and are compared byte-for-byte against the
grader's own constants by `grader_registration_error()` /
`require_registered_grader()`, alongside the existing `minIndependentN`,
`minDistinctDays` and `minDistinctBlocks` floors. A grader whose multiplicity
rule differs from the chained one by a single byte does not grade at all. The
same fields, plus the look count, ride in `repro/grading_protocol.csv` so the
snapshot path fails closed on exactly the condition the DB path does. Every
registry row and the JSON header publish `family_size`, `looks`, `divisor` and
`corrected_alpha`, so no interval travels without its price.

**What did NOT change.** No threshold was tuned, no data re-labelled or
re-banded, no predictor added or removed, no null redefined. `MIN_INDEPENDENT_N`,
`MIN_DISTINCT_DAYS` and `MIN_DISTINCT_BLOCKS` are untouched. The verdict map in
section 5 is untouched. No point estimate moves by a single basis point.

**Why this is admissible under section 9's argument.** It lands while every
structural row is still PENDING at 0/30 resolved, so no realized structural
outcome could have informed it. It is strictly interval-WIDENING: the divisor is
at least 1 and z is floored at the uncorrected 1.95996, so an interval can only
grow. Consequently it can turn VALIDATED into NO SKILL and HOLDING into WIDE and
never the reverse — it destroys favourable verdicts and creates none. The bar it
sets is fixed by this registration rather than by an outcome, which is the
opposite of threshold tuning: raising the divisor costs verdicts, so there is
nothing here to game. Its cost is real and accepted — with ~10 rows and a daily
cadence the divisor reaches the hundreds within a year, and the first VALIDATED
verdict this surface can publish is correspondingly further away.

**Chaining.** This file's SHA-256 is registered under `prereg-document`, and the
protocol change re-hashes the `grading-protocol` record, so both edits append
visible AMENDMENT records on the next `prereg-registrar` pass. Until that pass
runs, the grader refuses to grade at all: the chain names an older grader and an
unregistered `maxAlpha`, and `require_registered_grader()` exits rather than
publish a verdict under an unregistered protocol. That refusal is the intended
state, not a bug to work around by hand-writing a chain record.

---

## 13. AMENDMENT 2026-07-27 — a verdict may not be produced by a float64 epsilon, and the grader owns its own liveness precondition

Registered during the scheduled adversarial re-audit (`audits/2026-07-27-reaudit.md`).
Two changes to `tools/accuracy_registry.py`, both refusals, neither of which
moves an estimate.

**A. The boundary-null failure.** `verdict_for()` compared the interval against
the null with strict inequalities and no tolerance:

```
if hi < null_acc:  return "FAILED — significantly worse than the naive baseline"
if lo > null_acc:  return "VALIDATED — beats baseline"
```

The Wilson upper bound at p = 1 is EXACTLY 1 algebraically —
`centre + half = (1 + z²/2n + z²/2n) / (1 + z²/n) = 1` — and float64 returns
`0.9999999999999999`. So against a null of exactly 1.0, `hi < null_acc` was true
for **every possible record, including a perfect one**. Measured on a 15-block
fixture: `live_acc 1.0`, `null_acc 1.0`, `skill 0.0`, interval
`[0.7961, 0.9999999999999999]`, verdict `FAILED — significantly worse than the
naive baseline`. A row that ties its null by construction was published as
significantly worse than it, and FAILED is the verdict that carries
`retire=true`, which the daemon's model-health worker reads to stop publishing a
horizon. Against a 100% null the outcome was algebraically constant regardless of
the model's accuracy. The registry HAS published a 100.0% null (README, the
2026-07-26 run); only the 30-observation floor short-circuited before the
comparison was reached.

Two fixes, both at the arithmetic rather than at the symptom: `wilson()` and
`wilson_eff()` now return the p = 0 and p = 1 endpoints exactly, and
`verdict_for()` applies `VERDICT_EPS = 1e-12` **symmetrically** — a gap inside
the band yields NO SKILL, never VALIDATED and never FAILED. At 1e-12 the guard
sits ~4,000x above double-precision epsilon near 1.0 and ~10 orders of magnitude
below any accuracy difference this platform can measure, so it can only ever
suppress a verdict that arithmetic noise produced.

**This is not a loosened threshold.** It removes verdicts in BOTH directions and
adds none. It cannot manufacture a VALIDATED (the same epsilon guards that
branch), and the FAILED it removes is one no evidence supported. `MIN_INDEPENDENT_N`,
`MIN_DISTINCT_DAYS`, `MIN_DISTINCT_BLOCKS`, `MAX_ALPHA` and the multiplicity rule
are untouched; the verdict map in section 5 is untouched; no point estimate moves.

**B. The liveness precondition moved into the grader.** `ops/accuracy-registry.sh`
ran `tools/research_liveness.py` before the grader and refused to publish on
failure. That guarded one script, not the artifact: `data/accuracy_registry.json`
is what README.md and `/accuracy` read, and it can also be produced by invoking
the grader directly — which is how the registry dated `2026-07-27T01:01` came to
exist while the liveness check was failing on two claims (a narrated 48-rule grid
search with no judgment ledger, and a pre-registration that never forecast).
`require_research_liveness()` now runs inside `main()` beside
`require_registered_grader()`, so no path produces a registry while a narrated
search has no judgment record. It is read-only and can only suppress a
publication, never repair a ledger.

**Evidence at registration.** Regrading the live database with the amended
grader changed no verdict class: `directional-ensemble (1d)` INSUFFICIENT (27/30,
was 21/30 — data accrued, not a rule change) and `(1d, high conviction)`
INSUFFICIENT (12/30, was 9/30). All six structural rows remain PENDING at 0/30
resolved, so no realized structural outcome could have informed either change.

**Chaining.** Both edits re-hash the `grading-protocol` record and this
document's `prereg-document` record, appending visible AMENDMENT entries on the
next `prereg-registrar` pass. Until that pass runs the grader refuses to grade at
all, exactly as section 12 describes. That refusal is the intended state.
