# SignalDeck — Deep Report: The Process and The Result

Compiled 2026-08-03. Every number in §3 was read directly from the live
database, the live registry JSON, and the running daemon on this date, not
from prose in the repo. Where a document disagrees with the live artifact,
the live artifact wins and the disagreement is flagged.

> **Superseded in part, 2026-08-04.** This is a point-in-time record and is
> left as written. Two things it reports as open have since been closed, and a
> reader should not act on them as current:
>
> - §4 "the registry's `retire` flag reads `false` on every directional row
>   while `evidence_claims` still carries those models as refuted" — closed.
>   `internal/publication.BuildVerdict` now reconciles the two, retirement is
>   sticky (one-way, enforced by a `BEFORE INSERT` trigger), and all four
>   directional rows publish RETIRED against this same registry.
> - §3.6 "`/api/accuracy` returns **404**" — the route now exists and is
>   fail-closed.
>
> Everything in §3 about the MEASURED RESULT — the negative skill, the leg
> table, the zero-survivor research loops, the paper P&L — is unchanged. The
> fixes changed how honestly the system reports its record, not the record.

---

## 1. What the software is

A single-writer SQLite store (`data/signaldeck.db`, **3.69 GB, 101 tables**) fed
by a Go daemon (`signaldeckd`, `:8322`, ~90 internal packages) and read by a
Next.js 16 front end (`:8323`, 42–56 routes). It ingests market data, scores
every symbol on every horizon, records each score as a frozen prediction, waits
for reality, grades itself, and publishes the grade — including when the grade
is bad.

It is not an auto-trader. There is no order-placement path. The product is the
**measurement instrument**, not the signal.

Running right now: `signaldeckd` (uptime 5h 36m, revision `32fb93d`,
`resolvable: true, modified: false` — a clean attributable build),
`tickstreamd` on `:8321`, `/api/ready` → `{"ready":true}`.

---

## 2. The process

### 2.1 How one prediction is made

Seven stages, each with an explicit refusal condition.

| # | Stage | Module | Refusal condition |
|---|---|---|---|
| 0 | Ingest & validate | `marketcal`, `splitfix`, dq-auditor | any window containing a 1-day move > `maxSaneReturn = 0.65` |
| 1 | Feature vector | `pipeline.buildFeatureVector`, featureVersion 10 | `pred_raw`/`pred_cal`/`gbm_prob`/`meanrev_prob` excluded from `canonicalFeatureKeys` — no model can read its own output |
| 2 | Legs → independent P(up) | pressure, expectancy, forecast, GBM, meanrev, sentiment | unproven legs are **dropped, not down-weighted**: expectancy needs n≥5; GBM needs OOS lift>0 and ≥60 train rows; meanrev needs OOS lift>0 *net of 10bp cost*; sentiment needs ≥3 headlines ≤3 days old, scale capped 0.15 |
| 3 | Blend | `ensemble.WeightedProbability` + `adaptive` (6h) | weights ∝ `max(0, hitRate − 0.5)`, gated at n≥30/cell; `symbolagent` overrides after 40 resolved outcomes |
| 4 | Calibrate | prequential isotonic | below 30 resolved pairs returns identity and `calibrated=false`; blocks shrunk to base rate with pseudocount 25 |
| 5 | Record | `prediction_outcomes` + hash-chained `prediction_ledger` | probability frozen at write; append-only, never pruned |
| 6 | Grade | `modelhealth` (hourly), `tools/accuracy_registry.py` (daily) | see §2.2 |

The unusual part is stage 2. Most systems shrink a weak signal's weight toward
zero. This one deletes the leg. That converts a soft failure into a hard,
visible one.

### 2.2 The grading process — how a result is allowed to be published

This is the real engineering achievement, and it is where most of the code went.

**Independence.** The observational unit is one `(symbol, horizon, UTC-day)`,
not one row. Overlapping daily samples inflate n by roughly 63×. Measured
design effect on the live 1d record was **14.73×** — 13,065 raw rows collapsed
to an effective n of **887**. Every decision gate consumes effective N, not raw
count. (This was audit finding A1/A2, 2026-07-26; the module existed and was
correct but was wired only into *display* surfaces while *decision* gates used
raw counts. Thresholds were ~4× too tight.)

