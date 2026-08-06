# P3C — Ticker reuse: one symbol row holding two securities

**Date:** 2026-08-04 · **Opened by:** `proofs/P3B_PIT_UNIVERSE.md` §5 · **Status: CLOSED**
· **Backup taken before the change:** `data/signaldeck.db.bak-presplit-20260804` (4,331 MB)

---

## 1. The defect

Symbol row 1122, ticker `ATC`, held **two different companies**:

```
delisted_at   2022-08-16
segment 1     2021-02-04 .. 2022-08-16   383 daily bars   Atotech
segment 2     2026-05-12 .. 2026-08-04    58 daily bars   GraniteShares Autocallable COIN ETF
gap           1,365 days
```

An exchange recycled the ticker. `symbols` is keyed `UNIQUE(symbol, market)` and
ingestion resolves by ticker string, so the new company's bars landed on the
dead company's row. The consequences were not hypothetical:

- `store.TradableAt` deleted the **live** ETF from every point-in-time universe
  after 2022-08-16, because the row it lives on is marked delisted.
- Any backtest touching `ATC` read one continuous price series with a four-year
  hole and a $21 → $24 discontinuity across it.
- **`forecasts.n_train = 389`** for the 1d horizon. With `forecast.warmup =
  smaLong = 50`, 389 labelled samples requires 441 daily bars. The live security
  has 58. 383 + 58 = 441 exactly — the model was fitted straight across the
  splice, and the number proves it.

## 2. Scope, measured

69 tables carry `symbol_id`. Exactly 15 held any row for 1122; total **1,092
rows**. `predictions`, `prediction_outcomes`, `features`, `scores`,
`composite_scores` and `prediction_ledger` held **zero** — checked explicitly,
because `prediction_ledger` hashes `symbol_id` into its chain and a re-pointed
row would have broken it from that sequence onward.

## 3. Which row keeps the ticker

**The live ETF keeps `ATC`.** Ingestion addresses the string — this row's own
`dq_events` record `backfill: daily: alpaca: bars ATC: status 429` from today.
Move the live security off `ATC` and the next poll re-creates an `ATC` row and
re-splices within the hour. The **dead** company is the one that moves, to
`ATC.220816` (`<TICKER>.<YYMMDD>` of the delisting).

## 4. What was applied

Rehearsed first against a consistent online backup copy of the full 4.3 GB
production database, verified, and only then applied. `sdmaint
split-reused-tickers`, one transaction, through store's single writer:

| table | moved to `ATC.220816` | stayed on live `ATC` | deleted |
|---|---|---|---|
| `bars` | **383** | 153 (1d 58, 1h 26, 1m 69) | — |
| `research_weeks` | **66** | — | **11** |
| `news` | **5** | — | — |
| `news_symbols` | **8** | — | — |
| `expectancy` | — | — | **54** |
| `symbol_models` | — | — | **2** |
| `forecasts` | — | — | **2** |
| `regime_state` | — | — | **1** |
| `confluence_setups` | — | — | **1** |
| `rankings`, `tv_ratings`, `breakouts`, `dq_events`, `tv_exchange` | — | 23 | — |

Boundary is the **end** of the delisting UTC day
(`(delisted_at/86400)*86400 + 86399`), not the stamp: daily bars carry
00:00/04:00/05:00 UTC alignments, and Atotech's own last print is stamped
2022-08-16 **04:00**. A midnight cutoff would have filed the dead company's
final bar under the ETF.

### Three deletion categories, and why each is a deletion

- **`expectancy` (54), `symbol_models` (2)** — aggregates and fits over the
  spliced series, with no timestamp to partition by. Both workers rebuild from
  bars; verified self-healing.
- **The 15 spliced latest-state rows** (`research_weeks` 11, `forecasts` 2,
  `regime_state` 1, `confluence_setups` 1) — these sit on the **right** side of
  the boundary and look like the live security's own, which is why the first
  pass of this migration left them behind. They were computed from trailing
  windows reaching back across the splice, and **they cannot self-heal**: with
  58 daily bars the live row fails `histfeat.minTrailingBars = 60`,
  `regime.MinBars = 60`, and forecast's `warmup = 50` plus a 5-fold evaluation.
  Every refresh path returns `ok=false` and leaves the stale value **forever**.
  Deleted in a second pass (`-purge-spliced-only`).

### Deliberately left contaminated, and recorded rather than hidden

`rankings` — 7 rows published today whose `ret3m` was computed on the spliced
series. They are an append-only record of what the platform actually published.
Rewriting published output is worse than recording that one day is
contaminated. No `score_outcomes` rows exist for this symbol, so nothing grades
against them.

## 5. The root cause, fixed where every caller routes through

