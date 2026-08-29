# Workflow evaluation-integrity audit — 2026-08-29

Track B / step A1 of the live-trading plan. Everything below was **measured against the
current tree**, not recalled. Each finding states its evidence and whether it invalidates past
claims. Findings that came back CLEAN are recorded too — an audit that only reports problems
cannot be checked.

Method: OmniRoute worker lenses on the large Go files, direct measurement against
`data/signaldeck.db` for the data claims, adjudicated here. **Two claims were wrong and are
corrected in place**: a worker's top-ranked cost finding (F4) and my own initial HIGH grade on
F2. Both corrections are kept visible rather than quietly edited out.

STATUS 2026-08-29
| finding | severity | state |
|---|---|---|
| F1 fund / non-equity contamination | HIGH | **FIXED** |
| F2 added_at point-in-time gate | was HIGH, actually LOW | **RETRACTED + residual repaired** |
| F3 delisting handling | none | clean |
| F4 three cost conventions | MEDIUM | open — harness unification |
| F5 admitsLeg rank-before-lift | LOW | open — observability only |
| F6 forward-test benchmark bugs | none | clean, regression-tested |
| F7 split feasibility | informational | measured; constrains C1 |
| F8 Daily Rotation misses days | HIGH | **diagnosed, not changed** (see why) |

---

---

## F1 — Fund / non-equity contamination. SEVERITY: HIGH. **FIXED 2026-08-29.**

`research/dirfix/extract.py` filters funds by **name regex** (`FUND_PAT`). The file documents
why (using `fundamentals` presence traded fund bias for a far worse survivorship bias) and
that reasoning is sound. But the filter leaks.

Measured 2026-08-29 against `symbols where market='stocks'`:

| | count |
|---|---|
| symbols in `market='stocks'` | 2,943 |
| surviving `FUND_PAT` | 2,694 |
| **non-common-equity among survivors** | **353 (13.1%)** |

Breakdown of the 353: **254 SPAC units**, **58 warrants**, **12 rights**, **18
preferred/notes**, **11 closed-end municipal/bond funds** (BAF, BBF, BBK, BFY, BSD, BSE, EVY,
MTT, BPOPN …).

Why each damages a cross-sectional return study:

- **Warrants and rights** have option-like payoffs. Their daily return distribution is not the
  equity one and dominates any tail-sensitive statistic.
- **SPAC units** sit pinned near $10 pre-deal — near-zero-variance rows that inflate
  hit-rate-style metrics and depress measured volatility.
- **Closed-end muni/bond funds** are interest-rate instruments filed as stocks. They are
  precisely the class the ETF filter exists to remove, and it misses them because their names
  say "Trust" — which `FUND_PAT` deliberately excludes to protect REITs and ADRs.

Name matching is unreliable in both directions: a plausible extended pattern also
false-positives on real operating companies (AARD "Aardvark", CORE "Core Mark", CYBR
"CyberArk", AMWD "American Woodmark", XEC "Cimarex"). String matching cannot separate these.

**FIX SHIPPED.** `research/harness/instruments.py` classifies on the **exchange
ticker-suffix convention** — deterministic, not lexical — with name patterns kept only for
what a suffix cannot express (ETFs, closed-end funds). Rules, in order: dotted class suffix
(`BYN.U`) -> `dotsuffix`; 5-character ticker whose 5th letter is W/R/U/P -> `suffix-X`;
`CEF_PAT` -> `closed-end`; `FUND_PAT` -> `fund`; else common equity.

Two design points that cost a cycle each:
- **`L` is excluded from the suffix set** — `GOOGL` is common stock, not a preferred. The
  self-check asserts this by name so nobody re-adds it.
- **The dotted rule must run first.** `BYN.U` is 5 characters ending in `U`, so the
  5th-letter rule claimed it and mislabelled it. Caught by the self-check, not by reading.

Alpaca's asset endpoint was the first choice (it carries `class`) but needs a schema change,
a network backfill and a new ingest path. EDGAR SIC was the second and is **unusable**: only
951 of 2,943 stock symbols join `companies`, and none of the closed-end funds appear at all.
The suffix convention needs no new data.

`research/dirfix/extract.py` now calls it. Measured end-to-end:

