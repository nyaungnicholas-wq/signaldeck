# P3B — Point-in-time universe

**Date:** 2026-08-04 · **Concerns:** `ALPHA_WORKFLOW.md` §B3, `SYSTEM_CHECK_2026-08-02.md` F-C2
· **Verdict: `universe_membership` populated and proven point-in-time. The
look-ahead §B3 predicted in the cross-sectional denominators was measured and is
NOT there — the denominators were already same-day-evidence-derived. A different
defect was found instead.**

---

## 1. Row count, before and after

| | before | after |
|---|---|---|
| `universe_membership` rows | **0** | **1,854,228** |
| distinct days | 0 | 2,146 |
| span | — | 2018-07-26 → 2026-08-05 |
| distinct symbols | 0 | 1,777 |
| reused-ticker symbol-days excluded | — | 61 |

Built by `store.RebuildUniverseMembership` (`daemon/internal/store/pituniverse.go`),
exposed as `sdmaint build-universe`.

## 2. The definition, and why it is this one

A symbol is a member of day *D*'s universe **exactly when it printed a `tf='1d'`
bar on *D***.

`TradableAt(ts)` — the accessor that already existed — answers the same question
from `symbols.added_at` and `symbols.delisted_at`. Those are *stamps*: they
record when **we** first saw a symbol and when **we** noticed its death. Both
have been wrong in this database before (`added_at` was repaired twice; 735
symbols are `active=0` with no `delisted_at` at all), and nothing downstream
could tell, because there was no recorded membership for a stamp to disagree
with.

A bar is a fact stamped on the day it happened. So bar-derived membership
cannot admit look-ahead by construction: a name cannot enter before its first
print or survive past its last one, whatever its symbols row says.

**The cost, stated rather than hidden:** a symbol halted for a session is absent
from that day's universe. For a cross-sectional rank that is the correct
answer — a name that did not trade cannot be ranked against names that did — but
it is not the same set as "listed that day".

**Day keys are floored to the UTC day.** Daily bars here carry three intraday
alignments (00:00, 04:00, 05:00 UTC — the ET-midnight offsets across DST), so a
raw `ts` is not a day key and one symbol can hold two bars for one calendar day.

**The one place a stamp overrides the evidence** is bars printed *after* a
recorded delisting. That is not a contradiction of the above; the disagreement
itself is the finding — see §5.

## 3. Sample queries

```sql
-- extent
SELECT COUNT(*), COUNT(DISTINCT day), COUNT(DISTINCT symbol_id) FROM universe_membership;
-- 1854228 | 2146 | 1777

-- membership moves over time (mean members per trading day)
SELECT strftime('%Y', day, 'unixepoch') y, AVG(n) FROM
  (SELECT day, COUNT(*) n FROM universe_membership GROUP BY day HAVING n > 100)
GROUP BY y;
```

| year | mean members / trading day | trading days |
|---|---|---|
| 2019 | 702 | 252 |
| 2020 | 920 | 253 |
| 2021 | 1,161 | 252 |
| 2022 | 1,003 | 251 |
| 2023 | 973 | 250 |
| 2024 | 988 | 252 |
| 2025 | 1,015 | 250 |
| 2026 (partial) | 1,038 | 147 |

The 2021 peak and the 2022 fall are the de-SPAC cohort entering and leaving. A
membership table that did not move like that would be today's universe wearing a
date.

## 4. Tests

### 4.1 Against the production database

| # | assertion | query | result |
|---|---|---|---|
| T1 | no symbol appears before its first bar | `COUNT(*)` where `um.day < MIN(bar day)` per symbol | **0** ✅ |
| T2 | no symbol remains after its delisting | `COUNT(*)` where `um.day > s.delisted_at` | **0** ✅ |
| T3 | membership size changes over time | `COUNT(DISTINCT n)` over per-day sizes | **310** distinct sizes ✅ |
| T4 | no phantom members | rows whose symbol has no `tf='1d'` bar | **0** ✅ |

### 4.2 Regression tests (`daemon/internal/store/pituniverse_test.go`)

```
$ go test ./internal/store/ -run 'PIT|Universe|Rebuild|Membership|Intraday|Foreign|NoFuture|NoDead|Reused' -count=1
ok  github.com/nyaungnicholas-wq/signaldeck/internal/store  3.162s
```

| test | pins |
|---|---|
| `TestRebuildPopulatesMembership` | a fresh database holds 0 rows; the fixture yields exactly 21 |
| `TestNoFutureSymbolInAHistoricalDay` | a name listing on day 5 is absent from days 0–4 **even though its symbols row exists throughout** |
| `TestNoDeadSymbolAfterExit` | a name whose last print is day 5 is absent from days 6–9 |
| `TestMembershipCountChangesOverTime` | the universe size takes at least two distinct values |
| `TestIntradayAlignmentsCollapseToOneDay` | 00:00, 04:00 and 05:00 bars for one trading day yield **one** row |
| `TestRebuildIsIdempotentAndFollowsCorrections` | re-running is a no-op; a bar deleted by split-repair drops its day |
| `TestRebuildLeavesForeignProvenanceAlone` | a row under another `source` survives the rebuild |