Splitting the row repairs today's instance. It does not stop the next one — and
**716 rows carry a delisting stamp**, every one of them reachable the same way.

`store.UpsertSymbol` was `ON CONFLICT(symbol, market) DO UPDATE SET active=1`.
When a recycled ticker arrives, that silently **resurrects the dead row** and
re-splices the series. `UpsertDailyUniverseSymbol` had the identical hole.

Both now refuse to reactivate a row carrying `delisted_at`:

```sql
active = CASE WHEN symbols.delisted_at IS NULL OR symbols.delisted_at = 0
              THEN 1 ELSE symbols.active END
```

A genuine re-listing under the same ticker now needs an operator to clear
`delisted_at` deliberately. That is the intended cost: resurrection should be a
decision, not a side effect of a poll. `TestUpsertSymbolWillNotResurrectADelistedRow`
pins both halves — the delisted row stays inactive, and a symbol that was never
delisted still reactivates normally, so the guard is narrow.

## 6. Not ticker reuse: three stamps that were merely early

The naive detector — "any bar after `delisted_at`" — reported **hundreds** of
symbols. That was an off-by-one in the *query*, comparing a raw 04:00 bar
timestamp against a midnight stamp. Comparing **floored UTC days** gives the
truth: **4 symbols**, of which only ATC is reuse.

| symbol | gap to next bar | later trading days | verdict |
|---|---|---|---|
| ATC | 1,365 days | 58 | **ticker reuse → split** |
| ACACU | 1 day | 1 | early stamp → nudge |
| ELON | 3 days | 1 | early stamp → nudge |
| TRIL | 3 days | 1 | early stamp → nudge |

A delisting date comes from a filing; the tape can print for a settlement day or
two afterwards. Splitting those three would have invented a second company out
of a rounding difference. `NudgeDelisting` moves the stamp forward to the last
bar day, refuses to move a stamp backward, and refuses a gap wide enough to be
reuse.

## 7. Verification (production, after the change)

```
ticker reuse remaining                     : 0
symbols active=1 AND delisted_at>0         : 0
universe_membership rows                   : 1,854,289  (2,146 days, 1,778 symbols)
  T1 members before their first bar        : 0
  T2 members after their delisting         : 0
  T4 phantom members (no bars)             : 0
spliced latest-state rows on live ATC      : 0  (research_weeks/forecasts/regime_state/confluence_setups)
historical ATC.220816                      : 383 bars, 66 research_weeks, 5 news
pragma quick_check                         : ok
go test ./internal/store/                  : ok (full package)
```

The membership grew 1,854,228 → **1,854,289**, exactly **+61**: the 58 ETF days
that the delisting stamp had been suppressing, plus the 3 days recovered by the
stamp nudges. The number is the fix, restated.

## 8. Reversibility

Rows were **re-pointed, not deleted** for everything that moved, so the inverse
is one `UPDATE` per table from symbol id 1781 back to 1122 plus restoring
`delisted_at`. The 71 deleted rows are the only non-invertible part, and
`data/signaldeck.db.bak-presplit-20260804` was taken immediately before the
change specifically to cover them.

> **UPDATED 2026-08-05 — that copy has been retired, and the cover replaced.**
> The 4.3 GB presplit copy was deleted at the operator's instruction. Before it
> went, symbol 1122's rows were dumped from it into
> `proofs/P3_RETIRED_COPY_EVIDENCE.json`: **137 rows across the six tables this
> proof records deletions in** — `expectancy` 54, `research_weeks` 77 (of which
> §3 deletes 11), `symbol_models` 2, `forecasts` 2, `regime_state` 1,
> `confluence_setups` 1 — plus the `symbols` row itself with `delisted_at`
> intact. That is a superset of the 71, so the sentence above still holds with a
> 111 KB tracked artifact standing where a 4.3 GB untracked file stood.
>
> Independently, `data/backups/signaldeck-20260804-134451.db.gz` was written at
> 13:45 on 2026-08-04 and the presplit copy at 19:26, so the managed backup line
> also predates the split. Its bytes verify against the recorded digest and
> `ops/restore-rehearsal.sh` restores it clean. Note this is a compressed
> generation on a rotation, so it is a second line, not the record — the JSON is
> the record. Note that the new symbol id is not
reproducible — `symbols.id` is a plain INTEGER PRIMARY KEY, so an
undo-then-redo yields a different id and any record naming 1781 must be
rewritten rather than replayed.

## 9. Open

- `sdmaint` was run against a live database with the daemon **running**. It
  succeeded (single transaction, 5s busy timeout, `quick_check ok`), but the
  documented posture for this tool is daemon-stopped, and a busier moment could
  have hit `SQLITE_BUSY`. Future splits should stop the daemon first.
- The 7 contaminated `rankings` rows are left in place by decision (§4).