```
instrument filter: 2940 -> 2381 symbols (559 non-equity dropped:
  fund=230, suffix-U=181, suffix-W=51, dotsuffix=45, closed-end=32,
  suffix-R=12, suffix-P=8)
rows 2427606  symbols 2381  days 1926  2018-07-26 -> 2026-08-28
delisted symbols present: 1519
```

Sanity assertions in `instruments.demo()`: QTS Realty Trust, CyberArk, Aardvark, Core Mark,
American Woodmark, Cimarex, AssetMark and Park Ha (Ordinary Shares) all stay in; the eight
known non-equity forms all resolve to their exact expected class. `python
research/harness/instruments.py` prints OK.

**Invalidates past claims?** Yes — any distributional or tail statistic computed on a panel
built before 2026-08-29 carries the 13% contamination. The known 52.7% ETF contamination was
fixed earlier; this residual was never measured until now. `panel.parquet` has been rebuilt.

---

## F2 — `added_at` point-in-time gate: FIRST GRADE WAS WRONG. Now REPAIRED. SEVERITY: was HIGH, actually LOW

**The original finding was wrong and is retracted.** It observed that `added_at` clusters
hard (928 symbols on 2020-07-27, 691 on 2019-01-02, only 603 distinct dates) and concluded
`added_at` was an ingest date being misused as a point-in-time gate by
`store.TradableAt` (`added_at <= ts`), `tools/alpha/xsection.py:66` and `h0471.py:146`.

The gate usage is real. The premise was not. Measured: **2,908 of 2,940** stock symbols with
daily bars (98.9%) already had `added_at` equal to their first 1d bar within 3 days.
`store.RepairAddedAtFromBars` exists precisely for this defect, is documented against it, and
had already been run. The clustering is genuine — those symbols' first bars really are on
those dates.

**Real residual, now fixed.** 24 symbols still had `added_at` later than their first bar by
more than 7 days — repair had run before their history was backfilled. 19 of the 24 were
stranded at 2020-07-27 with bars from 2019-01-02 (CPRX, ESPR, CCRN, JHG, NSA, PINC, PRA,
SEM, WSR, SANW, SSKN, SIC, BLD, CARM, EEX, NFBK, TBRG, LYRA, PARA), so `TradableAt` hid them
from the point-in-time universe for ~19 months they were actually trading. A conservative
distortion, not a look-ahead, but it silently shrank the pre-2020-07 cross-section.

Repaired 2026-08-29 with the existing tool, after a full `VACUUM INTO` backup to
`data/backups/pre_repair_added_at_2026-08-29.db`:

```
cd daemon && go run ./cmd/sdmaint repair-added-at -db ../data/signaldeck.db   -probe 2019-06-01,2020-03-01,2020-09-01,2021-06-01,2023-06-01,2025-06-01
```

`repaired: 1219, skipped_no_daily_bars: 3`. `TradableAt` moved 701 -> 713 at 2019-06-01 and
721 -> 733 at 2020-03-01. **Post-repair: 2,940 of 2,940 (100.00%) match, 0 late.**

**Lesson recorded:** the clustering was real evidence of nothing. Grading a finding HIGH on a
pattern that has an innocent explanation, without testing the innocent explanation first, is
the same error the audit exists to catch. The test that settled it — compare `added_at`
against `min(ts)` per symbol — cost one query.

---

## F3 — Delisting handling is CLEAN. SEVERITY: none

1,884 of 2,950 symbols carry `delisted_at`, and **zero** delisted symbols have 1d bars more
than 5 days past their delisting timestamp. `extract.py`'s docstring claim
("Survivorship-complete: keeps delisted symbols") is accurate. No action.

---

## F4 — Cost model: the plan's criticism was WRONG; the real gaps are different. SEVERITY: MEDIUM

