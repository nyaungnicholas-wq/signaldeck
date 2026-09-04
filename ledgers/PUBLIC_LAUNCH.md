# Public launch ledger

Branch `public-launch`. Resumable state for the competition build. Append only;
do not rewrite history here.

Plan: `~/.claude/plans/this-is-going-to-structured-blossom.md`

## Standing constraints
- G1 public site never exposes execution, money, positions, paper trading.
- G2 prereg seq 87 and the frozen directional code stay byte-identical.
- G3 every "we failed" surface stays published at full size.
- `tools/accuracy_registry.py` sha256 is pinned in the chain: NEVER edit it.
- Repo stays private. Prereg verifiability comes from OpenTimestamps instead.

## Done
- [x] P0.1 docker-build dirty-tree exemption (shared `sd_dirty_excluding_generated`)
- [x] P0.2 forward-test DB-lock retry; first eligible session no longer lost
- [x] P0.3 SIGNALDECK_LOG_FILE onto the volume
- [x] P0.4 README agent count 12 -> 100+
- [x] P1.1 public-surface allowlist + source-scanning coverage tests
- [x] P1.2 OPEN_SIGNUP closed explicitly on a published deploy
- [x] P1.3 451 guard fails closed (config-gated, not header-gated)
- [x] P1.5 REFUSED banner styled + e2e asserts styling
- [x] P1.6 grader runs in the container (ops/grade.sh, python3, collapsecheck)
- [x] P3.1 public landing page at `/`, dashboard moved to `/dashboard`
- [x] P3.2 public IA via src/lib/publicRoutes.ts (one list, both gates)
- [x] P3.4 waitlist: table, endpoint, validation, tests

## Blocked on Nicholas
- [ ] P2.1 Oracle Cloud Always Free account + Ampere A1 instance + SSH access.
      Claude cannot create accounts or enter card details.
      GOTCHA 1: opening 80/443 in the OCI Security List is NOT enough; the
      image ships restrictive iptables that silently DROPs. Also run
      `iptables -I INPUT 6 -p tcp --dport 443 -j ACCEPT` and persist it.
      GOTCHA 2: A1 capacity is often exhausted; retry or change AD.
- [ ] Alpaca / NVIDIA keys placed in the box's .env by hand.

## Next
- [x] P4a internal/harrv: estimator + fit + nulls + losses + horizon (29 tests)
- [x] P4b internal/harvar: FHS VaR/ES + Kupiec + Christoffersen (18 tests)
- [x] P4c tools/rv_forecast_backtest.py + cmd/rvcrosscheck
- [x] P4e store tables + rv-forecast-runner + rv-outcome-runner + the record API
- [x] P4d pre-registration WRITTEN and digest-pinned (internal/volprereg,
      f44e22b2...f9e8). NOT FILED: filing is a one-way chain write needing a
      deploy, CountResolvedRV()==0, a dry run and Nicholas's go-ahead.
- [x] P4f /volatility -- the page a non-expert reads
- [x] P4g adversarial checks (tools/rv_adversarial.py) -- all pass
- [x] P1.7 container provenance (docker-build proof + ops/oracle-verify.sh)
- [x] P3.5 404 / error boundary / robots / sitemap / OG-less polish
- [x] P3.6 mobile verified on all four public pages + e2e assertions
      Two exploratory looks already spent (disclosed in the plan) and must be
      declared in the multiplicity divisor.
- [ ] P1.4 licence guards on the six vendor endpoints (currently closed by the
      allowlist; the extra layer is defence in depth, not the gate)
- [ ] P5.1 OpenTimestamps the prereg chain head (currently seq 104)
- [ ] OG image (deferred; robots/sitemap/404/error are done)

## Measured facts worth not re-deriving
- DB 5.46 GB; bars 1d 2,708,273 rows / 2,947 symbols / 2018-07-26 onward.
- 1m bars cover only 22 real sessions -> intraday RV is NOT testable now.
- 1h bars: 1,127,732 rows / 1,112 symbols since 2025-06-23 (~300 sessions).
- Go/Python estimators CROSS-CHECKED on SPY: 1,928 values, 0 mismatches,
  max relative difference 9.4e-16. Rerun with cmd/rvcrosscheck.
- lineage.RevisionStamp() is "" under `go test`; the workers take an
  injectable Rev for that reason. Production is unaffected -- the daemon
  refuses to start from an unattributable build.
- HAR probe (exploratory, 150 symbols): 5-session target, Jensen-corrected,
  QLIKE HAR 0.51227 vs EWMA 0.58194 vs flat22 0.71293, DM t = -1.24 (NOT
  significant, and pooled rather than date-clustered so it is optimistic).
