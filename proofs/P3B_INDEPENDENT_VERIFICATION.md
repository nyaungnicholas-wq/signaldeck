# P3B — Independent verification against the live database

**Filed:** 2026-08-04T17:3x-07:00
**Phase:** P3B (blocking)
**Relationship to `P3B_PIT_UNIVERSE.md`:** that document is the P3B deliverable, and it
verifies the rebuild with Go unit tests over fixtures plus a production run. This file
records an **independent check by a second agent, run as ad-hoc SQL against the live
4.2 GB database**, plus one verification trap worth recording.

Independent because the failure mode that matters here is a rebuild that is correct on a
fixture and wrong on production data.

---

## 1. Results

All queries read-only against `data/signaldeck.db`.

| Test | Query intent | Result |
|---|---|---|
| A | membership on a calendar day **before** the symbol's first daily bar | **0** ✅ |
| B | membership on a calendar day **after** the symbol's last daily bar | **0** ✅ |
| B2 | membership after `symbols.delisted_at` | **0** ✅ |
| C | universe size varies over time | ✅ — see below |
| D | later-delisted names present in historical years | ✅ — see below |

Population: **1,854,228 rows**, **1,777 distinct symbols**, 2018-07-26 → 2026-08-05,
`source = 'bars-1d'` only. `symbols.delisted_at` populated for **716** symbols.

### C — the universe shrinks, which is the whole point

| year | symbols | | year | symbols |
|---|---:|---|---|---:|
| 2019 | 712 | | 2023 | **998** |
| 2020 | 1,350 | | 2024 | 1,026 |
| **2021** | **1,501** | | 2025 | 1,073 |
| 2022 | 1,210 | | 2026 | 1,083 |

**2021 exceeds 2023 by 503 names.** A "today's list backfilled" universe cannot produce
that inversion. This independently corroborates the `TradableAt` repair described in
`CASE_STUDY.md` (`2021-06-01: 0 → 1,325`).

### D — the dead names are present, and decay toward the present

2019: 15 · 2020: 595 · **2021: 663** · 2022: 337 · 2023: 103 · 2024: 87 · 2025: 65 ·
2026: 22 later-delisted names carried in each year's universe. The monotone decay toward
today is correct: recent names have had less time to die.

## 2. A verification trap, recorded so the next auditor does not lose an hour

The first pass of TEST A reported **1,770 violations** and TEST B reported
**1,854,228** — i.e. "every row is wrong". Both were artifacts of the *verifier*.

`universe_membership.day` is epoch seconds at **midnight UTC**. `bars.ts` for a daily bar
carries the **market-session** timestamp. Compared as raw integers, every first-day
membership row looks early:

```
first-bar ts is 18000s after the midnight-UTC membership stamp   (UTC-5, EST)
first-bar ts is 14400s after the midnight-UTC membership stamp   (UTC-4, EDT)
```

The signature identified it: **exactly one row per symbol** (1,770 rows across 1,770
symbols), `max_days_early = 0`, `avg_days_early = 0.0`, and `member from == first bar`
on the same calendar date in every case — a uniform one-row-per-symbol offset is a
units bug, never a data defect. Re-running at `date(…,'unixepoch')` granularity returned
0 for both tests.

**Compare membership days to bar days at calendar-day granularity, never as raw
timestamps.** A verifier that emits 1.8M false violations is as dangerous as one that
emits none — the first gets ignored, the second gets trusted.

## 3. Corroboration of the residual gap

Independently reproduced the reused-ticker gap that `P3B_PIT_UNIVERSE.md` reports as
`RebuildResult.ReusedTickerDays = 61`:

| symbol | bar-days with no membership row |
|---|---:|
| ATC | 58 |
| ACACU | 1 |
| ELON | 1 |
| TRIL | 1 |

1,854,289 daily bars vs 1,854,228 membership rows. All 1,777 symbols with daily bars
appear in the table, so this is a per-day gap, not a missing name — consistent with one
`symbol_id` spanning two separate listings. **0.003% of rows**, counted rather than
swallowed.

## 4. A claim this verification withdrew

An earlier draft of this file asserted that cross-sectional features were still computed
against a non-PIT denominator and required a full rebuild, on the reasoning that
populating the table cannot retroactively repair a persisted feature.

**That assertion was checked against `P3B_PIT_UNIVERSE.md` §6 before filing and does not
hold.** Each cross-sectional feature is built per-day from the `features` rows recorded
that day — a same-day fact, PIT-valid by construction — with the single documented,
non-default exception of `--universe active`. No feature was computed against a
denominator that needed repair, so no rebuild is owed.

Recorded rather than deleted: the reasoning was plausible and wrong, and the only reason
it did not become a false finding in this proof set is that the concurrent work had
already measured it. The general lesson is the one this whole remediation exists for —
*measure it, do not reason about it*.

## 5. Status

| P3B done-when | Independent result |
|---|---|
| `universe_membership` populated | **CONFIRMED** — 1,854,228 rows / 1,777 symbols on live data |
| No future symbols in historical denominators | **CONFIRMED** — 0 |
| No dead symbols after exit date | **CONFIRMED** — 0 and 0 |
| Membership changes over time | **CONFIRMED** — 2021 exceeds 2023 by 503 |
| Cross-sectional features PIT-valid | **DEFERRED** to `P3B_PIT_UNIVERSE.md` §6, which measured it |
