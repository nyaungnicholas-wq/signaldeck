# Ship readiness — honest audit (2026-07-25, updated 2026-09-20)

> **HOW TO READ THIS FILE.** Every section heading states its CURRENT status
> and the date that status was verified. Findings from an earlier pass that no
> longer hold are kept, indented and explicitly dated, as history — not deleted,
> and not left standing as though they were current. The block between the
> `GENERATED live_accuracy` markers in §4 is written by
> `tools/live_accuracy.py` from the registry and must not be edited by hand.
>
> **UPDATE 2026-09-20.** Reconciled against the source and the live evidence.
> §1 and §3 were still asserting the opposite of what the repository contains
> — there IS a LICENSE, and `SIGNALDECK_PUBLIC_READS` has not defaulted to
> `true` since it started deriving from `reachablePrivately()`. §4 still framed
> 2026-08-07 as a future date and the structural claims as ungraded; both are
> past. §4 also promised the evidence blocker "needs time, not code" next to a
> refusal window that cannot age out; that promise is withdrawn and the reason
> is stated there. The generated region was not touched.

> **UPDATE 2026-09-19.** Blocker 5 is partly cleared: the image builds, runs and
> serves — see that section. Blocker 4 has NOT moved and the reason is recorded:
> re-registering the graded window yields INSUFFICIENT DAYS (6 credible days
> against a floor of 10), and 1w grades 46.0% against a 54.7% null, so clearing
> the cross-section gate was never going to show good news. The decision on
> record is to leave the gate alone. Blockers 2 and 6 remain business decisions.
>
> **UPDATE, same day.** Blockers 1 and 3 are FIXED (LICENSE; PublicReads now
> derives from `reachablePrivately()` — loopback bind AND no tunnel in the
> allowlist, not the bind address alone, which A9 showed was insufficient).
> Blocker 2 has an in-code data classification with a 451 guard on raw bar
> export, but the REDISTRIBUTION decision behind it is still open and is a
> business question, not a code one. Blocker 4 was RE-TESTED against the
> survivorship-clean
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

### 1. No LICENSE file — CLEARED 2026-09-19
`LICENSE` exists and is a Source-Available License, "Copyright (c) 2026
Nicholas Nyaung. All rights reserved." A reader can now tell what they are
allowed to do with the repository, which is all this blocker ever asked for.

This audit verified the file's presence and its heading. It did NOT review the
licence text, its fitness for any distribution model, or its interaction with
the data-redistribution question in §2 — those are legal questions and no
engineering check settles them.

> *Original finding, 2026-07-25, retained as history:* "There is none in the
> repo. Legal review stops here on contact, because without one the default is
> 'all rights reserved' and nobody can evaluate what they are allowed to do
> with it."

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

### 3. `SIGNALDECK_PUBLIC_READS` defaulted to `true` — CLEARED 2026-09-19
It no longer does. `daemon/internal/config/config.go` resolves it as
`boolEnv("SIGNALDECK_PUBLIC_READS", private)`, where `private` is
`reachablePrivately(addr, allowedHosts)` — loopback bind AND no reverse tunnel
in the allowlist. Both signals must agree before reads open; when they
disagree the answer is closed. An operator who wants open reads still says so
in one env var.

The bind address alone was not enough, and that is on the record: A9
(2026-07-26) found this machine's own allowlist naming a reserved ngrok
hostname while the heuristic still evaluated "private", so the safe-by-default
check read safe on the exact deployment that was public.

Still true, and not a code question: a deployment should set
`SIGNALDECK_PUBLIC_SURFACE` deliberately rather than relying on the denylist,
because `PublicReads` answers "is this route one we chose to keep private?" —
so a route added later is public by forgetting.

> *Original finding, 2026-07-25, retained as history:* "Read endpoints answer
> without authentication so localhost works out of the box. That default is
> correct for a personal tool and wrong for anything exposed."

---

## BLOCKING — evidence

### 4. There is no demonstrated edge yet, and that is the honest headline
This is the question a serious evaluator asks first, and the current answer is:

