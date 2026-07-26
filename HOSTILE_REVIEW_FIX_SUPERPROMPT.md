# SUPER PROMPT — Remediate the 2026-07-26 hostile review

**Verdict being answered:** REJECT for institutional deployment. Four independent
adversarial reviewers plus direct verification against the live 2.1 GB database.
Three user-facing surfaces emit arithmetically impossible or inverted numbers,
the tamper-evident ledger is not tamper-evident, and every confidence interval on
the live path is 3.8–4.9× too narrow.

**The one-line diagnosis, and it is the thing to internalise:** the written
methodology is better than most sell-side research; the *implementation diverges
from it* in ways that invalidate the headline claims. Disclosure is being used as
a substitute for correction — the numbers ship unadjusted beside the caveat
explaining why they are wrong.

---

## HOW TO USE THIS DOCUMENT

You are fixing a quantitative research platform under adversarial review. Follow
these rules on every task in it.

1. **Reproduce before you fix.** Every finding below was verified first-hand by a
   reviewer. Verify it yourself against the live database or a test fixture
   before changing code. If you cannot reproduce it, say so and stop — a fix for
   a defect that does not exist is a new defect.
2. **Fix the cause, not the symptom.** `signalbt` reporting −99.95% is not a
   display bug. Do not clamp the number.
3. **A withheld number beats a wrong number.** Where the honest output is "we
   cannot say yet", ship that. `null`, never `0`. "Not yet graded", never a
   backtest constant rendered beside a live forecast.
4. **Prove the fix with a test that fails before it.** Write the failing test
   first, watch it fail, then fix. The test must encode *why*, in a comment a
   future reader can act on.
5. **Never widen a claim to match an implementation.** If the code disagrees with
   a published constant, the published constant is what needs re-deriving or
   retracting — see C5 and H2.
6. **Say what you did not do.** Partial fixes are acceptable and expected.
   Silently narrowed scope is not.

**House invariants** (violating one of these is itself a defect):
- One observation per (symbol, UTC-day). Raw row counts are never a sample size.
- Every interval resamples **days**, never rows.
- Gates withhold with a stated reason. A gated metric is `null`.
- Accuracy ≠ return. The most accurate band can carry a negative forward return,
  and it does.
- Backtest constants and live measurements are never rendered in the same field.

---

## SECTION 1 — CRITICAL (all verified first-hand by reviewers)

### C1 · `signalbt` reports −99.95% while barely trading
`signalbt.go:240-265` compounds `eq *= (1 + target*f)` across rows that are
**one per (symbol, day)** — so ~1,000 symbols compound as if they were 1,000
sequential days. The benchmark is built per distinct day, so the two series are
not comparable at all. Live: `strategyReturn=-0.9995` at `turnover=0.00032`,
`gated=False` — i.e. displayed, not withheld.

Losing 99.99% of capital at 0.02% turnover is arithmetically impossible from
costs. **This is the most damaging single artifact on the site**; a reviewer who
opens this page stops the interview there.

