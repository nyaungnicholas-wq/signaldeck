# Ship readiness — honest audit (2026-07-25)

What would actually have to be true before this goes to a company, separated by
whether the blocker is legal, evidential, or operational. Verified against the
repository, not estimated.

**Summary: the engineering is in good shape. The blockers are almost entirely
NOT code.** Two of them — data licensing and an unproven edge — cannot be
solved by writing more of it.

---

## BLOCKING — legal

### 1. No LICENSE file
There is none in the repo. Legal review stops here on contact, because without
one the default is "all rights reserved" and nobody can evaluate what they are
allowed to do with it. This is a business decision (permissive vs proprietary
vs dual), so it is not something to pick on the author's behalf — but it is
the cheapest blocker on this list to clear.

### 2. Data redistribution — the serious one
Several ingest paths are fine to *consume* privately and would be a problem to
*redistribute* commercially:

| source | how it is accessed | exposure |
|---|---|---|
| `tvscanner` | TradingView's public scanner endpoint, no account | automated access is against TradingView's terms; their ratings are their IP |
| `stocktwits` | undocumented public JSON stream, no key | same class — tolerated for personal use, not a commercial supply chain |
| `alpaca` | licensed market data | redistribution is prohibited by the agreement |
| `edgar`, `fred`, `finra`, `cftc`, `cboe` | US government / public | genuinely free to use and redistribute |

`GET /api/bars` currently serves stored vendor bars. On localhost that is
personal use. Pointed at a customer it becomes redistribution of licensed data,
which is the single fastest way to turn a portfolio project into a legal
problem.

**The shape that works** (already identified in earlier research): ship the
PLATFORM and have the customer bring their own data keys. Analytics *derived*
from data can be sold where the raw data cannot. That reframing costs no
engineering — it is a packaging decision — but it has to be made deliberately.

### 3. `SIGNALDECK_PUBLIC_READS` defaults to `true`
Read endpoints answer without authentication so localhost works out of the box.
That default is correct for a personal tool and wrong for anything exposed.
Any deployment must set it to `false`; a security reviewer will find this in
minutes and it reads worse than it is.

---

## BLOCKING — evidence

### 4. There is no demonstrated edge yet, and that is the honest headline
This is the question a serious evaluator asks first, and the current answer is:

- The directional model was **automatically retired** — 48.0% accuracy against
  a 54.4% naive baseline over 12,696 independent observations. Significantly
  negative skill, measured on live forward data.
- The structural forecasts (trend21 82%, vol21, liquidity21) are **backtest
  claims with no live grade yet**. The first ones become gradable **2026-08-07**.

So today the defensible claim is *"a platform that measures honestly and
retires its own failures"* — which is genuinely rare and worth saying — and NOT
*"a system with predictive edge."* Claiming the second before 2026-08-07 would
be the exact failure this codebase was built to prevent.

**What clears it:** 8–12 weeks of resolved forward outcomes. The infrastructure
to produce that proof is already built and running; it needs time, not code.

---

## BLOCKING — operational

### 5. Never deployed
Runs on one Mac, bound to `127.0.0.1`, stopped nightly at 13:10 PT for a
backup. No HA, no failover, one disk. The Dockerfile and Render blueprint exist
but have never been executed. "Works on the author's laptop" is not a
deployment story.

### 6. Free-tier data is not institution-grade
Alpaca's free IEX feed is roughly 2–3% of consolidated volume. Fine for
research on daily bars; not what anyone would trade real size against. The
measured upgrade path is roughly $130/month (Alpaca Algo Trader Plus + Tiingo),
which flips a single feed flag.

---

## NOT blocking — genuinely strong

These are the parts that would survive scrutiny, and they are the unusual ones:

- **86 Go packages green**, CI runs `go test ./...` plus the web build on every
  push.
- **Bias defences that most platforms fake**: point-in-time fundamentals,
  no-lookahead and fill-timing tests, survivorship-clean research universe with
  a reconstructable point-in-time membership.
- **A hash-chained prediction ledger** — 235k rows, tamper-evident.
- **Automatic model retirement that has actually fired** on the flagship model.
  Most systems cannot switch anything off; this one did, to its own detriment,
  which is the point.
- **Independence discipline everywhere**: one observation per symbol per day,
  because pooling intraday rows inflates n roughly 60×.
- **Security already hardened**: CSRF, origin/Host allowlists, rate limits,
  tenant scoping, constant-time token compare.
- **Sub-100ms reads** across every user-facing page.
- Portable: exactly one hardcoded path in the daemon.

---

## The honest sequencing

1. **Pick a license.** An afternoon. Unblocks every conversation.
2. **Decide the commercial shape** — bring-your-own-keys platform, not a data
   product. A packaging decision, not an engineering one.
3. **Deploy it somewhere real** with `PUBLIC_READS=false`. Days, not weeks; the
   blueprints exist.
4. **Wait for 2026-08-07, then let the record speak.** If the structural claims
   hold live, there is a real product. If they do not, the platform that proved
   it is still the thing worth showing.

Steps 1–3 are days of work. Step 4 cannot be compressed, and trying to is how
the directional model shipped for months at negative skill.