**Multiplicity.** Live today: `family_size = 12`, `looks = 7`,
`divisor = 84`, `corrected_alpha = 0.000595`, `ci_z = 3.4338`. Both counters
are monotone and folded with `max()` — publishing fewer rows cannot refund
multiplicity a wider family already spent, and log rotation cannot refund a
look already taken. The z is floored at the uncorrected 1.95996, so correction
can only ever *widen* an interval.

**Refusal floors.** `min_independent_n = 30`, `min_distinct_blocks = 10`.
Below either, the grader emits no interval and therefore no verdict — the row
publishes as `INSUFFICIENT`, not as a number without error bars.

**Null.** Not 50%. It is the prequential majority: each day's constant guess is
the majority class over days strictly before it. For structural predictors the
null is persistence, not a coin flip (e.g. liquidity21 null = 0.876).

**Pre-registration.** 35 records on a hash chain in `prereg_records`: 3
`prereg-document`, 9 `grading-protocol`, 8 `grading-look`, 3
`null-quarantine-manifest`, plus one per structural claim. The grader itself is
pinned by SHA-256 (`grader_sha256: 6908c6f9…`). Amendments append; they never
overwrite. 5 ed25519 `ledger_anchors`.

**Quarantine.** 1,157 unmatched-null rows frozen into a manifest with a chained
digest, marked "not growable". They are never backfilled, relabelled, deleted,
or graded — they grade NO BASELINE and stay out of every denominator.

**Auto-retirement.** Registered 2026-07-26, SHA `d02c3374…`: the first time a
directional row reaches 30 independent observations over 10 distinct days, if
the *upper* bound of its clustered Wilson interval sits below the prequential
null, the verdict is FAILED and the row carries `retire=true`. "No grace
period, no re-window, no threshold revision after the evidence arrives."

**Fail-closed publication.** If the grader cannot run, the README accuracy
table is *removed*, not reprinted stale.

**Anti-rescue.** `modelhealth.go` refuses `emitting: true` for any
`-inverted`, `-relabeled`, or `-flipped` variant unless it passes a full canary
gate (Wilson lower bound above both incumbent and null, ≥30 independent
observations spanning ≥14 days).

### 2.3 The research process

Two loops, both deliberately unable to promote their own findings.

**The discovery loop** (`research_loop_*`) runs a fixed 48-rule grid nightly
against the accumulated corpus, Bonferroni-corrected across the whole grid with
the divisor growing by the grid size on every look. `researchloop.go:32-35`
forbids self-promotion — the loop is shadow-only.

**The hypothesis ledger** (`research_ledger_hypotheses`) holds 19 named
hypotheses with priors, posteriors, replications and contradictions, updated by
86 evidence records.

**The "eighty percent" program** (`research/eighty/`, 58 hypothesis scripts)
targets *precision on issued calls*, not accuracy — abstention is the lever.
The bar is nine simultaneous conditions on a sealed era: precision ≥ 0.80;
precision − issued-subset base rate ≥ 0.10; corrected interval's lower bound
strictly above the base rate; ≥30 independent observations and ≥10 distinct
days; Deflated Sharpe clears its threshold for the trial count actually run;
PBO < 0.5; edge survives spread + commission + slippage at intended size;
pre-registered before grading; reproduces from a cold clone.

The program's own framing is the most honest line in the repo: *"80% raw
directional accuracy on next-day moves is not a stretch goal, it is a red
flag."*

### 2.4 The verification process

Adversarial re-audits on a schedule, each writing findings to `audits/`.

| Date | Findings | Most serious |
|---|---|---|
| 2026-07-26 | 16 | A9 CRITICAL: ngrok tunnel defeated loopback detection, making public reads + open signup default-true on a public URL. A5 HIGH: mean-reversion leg served on `pred_raw` — self-reference; 7/37 forecasts had `lift>0` from the flaw. A1/A2 HIGH: canary gate used raw counts. 13/16 fixed immediately. |
| 2026-07-27 | 9 | A1 HIGH: float64 edge case retired a model tied with its null. A2 HIGH: registry deadlock — deployed binary lacked the `multiplicityRule` field the grader required. 7/9 fixed. |
| 2026-08-03 | 5 | F-1 HIGH: `/accuracy` rendered a REFUSED registry as a successful one (blank fields, no refusal message). F-2 HIGH: `/api/recommendation` served a 10-day-old close under `AsOf: time.Now()`. F-4: WAL checkpoint starvation, 706 MB WAL. F-5: grader had not run for 33h (Task Scheduler `last=11/30/1999`). |

