# Data sources and what may be done with them

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** AUTHORITATIVE — freeze lifted 2026-08-04
> **Scope:** Data-source inventory, licensing classification, and raw-export controls. The licensing table and the HTTP 451 guard are not frozen and remain in force.
> **Frozen claim classes:** FC3 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, statuses in `proofs/P10_FREEZE_LIFT.md` §4
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE — caveats are the frozen classes above

> ## ⚠ P0 freeze (2026-08-04) — LIFTED 2026-08-04 by `proofs/P10_FREEZE_LIFT.md`
> Remediation complete; freeze lifted. Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **survivorship boundary · point-in-time data.**
> The licensing table and the HTTP 451 guard are **not** frozen and remain in force.
> Open defect: the "Survivorship boundary" section describes `SURVIVORSHIP_EPOCH`
> protecting *published registry claims*, which is correct — but it does **not**
> protect any backtest, and every headline structural band table in this repository
> is a backtest. See `ALPHA_WORKFLOW.md` §B2.
> Authority: `proofs/P0_FREEZE.md`, **lifted 2026-08-04** by `proofs/P10_FREEZE_LIFT.md`.

Authoritative classification lives in code at
`daemon/internal/datalicense/datalicense.go`, next to the guard that enforces
it — this file is the readable mirror, not the source of truth. Tests fail if a
Licensed or Restricted source is ever flagged redistributable.

The Status column was added 2026-10-22 after the data-licence review and updated
2026-10-23 for the Alpaca and Kadoa decisions (operator-only content, below). It is
not mirrored in `datalicense.go`, which still lists every source below. The `fred`,
`finra` and `cboe` rows are Restricted in `datalicense.go` since the 22 October
2026 retirement, and `kadoa` since 23 October.

| Source | Class | Provider | Raw redistribution | Status (2026-10-23) |
|---|---|---|---|---|
| `alpaca` | Licensed | Alpaca Markets | **No** — prohibited by agreement | In use, operator-only since 2026-10-23. Alpaca licenses its market data for personal, non-commercial use, and its Developer Relations team wrote on 2026-09-03 that derivations may not be provided to third parties either. So on a published deployment everything derived from it (prices, forecasts, grades, accuracy and other statistics) is answered HTTP 451 to everyone but the operator until Alpaca gives written permission |
| `cryptohist` | Licensed | Kraken | **No** — derived works included | Paused 2026-10-22: crypto paused; history is kept in the database, open crypto forecasts voided (never deleted). On a published deployment it is shown only to the operator since 2026-10-23 |
| `cryptolive` | Licensed | Coinbase / Kraken | **No** | Paused 2026-10-22: crypto paused |
| `hyperliquid` | Licensed | Hyperliquid | **No** — commercial terms unestablished | Paused 2026-10-22: crypto paused |
| `news` | Licensed | Alpaca news | **No** — headlines are publisher IP | In use, operator-only since 2026-10-23 (Alpaca's terms, as for `alpaca`). Since 2026-10-23 headlines are no longer sent to NVIDIA: the LLM sentiment tagger is retired (see `nvidia` below) |
| `tvscanner` | Restricted | TradingView | **No** — automated access is against their terms; ratings are their IP | Retired 2026-10-21/22 |
| `stocktwits` | Restricted | StockTwits | **No** — undocumented endpoint, personal use only | Retired 2026-10-21/22 |
| `edgar` | Public | SEC | Yes — SEC data is free to access and reuse | In use |
| `fred` | Restricted | St. Louis Fed | **No** — data-licence audit: clearly forbidden | Retired 2026-10-22: licence review (forecasts before then used FRED data) |
| `finra` | Restricted | FINRA | **No** — data-licence audit: clearly forbidden | Retired 2026-10-22: licence review |
| `cftc` | Public | CFTC | Yes | In use |
| `cboe` | Restricted | Cboe | **No** — data-licence audit: clearly forbidden | Retired 2026-10-22: licence review |
| `congress` | Public | STOCK Act disclosures, via third-party datasets (Kadoa's congress-trades file; older rows House/Senate Stock Watcher) | Not the stored rows (Kadoa's terms, see `kadoa`); the filings themselves are public | Retired 2026-10-23: no stored row records its source, so every row is treated as Kadoa's and shown only to the operator; collection has stopped. The Ethics in Government Act commercial-use question stays open for any future direct use of the filings |
| `kadoa` | Restricted | Kadoa (congress trades dataset) | **No** — its terms (s.4, s.8) bar automated access, reproduction and sharing its compiled rows without written permission | Retired 2026-10-23: poller unwired; stored rows kept and shown only to the operator |
| `wikimedia` | Public | Wikimedia | Yes — page-view counts are CC0 | In use |
| `nvidia` | Restricted | NVIDIA (hosted model API, a processor rather than a data feed) | **No** | Retired 2026-10-23: used under NVIDIA's trial API terms, which do not permit production use. The daemon refuses its hosts, the sentiment tagger is unwired, and the stored LLM ratings (`sentiment_score`, `sentiment_n`) are no longer a model input or a composite tile; forecasts made before then used them |

The operator's own TradingView webhook alerts (`tv_signals`, received at `/api/tv-webhook`) are stored for the record but are no longer used as a model input since 2026-10-22 (licence review: TradingView's terms bar non-display use of alerts).

