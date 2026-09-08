# Competition Submission Package — SignalDeck

## Project description

SignalDeck is a self-grading market-research instrument. A Go daemon records market data for about 330 US stocks and a few crypto pairs, computes signals, freezes pre-registered forecasts into a hash-chained prediction ledger before outcomes are known, and later grades every forecast against realized prices. Verdicts are published including failures.

## Audience and problem

**Audience**
- Students and self-directed learners of market research who want to check claims rather than trust them
- Anyone evaluating a forecasting claim

**Problem**
Forecast products publish win rates nobody can verify. There is no public, re-verifiable ledger that shows every prediction, every outcome, and every grade — including the failures.

## Claims you may make

- Grades itself in public with a hash-chained, re-verifiable prediction ledger (about 477,000 entries)
- Retired its own flagship directional model by a pre-registered auto-retire rule because its live record was worse than the majority-class baseline; retirement is permanent by design
- Refuses to publish accuracy figures when the graded window contains collapsed cross-sections (days when the model gave the whole universe a handful of distinct probabilities); the public pages say so plainly and show no figures until the window clears
- Volatility forecast (HAR realized-variance model versus random walk and RiskMetrics EWMA) pre-registered on 2026-09-04 (chain sequence 105) and accruing a live record; needs 60 distinct trading days before any verdict; no skill is claimed
- Paper trading is a clearly labelled simulation: a manual market-order book with a cost model, plus the platform's own model-driven books; no real money, no brokerage connection

## Claims you must not make

- Any claim of profitability or a trading edge
- Any accuracy percentage as a live result while publication is refused
- Comparison to professional firms
- Any Sharpe ratio or return figure
- Describing simulations as real trading
- "Institutional" quality

## Key dates

- Congressional App Challenge submission deadline: 12:00 pm EDT, Monday, October 26, 2026 (one entry per student per year).
- FirstCommit deadline: September 30, 2026, 5:00 pm EDT (separate event; see the last section).

## Demonstration sequence

1. **Landing page (/)** — Show the "publication refused" banner. Say: "This banner means the honesty machinery is working: the graded window has collapsed cross-sections, so no accuracy figures are published." ~15 seconds
2. **/accuracy** — Show the refusal notice, the plain-English summary, and the dated historical record of the 2026-07-24 retirement (no figures). Say: "The grade was computed, but the publication gate withholds it until the evidence window clears." ~15 seconds
3. **/proof** — Click "Verify chain" and show the re-verification result. Say: "The hash chain re-verifies end to end; about 477,000 entries." ~15 seconds
4. **/volatility** — Show the accruing live record for the HAR model (sequence 105). Say: "Pre-registered 2026-09-04; needs 60 distinct trading days; far fewer today; no skill claimed." ~15 seconds
5. **Sign in** — Authenticate and land on /dashboard. Say: "Session auth with CSRF guard; private workspace unlocks." ~10 seconds
6. **/dashboard** — Show the overview widgets. Say: "Live data, watchlist shortcuts, and quick links to research and lab tools." ~15 seconds
7. **/s/stocks/SPY** — Show the symbol research page with derived analytics. Say: "Derived analytics only; licensed market data is never redistributed." ~15 seconds
8. **/lab/paper** — Place a manual simulated market order. Say: "Simulation only: cost model, no brokerage, no real money." ~15 seconds
9. **/lab/track-record** — Show the prediction track record and its gates. Say: "Every frozen forecast is resolved against real bars; skill stays withheld until the day-count and observation gates clear." ~15 seconds

Total: ~2 minutes 15 seconds

## Video outline (1-3 minutes)

| Time | Beat | Required element |
|------|------|------------------|
| 0:00–0:05 | Title card | App name: SignalDeck |
| 0:05–0:10 | Presenter | Student name: [NICHOLAS TO CONFIRM] |
| 0:10–0:15 | Purpose sentence | "A self-grading market-research instrument that freezes forecasts before outcomes, grades them in public, and refuses to publish when evidence is unusable." |
| 0:15–0:20 | Target audience | "Students and self-directed learners who want to verify forecasting claims." |
| 0:20–0:30 | Tools and languages | "Go 1.26 daemon, SQLite, Next.js 16, React 19, TypeScript, Tailwind, Python grading pipeline, Playwright E2E tests." |
| 0:30–0:45 | Landing page | Show refusal banner; explain honesty machinery |
| 0:45–1:00 | /accuracy | Show grades page with refusal notice |
| 1:00–1:15 | /proof | Verify hash chain live |
| 1:15–1:30 | /volatility | Show pre-registered HAR model accruing record |
| 1:30–1:45 | Sign in + /dashboard | Auth flow and private workspace |
| 1:45–2:00 | /s/stocks/SPY | Symbol research with derived analytics |
| 2:00–2:15 | /lab/paper | Manual simulated order with cost model |
| 2:15–2:30 | /lab/track-record | Paper-trading track record |
| 2:30–2:45 | Closing | "No profitability is claimed. All AI usage is disclosed in the submission." |

## Written answers — drafts and placeholders

**1. Title**  
SignalDeck