- The directional model was **automatically retired** — every horizon graded
  below its own majority-class null on live forward data. The figures are not
  typed here; they are the generated block below, which is the same block every
  other document in this repository carries.

<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (registry `REFUSED` since 2026-09-13T14:43:41) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **GRADING REFUSED — no accuracy figures are published.** Reason: publication gate: the graded window contains 18 collapsed cross-section(s) of 82 day(s): 1d 2026-07-27 (6 distinct across 330 symbols), 1d 2026-07-28 (8 distinct across 330 symbols), 1d 2026-07-29 (13 distinct across 328 symbols), 1d 2026-07-31 (6 distinct across 328 symbols), 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-26 (21 distinct across 326 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld. The window starts at the survivorship epoch and does not roll forward, so a collapsed day stays in it: this clears when the window is re-registered, not by waiting for more grades.. The grade computed at 2026-09-13T14:42:10 (166.0h old) is withheld, not lost: it is retained inside the registry under `stale_last_registry` for the historical record and is deliberately not reprinted here, because a number the publication gate refused to stand behind is not a live number. The in-app `/accuracy` page and `/api/accuracy` apply the same gate from the same registry.

<!-- END GENERATED live_accuracy -->

- The structural forecasts are **no longer ungraded**. 2026-08-07 has passed and
  outcomes are resolving: `tools/structural_liveness.py` on 2026-09-20 reports
  9,211 of 26,105 trend21, 9,262 of 26,193 vol21 and 9,155 of 25,864
  liquidity21 outcomes resolved, with no dead arm. `trend63` is WAITING — 26,105
  forecasts, 0 resolved — because its horizon has not elapsed, and `liquidity21`
  carries one overdue outcome, below the dead-arm threshold. Resolved outcomes
  are not a verdict: none of these has published a skill claim.
- Volatility has a live record and **no verdict either way**: 10 of the 60
  distinct trading days required at 1 session, 6 of 60 at 5 sessions. Days are
  counted, not rows, because forecasts resolving on one day share a market
  shock.

So today the defensible claim is *"a platform that measures honestly and
retires its own failures"* — which is genuinely rare and worth saying — and NOT
*"a system with predictive edge."*

**What clears it, and what does not.** More resolved outcomes are what the
structural and volatility arms need, and those accrue on their own. The
DIRECTIONAL refusal above does not: the collapsed window is anchored to the
survivorship epoch and does not roll forward, so a collapsed day stays in it
however long anyone waits. This section used to end "it needs time, not code",
printed directly beneath a refusal that time cannot clear. That sentence is
withdrawn. Clearing the directional window is a re-registration decision with
its own evidence — and the decision on record (see the 2026-09-19 update at the
top) is to leave the gate alone, because re-registering yields INSUFFICIENT
DAYS and 1w grades 46.0% against a 54.7% null.

---

## BLOCKING — operational

### 5. Never deployed — PARTLY CLEARED 2026-09-19
Still one host, bound to `127.0.0.1`, stopped at 13:10 PT for a backup. No HA,
no failover, one disk. Those remain true.

What is no longer true is "the Dockerfile has never been executed". Measured
2026-09-19 at commit `bc353f2`:

- `ops/docker-build.sh` builds `signaldeck:latest` (1.21 GB) from a clean tree,
  with the commit store baked in and verified to resolve the built revision.
- `docker run` on a scratch volume reports **healthy in 12s** against the
  image's own HEALTHCHECK, which probes `/api/health` THROUGH the web app, so
  both halves have to be alive.
- The container serves `/`, `/proof`, `/volatility`, `/accuracy` and
  `/api/health`, all 200, and `/api/ledger/verify` returns `intact` rather than
  a 503.

So the deployment story is now "builds, runs and serves on demand"; what is
left is choosing a host and running it there with `PUBLIC_READS=false`. Note
there is **no `render.yaml` in the repo** — the Render blueprint this document
referred to does not exist, and picking a host is still an open decision.

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
