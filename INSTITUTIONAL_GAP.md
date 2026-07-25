# Institutional spec — what's covered, what's missing, what's cargo cult

Audited 2026-07-25 against a full institutional quant-platform specification.
Split three ways, because the honest answer is not "build all of it": a spec
written for a bank running billions contains controls that are load-bearing at
that scale and pure ceremony at this one. Adopting those anyway is how a
project acquires the appearance of rigor without the substance.

## Already built (verified, not claimed)

| Spec requirement | Where |
|---|---|
| Point-in-time data, no look-ahead | `test_pit_fundamentals`, `test_no_lookahead`, `test_fill_timing` |
| Survivorship-bias control | `store.ResearchUniverse` / `TradableAt`; daily bars pruning-protected in code |
| Walk-forward, non-overlapping validation | `tools/revalidate_structural.py` — 54,969 obs, quarter-block CIs |
| Realistic execution simulation | commission + slippage on traded notional, `next_open`/`same_close` fill timing |
| Model registry + governance | `internal/modelhealth` + hourly worker, verdicts persisted |
| **Automatic model retirement** | fired live — directional ensemble retired at 48.0% vs 54.4% baseline |
| Concept/feature drift monitoring | `modelhealth/drift.go` — two-sample KS, n-adjusted critical value |
| Calibration monitoring | Brier skill + reliability curve, `/api/calibration` |
| Explainability | `AuditTrend` — signed contributors + historical analog, `/api/explain` |
| Forensic audit trail | hash-chained `prediction_ledger`, 235k rows, tamper-evident |
| Data quality / freshness checks | `dq-auditor`, staleness gates, split-corruption detector |
| Corporate actions | `internal/splitfix` + repair worker with verify-and-learn |
| **Kill switch + pre-trade risk limits** | `stock-trader/trader/risk_gate.py` — fail-closed, file-based kill switch |
| Slippage measurement | `slippage_log.jsonl`, executed-qty ledger reconciled against the broker |
| API rate limits, backoff, reconnect | Alpaca client: 429 backoff, shared limiter, websocket reconnect |
| CI with unit + integration tests | `.github/workflows/ci.yml` — `go test ./...` + web build |
| Secrets management | `.env` gitignored, constant-time token compare, no keys in repo |
| Backup + offsite | nightly VACUUM INTO + iCloud copy, 7-day retention |
| Data licensing controls | `internal/datalicense` + HTTP 451 guard on raw bar export |

## Genuinely missing and genuinely worth building

1. **Multi-source price validation.** Compare closes across providers and lower
   confidence on disagreement. Real value: it is the only check that catches a
   single provider being quietly wrong, which no internal consistency test can.
   Cheapest path is yfinance as a free second opinion.
2. **Canary deployment / staged rollout.** Today a new model version reaches
   100% of decisions at once. Matters more once anything trades real money.
3. **Automated nightly bias regression.** The bias tests exist but run on
   commit; running the full suite nightly against live data would catch drift
   in the *tests'* assumptions, not just the code.
4. **Dataset versioning + checksums.** Needed for exact backtest
   reproducibility when a provider silently revises history — which they do.

## Deliberately NOT building (cargo cult at this scale)

Each of these is genuinely important at a bank and actively harmful here,
because it adds operational surface without reducing any risk this system has.

- **Kafka / Kubernetes / multi-AZ failover.** One user, one machine, daily
  bars. This would add failure modes, not remove them.
- **Order-book replay simulation.** The strategies trade daily closes. There is
  no order book in the decision, so simulating one models nothing.
- **FIX protocol.** Alpaca's REST API is the execution path.
- **SOC2 / formal compliance program.** No customers, no custody, no
  regulated activity. Becomes real the day any of those change.
- **PagerDuty / 24-7 on-call rota.** macOS notification plus a Discord webhook
  is the proportionate version of the same control.
- **Separate risk-officer approval workflow.** One engineer. The control that
  actually substitutes for it is the automated gate that cannot be argued with.

## The honest summary

The spec's *principles* — fail closed, measure independently, never trust raw
data, retire what stops working, log enough to reconstruct any decision — are
implemented, and in several places more strictly than the spec asks. The spec's
*infrastructure* is sized for a different problem. The gap that matters is not
architectural; it is that the platform needs live forward evidence, and the
first of it arrives 2026-08-07.
