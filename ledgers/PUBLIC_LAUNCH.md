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
- [x] P4a internal/harrv: estimator + fit + nulls + losses (26 tests)
- [ ] P4b internal/harvar: FHS VaR/ES + Kupiec + Christoffersen
- [ ] P4c tools/rv_forecast_backtest.py (independent reimplementation)
- [ ] P4d PRE-REGISTER, then run. NOT the reverse.
      Two exploratory looks already spent (disclosed in the plan) and must be
      declared in the multiplicity divisor.
- [ ] P1.4 licence guards on the six vendor endpoints (currently closed by the
      allowlist; the extra layer is defence in depth, not the gate)
- [ ] P1.7 container provenance substitute for `resolvable:true`
- [ ] P3.5 404/error boundary, robots, sitemap, OG image, operator copy
- [ ] P5.1 OpenTimestamps the prereg chain head (currently seq 104)

## Measured facts worth not re-deriving
- DB 5.46 GB; bars 1d 2,708,273 rows / 2,947 symbols / 2018-07-26 onward.
- 1m bars cover only 22 real sessions -> intraday RV is NOT testable now.
- 1h bars: 1,127,732 rows / 1,112 symbols since 2025-06-23 (~300 sessions).
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