- 4.05% of daily bars have H == L, where a range estimator reads zero variance.
- Go 126 packages ok; python 376 tests ok.
- harrv estimator over the live table (2,624,945 bars / 2,383 symbols with
  >=250 sessions): 96.42% estimable, 3.44% flat H==L, 0.049% split-guarded,
  ZERO non-positive, ZERO negative-GK (the Parkinson fallback is a
  data-integrity path that has never fired on real bars).
- HAR bw and bm are NOT individually identified (overlapping windows of one
  series); their SUM is. Do not read meaning into a fitted weekly-vs-monthly
  split on a real symbol.


## HAR-RV backtest: what has been measured, and what it does NOT yet establish

Full universe run, 758 operating companies, survivorship-clean (NOT screened
on active=1), walk-forward, losses collapsed to ONE observation per DAY before
any test.

  h=1  971,151 forecasts over 1,398 day-clusters
  h=5  967,045 forecasts over 1,394 day-clusters

HEADLINE (QLIKE, HAR vs RiskMetrics EWMA(0.94); negative = HAR better):
  h=1  mean -0.0469  DM t = -8.79   bootstrap [-0.0584, -0.0355]
  h=5  mean -0.0431  DM t = -8.91   bootstrap [-0.0541, -0.0323]
Both inference methods agree, unlike on the 25-symbol smoke run where they
disagreed because of skew. Also significant against the random walk and the
flat 22-day window, and under MSE as well as QLIKE.

SUB-PERIOD STABILITY (the thing this repo's settled verdicts care most about,
because its own IC flips sign per sub-period): HAR wins in EVERY year, both
horizons, 2021-2026. No flip.
  h=1  t = -3.43, -1.92, -6.41, -7.62, -4.13, -2.29
  h=5  t = -2.17, -2.69, -4.86, -6.91, -4.67, -2.98

ESTIMATOR-ARTIFACT CONTROL (RV^CC, construction-independent, MSE), FULL
UNIVERSE: h=1 t = -5.77, h=5 t = -4.47. SURVIVES. The advantage is not an
artifact of the estimator it is scored on. 8,095 zero-return days dropped at
h=1, 5 at h=5, reported not hidden.

ADVERSARIAL CHECKS (tools/rv_adversarial.py, 60 symbols) -- ALL PASS:
  Hansen SPA, benchmark=EWMA challenger=HAR : consistent p = 0.0000
    (the direction that supports the claim; multiplicity of 3 nulls charged
     by a 5,000-rep stationary bootstrap)
  Hansen SPA, benchmark=HAR                 : consistent p = 0.5048
    (nothing beat HAR)
  Refit cadence 5 / 21 / 63 : t = -7.11 / -7.13 / -7.11 (h=1). FLAT, so the
    advantage is not tracking recent noise.
  Symbol clustering : 95% of symbols favour HAR; removing the 5 most extreme
    names makes it STRONGER (t -8.71 -> -9.98). Not carried by outliers.

WHAT THIS IS NOT:
  - It is a BACKTEST. Zero live forecasts have resolved.
  - It is NOT pre-registered. The pre-registration covers the LIVE test and is
    not yet filed.
  - Two exploratory looks were spent before the harness existed and are
    disclosed in the plan; they must be charged to the multiplicity divisor.

VaR coverage, recomputed with a CAUSAL quantile after the first run was
withdrawn for lookahead (commit b86457a):
  5% level: 740 symbols, breach rate 4.95%, 30 Kupiec rejections (4.05%)
  1% level: 642 symbols, breach rate 1.08%, 17 rejections (2.65%)
  against 5% expected by chance. The withdrawn version reported ZERO
  rejections, which is what a lookahead looks like.

STILL TRUE AND UNCHANGED BY ANY OF THIS: it is a BACKTEST, zero live
forecasts have resolved, nothing is pre-registered, and the two exploratory
looks spent before the harness existed must be charged to the multiplicity
divisor.


## Traps found the hard way on this branch (do not re-learn these)

- A route can be shadowed by a legacy redirect in next.config.ts. /deck and
  /risk were BOTH already redirected; pages there were unreachable. curl the
  route, never assume it resolved.
- lineage.RevisionStamp() is "" under `go test`, and the store refuses a row
  it cannot attribute to a build. The workers take an injectable Rev.
- NEXT_PUBLIC_* is inlined at BUILD time. Passing NEXT_PUBLIC_SITE_URL to
  `next start` silently produced a sitemap full of localhost URLs that looked
  entirely correct.
- This Next version's error boundary takes `retry`, not `reset`. Typing the
  props by hand meant tsc validated a contract the file invented and reported
  nothing. web/AGENTS.md says to read node_modules/next/dist/docs/ first.
- `git add -A` swept 657 lines of unreviewed worker output into a commit and
  broke the web typecheck for two commits. Read worker output before staging.
- A test that never fails is not a test: the VaR lookahead was caught by ZERO
  Kupiec rejections across 740 symbols, not by any gate.