**2. Purpose**  
A self-grading market-research instrument that records market data, freezes pre-registered forecasts into a hash-chained prediction ledger before outcomes are known, grades every forecast against realized prices, and publishes verdicts including failures — refusing to publish when the evidence window is unusable.

**3. What inspired it**  
[NICHOLAS TO CONFIRM]

**4. Technical/coding difficulty faced and how it was addressed**  
Two verified challenges from code history:  
- The landing page printed a "publication refused" banner above a table of the refused figures because an evening job regraded the registry file without the publication gate and the page read the file directly. Fixed by making one API endpoint the single publication authority; every page, document, and export now follows it.  
- Readiness reported 503 for weeks because workers that deliberately abstain (benched models) were counted as failures. Fixed by distinguishing "declined to deliver" from "failed" in the health check.

**5. What was learned and the biggest takeaway**  
[NICHOLAS TO CONFIRM]

**6. What would change in a 2.0 version**  
- Replace the retired directional model with a new pre-registered approach that avoids collapsed cross-sections  
- Extend the volatility forecast to its 60-day minimum and publish a first verdict  
- Add a public API for the verified ledger so external auditors can re-grade independently  
- Move the daemon off Windows Task Scheduler to a managed scheduler with observability  
- Open-source the grading pipeline and publication gate as a reusable library

## AI-assistance disclosure (draft)

AI assistance was substantial. Claude Code (Anthropic) acted as the engineering agent for much of the codebase, with additional code drafts from other large language models routed through a local gateway. The 2026-09-08 release pass — publication-contract repair, public pages, readiness fix, and documentation — was done by Claude Code under my direction. I personally designed the prediction ledger schema, the publication gate logic, the auto-retire rule, the readiness health distinction, the paper-trading cost model, and the daily grading pipeline architecture. I wrote the Go daemon's worker orchestration, the Next.js proxy and session auth layer, and the Python grading pipeline. I understand every component listed in the "What you should be able to explain and demonstrate" section below. [NICHOLAS TO CONFIRM — adjust or confirm this paragraph]

## What you should be able to explain and demonstrate

- [ ] The ledger hash chain: how entries are appended, how the chain is verified, what the receipts page shows
- [ ] The publication gate and refusal envelope: why collapsed cross-sections block publication, how the single API authority works
- [ ] The auto-retire rule: the pre-registered condition, the 2026-07-24 retirement event, why it is permanent
- [ ] The readiness/health distinction: "declined to deliver" vs "failed", how the 503 bug was fixed
- [ ] The paper-trading cost model: slippage, fees, spread, how simulated fills are recorded
- [ ] The Next.js proxy and session auth: host allowlist, CSRF guard, rate limiting, private vs public routes
- [ ] The daily grading pipeline: Python scripts, registry file, grade computation, publication decision

## Contribution record

| Existing work before 2026-09-08 (git history) | Changes on 2026-09-08 by AI release pass | Facts only the student can confirm |
|-----------------------------------------------|------------------------------------------|-------------------------------------|
| Go daemon with ~100 workers, SQLite, HTTP JSON API, host allowlist, session auth, rate limiting, CSRF guard | Publication-contract repair: single API authority for publication gate | Personal design of ledger schema, auto-retire rule, readiness distinction, paper-trading cost model |
| Next.js 16 / React 19 / TypeScript / Tailwind web app with public and private routes | Public pages (/accuracy, /proof, /volatility, /glossary) readiness fix | Authorship of Next.js proxy, session auth, private workspace pages |
| Python daily grading pipeline and documentation gates | Documentation updates and readiness fix | Authorship of grading pipeline, understanding of every component |
| Playwright end-to-end tests | | |
| Hash-chained prediction ledger (~477k entries) with re-verification on /proof | | |
| Auto-retire rule implemented and triggered 2026-07-24 | | |
| Volatility forecast pre-registered 2026-09-04 (sequence 105) | | |
| Paper trading simulation with manual and model-driven books | | |
| Data ingestion: Alpaca IEX, Kraken/TickStream, FRED VIX, SEC EDGAR, FINRA short volume | | |
| Windows Task Scheduler deployment | | |

## FirstCommit (separate)

**Rules summary**  
- Open to ages 13–21  
- Main project work must be completed during the event window  
- Submitting a project created before the hackathon results in disqualification  
- Public GitHub repository required with multiple meaningful commits (single-commit upload disqualified)  
- README with overview, tech stack, setup, credits, and AI disclosure required  
- Significant AI usage must be disclosed; organizers may request AI chat history  
- Judging emphasizes learning and growth, understanding of technology, creativity, execution, presentation  
- Deadline: September 30, 2026, 5:00 pm EDT

**Why SignalDeck does not qualify**  
SignalDeck is a multi-month project with substantial work completed before the FirstCommit event window. Polishing SignalDeck does not qualify. The private repository holds database backups as release assets and must not be made public as a shortcut. A single-commit upload of SignalDeck would be disqualified.

**Plan for eligible new event-period work**  
- Start a genuinely new, standalone project after the event opens  
- Create a new public GitHub repository  
- Make multiple meaningful commits during the event window  
- Include a README with overview, tech stack, setup, credits, and full AI disclosure  
- Build a demo presentation  
- No specific project is promised here; the plan is to follow the rules exactly