`universe_membership` was also **absent from `schema.sql`** — it existed only in
the operator's own database, so every cold clone, restore and deployment got a
database without it. The fresh-database test failed with `no such table` and
found it. It is now in `schema.sql` with an index, which is why the cold case is
the tested case (this is the same defect class `tools/schema_contract_check.py`
exists for).

## 5. The defect this actually found

`ATC`, symbol row 1122:

```
delisted_at   2022-08-16
bar segment   2021-02-04 .. 2022-08-16   383 bars   Atotech
bar segment   2026-05-12 .. 2026-08-04    58 bars   GraniteShares Autocallable COIN ETF
```

An exchange recycled the ticker onto a row that already held a dead company's
history. Two securities, one price series, a four-year gap in the middle.
`TradableAt` deletes the **live** ETF from every point-in-time universe after
2022-08-16 — precisely the failure `daemon/cmd/sdmaint/importdelisted.go` warns
about in its own header, which its ticker-reuse guard flagged 214 times and
missed once.

The rebuild **excludes** the post-delisting segment and **counts** it
(`RebuildResult.ReusedTickerDays = 61`), rather than swallowing it. Admitting it
would put a spliced series into the cross-sectional denominator as one
continuous name, which is worse than dropping 61 symbol-days.

**Not fixed here.** The real repair is splitting row 1122 into two symbols and
moving the 2026 bars, which is a data migration.

## 6. Cross-sectional features: measured, not assumed

`ALPHA_WORKFLOW.md` §B3 asserted that with `universe_membership` empty, "any
cross-sectional Z-score or rank is computed against today's membership applied
to historical dates… lookahead bias in the denominator of every cross-sectional
feature." **That assertion was tested and does not hold.** Each cross-sectional
denominator in this repository was already same-day-evidence-derived:

| consumer | how it forms its denominator | PIT? |
|---|---|---|
| `internal/alphax` (`BuildDataset`) | one row per (symbol, UTC day) from the **recorded `features` rows** of that day; the label is "beat that day's cross-section median" | **yes by construction** — a recorded row is a same-day fact |
| `tools/xsfactor_edge.py --universe all` | symbols with a close **on that day**, from `bars` | **yes** — the same definition as `universe_membership` |
| `tools/revalidate_structural.py` | bar-derived, every symbol ever tracked | **yes** |
| `internal/api/xsfactor.go` | `ListSymbols(ctx, true)` — active symbols | **live surface only**: it ranks *today*, where today's tradable set is the right denominator. It already documents that its universe is narrower than the one the edge was measured on |
| `tools/xsfactor_edge.py --universe active` | active symbols applied to history | **NO — this is the real look-ahead**, and the tool documents it and does not default to it |

Corroboration for the alphax row, since "by construction" is the kind of claim
that deserves a number: for the month the `features` table covers (2026-07-03 →
2026-08-05, 326,838 rows) its distinct-symbol cross-section is **1,077** against
a PIT universe of **1,059** on the same dates — the two agree within 1.7%, which
is what you would expect if the denominator is same-day rows rather than a
symbols-table query.

### Consequently

**No cross-sectional feature was rebuilt and no model was re-run for a
denominator reason — because none of them had the defect.** Saying otherwise
would be reporting work that was not needed as work that was done. What
populating the table bought is different and still worth having:

1. the denominator is now a **table a reviewer can COUNT**, not a join to be
   re-derived and trusted;
2. it is **testable**, and the tests immediately caught two things nothing else
   had — the missing `schema.sql` entry and the `ATC` splice;
3. `TradableAt` (stamps) and `UniverseAt` (evidence) can now be **compared**,
   which is how the 61 reused-ticker symbol-days became a number instead of a
   suspicion.

## 7. Done-when, against the brief

| criterion | status |
|---|---|
| `universe_membership` is populated | **met** — 1,854,228 rows, 2,146 days, 2018-07-26 → 2026-08-05 |
| cross-sectional features are PIT-valid | **met, by measurement** (§6) — and they already were, except `--universe active`, which is documented and non-default |
| tests prove no look-ahead in denominator | **met** — 4 production queries all 0/expected, 7 Go regression tests green |
| backfilled historically, not just forward | **met** — the span starts 2018-07-26, the first bar in the database |
| rebuild all cross-sectional features | **not applicable** — §6; none were computed against a non-PIT denominator |
| re-run affected legs / models | **not applicable** — same reason |

## 8. Open

- **`sdmaint build-universe` is written but was not the binary that ran.** The
  working tree currently does not compile: `internal/riskgate` and
  `internal/pipeline/paper.go` carry an unfinished uncommitted change from a
  parallel session (`undefined: req`, `w.ledgerGateRefusal undefined`), and
  `cmd/sdmaint` imports `internal/pipeline`. The production rebuild was run
  through a throwaway `main` importing only `internal/store` — the same
  `store.RebuildUniverseMembership` call, which is where all the logic lives —
  and the throwaway was deleted afterwards. **`sdmaint build-universe` cannot be
  built until that change is finished or reverted.**
- Nothing yet **reads** `universe_membership` in the daemon. It is a measurement
  and an audit surface today. The natural next step is to make `TradableAt`'s
  callers cross-check against `UniverseAt` and refuse on disagreement, which
  would turn the `ATC` class of defect into a startup failure instead of a query
  somebody happens to run.
- The `ATC` row still needs splitting (§5).