Four failure modes recur across all three rounds:

1. **Correct module, wrong wiring.** `clusterstat` was right and was used on
   display surfaces while decision gates used raw counts. The bug is never in
   the statistics; it is in which call site consumes them.
2. **Self-reference.** Components repeatedly found ways to read their own
   output — the meanrev leg on `pred_raw`, feature keys leaking model
   probabilities.
3. **Platform portability.** The macOS→Windows move silently disabled guards
   that depended on `caffeinate`, `sqlite3`, `pgrep`, `osascript`. Backup
   content verification, daily grading, and alerts all became no-ops that
   reported success.
4. **Missing freshness guards.** Stale data served without an age check, and
   stale *refusals* with no timeout — a 33-hour-old refusal raised no alarm.

Failure mode 3 is the expensive one. A guard that fails open is worse than no
guard, because it also buys false confidence.

---

## 3. The result

### 3.1 Scale actually achieved

| Artifact | Count (verified 2026-08-03) |
|---|---|
| Database | 3.69 GB, 101 tables |
| Bars | **13,225,071** — 1d: 1,851,416 over 1,777 symbols back to 2018-07-26; 1h: 838,878; 1m: 10,534,882 since 2026-06-05 |
| Symbol universe | 1,780 total — **716 delisted**, 329 active |
| Scores / outcomes | 1,758,362 / 1,867,879 |
| Predictions | 297,407, with 343,789 resolved outcomes and 293,074 hash-chained ledger rows |
| Structural forecasts | 23,793 issued, **0 resolved** |
| Research corpus | 326,043 `research_weeks` |
| Lineage edges | 15,092 |

The survivorship repair is real work: 21 delisted symbols → **716**, and the
1d bar table grew from 1.64M to 1.85M rows as delisted history was backfilled.

### 3.2 The measured predictive result

**Every one of the 12 published rows carries `ci_method: "withheld"`.**
Nothing on this surface currently has a confidence interval, and therefore
nothing has a verdict.

| Predictor | n | Accuracy | Prequential null | Skill | Distinct days |
|---|---:|---:|---:|---:|---:|
| directional-ensemble (1d) | 2,257 | **46.26%** | 52.84% | **−6.58 pp** | 9 |
| directional-ensemble (1w) | 912 | **47.81%** | 50.16% | **−2.36 pp** | 4 |
| directional-ensemble (1d, high conviction) | 297 | **55.22%** | 60.27% | **−5.05 pp** | 5 |
| directional-ensemble (1w, high conviction) | 63 | 47.62% | 46.83% | +0.79 pp | 4 |
| prequential-majority benchmark (1d) | 1,644 | 55.47% | 51.03% | +4.44 pp | 6 |
| filingsdrift21, liquidity21, liquidity21-crypto, trend21, trend21-crypto, trend63, vol21 | **0** | — | — | — | 0 |

The high-conviction row is the one that matters commercially — it is the tier a
user would actually trade — and it is **5 percentage points worse than a
constant guess.**

The evidence store carries the earlier, larger-window refutation with intervals
attached:

| Claim | Status | Value | n_eff | CI | Baseline |
|---|---|---|---|---|---|
| directional-ensemble-1d | **refuted / retired** | 0.4812 | 886.8 | [0.4485, 0.5141] | 0.5459 |
| directional-ensemble-1w | **refuted / retired** | 0.4624 | 605.9 | [0.4230, 0.5022] | 0.5444 |
| trend21-structural | weak / active | 0.8205 | **9.0** | none | backtest only |
| liquidity21-structural | weak / active | 0.7128 | **9.0** | none | backtest only |

Inversion is not a rescue and the system says so: inverting 48.1% gives 51.9%,
still 2.7 points below the 54.6% baseline. The original would have to sit below
**45.4%** for its inverse to beat a constant guess.

### 3.3 Where the failure lives — leg by leg

Measured over 132,156 pairs at horizon 1d across 29 distinct UTC days:

