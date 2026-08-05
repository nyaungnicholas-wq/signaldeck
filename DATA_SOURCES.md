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

| Source | Class | Provider | Raw redistribution |
|---|---|---|---|
| `alpaca` | Licensed | Alpaca Markets | **No** — prohibited by agreement |
| `cryptohist` | Licensed | Kraken | **No** — derived works included |
| `cryptolive` | Licensed | Coinbase / Kraken | **No** |
| `hyperliquid` | Licensed | Hyperliquid | **No** — commercial terms unestablished |
| `news` | Licensed | Alpaca news | **No** — headlines are publisher IP |
| `tvscanner` | Restricted | TradingView | **No** — automated access is against their terms; ratings are their IP |
| `stocktwits` | Restricted | StockTwits | **No** — undocumented endpoint, personal use only |
| `edgar` | Public | SEC | Yes — US government, public domain |
| `fred` | Public | St. Louis Fed | Yes — attribution requested |
| `finra` | Public | FINRA | Yes |
| `cftc` | Public | CFTC | Yes |
| `cboe` | Public | Cboe | Yes |
| `congress` | Public | STOCK Act disclosures | Yes |
| `wikimedia` | Public | Wikimedia | Yes — CC-licensed |

## What the guard does

`GET /api/bars` returns **HTTP 451** with an actionable notice when all three
hold: raw export is not explicitly asserted, the daemon is reachable (not
loopback-only), and any contributing price source forbids redistribution —
which today is always, because every price feed in use is licensed.

Consuming this data privately on your own machine is unaffected. **Derived
analytics — forecasts, regimes, risk metrics, explanations — are deliberately
never restricted.** The line is drawn at raw records, not at insight computed
from them, which is also the line that makes a commercial product possible.

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
graders filter to `ts >=` the epoch at the SQL layer, and every row published
to `data/accuracy_registry.json` is stamped `survivorship_clean: true` because
no pre-epoch row can reach a tally. Pre-epoch data remains in the database for
private inspection only.

## The shape that is sellable

Ship the platform; the customer brings their own provider keys and operates
under their own agreements. Analytics derived from data can be sold where the
underlying records cannot. `SIGNALDECK_ALLOW_RAW_EXPORT=true` records an
operator's assertion that they hold the necessary rights — it does not grant
them.
