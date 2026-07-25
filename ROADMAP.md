# SignalDeck — Full Roadmap (2026-07-18)

Everything worth adding, organized by category, ranked within each. Grounded in
what's already built: validated regime signals (trend21/liquidity21/vol21 +
vol63), signal reports, research ledger, paper book, EDGAR intel, alerts,
per-user auth. Free-data-only constraint respected throughout.

## A. Credibility (make the honesty story unbeatable)
1. **Live regime-forecast grading** — resolve trend/liquidity/vol21 calls
   forward like predictions (outcome rows → resolver → track-record section
   "REGIMES — live"). Turns backtested tiers into a live, hash-chained record.
2. **Earnings awareness** — surface "earnings ~N days" (EDGAR cadence
   heuristic exists) on peek/symbol/regime tables; flag or withhold
   high-conviction calls inside the earnings window.
3. **Regime-call postmortems** — when a high-conviction call misses, auto-write
   a one-paragraph "what happened" (data exists: postmortem engine).

## B. Daily-habit product surfaces
4. **"Today" page** — briefing + overnight regime transitions + top-conviction
   calls + yesterday's patterns + your watchlist changes, one screen.
5. **"My Deck" watchlist-first view** — your symbols as cards: regime stack,
   alerts, earnings window, one-click peek.
6. **Cmd+K command palette** — global symbol/page search (34 pages, 10k
   companies).
7. **Symbol compare** — two tickers' signal stacks side by side.

## C. New honest signal coverage
8. **Crypto discovery loop** — rerun the alpha harness on crypto daily + 1h
   bars (huge N, vol clusters hard); likely second validated universe and
   possibly intraday-cadence regime calls.
9. **TREND63 shipped kind** — measured 70.0% all / 83.7% top tier; quarterly
   trend horizon as a fourth structregime kind.
10. **Intraday gap-fill surface** — the unconditional gap edge (72-87%) is
    real but only actionable at the open; needs an open-time snapshot path
    (stock-streamer) before it can ship honestly. Revisit when intraday
    cadence exists.
11. **Sector/index regime calls** — same engines on SPY/QQQ/sector aggregates
    (small N per symbol but high user value; grade honestly, ship if it clears).

## D. Access & deployment (saved in memory as deploy checklist)
12. **Tailscale** — phone access to localhost:8323; hardening flags already built.
13. **Telegram/Discord alerts** — internal/notify is dormant; env vars only.
14. **PWA manifest** — installable from phone browser.
15. **Cold-load fix** — precompute dashboard/movers cells in a worker; cache
    track-record ledger verification (12s → instant).
16. **Weekly digest** — watchlist regime-change summary via Telegram.

## E. Research engine depth
17. **Wire AD-* auto-discovered hypotheses into live replication** (known
    follow-up: spec'd hyps can't accrue post-discovery evidence).
18. **Strategy builder UI** — compose simple rules (rank decile, regime filter,
    hold period) over the existing backtest engine.
19. **Options/vol expression tracker** — no IV data free; nearest honest step:
    paper-track straddle-proxy P&L on high-conviction vol calls using realized
    vol, capacity-caveated.
20. **Delisted-universe caveat automation** — label every survivorship-exposed
    stat automatically (penalty exists in ledger; propagate the label to UI).

## F. Polish / customer-readiness
21. **Signals table deep compaction** — sticky headers, inline row expansion
    (recorded in UX_OVERHAUL_SUPERPROMPT as the deferred phase).
22. **Onboarding tour** — first-login walkthrough: the 3 validated signals,
    the honesty pages, the experimental tier.
23. **Export** — CSV/image export for reports and tables (CSV endpoint exists
    for some kinds; unify).
24. **Mobile pass** — peek + dashboard at 375px; nav folding audit.

## Recommended order
1 → 4 → 2 (credibility + daily habit, all assembly) · then 12-13 (go live on
phone) · then 8 (crypto loop) · then 5-6 · rest as wanted.