The plan — and a worker lens, which ranked it the #1 money-losing assumption — claimed
`CostBps` is charged as a flat fraction of *equity* and therefore mismeasures relative to
traded notional. **Verified against the code: false for `daemon/internal/backtest/backtest.go`.**
That engine is single-symbol long/**flat** with a `bool` position and no sizing, so at a
transition equity *is* the traded notional and `eq *= (1 - cost)` is correct. Recorded because
the worker asserted it confidently and the plan repeated it.

The real gaps, all self-documented in the file header:

- one **flat constant for every symbol** — no ADV, price-level, or liquidity variation;
- **no spread modelled separately** (`CostBps` is a "proxy for commission+spread+slippage");
- **no market impact, no partial fills, no rejections** — every order fills fully at the next
  bar's open;
- Sharpe uses `rf = 0`.

The genuine defect is that the three engines **disagree on cost convention**:

| engine | convention |
|---|---|
| `internal/backtest` | flat per-side bps on full equity, binary long/flat |
| `internal/signalbt` | per-side bps on *the fraction of the book that turns over*; default 7.5bps |
| `internal/papertrade` | `SpreadBps` (7.5 stocks / 3.5 crypto, env-overridable) **plus** a size-dependent `ImpactBps` and an ADV participation cap |

`papertrade` is the only one modelling impact, and its own comment says a flat constant "is a
cost assumption a fill can beat, and the live book's fills were beating it." Research must call
`papertrade`, per the plan.

**Invalidates past claims?** Any number compared across two engines is suspect. Within one
engine, numbers are internally consistent.

---

## F5 — `admitsLeg` ranking-before-lift is INTENDED AND DOCUMENTED. SEVERITY: LOW (observability only)

`daemon/internal/ensemble/ensemble.go:369`:

```go
func admitsLeg(c Components, leg string, lift *float64) bool {
	if e, ok := c.RankEdge[leg]; ok {
		return e > 0
	}
	return admits(lift, c.RequireMeasuredLegs)
}
```

Confirmed: once a leg has a ranking grade, lift gets no vote **in either direction** — a strong
positive lift with marginally negative `RankEdge` is dropped, and a positive `RankEdge` with
negative lift is admitted. The header comment states this explicitly and gives the rationale
(the lift gate benched the forecast leg on 80% of rows, and the benched rows were the ones that
ranked). `admitsOptIn:380` has the same structure.

Verdict: **not a defect.** The residual issue is that the drop is **silent** — no log line,
metric, or diagnostic marks a leg excluded by the rank gate, so "the book is empty" and "a leg
was rank-gated" are indistinguishable from outside.

**Action:** emit a per-leg admission reason. Observability only; no behaviour change.

---

## F6 — The three forward-test benchmark bugs are FIXED and regression-tested. SEVERITY: none

All three are closed in `tools/forward_test.py` with asserts in its own `demo()`:

- **stale-feed fold** — `date(ts)` vs `(ts-18000)/86400` disagreement. The test now checks
  **both directions** (a name stale during the session is cut; a name stale the day before
  survives) plus a sticky-screen check.
- **zero-filling** — `:290` "Never zero-fill. A session with no benchmark cannot be graded";
  `:692` warns and skips; `:442` asserts the benchmark-less session is **absent**.
- **per-trade vs per-session averaging** — session-unit aggregation asserted.

No action. This is the file the forked grader copies, so it copies the fixes.

---

## F7 — Split feasibility for the C1 protocol. SEVERITY: informational (but it constrains the plan)

The C1 gate battery requires a final holdout of >=250 sessions with >=80% power, and the split
boundaries are hashed into the prereg record and cannot be revised afterwards. So the windows
had to be measured, not assumed, and measured **on the post-F1 clean universe** — the old
universe would have sized them wrong.

Distinct symbols carrying a 1d bar, by year: 2018 = 1, 2019 = 723, 2020 = 2054, 2021 = 2555,
2022 = 2326, 2023 = 2120, 2024 = 1713, 2025 = 1445, 2026 = 1207. The 2018 - 2019 thinness
looked disqualifying, but per-session breadth is what matters and it is healthy throughout:

| minimum clean-equity names in the cross-section | sessions | span |
|---|---|---|
| >= 100 | 1,925 | 2019-01-02 -> 2026-08-28 |
| >= 300 | 1,925 | 2019-01-02 -> 2026-08-28 |
| >= 500 | 1,916 | 2019-01-02 -> 2026-08-28 |

1,925 usable sessions. A feasible three-way split, to be frozen at C1:

| window | span | sessions |
|---|---|---|
| development | 2019-01-02 -> 2024-08-29 | 1,425 |
| validation | 2024-08-30 -> 2025-08-29 | 250 |
| final holdout | 2025-09-02 -> 2026-08-28 | 250 |

The holdout clears the >=250 floor **exactly**, with no slack. Two consequences the plan must
absorb: the holdout cannot be widened later without re-registering, and D3 must be pinned as a
frozen snapshot now (per plan) because every additional session of drift eats into the only
untouched window. Whether 250 sessions delivers >=80% power against delta = 2 bps/session is a
separate calculation to run against the validation-set standard deviation before the split is
hashed — if it does not, the plan says the holdout test is not run at all, and that outcome
must be discovered now rather than at finalist time.

---

## F8 — `stock-trader Daily Rotation` misses trading days silently. SEVERITY: HIGH. **DIAGNOSED, NOT CHANGED.**

The only process currently placing orders. Diagnosed 2026-08-29; deliberately left alone, for
the reason in the last paragraph.

First, a correction: the failing result is `0x800710E0`, not `0x800704A0` as first recorded.
That is `ERROR_TASK_NOT_RUNNING` (Win32 4320) — the task was **stopped**, not crashed.

Four independent defects compound:

1. **The trigger is `MSFT_TaskWeeklyTrigger`.** A task named "Daily Rotation" is scheduled
   WEEKLY. This is the cadence in the log, not a coincidence.
2. **`WakeToRun = False`.** The machine does not wake for it.
3. **`StartWhenAvailable = False`.** A run missed while the machine is off is lost **silently
   and permanently** — no catch-up, no error, no entry anywhere.
4. **The verifier is dead.** `stock-trader Verify Rotation Fired` has a **one-time**
   `MSFT_TaskTimeTrigger` dated `2026-08-10T07:15`. It fired once, returned `rc=1`, and can
   never fire again. The safety net that exists to catch exactly this has been inert for 19
   days.

Evidence from `stock-trader/data/rotation_cron.log`. Rebalances land on 07-31, 08-03, 08-06,
08-09, 08-10, 08-13, 08-14, 08-17, 08-20, then **a seven-day gap to 08-27**, whose entry reads
`held 7d` — the trader noticed the gap even though nothing else did. 08-21, 08-24, 08-25 and
08-26 are absent entirely, and a run that merely skipped would have logged `skip — market
closed` as 08-14 does. Nothing at all for 08-28. The box last booted 2026-08-28 19:14, i.e.
long after that morning's 07:00 PT trigger. Machine timezone is Pacific, so the trigger is
07:00 PT = 10:00 ET; there is no battery, so the battery settings are not implicated.

**Why this was not fixed automatically.** The obvious remedy — set `StartWhenAvailable = True`
so missed runs catch up — is **actively dangerous here**. A rotation that missed 07:00 would
then fire whenever the machine next woke, which on 08-28 would have been 19:14, and it would
rebalance a real book against stale decision prices hours after the close. Changing *when* a
trading bot fires is not a safe unattended edit, and the correct fix (wake the machine for a
daily trigger, and refuse to trade outside a permitted window rather than catching up blindly)
is Nicholas's call.

Remediation, for him to run and confirm:

```powershell
# daily, not weekly; wake the box; do NOT enable blind catch-up
$t = Get-ScheduledTask -TaskName "stock-trader Daily Rotation"
$t.Settings.WakeToRun = $true
Set-ScheduledTask -TaskName "stock-trader Daily Rotation" -Settings $t.Settings
# and re-arm the verifier on a RECURRING daily trigger, not a one-time one
```

The permitted-hours guard now in `execution/guards.check_calendar` is the pattern the rotation
needs: a late run should REFUSE, not trade.

---

## Open, not yet audited
- Corporate-action / split-dividend adjustment in the bar store — not yet measured.
- F4's shared evaluation function — designed in the plan, not yet built.
- F5's per-leg admission reason — not yet emitted.
- `SURVIVORSHIP_EPOCH` enforcement — confirmed as a constant, not yet traced to every consumer.

---

## Bearing on the plan

- **F1 and F2 must be fixed before the C1 splits are frozen.** Both change what the
  development / validation / holdout windows actually contain, and the splits are hashed into
  the prereg record and cannot be revised afterwards.
- F4 confirms the plan's "one shared evaluation function" requirement and corrects the stated
  reason for it.
- F5 and F6 downgrade two plan items from defects to an observability task and a no-op.

---
