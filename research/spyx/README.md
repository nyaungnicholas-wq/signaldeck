# SPYX — sealed-holdout evaluation of a strategy against SPY

Target: on ONE sealed out-of-sample block, beat SPY total return by **≥10.0 percentage
points (cumulative)** and achieve **net annualised Sharpe ≥1.00**, over identical dates,
after costs. Read [PROTOCOL.md](PROTOCOL.md) before anything else — it defines the splits,
the gates, the cost model, and the contamination status of the seal.

Interpreter: `signaldeck/.venv/Scripts/python.exe`. Repo commit at freeze: `ada8af42f5b8`.

## Reproducible commands

Run from the `signaldeck` repo root. **The seal must stay closed for every command in the
first block** — that is the point of the guard.

```bash
.venv/Scripts/python.exe research/spyx/evalcore.py
```

```bash
.venv/Scripts/python.exe research/spyx/strategies.py
```

```bash
.venv/Scripts/python.exe research/spyx/integrity.py
```

```bash
.venv/Scripts/python.exe research/spyx/panel_probe.py
```

```bash
.venv/Scripts/python.exe research/spyx/candidates.py
```

```bash
.venv/Scripts/python.exe research/spyx/audit_finalist.py
```

The grading run is the only step that opens the seal, and it may be run **once**:

```bash
SPYX_SEAL=OPEN .venv/Scripts/python.exe research/spyx/seal_run.py
```

## Files

| File | Role |
|---|---|
| `evalcore.py` | single source of truth — loading, cost model, metrics, seal guard |
| `strategies.py` | weight builders; every signal lagged one day |
| `integrity.py` | data-integrity audit → `out/integrity.json` |
| `panel_probe.py` | Phase 1 baseline panel over DEV and VAL |
| `candidates.py` | Phase 4/5 — pre-registered grid, floors, DSR, PBO → `out/finalist_manifest.json`, `out/trials.jsonl` |
| `audit_finalist.py` | Phase 5 adversarial audit → `out/audit.json` |
| `seal_run.py` | Phase 6 — grades the frozen finalist once → `out/SEAL_RESULT.json` |

## The seal guard

`evalcore.load()` raises `PermissionError` for any request with `end >= 2021-01-01` unless
`SPYX_SEAL=OPEN`. The guard runs **before** any file or network access, so a warm cache
cannot defeat it. `audit_finalist.py` check A1 asserts the guard actually fires.

`seal_run.py` adds three more refusals: it exits 2 if the seal is closed, exits 3 if
`out/SEAL_RESULT.json` already exists (the block is graded once, and a previous result is
never deleted), and exits 4 if the sha256 of `strategies.py` or `evalcore.py` differs from
`frozen_hashes` in the manifest — i.e. if the code changed after freezing.

## Frozen finalist

`dt_sma150_invvol` — 9 sleeves (SPY EFA QQQ IWM TLT IEF GLD DBC VNQ), each held when its own
price is above its 150-day SMA, inverse-volatility weighted within the in-trend set, monthly
rebalance, signal applied from the next session.

Leverage **3.29 is derived, not searched**: it vol-matches the book to SPY's own realised
volatility over DEV+VAL. This is deliberate — it stops the return gate being bought with
leverage, and couples both gates to the same underlying quantity.

Frozen hashes: `strategies.py` `bec0f3fb07048f9a…`, `evalcore.py` `ef5bfd309f9d9ea0…`.

## Costs

10 bps on |Δw| (charged on the *levered* weights, so leverage changes are themselves
charged), financing at risk-free + 90 bps annual on borrowed capital, idle cash earns the
risk-free rate. One convention, `evalcore.cost_returns`, used by every backtest here — this
closes audit finding **F4** (three engines previously carried three different conventions).

**Excluded and disclosed:** taxes; market impact beyond the flat spread; borrow cost for
shorting (nothing here shorts); queue position and partial fills. `seal_run.py` reports a
cost-sensitivity sweep at 5/20/40 bps and at a 150 bps financing spread so a result that
survives only the cheapest assumption is visible as one.

## Known limitations

- The sealed block is **not virgin**; see PROTOCOL.md §6. Prior sessions measured full-window
  aggregates covering those dates. This is a contaminated sub-period robustness test.
- The instrument set is **survivorship-selected** — all nine ETFs were chosen in the present
  and all nine still exist. No computation here removes that.
- The multi-sleeve joint sample begins **2006-02-06**, bounded by DBC's inception. The DEV
  block is ten years, containing one crisis.
- PBO and the Deflated Sharpe price only the 8 configurations in this grid. They do **not**
  price the sleeve-set, cost-model and rule-family choices inherited from prior sessions, so
  both figures are optimistic.
- A Sharpe measured on ~1,420 days carries a standard error near 0.42. The 1.00 threshold
  sits inside that band, so a single sealed window cannot separate skill from a favourable
  draw in either direction.

Nothing in this directory authorises live trading, and no live-money order is placed by any
of it.