## What the guards do

Two guards in `daemon/internal/api/security.go` apply on a published deployment
(one reachable beyond loopback):

1. **Operator-only content (since 2026-10-23).** Only the operator (an admin
   session, or the loopback bearer token) receives content derived from Alpaca
   market data or news, or from Kadoa's congress compilation. Every other caller,
   signed in or not, gets **HTTP 451** with
   `{"error":"withheld","reason":"licence","source":...,"notice":...}`. What a
   non-operator may still reach is an allowlist, `datalicense.NonOperatorRoutes`
   (account flows, the caller's own watchlist, alert settings, export and deletion,
   the pre-registration wording and chain hashes, and SEC EDGAR data); a route on
   no list is withheld, so it fails closed.
2. **Raw vendor rows.** `GET /api/bars`, `/api/news`, the CSV exports and the other
   raw-row routes answer **HTTP 451** to everyone, the operator included, unless
   `SIGNALDECK_ALLOW_RAW_EXPORT=true` records the operator's assertion that he
   holds an agreement permitting it.

Consuming this data privately on your own machine (an unpublished deployment) is
unaffected. **Derived analytics are not exempt:** Alpaca-derived forecasts, grades
and statistics are withheld from everyone but the operator until Alpaca gives
written permission, and the repository's generated documents carry them only once
the operator records that permission with `SIGNALDECK_PUBLISH_DERIVED=1`.

## Survivorship boundary

`symbols.delisted_at` has only existed since the 2026-07-24 survivorship wave
(`daemon/internal/store/store.go`). Every row recorded before that date was
produced against a universe seeded from 2026 survivors — names that had
already died could never have entered it, which is exactly the bias that most
inflates oversold and mean-reversion results.

**Pre-epoch history is survivor-seeded and unfit for published claims.** Any
accuracy number, backtest, or verdict quoted externally must be built
exclusively from rows created at or after the epoch. This is enforced in code:
`tools/accuracy_registry.py` carries `SURVIVORSHIP_EPOCH = 2026-07-24`, both
graders filter to `ts >=` the epoch at the SQL layer, so no pre-epoch row can
reach a tally. Pre-epoch data remains in the database for private inspection
only.

The epoch filter is not the same thing as a clean universe, and the published
rows do not claim one. Each row's `survivorship_clean` is measured every cycle
by `measure_universe_completeness` in the pinned grader: a graded symbol counts
as resolvable when it carries `delisted_at` or has `active = 1`, and the row
publishes `false` plus a reason otherwise.

### What `survivorship_clean` does and does not measure

`symbols.active` is the lean **watch** flag, not a listing status. The 13:15 PT
weekday sweep (`ops/signaldeck-refresh.sh`) sets it on every stock and then
prunes back to the most liquid names plus held ones. So the registry's
"inactive symbol(s) with no delisted_at" is **not a count of delistings**
(a read-only measurement on 2026-10-23 found nearly all of them still printing
daily bars; the counts are Alpaca-derived and withheld), and the flag moves with
whether the grade overlaps the sweep. The grader is SHA-pinned in the pre-registration chain and
is not edited; its rule and every historical result stay as published.

Beside it, after each grade, `tools/survivorship_status.py` writes
`data/survivorship_status.json`: **survivorship-status v1 (2026-10-23)**, a
separately versioned measure over the same graded symbols. It keeps watch
status, universe membership, listing evidence and effective-dated exits apart:

| class | rule (first match wins) |
|---|---|
| RETIRED_SOURCE | crypto with a dated exit: a retired data source, not a listing event |
| UNKNOWN (conflict) | a dated exit contradicted by a later 1d bar or by SEC listing the ticker on a national exchange; reported, never resolved |
| EXITED_DATED | `delisted_at` on or before the as-of date |
| WATCHED | `active = 1`, no dated exit, a 1d bar within N sessions (current date only) |
| UNWATCHED_TRADING | not watched, no dated exit, a 1d bar within N sessions |
| TRADING | the same at a historical as-of date, where watch status was not recorded |
| UNKNOWN | no dated exit and no recent 1d bar, watched or not (`active = 1` is not listing evidence) |