| Leg | Coverage | Precision | Base | Edge | ±2se |
|---|---:|---:|---:|---:|---:|
| Pressure | 100% | 0.4801 | 0.5280 | **−0.0479** | 0.0604 |
| Expectancy | 96.0% | 0.4955 | 0.5238 | **−0.0282** | 0.0369 |
| Forecast | 93.3% | 0.5150 | 0.5228 | **−0.0078** | 0.0284 |
| Sentiment | 8.5% | 0.4263 | 0.5011 | **−0.0748** | 0.0591 |
| GBM | 28.1% | 0.4819 | 0.5413 | **−0.0593** | 0.0612 |
| MeanRev | 29.1% | 0.5941 | 0.5369 | +0.0572 | 0.0695 |
| Blend (raw / calibrated) | 100% | 0.4975 / 0.4832 | 0.5280 | −0.0305 / −0.0448 | — |

Five of six legs are negative. MeanRev's apparent +5.7pp **did not survive
scrutiny**: its CI includes the baseline, and split by direction the edge is
exactly **+0.0000** in all five subsets — the textbook signature of a
classifier that has learned the class proportion and nothing else. One band
inverted catastrophically (UP 0.8–0.9: precision 0.2056 against a 0.7944 base,
n=1,138). Calibration made the blend *worse* (0.4975 → 0.4832) because isotonic
regression was fitting a map onto an anti-signal.

### 3.4 What the research loops found

**The discovery loop: 8 nightly runs, 48 rules, 192 judgments, zero survivors —
ever.**

| Day | Grid | Divisor | Observations | Survivors |
|---|---:|---:|---:|---:|
| 2026-07-28 | 48 | 144 | 166,285 | **0** |
| 2026-07-29 | 48 | 192 | 166,367 | **0** |
| 2026-07-31 | 48 | 240 | 166,671 | **0** |
| 2026-08-01 | 48 | 288 | 296,712 | **0** |
| 2026-08-02 | 48 | 336 | 296,727 | **0** |
| 2026-08-03 | 48 | 384 | 326,043 | **0** |

All 48 hypotheses sit at `status = rejected`, every one `rejected_by =
wilson_lower`. The best rule in the entire grid has a Wilson lower bound of
**0.1509** against a null of 0.50 — not marginal, not close.

**The hypothesis ledger: 19 hypotheses, 0 confirmed.** Eight rejected with
posteriors collapsed to 0.02–0.048 (including H001 "Pressure predicts
direction" at 0.02 and H002 "the weekly pressure-inverse call has edge" at 0.02
after 5 replications and 5 contradictions). Ten remain `uncertain`, one
`doubtful`.

**H018 / pairs trading — refuted with an unusually clean mechanism.** 938
trades over 26 blocks, 2019–2026. Correlation rank persistence is genuinely
real: ρ = +0.725, CI [0.706, 0.747]. But *cointegration* rank persistence is
ρ = −0.004, CI [−0.014, +0.011] — indistinguishable from zero. The cointegrated
arm returned +0.339%/trade at zero cost, but random same-sector pairs returned
within 0.017% of it. The persistence is shared market beta, not a tradable
spread. Win rate 44.1%, 48% of trades stopped out, P&L concentrated in 3 of 24
quarters.

**Self-referential features — confirmed harmless.** Ablation over 37 legs and
27,490 rows: admission-gate decisions unchanged, admitted legs' lift identical
with and without the shortcut features (+0.0076 vs +0.0069).

### 3.5 Paper trading

Both books, started at $100,000 on 2026-07-02:

| Book | Final equity | Return |
|---|---:|---:|
| flagship-1d | $97,786.28 | **−2.21%** |
| flagship-1w | $98,512.60 | **−1.49%** |

45 trades, 21 positions. Consistent with a −5pp directional deficit and 1.78×
turnover.

### 3.6 Current operational state

- Daemon up 5h 36m on a clean attributable build; `/api/ready` → ready.
- `health.json` → `ok: false`, stale workers: `signalbt-weekly`, `weekly-report`.
- `hud-sync` degraded — `trader-hud` on `:8787` is not running (cross-project dependency, not a SignalDeck fault).
- Worker fleet quiesce is timing out: `drain=30.0s blocked=18 fleet=97 drainTimedOut=true` — audit finding F-4, still open.
- `/api/accuracy` returns **404**. The route referenced in audit prose has never existed.
- `/api/honesty` returns live data but its own note says 43 independent symbol-days span **only 1 market day**.
- Survivorship is **not clean**: 327/335 graded symbols resolvable (97.6%); 8 inactive symbols carry no `delisted_at`. Worst-case bound on the effect: **0.28 pp** — small and honestly quantified.

