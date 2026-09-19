# ENTITLEMENT RECORD — what each data source feeds, and who can reach it
Measured 2026-09-15. The licensing JUDGEMENT is not made here — see "What is still yours" at the end.

The licence-governed routes (10 in total) are not accessible anonymously, as confirmed by the empty intersection with the public allowlist (24 routes) and live probes returning HTTP 401 for /api/bars, /api/news, /api/tv-rating, and /api/export/bars.csv. This establishes that access to licence-governed data requires authentication, with the HTTP 451 licence guard acting as a second layer behind authentication.

| Source | Class | Provider | Raw redistribution | Exposed via | Audience |
|---|---|---|---|---|---|
| alpaca | Licensed | Alpaca Markets | No — prohibited by agreement | /api/bars, /api/export/bars.csv, /api/export/scores.csv, /api/export/outcomes.csv | authenticated only (401 anonymous) |
| cryptohist | Licensed | Kraken | No — derived works included | no route of its own; a PriceBarSources member reaching users only via /api/bars | authenticated only (401 anonymous), governed as alpaca |
| cryptolive | Licensed | Coinbase / Kraken | No | /api/snaps | authenticated only (401 anonymous) |
| hyperliquid | Licensed | Hyperliquid | No — commercial terms unestablished | /api/crypto-perp (funding, openInterest, markPx) | authenticated only (401 anonymous); **governed from 2026-09-15 — it was not before** |
| news | Licensed | Alpaca news | No — headlines are publisher IP | /api/news | authenticated only (401 anonymous) |
| tvscanner | Restricted | TradingView | No — automated access is against their terms; ratings are their IP | /api/tv-quote, /api/tv-rating, /api/tv-signals | authenticated only (401 anonymous) |
| stocktwits | Restricted | StockTwits | No — undocumented endpoint, personal use only | /api/stocktwits | authenticated only (401 anonymous) |
| edgar | Public | SEC | Yes — US government, public domain | derived analytics | redistributable |
| fred | Public | St. Louis Fed | Yes — attribution requested | derived analytics | redistributable |
| finra | Public | FINRA | Yes | derived analytics | redistributable |
| cftc | Public | CFTC | Yes | derived analytics | redistributable |
| cboe | Public | Cboe | Yes | derived analytics | redistributable |
| congress | Public | STOCK Act disclosures | Yes | derived analytics | redistributable |
| wikimedia | Public | Wikimedia | Yes — CC-licensed | derived analytics | redistributable |

The first gate is authentication: all licence-governed routes require valid credentials (returning 401 to anonymous requests). The second gate is the HTTP 451 licence guard, which triggers when: raw export is not explicitly asserted via SIGNALDECK_ALLOW_RAW_EXPORT, the daemon is reachable (not loopback-only), and a contributing source forbids redistribution. The flag SIGNALDECK_ALLOW_RAW_EXPORT defaults to false and only records an operator's assertion of rights; it does not grant redistribution rights.

## What is still yours
This document records what is exposed to whom — an engineering fact of the system's current configuration. It does not establish that each provider's current terms permit such exposure, which is a separate legal judgement. Three open questions remain: (a) whether the classifications (Licensed/Restricted/Public) are still correct against each provider's terms today, (b) whether "derived analytics" are genuinely exempt under each provider's agreement rather than assumed, and (c) the status of hyperliquid's commercial terms, which are recorded as unestablished and unresolved. Where it is unclear, the output is withheld and the unresolved permission is recorded rather than guessed.
## Correction made while compiling this record

`/api/crypto-perp` was **not** governed by the licence guard. It serves Hyperliquid
`funding`, `openInterest` and `markPx` — a vendor price — from a source classed
Licensed with commercial terms recorded as *unestablished*. Because
`RouteRedistributable` reports any path absent from `RestrictedRoutes` as
redistributable, the 451 guard could never fire for it.

It was the only Licensed source with a serving route and no entry. `stocktwits`,
added to the same `dataexpansion.go` package, was governed; this one was missed.

The asymmetry that hid it: an unknown source KEY fails closed, but an ungoverned
ROUTE fails **open**. A missing row is silent in the direction that looks safe.

Fixed, and pinned by `TestEveryNonRedistributableSourceWithARouteIsGoverned`,
which fails with the exact consequence when the row is removed. Loopback
operators are unaffected — the guard also requires the daemon to be reachable
beyond localhost before it refuses.