Silence is never read as an exit, and a missing Form 25 or notice is never read
as "still listed". Delisting, merger, ticker change and exchange transfer are not
told apart because no corporate-action record is persisted; the report names
only the evidence it has (a retired source, an archived reused-ticker row, an
OTC mapping in SEC data). Historical runs use only bars, exits, graded rows and
SEC rows dated on or before the as-of date. Exits are effective-dated, because
the time a stamp was written is not stored. The tool's docstring holds the full
definitions, and the generated live-accuracy block quotes its counts only when
publication is permitted (`SIGNALDECK_PUBLISH_DERIVED=1`); until then the block is
the licence-withheld notice.

## The shape that is sellable

Ship the platform; the customer brings their own provider keys and operates
under their own agreements. Under SignalDeck's own Alpaca terms, analytics derived
from Alpaca data can be neither sold nor shown to others (above); whether a
customer's own agreement permits it is theirs to establish. `SIGNALDECK_ALLOW_RAW_EXPORT=true` records an
operator's assertion that they hold the necessary rights — it does not grant
them.

## What the pre-publish scan flags, and what each file contains

The pre-publish scan flags every tracked data file by its path, not by inspecting its contents, so the "!" marker is a prompt for a human to verify rather than an automatic finding. Verified on 2026-09-20 against the file headers, none of the tracked files below contain a price, quote, volume or news-body column – the categories marked non-redistributable in the table above. Since 2026-10-23 the scan also FAILS on any tracked JSON record holding a price-shaped value: `research/dirfix/paper_ledger.jsonl` carried 40 Alpaca closing prices and was moved to the gitignored `data/` that day (it remains in git history).

**Under the 2026-10-23 Alpaca decision, the derived files below are Alpaca-derived as well** (day tallies, claimed accuracies, backtest summaries), so containing no raw column no longer makes them publishable. They are frozen reproduction inputs that CI verifies; whether to withdraw them from the public repository is an open decision for the owner.

| file | columns | class |
|------|---------|-------|
| `repro/directional_days.csv` | horizon, day, n, correct, up_days + high-conviction slice | derived day tallies |
| `repro/structural_days.csv` | kind, horizon_days, day, n, correct | derived day tallies |
| `repro/structural_naive_days.csv` | kind, horizon_days, day, n, correct | derived day tallies |
| `repro/structural_claims.csv` | kind, horizon_days, forecasts_recorded, claimed_accuracy, first_ts | claim metadata |
| `repro/prereg_claims.csv` | kind, claimed_accuracy, spec_hash, registered_ts | claim metadata |
| `repro/grading_protocol.csv` | grader hash, floors, alpha, multiplicity rule | protocol metadata |
| `repro/pairs_inputs.csv` | symbol, timeframe, first_ts, last_ts, n, sha256 | FINGERPRINT, no series |
| `repro/xsfactor_inputs.csv` | symbol, timeframe, active, first_day, last_day, n, sha256 | FINGERPRINT, no series |
| `research/dirfix/ablation.csv` | h, k, weighting, sharpe, ann_ret, max_dd, t_nw, turnover | backtest summary statistics |
| `ops/prune-20260719-181723/deactivated_symbols.csv` | id, ticker | symbol identifiers |

### The fingerprint files are the ones to understand

The two files named `pairs_inputs.csv` and `xsfactor_inputs.csv` look like the dangerous "inputs" files but they are the opposite: each row reduces a symbol series to its temporal bounds, its row count and the SHA-256 hash of the canonical serialisation. No price bar is exported.

Someone who already possesses the licensed bars can confirm they hold the byte-identical dataset before comparing results, which makes a published number reproducible without redistributing the underlying data.

The generator script `tools/make_repro_snapshot.py` states the same in its docstring ("The series themselves are NOT exported — bar data is license-classified (A10) and not redistributable"), so the intent is recorded both in code and here.

### What would change this answer

- a new column carrying a price, quote, volume or news body, in any of these files or a new one
- a snapshot generator change that exports a series rather than its hash
- a new tracked data file from a Licensed or Restricted source in the table above

Any of those would turn the "!" into a genuine finding again and this section would need to be re-derived rather than simply re-read.

**Not a legal review.**
This section records what the files CONTAIN, which is an engineering question with a checkable answer. Whether publishing derived analytics is permitted is a separate matter that no check in this repository settles. For Alpaca it was decided on 2026-10-23 from Alpaca's own documents: derived analytics may not be shown to others without its written permission. What remains open is the owner's decision on the derived files already committed here and on copies published elsewhere before that date.
