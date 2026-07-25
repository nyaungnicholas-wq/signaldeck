# Data sources and what may be done with them

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

## The shape that is sellable

Ship the platform; the customer brings their own provider keys and operates
under their own agreements. Analytics derived from data can be sold where the
underlying records cannot. `SIGNALDECK_ALLOW_RAW_EXPORT=true` records an
operator's assertion that they hold the necessary rights — it does not grant
them.
