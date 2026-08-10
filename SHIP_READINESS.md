# Ship readiness — honest audit (2026-07-25)

> **UPDATE, same day.** Blockers 1, 2 and 3 are FIXED (LICENSE, in-code data
> classification with a 451 guard on raw bar export, PublicReads now defaults
> from the bind address). Blocker 4 was RE-TESTED against the survivorship-clean
> universe — see "What the re-validation found" at the end. Blockers 5 and 6
> (deployment, paid feed) remain open and are decisions, not code.

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

- The directional model was **automatically retired** — every horizon graded
  below its own majority-class null on live forward data. The figures are not
  typed here; they are the generated block below, which is the same block every
  other document in this repository carries.

<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-08-09T14:05:15) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 1,997 | 43.0% | 53.4% | -10.3pp | 14 | [33.6%, 53.0%] |
| prequential-majority (1d) | all | 1,665 | 55.6% | 52.6% | +3.0pp | 11 | [33.6%, 75.5%] |
| directional-ensemble (1w) | all | 1,597 | 40.6% | 60.1% | -19.5pp | 8 | withheld |
| prequential-majority (1w) | all | 926 | 65.7% | 62.7% | +2.9pp | 5 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 451 | 44.6% | 46.5% | -1.9pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 117 | 38.5% | 50.0% | -11.5pp | 7 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (5/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (8/10 distinct days) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (7/10 distinct days) — no interval, so no verdict

### Backtested claims with no live record yet

- `filingsdrift21` — registered claim 50.0%, 142 forecasts recorded, 0 graded. Not a live result.
- `liquidity21` — registered claim 59.5%, 3,565 forecasts recorded, 0 graded. Not a live result.
- `liquidity21-crypto` — registered claim 79.5%, 86 forecasts recorded, 0 graded. Not a live result.
- `trend21` — registered claim 73.1%, 3,596 forecasts recorded, 0 graded. Not a live result.
- `trend21-crypto` — registered claim 93.4%, 86 forecasts recorded, 0 graded. Not a live result.
- `trend63` — registered claim 70.0%, 3,596 forecasts recorded, 0 graded. Not a live result.
- `vol21` — registered claim 55.8%, 3,608 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=13, looks=18, divisor=234, corrected_alpha=0.00021367521367521368.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 327/336 graded symbols (97.3%): 9 inactive symbol(s) with no delisted_at; measured effect +0.24pp (active-only 83.51% minus survivorship-clean 83.27%, n=75,062 clean vs 17,879 active, revalidation of 2026-08-09T10:20:22+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->

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


---

## What the re-validation found (2026-07-25)

`tools/revalidate_structural.py` re-ran trend21 over the survivorship-clean
universe — every symbol ever tracked, including delisted names — with
non-overlapping 21-session sampling and quarter-block bootstrap CIs.

**54,969 independent observations across 24 quarters**, roughly 3x the original
sample and the first run that includes the graveyard:

| conviction band | active only | delisted only | survivorship-clean |
|---|---|---|---|
| low (<0.5) | 73.6% | 72.6% | **72.9%** |
| moderate (0.5–0.8) | 91.0% | 90.2% | **90.5%** |
| high (0.8–0.9) | 95.1% | 95.1% | **95.1%** |
| very-high (>=0.9) | 97.8% | 97.5% | **97.6%** |
| overall | 83.6% | 82.7% | **83.0%** |

**The claim survives.** Survivorship inflation is +1.0pp overall, and the
band structure is essentially unchanged — the shipped 97.2% for very-high
conviction measures 97.6% on the clean universe. Dead companies were very
slightly harder, not dramatically easier, which is the outcome that leaves the
published numbers usable.

### But the headline number is NOT skill, and must never be quoted as one

The naive strategy for "will price stay on the same side of its 200-day
average" is to always answer yes. That scores the base rate of persistence —
**the same 83%** the model scores, because the model also answers yes almost
every time. Quoting "83% accurate" claims credit for the base rate.

The real product is **discrimination**: the model sorts calls into bands whose
accuracy genuinely differs, ~73% at low conviction versus ~98% at very-high, a
**24.7pp spread** that holds across 24 quarters and survives the graveyard.
Knowing WHICH regime calls are reliable is the useful thing. The average is not.

This is what can honestly be said before 2026-08-07, and it is a stronger claim
than the headline was, because it is the one that is actually true.