### 3.7 The UX result

The front end grades itself against a rubric (`src/lib/rubric.ts`) via a
Playwright crawler over 42 routes. Score went **54.8 (Poor) → 80.4** across 9
passes.

The system's own audit of that number is the interesting part: **more than half
of the ~26 points came from fixing broken metrics, not from helping users.**
Pass 2 gained +12.3 points and the note reads "not one user was helped." The
metrics were measuring the crawler, not the product — `hasLoading` counted 5
pages because a healthy crawl never sees a loading state; `personalized` read
the wrong DOM attribute; lazy `Reveal` components hid content until the crawler
learned to scroll.

Weakest category: Personalization 3.8/10. All behavioral categories still read
`—` because fewer than 5 real sessions have been recorded, so the grade is
half-blind by design and says so.

SIMPLE mode folds 35 of 56 surfaces behind one labelled `/advanced` door. That
door scores highest in the app (84/100, 134 words, 1 screen). The review's own
verdict: *"The four surviving nav items still lead to pages built for the
author. Fix the destinations, not the nav."*

---

## 4. Two things that need attention

**The graded window has collapsed and no verdict can publish.** The 1d record
is down to 2,257 observations over 9 distinct days, one short of the
`min_distinct_blocks = 10` floor. Because there is no interval, the registry's
`retire` flag reads `false` on every directional row — while `evidence_claims`
still carries those same models as `refuted / retired`. The two surfaces
disagree today.

This is *probably* safe: the `modelhealth` gate retires on `edge < 0`
independently of the registry flag, and the measured edge is −0.0658. Defence
in depth is doing its job. But the registry is the surface a human reads, and
right now it reads as though nothing has been retired. Worth confirming which
gate the publication path actually consults.

**The structural claims — the entire remaining hope for an edge — have not
started grading, and the date moved.** 23,793 forecasts issued, 0 resolved.
Several documents cite a first-resolution date of 2026-08-07 or 2026-08-14.
The live liveness check disagrees:

```
The earliest unresolved call can first be graded on 2026-08-17 (+13.4 days).
```

Two weeks further out than the docs claim. Nothing is overdue and nothing is
broken — the calls simply have not matured — but any plan keyed to 08-07 is
keyed to the wrong date.

---

## 5. Assessment

**As a predictive system: refuted, and refuted by its own machinery.** The
directional ensemble loses to guessing the majority class by 6.6 pp at 1d and
by 5.1 pp in the high-conviction tier that would actually be traded. Five of
six component legs are individually negative. The sixth was a class-proportion
artifact. The 48-rule discovery grid has produced zero survivors across eight
nightly runs and a divisor that has grown to 384. Nineteen ledgered hypotheses
have produced zero confirmations. The pairs edge was traced to shared market
beta. Paper trading is down 2.2%. Every remaining structural claim is
backtest-only with an effective n of 9 and cannot be graded for another two
weeks.

**As a measurement instrument: this is the actual product, and it works.**
The system caught its own flagship model, quantified the failure with
cluster-corrected intervals, refused to publish numbers it could not support,
declined the obvious inversion rescue with arithmetic showing why it fails,
and traced the failure down to the specific leg and the specific probability
band. The self-audits found leakage the author had not looked for, and the
notes record when a headline number *failed re-derivation* — three of three
analysis passes flagged "always re-derive."

The gap between those two paragraphs is the whole point. Most systems in this
category would be reporting 55% accuracy right now by pooling overlapping
samples, benchmarking against 50% instead of the majority class, and quietly
dropping the days that went badly. Every one of those shortcuts was available
here, was closed deliberately, and closing them is what turned an apparent
positive into a measured negative.

**What that leaves.** A refuted signal and a trustworthy scale. A scale is
worth more than a wrong signal, but it is not worth anything until it weighs
something that isn't zero. The honest positioning is the one already written
into `SHIP_READINESS.md`: *a platform that measures its own failure and
retires negative-skill models* — a real moat, and not a trading edge.

The next real decision point is **2026-08-17**, when the first structural
forecast becomes gradeable. Until then there is no new predictive evidence to
be had, and the useful work is operational: close F-4 (WAL starvation), close
the `/api/accuracy` 404, get a backup onto physically separate media, and
reconcile the two ICs that still contradict each other (+0.0497, t=+8.71
cross-sectional versus −0.020 live).
