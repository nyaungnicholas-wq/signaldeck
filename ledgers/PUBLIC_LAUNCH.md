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
- [ ] P4d PRE-REGISTER the live test. Workers are deployed and
      CountResolvedRV() is the pre-flight: file only while it returns 0.
- [ ] P4f the web page a non-expert reads
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

ESTIMATOR-ARTIFACT CONTROL (RV^CC, construction-independent, MSE):
40-symbol subset only so far -- h=1 t = -2.61, h=5 t = -4.64, both survive.
THE FULL-UNIVERSE CONTROL HAS NOT RUN. Until it does, the headline is
consistent with HAR smoothing the proxy's measurement error rather than
forecasting the market, and that outcome has its own registered verdict name.

WHAT THIS IS NOT:
  - It is a BACKTEST. Zero live forecasts have resolved.
  - It is NOT pre-registered. The pre-registration covers the LIVE test and is
    not yet filed.
  - Two exploratory looks were spent before the harness existed and are
    disclosed in the plan; they must be charged to the multiplicity divisor.

VaR coverage: the first run was WITHDRAWN for lookahead (see commit b86457a).
Recomputation with causal quantiles in progress.