**Fix:** portfolio accounting must aggregate cross-sectionally *within* a day
(weighted mean return across that day's positions, weights summing to ≤1), then
compound *across* days. Strategy and benchmark must share the same day index.
**Add a sanity assertion:** no surface may publish |return| > 100% for a
long-only or capped book; if the accounting produces one, gate the surface with
the reason rather than printing it.

### C2 · The tamper-evident ledger is not tamper-evident
The chain's only anchor is a row in the same SQLite file
(`meta.ledger_verify_checkpoint`). A reviewer demonstrated the break
constructively: delete all 200 honest rows, delete the meta key, regenerate 200
fabricated rows through the same `AppendLedger` — both the cached verify and the
full `?full=1` walk return `intact = true`.

The chain proves **internal consistency**. It cannot prove **anteriority**, which
is the only property an allocator cares about. `api/ledger.go:13` claims it
"exposes the tamper-evidence buyers/allocators require". It does not.

**Fix — pick one and be honest about which:**
- (a) Anchor externally: sign head hashes with a key held outside the DB, and/or
  publish the head hash to a third party or public timestamp on a cadence. Then
  the claim is true for everything after the first anchor.
- (b) Retract the claim. Rename to what it is — an append-only internal
  consistency chain — and state in the payload that it detects *edits*, not
  *fabrication by the operator*.

Do not leave the current wording. **This is the single biggest gap between what
the platform believes its moat is and what the moat actually is.**

### C3 · Calibration is inverted, non-monotone, and fit on the wrong variable
Three compounding defects:
- **496 of 929 live maps (53%) violate monotonicity** — the one property isotonic
  regression exists to guarantee. Symbol 12/1d: raw 0.3611 → 63.8%, raw 0.3652 →
  41.8%. A more bullish input yields a 22-point lower published probability, so
  **every ranking keyed on `cal_prob` is scrambled**.
- The global map is **fit on `cal_prob` and applied to `raw`**
  (`predict.go:417-429`), and each day's output trains the next day's map —
  recursive.
- Realized reliability is **monotonically inverted**: the 0.94 bucket resolves up
  48.1%; the 0.02 bucket resolves up 60.1%.

`/api/calibration` publishes bare `brier: 0.302` and omits the skill score, which
is **−0.226** — 23% worse than a constant base-rate forecast. The same codebase
computes `brierSkill` correctly on another endpoint, so the omission reads as
selective.

**Fix:** isotonic that is actually monotone (assert it on write — a non-monotone
map must fail, not ship); fit on `raw`; break the recursion (train strictly on
out-of-sample resolved outcomes, never on prior calibrated output); publish
Brier **skill** everywhere Brier appears.

### C4 · Every live CI is 3.8–4.9× too narrow (design effect measured)
The repo fixed intraday pooling (~60×) but never fixed **cross-sectional day
clustering**. Deduping to one row per symbol-day still leaves ~1,000 symbols
sharing one market move.

```
observed sd of daily accuracy : 0.0944
sd expected if independent    : 0.0191
DESIGN EFFECT                 : 24.4x  -> effective N ~533, not 13,008
(an independent reviewer measured 14.7x by a different method -> eff N 887)
```

Worse: the model is **not making 13,000 cross-sectional calls — it makes roughly
one market-wide bet per day.** Fraction of universe predicted UP on consecutive
days: 98.6%, 99.4%, 98.4%, then 8.5%, 6.9%, then 99.2%, 99.2%, then 2.1%.
Accuracy just tracks that day's up-rate.

| framing | accuracy | 95% CI | vs 54.6% baseline |
|---|---|---|---|
| as reported (raw N=13,008) | 48.06% | [47.20, 48.92] | far outside → "significantly negative" |
| corrected for measured clustering | 48.06% | [43.82, 52.30] | still outside → survives |
| day as the unit (19 bets) | 42.1% | [19.9, 64.3] | includes 50% → no significant skill |

**The platform's conclusion (no demonstrated directional edge) is correct and
survives all three framings. The advertised strength is a counting artifact** —
"ten independent confirmations" re-use the same correlated sample.

**Fix:** ONE cluster-robust statistics module that every surface must call. No
endpoint computes its own interval. **Day is the unit of resampling everywhere.**
Report distinct-day count beside every N, on every surface.

### C5 · Zero live out-of-sample evidence — every shipped accuracy is a constant
```
regime_outcomes: liquidity21 3642 recorded / 0 resolved | trend21 3672 / 0
                 trend63 3672 / 0 | vol21 3719 / 0
```
14,831 frozen calls, none resolvable before 2026-08-07. The numbers users see
come from lookup tables (`volregime.go:120-133`, `structregime.go:263`) while the
field is documented as "MEASURED walk-forward acc". **Backtest constants are
rendered beside live forecasts.**

**Fix:** rename the field so a constant cannot be mistaken for a measurement, and
render "not yet graded — backtest claim, first gradable 2026-08-07" until a live
record exists. The pre-registration chain (`internal/prereg`, `/api/prereg`)
already froze these claims — link them.

### C6 · Unauthenticated remote DoS via attacker-controlled cache keys
The SWR cache key is the **raw unvalidated query string**
(`composite.go:374`, `signal8home.go:292`, `predict.go:110`). Every novel key is a
cold miss that builds inline, holding one of only four read connections for
22–45s. Verified:
```
/api/composite/top        -> 200 X-Cache:hit   0.13s
/api/composite/top?zz=1   -> TIMEOUT          60.0s
/api/composite/top?zz=2   -> 200 (cold)       28.5s
```
Five junk parameters exhaust the read pool and stall the daemon including
`/api/health`. The entry map is never evicted, so each junk key leaks ~12 KB
permanently. Reachable anonymously while the tunnel is up.

**Fix:** build the cache key from a **whitelist of known parameters**, normalised
— never the raw query string. Cap concurrent cold builds (single-flight per key +
a global ceiling). Bound and evict the entry map.

### C7 · Risk stress-testing is a constant dressed as a computation
`stress.go:59-74` computes each holding's beta **against the portfolio itself**,
so Σ wᵢβᵢ ≡ Var(p)/Var(p) = 1 and `marketPnL(shock) ≡ shock` for every book ever
constructed. A concentrated book and an all-Treasury book both return exactly
−0.4000000000, rendered as "would move about −40.0% (~$40,000)".

Related: 95% historical VaR computed **from 3 observations** (`var.go:63-78`,
idx=2 at the 60-close minimum), rendered as a dollar figure.

**Fix:** betas against an **exogenous** market factor (SPY), a real minimum-sample
gate on VaR that withholds below it, or **delete the module**. A decorative risk
surface is worse than none.

### C8 · Credentials in cleartext across four surfaces
- **Session tokens stored raw.** `auth.go:74-77` generates via
  `hex.EncodeToString(rand)`, line 193 passes that value straight to
  `CreateSession`. A reviewer checked specifically whether the 64 hex chars were
  a SHA-256 digest — they are not. 76 live sessions, 30-day TTL, in a DB that is
  `-rw-r--r--` and copied verbatim to iCloud. **Any DB read is full account
  takeover.**
- **TV webhook secret persisted in every payload** — 162 of 162 `tv_signals` rows
  contain it; the endpoint also accepts it as a URL query parameter, putting it
  in tunnel and proxy logs.
- **ngrok authtoken echoed into world-readable logs** — 12 occurrences each in
  `tunnel.out.log` / `tunnel.err.log`.
- **`daemon/.env`** (LLM key + webhook secret) is `-rw-r--r--`, should be `0600`.

**Fix:** store only a SHA-256 digest of the session token, compare in constant
time, invalidate existing sessions on migration. Strip the secret before
persisting a webhook payload and reject the query-parameter form. Redact the
authtoken at the log boundary. `chmod 600` the env file.

---

## SECTION 2 — HIGH

- **H1 · No purge or embargo in walk-forward.** `gbm.go:392-399` —
  `train := samples[:trainEnd]; test := samples[trainEnd:testEnd]`, zero gap. A
  grep for `embargo|purge` across backtest, signalbt, researchlab, researchx, gbm
  returns **zero hits**. With 1-week labels sampled every 10 minutes, ~1,000
  training rows straddle each boundary carrying labels realised inside the test
  block. This is the de Prado failure mode by the textbook, and `Lift > 0`
  computed on it is the gate admitting legs to the live blend. **Highest-value
  single predictive change.**
- **H2 · A shipped cross-sectional factor has the wrong sign.** `xsfactor/edge.go`
  ships liquidity at +2.50pp (21d) / +3.04pp (63d); independent equity-only
  non-overlapping replication over 72,451 observations returned **−2.09pp and
  −2.61pp**. The low-vol leg replicated correctly (+2.57 vs +2.80), so the harness
  is sound and the disagreement is specific to liquidity. **Compounding: the
  script that produced these constants is not in the repo** — an unauditable
  shipped constant is indefensible. Re-derive with a committed script, or retract.
- **H3 · Disclosed downside understates measured loss by 5×.** trend21's accuracy
  ladder replicates almost exactly (72.59/89.98/94.10/97.07 vs claimed
  73.1/90.0/94.6/97.2) — but the direction-signed forward return at the top band
  measures **−2.05%** where the platform discloses **−0.39%**. Correct the
  disclosure. Also: the classifier always predicts persistence, so its accuracy is
  P(no SMA200 crossing); a driftless random walk at the measured median
  distance-to-barrier already gives ≈91.6%. **Only the 24pp discrimination spread
  is product.**
- **H4 · Adaptive weights are a winner's-curse estimator.** `adaptive.go:163-171`
  assigns weight ∝ `max(0, hitRate − 0.5)` with no interval, shrinkage or
  multiplicity control. At n=30, SE = 0.091; across 7 legs × ~5 regime cells the
  best pure-noise leg reliably scores ≈0.65 and receives the largest weight. Use
  James-Stein or empirical-Bayes shrinkage.
- **H5 · Sample floors satisfiable in ~3 independent days.** 158,204 resolved rows
  are 13,058 symbol-days — 12.1 rows per symbol-day (one case: 152 rows, 4
  distinct outcomes). `adaptive.MinCellSamples=30`, `symbolagent.MinPersonal=40`,
  `ensemble.MinCalibrationPairs=30` all count **raw rows**. `metalabel` is the only
  module enforcing a distinct-day floor — proof the team knows, and that the fix
  stopped at one module. Generalise it.
- **H6 · Self-reference loop in the mean-reversion leg.** `gbmtrain.go:66-72`
  carefully excludes `pred_raw`/`gbm_prob`/`meanrev_prob`/`alphax_prob` from GBM
  training — then `meanRevSamplesFromLabeled` reads exactly `r.Vec["pred_raw"]`
  forty lines below. `pred_raw` is the blend output computed with `MeanRevProb` in
  it. Closed loop, in the same file that states the doctrine.
- **H7 · CI is a smoke test, and three tests skip rather than fail.** `ci.yml` runs
  build/vet/test + web build. Zero mentions of `race`, lint, or coverage.
  `npm run lint` and the Playwright suite exist and are never invoked. `ingest/`,
  `marketdata/`, `aiagents/` have **no test files at all** — the entire vendor-data
  boundary. Worse: `structregime/expectancy_test.go:33` does
  `t.Skip("band's measured return is not positive in this build")` — a
  quantitative regression reports PASS. (In fairness: `go test -race` over store,
  api, pipeline, metalabel passes clean. The concurrency design holds; it just
  isn't defended by CI.)
- **H8 · Backups never verified, no restore ever tested.** No
  `integrity_check|quick_check|restore` anywhere in the backup package or ops
  scripts. Success is `os.Stat(target)` returning without error, so a
  corrupt-but-nonzero copy is accepted — and `KEEP=7` rotation then destroys the
  last good generation behind it. **This is an unbacked-up system that believes it
  is backed up.**
- **H9 · Nothing pages a human.** One macOS banner per 6 hours; remote transports
  unconfigured (54 log lines: `remote notify: no transports configured`). Errors
  already going unseen. One Mac, one process, one disk, lid-closed silence as the
  failure mode.
- **Also high:** mean-variance optimizer estimates returns and covariance on the
  same window with no shrinkage (error-maximization presented as allocation);
  research-ledger posteriors are products of hand-entered Bayes factors (55 of 86
  evidence rows carry n=0 with asserted BFs at round values *including the cap of
  20.0*), bypassing the exact beta-binomial integral that exists; the Bonferroni
  divisor is a caller-supplied argument and nightly re-testing of a shrinking
  shadow pool lowers the bar over time; `researchx` has 0% test coverage;
  `flatten` zero-fills absent features where zero is meaningful for `macro_*_chg`
  and `micro_*`, so one FRED read failure zeroes a contiguous time block and lets
  a tree split on a date range; canary grades a challenger against an incumbent
  observed for 0.17 days while applying a 14-day rule to the challenger only;
  paper fills contradict stored bars (23 of 44 match the bar open, errors to
  90.3 bps against a 7.5 bps cost assumption, no spread/impact/ADV cap/partials).

---

## SECTION 3 — WHAT IS SOUND (do not "fix" these)

Credibility requires knowing what to leave alone.

- **The volatility-regime edge is real and replicated independently** —
  55.95/64.85/67.72/74.55% vs claimed 55.8/64.3/66.8/72.0, against a genuinely
  balanced label (majority base 50.6–52.4%), so accuracy really does equal skill
  here. **The one honest edge in the platform; it survived every attack.**
- **The pairs study is exemplary** — non-overlapping 63-session blocks, block
  bootstrap, correct Engle-Granger critical values (−3.34, not the naive −2.86),
  formation-frozen hedge ratios, matched nulls including a least-cointegrated arm,
  918-name universe including delisted names, and a do-not-ship conclusion.
  Publishable methodology.
- **No lookahead survived direct attack in the execution path** — next-bar fills,
  stored (not re-derived) forward returns, `INSERT OR IGNORE` so split repairs
  cannot retroactively regrade, strategy windows that are cited published
  constants (Brock 1992, Jegadeesh–Titman 1993) rather than mined. The 237k-row
  ledger was hypothesised to be a backfill and disproved: every entry has
  `predicted_at − bar_ts ≤ 3d`.
- **Determinism and concurrency** — no RNG in production model paths (only
  `crypto/rand` for tokens); frozen hyperparameters, so no leakage via
  hyperparameter search; single-writer-connection design is correct; worker
  de-phasing uses an FNV hash of the name, not RNG.
- **`metalabel` and `macrofeat` are built to institutional standard** — distinct-day
  floors separate from trade counts, refusal to grade a filter on an edgeless
  primary, expectancy-per-decision rather than precision, exclusion of revised
  FRED series for want of point-in-time vintages.
- **The `/api` proxy resists SSRF** — dot-segment rejection, per-segment encoding,
  manual redirects, header allowlists both ways, deliberate refusal to attach the
  bearer token with a comment explaining the bug that taught them.
- **The platform published its own failure** — 48.1% against a 54.6% baseline,
  `emitting: false`, auto-retirement fired on the flagship. **Rarer in this
  industry than every defect above is common.**

---

## SECTION 4 — SCORES TO MOVE

| Dimension | Now |
|---|---|
| Statistical validity | 24 |
| Institutional readiness | 15 |
| Competitive moat | 19 |
| Production readiness | 21 |
| Scalability | 28 |
| Scientific credibility | 33 |
| Research quality | 34 |
| Software engineering | 57 |
| Overall architecture | 61 |
| Explainability | 79 |

Explainability is capped below 90 for one reason: **disclosure substitutes for
correction.** Fixing C1/C3/C4 moves statistical validity more than any new
feature ever will.

---

## SECTION 5 — REDESIGN TARGETS (structural, not patches)

1. **The entire evaluation/inference layer.** One cluster-robust statistics module
   every surface must call. No endpoint computes its own interval. Day is the unit
   of resampling everywhere.
2. **The calibration layer.** Isotonic that is actually monotone, fit on raw,
   evaluated strictly out-of-sample, Brier skill mandatory in the payload.
3. **The ledger's trust model.** Anchor externally or stop calling it
   tamper-evident.
4. **The risk layer.** Exogenous betas, VaR with a real minimum sample, a
   capacity/impact model — or delete it.
5. **`signalbt`.** Aggregate cross-sectionally per day, then compound across days.

---

## SECTION 6 — THE TWO BLOCKING QUESTIONS (answer these, don't dodge them)

**What stops a billion-dollar fund using this?** Capacity, before anything
technical. The only replicated edges — low-volatility and volatility-regime — are
public, capacity-constrained factors with implied IC ≈ 0.03–0.07. A fund
deploying billions cannot express them at size without erasing them. Below that:
no point-in-time delisting-inclusive data (**the survivorship bound is
unmeasurable from inside the DB, because the universe was seeded from 2026
survivors**), free IEX data at 2–3% of consolidated volume, single-machine
deployment, and a risk layer that returns constants.

**What stops publication in a top journal?** (1) Invalid inference — every CI
assumes independence the data does not have. (2) No reproducibility — the DB is
gitignored and the xsfactor script is absent, so a referee cannot re-derive the
headline numbers. (3) No novel claim survives — vol persistence and the low-vol
anomaly are decades old. **The exception:** the pairs study — correlation rank
persists (ρ +0.725) while cointegration rank does not (ρ −0.004), matched-null
design, honest do-not-ship conclusion. **That is a publishable negative result
today.**

---

## SECTION 7 — EXECUTION ORDER

**Ship-blockers, in this order:**
1. C1 `signalbt` — most damaging single artifact.
2. C6 DoS + C8 credentials — anonymous reachability while a tunnel is up.
3. C3 calibration monotonicity + Brier skill.
4. C4 cluster-robust module, then wire every surface to it.
5. C2 ledger claim — anchor or retract.
6. C7 risk — fix betas or delete.
7. C5 relabel constants as ungraded until 2026-08-07.

**Then:** H1 purge/embargo (highest-value predictive change), H8 backup
verification, H7 CI hardening, H2 re-derive or retract liquidity, H3 correct the
−0.39% disclosure, H6 break the meanrev loop, H4/H5 shrinkage and distinct-day
floors.

**Definition of done for every item:** the failing test exists and now passes;
`go build ./... && go vet ./... && go test ./...` is green; the live endpoint was
re-read and the number changed in the direction claimed; and what was *not* fixed
is written down.
