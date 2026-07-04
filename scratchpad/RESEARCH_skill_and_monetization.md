# SignalDeck: The Honest Verdict — Can This Be Sold, and How Good Is It?

*Chief strategist's definitive report. Every number here was pulled live from the running daemon (`:8322`) and `data/signaldeck.db` on 2026-07-03, or read directly from the code. Nothing is estimated where it could be measured. This report models the integrity the product is supposed to sell: no fabricated winrate, no hedging-as-evasion.*

---

## 1. The honest verdict on prediction quality

**Lead truth: SignalDeck has demonstrated no robust out-of-sample edge, and it has essentially no live track record. Every headline "skill" number visible today is either within noise or a statistical artifact.** That is not a failure of the machinery — the machinery is genuinely well-built — it is the honest state of a system that went live ~1 day ago.

### What is MEASURED today

**A) The walk-forward forecast is honestly gated OFF for almost everything.** Running `forecast.Evaluate` (expanding-window, out-of-sample) across the universe:

| Metric | 1d horizon | 1w horizon |
|---|---|---|
| Mean out-of-sample **Lift** (Accuracy − base rate) | **−0.020** (small-liquid subset: −0.045) | **−0.039** |
| Mean **AUC** | **0.504** | 0.504 |
| Symbols with Lift > 0 | **21.4%** | 21.0% |
| Symbols with AUC > 0.55 | **6.6%** (33 / 501) | — |

The AUC distribution across 501 symbols is a textbook **no-skill** curve: min 0.414, p50 **0.503**, p90 0.542. The right tail (EXPD 0.599, EG 0.597) is exactly what multiple-testing noise across 501 names produces, not durable edge. The 8-feature logit — all price/volume-derived (returns, RSI, price-vs-SMA, realized vol, volume ratio) — has **no measurable directional edge on liquid large-caps**, which is honestly what near-zero-edge features *should* show. The ensemble correctly **drops** the forecast leg whenever Lift ≤ 0 (`ensemble/ensemble.go:116`), so fleet-wide the forecast currently contributes nothing.

**B) The "IC = −0.515" on the /honesty page is an artifact, not an inverse signal.** The live endpoint reports IC = **−0.5155** over "n=2098" resolved 1d outcomes. Direct SQLite proves this is pseudo-replication:

```
resolved 1d score_outcomes: total=2098 | distinct symbols=8 | calendar days=2 (2026-07-01, 07-02) | distinct (symbol,fwd_return) pairs=48
```

Each non-BTC symbol has ~238 minute-cadence scores all mapping onto the **same ~2 daily forward moves**. The effective independent N is **~8, not 2098** — the displayed confidence is overstated ~40×. And the sign is driven by two unlucky names over one down window:

```
TSLA  score +0.71 → −7.42% fwd  (n=238)
AMD   score +0.62 → −4.21% fwd  (n=228)
```

Two names on two days. This is **noise, not measured negative skill.** (The out-of-sample pooled score IC, computed properly, is ≈ 0: −0.0073 at 1d over 140,735 obs — with a mild per-symbol *mean-reversion* tilt, median −0.046. More on that in §5.)

**C) The real product — the calibrated live prediction — is unmeasured.** `prediction_outcomes` has **1,955 rows at 1d but only 23 resolved; 0 of 1,955 resolved at 1w.** The `/api/calibration` endpoint returns one populated bin (N=23). There is no live winrate. There cannot be one yet.

**D) The generic backtester is honest but is NOT the product's signal.** `POST /api/backtest` grades *user-typed* SMA/RSI rules, not SignalDeck's own pressure score. It runs on ~502 daily bars (~2yr, split-adjusted IEX) with 10 bps/side costs. Representative live runs:

- SPY 50/200 cross: +20.4% total, CAGR 9.8%, Sharpe 1.11, maxDD 9.1% — but **−15.3pp vs buy-and-hold**, and **1 trade** (winrate 100% is meaningless at N=1).
- NVDA: +23.1%, **−35.5pp vs buy-hold**. MSFT: **−5.9%**. The strategy **underperforms buy-and-hold in 4 of 5 names.**

The engine surfaces disappointing truth correctly — a strength — but it is not evidence of the platform's own edge.

### What is NOT yet measured (and when it will be)

- **Live winrate / IC / calibration of the calibrated ensemble:** empty until resolutions accrue. 1d needs ~30+ *independent* days; 1w needs ~20 weeks. Realistically **8–12+ weeks minimum**, across at least one up and one down regime, before any honest read exists.
- **Net-of-cost tradability of the pressure score itself:** never measured end-to-end. No backtester grades the actual score/ensemble net of costs (see §5, the #1 build item).

### Is there a real edge, or is it noise?

**On the evidence that exists today: there is no demonstrated edge — the numbers are within noise or artifacts.** That is the honest verdict. It is *not* the same as "the signal is bad" — the sample is far too short (1–2 days) to conclude negative skill either. The correct statement, which the honesty doctrine demands: **skill is currently unproven, and no positive out-of-sample track record exists.**

---

## 2. What you can actually sell

Three asset classes, three different answers.

### ❌ Raw market data — OFF-LIMITS, not yours to sell
- **Alpaca IEX bars** (the entire stock side): Alpaca T&C — *"Content is provided exclusively for personal and noncommercial access and use. No part … may be copied, reproduced, republished … transmitted or distributed in any way (including mirroring) … for any commercial enterprise, without Alpaca's express prior written consent."* ([files.alpaca.markets](https://files.alpaca.markets/disclosures/alpaca_terms_and_conditions.pdf))
- The free feed is **IEX-only ≈ 2.4% of consolidated SIP volume** (Alpaca's own docs: AAPL 2023-09-29 = 12,630 IEX trades vs 535,136 total). A material disclosure quite apart from legality. ([docs.alpaca.markets](https://docs.alpaca.markets/us/docs/market-data-faq))
- **Crypto is worse.** Coinbase Market Data Terms bar redistributing the data *"or any data, charts, analytics, research, or other works based on, referring to, or derived from the Market Data (Derived Works)"* and forbid using it *"as a benchmark"* or to value *"any digital currency … or financial derivatives."* That clause reaches your **BTC/ETH pressure scores and predictions directly.** ([coinbase.com/legal/market_data](https://www.coinbase.com/legal/market_data))
- **The gzip-CSV cold archive** (`internal/archive`) IS raw vendor OHLCV — it must **never** ship to a customer.

### ✅ Your derived code and analytics — genuinely yours
- The pressure-score engine (`signals/score.go`), ensemble/adaptive/per-symbol agents (`ensemble/`, `adaptive/`, `symbolagent/`), the feature store (`store/features.go`), and calibration/honesty machinery are your original IP.
- **Dependencies are clean:** `go.mod` has only 3 external deps — coder/websocket (ISC), golang.org/x/crypto (BSD-3), modernc.org/sqlite (BSD-3). No copyleft. **But there is no LICENSE file** — the repo is currently all-rights-reserved by default (fine for proprietary, but must be addressed before any source sale).
- `.gitignore` excludes `data/` and `.env`, so **the git artifact is pure algorithms** — the sellable-code asset is physically separable from the unsellable data.
- **But your derived signals are worth ~nothing to a signal buyer today** because there is no costed out-of-sample track record (§1). Code without a proven signal is a platform, not alpha.

### ✅ The most defensible thing to sell now: the PLATFORM
Sell **SignalDeck the software** (self-hosted or SaaS) where **each customer brings their own data license and API keys.** This sidesteps redistribution entirely — the vendor-terms problem becomes the customer's. It is sellable immediately, requires no track record, and turns clean code IP into a product. (See §3 and §5.)

**The one line to hold:** sell your *derived analytics and the honesty methodology* and *the platform* — never the underlying feed, bars, or the cold archive.

---

## 3. Who buys it and for how much

Every serious buyer gates on the one thing SignalDeck lacks: a **verifiable, point-in-time, out-of-sample, costed track record.** Channels ranked for a *solo builder with no track record and a free, non-redistributable feed*:

| Rank | Channel | Pricing anchor (named comparable) | Entry bar | Open to you now? |
|---|---|---|---|---|
| **1** | **Self-hosted / SaaS platform** (customer brings own data license) | Tool-model: **TrendSpider** Business from **$399/mo**; **Composer.trade $32/mo** (SoFi-acquired); **Danelfin** $22–59/mo | Working software + a EULA. **No track record required.** | ✅ **Yes, this quarter** |
| **2** | **SaaS "honesty lab" analytics** (transparency, not picks) | **Danelfin** $22/mo, **Quiver** $25/mo, undercut both | Working tool + honest framing. Publisher's-exclusion safe (§4). | ✅ **Yes, this quarter** |
| **3** | **Broker/affiliate rev-share** (thin add-on) | IBKR affiliate; Webull/Public/Alpaca CPA per funded account | An audience (you have none yet); FTC disclosure | ✅ Small top-up |
| **4** | **Retail signal / copy-trade marketplace** | **Collective2** Trade Leader **$20–$200+/mo** per subscriber (+autotrade $49/mo); top strategy ever ≈ $500K–$782K cumulative, *"atypical"* per C2 | Connect a real broker account; **months of audited forward, real-fill results** | ⚠️ Only after 3+ mo live |
| **5** | **Retail alt-data subscription** (Quiver/Dataroma-style) | **Quiver Premium $25/mo** ($300/yr); Dataroma free/ad | A **differentiated dataset** (yours is commoditized TA on free bars) | ⚠️ Weak differentiation |
| **6** | **License signals to funds/pods** | Undisclosed; anecdote: backtest Sharpe ~3.15 → 1.5–2.0 expected in prod | IC ≥ ~0.05 sustained **5–10 yrs** across regimes, factor-neutralized, capacity + decay models, FISD/DDQ, network | ❌ Closed |
| **7** | **Data marketplaces** (Databento / Nasdaq Data Link / Snowflake / AWS) | Databento CME Standard $199/mo → Unlimited $4,500/mo; requires exchange redistribution licenses (NYSE non-display Cat 3 ~$2,000/mo) | Redistribution license you don't hold; point-in-time history | ❌ **Legally closed** |

**Market context (for scale, not for you *yet*):** the alt-data-to-funds market hit **$2.5–2.8B in 2024–25, +21–27%/yr**; average dataset earns ~$1.1M/yr, elite >$20M/yr (Neudata; Kadoa). But that spend is for **genuinely alternative, exclusive, point-in-time data** — not a technical-indicator score on free public bars. Institutional buyers require FISD compliance, a Due-Diligence Questionnaire, ≥75-ticker universe, deep point-in-time history, and a ≥3-month trial (Eagle Alpha). SignalDeck meets universe size (~500) but **fails on data-rights, point-in-time depth, and differentiation.**

*Sources verified via fcrawl: Collective2, Quiver Quantitative, Danelfin, TrendSpider, Composer.trade, Databento pricing pages; Neudata / Kadoa / Grand View market-size; Eagle Alpha & Nasdaq Data Link onboarding.*

**Realistic revenue for a solo builder:** low-hundreds/mo initially on the SaaS tool angle, scaling to **~$1–3K/mo if 50–150 traders convert on transparency**. Anything faster requires implying skill the data does not support — which destroys the only asset (honesty).

---

## 4. The compliance bright lines

**You CAN legally sell impersonal, generally-circulated scores/analytics/newsletters without registering as an RIA — IF you stay inside the "publisher's exclusion."**

### The safe harbor
- **Investment Advisers Act §202(a)(11)(D)** excludes *"the publisher of any bona fide newspaper, news magazine or business or financial publication of general and regular circulation."* **Lowe v. SEC, 472 U.S. 181 (1985):** *"The mere fact that a publication contains advice and comment about specific securities does not give it the personalized character that identifies a professional investment adviser."* ([supreme.justia.com](https://supreme.justia.com/cases/federal/us/472/181/))
- **Seeking Alpha won on exactly this model (SDNY, Aug 2024):** email alerts on rating changes, portfolio risk warnings, and filter/compare-by-preference features *"merely allow the subscriber to filter generally available content"* — that is not individualized advice. Directly on point for SignalDeck's per-symbol scores, watchlists, and alerts. ([gtlaw.com](https://www.gtlaw.com/en/insights/2024/8/no-need-for-seeking-alpha-to-seek-registration))

### The 3-part bona-fide test (must satisfy all)
1. **Not** a personal communication (impersonal, not tailored to a subscriber's situation).
2. **No** false or misleading information.
3. **Not** designed to tout a security the publisher holds.

### Bright lines that turn you into an RIA / trigger liability — never cross these
- **Personalized advice** tied to a subscriber's portfolio/situation → RIA. (*Weiss Research* **lost** the exclusion for *"personalized communications with its subscribers."*)
- **Auto-trading.** SEC: *"the SEC considers firms that publish investment newsletters and that also engage in auto-trading to be investment advisers."* If a broker ever executes off your signals without per-trade consent, the exclusion evaporates. ([sec.gov auto-trading alert](https://www.sec.gov/about/reports-publications/investorpubsautotradinghtm))
- **Guaranteed / "can't-lose" returns.** Cherry-picking wins, ignoring losses.
- **Touting positions you hold.**

### Advertising exposure (the real risk, not registration)
- As a **publisher** you're under **FTC truth-in-advertising**: claims must be truthful and substantiated; endorsements must reflect typical results and disclose connections.
- **The live-vs-backtest gap is a legal exposure, not just an honesty one.** The hit-rates on `/api/honesty` (e.g. neutral bucket 0.64, n=732) are **backtest/in-sample expectancy on 2 days of clustered data — NOT a live track record** (calibration is empty, n=23). Showing a "0.64 hit-rate" or the "IC" without prominently labeling it *"backtested, not live, no resolved out-of-sample record yet"* is precisely the misleading-performance claim the FTC treats as deceptive. If you ever *register* as an RIA, the Marketing Rule 206(4)-1 bites hard — the SEC fined **nine RIAs $850K combined (Sept 2023)** just for hypothetical performance on public sites without policies.

### What the code already gets right
FilingMind's charter (`filingmind.go:54`): *"NO INVESTMENT ADVICE… This is analysis, not a recommendation."* The briefing (`briefing.go:257`) and footer (`Shell.tsx:229`): *"SignalDeck measures and stores; it does not advise… Not financial advice."* **This is the correct impersonal-publisher posture — keep it as the template for all generative text.**

### Required before any paid launch (conservative)
1. **Terms of Service + subscriber agreement** with a hard limitation-of-liability cap (= fees paid), AS-IS/no-warranty, arbitration/venue. *(None exists today — searched `web/src/app`: no ToS/legal/privacy/disclaimer page. A paid product with no LoL cap is uncapped exposure.)*
2. **A standalone Disclaimer page:** informational/educational only, not advice, publisher-not-RIA, *"past performance and backtested results do not indicate future results,"* gated behind explicit acceptance at checkout.
3. **Label every performance figure at point of display** as backtested/in-sample with its sample size, until live resolutions accrue.
4. **A one-time securities-attorney review** scoped to: (a) home-state RIA rules vs the federal exclusion (some states are narrower), (b) the Alpaca/exchange **data-redistribution** question (breach-of-contract risk entirely separate from securities law), (c) sign-off on ToS/disclaimer copy.

*This is legal research, not legal advice.*

---

## 5. The gap to sellable, and the roadmap

### The ONE thing that unlocks everything
**A verifiable, tamper-evident, costed, out-of-sample track record of the product's OWN signal.** Without it, honesty doctrine (and every buyer) correctly values the signals at zero. With it — *if* it turns positive — every channel in §3 opens. The most damaging structural gap: predictions are stored with `INSERT OR REPLACE` (`store/predict.go`), carry **no wall-clock publish timestamp and no hash**, so nothing proves a prediction was logged *before* its outcome. That is the single property allocators pay for.

The build order is forced: **(1) make the record unfakeable → (2) run it forward long enough to measure real skill net of costs → (3) sell it.** Selling before (2) resolves is dishonest.

---

### NOW (this quarter — plumbing + truth-in-labeling; unblocks trust immediately)

| # | Action | Effort | Why |
|---|---|---|---|
| N1 | **Fix the /honesty page (doctrine violation).** De-duplicate resolved outcomes to **one observation per (symbol, forward-period)** before computing IC/quintiles/Brier; report both raw-n and **effective-independent-n**; gate the IC display behind a minimum independent-N (e.g. ≥30 distinct symbol-days) and show *"insufficient independent resolutions"* instead of a number. Localized change in `api.go pearson()` + honesty handler. | **S** | The live **IC = −0.515 is an artifact** (8 symbols, 2 days, 48 pairs). Any quant spots it in seconds and discredits the whole brand. This is the #1 due-diligence killer and the cheapest, highest-trust fix. |
| N2 | **Label every skill number** with independent-N and a *"NOT YET MEASURABLE / backtested-not-live"* badge until thresholds clear. | **S** | One premature *"IC −0.51"* or *"86% hit"* screenshot destroys the honesty brand during cold-start. Also the FTC exposure in §4. |
| N3 | **Stop annualizing short windows.** Only report CAGR when the backtest spans ≥~1yr **and** ≥~20 trades; else show total return + trade count. Auto-detect bars/year so Sharpe scaling is correct on 1h/minute bars (currently hardcoded `sqrt(252)`). | **S** | Kills the *"168% CAGR off one trade"* (SOXL) headline that reads as fabrication to an allocator. |
| N4 | **Ship the platform + a LICENSE.** Package as self-hosted/SaaS where the customer brings their own data keys; add a proprietary EULA (repo is currently unlicensed). Add a build/export guard + `DATA_LICENSING.md` that **excludes `data/`, the gzip-CSV cold archive, and any raw-price endpoint** from all distribution. | **M** | The only revenue path open today (§2, §3) — zero data-licensing or track-record dependency. The export guard prevents an accidental Alpaca/Coinbase ToS breach that could terminate data access. |
| N5 | **Add ToS + Disclaimer + LoL cap** and keep the product strictly impersonal (audit all LLM/agent "personality" copy so nothing says *"you should buy/sell"*). | **M** | Closes uncapped-liability gap and anchors the publisher's-exclusion defense (§4). |

### NEXT (weeks — build the proof surface, start the clock)

| # | Action | Effort | Why |
|---|---|---|---|
| X1 | **Build the ONE honest end-to-end signal backtester.** Replay the persisted feature-store vectors (`LabeledFeatures()` already exists, no lookahead) + resolved outcomes through the pressure-score/ensemble; report **OOS IC-decay, quintile spread, turnover, and PnL net of costed spread** (crypto ~2–5bps, stocks 5–10bps) with a benchmark-relative equity curve. Reuse `internal/backtest`'s honest next-bar/cost loop. | **L** | Converts *"we have signals"* into *"here is the costed, out-of-sample track record"* — **the single artifact a buyer pays for.** No such harness exists today; the generic backtester grades user text, not the product. |
| X2 | **Append-only, hash-chained prediction ledger.** New immutable table `prediction_ledger(seq, predicted_at wall-clock, symbol, horizon, bar_ts, cal_prob, feature_hash, model_version, prev_hash, entry_hash=sha256(prev_hash‖canonical_json))`, written in the same tx as the prediction and **before** any outcome can exist; never UPDATE/REPLACE. | **M** | Makes back-editing detectable — the property allocators actually buy. No tamper-evidence primitives exist today (only `sha256` in auth). |
| X3 | **Anchor the chain head externally** (hourly + daily) via OpenTimestamps/RFC-3161 + mirror to an append-only public location. | **M** | Turns *"trust us"* into *"verify independently"* — validityBase already sells exactly this to managers. |
| X4 | **Live out-of-sample track-record page over RESOLVED calibrated predictions** (not raw scores): winrate, Brier, reliability curve, IC + IC-decay by horizon/regime/symbol-agent, with confidence intervals and an *"n too small / not significant"* gate. Show it **empty and honest today.** | **M** | The public proof surface. Being visibly honest while empty is itself a credibility asset. |
| X5 | **Let it run 8–12+ weeks** to accrue resolved 1d/1w outcomes across ≥1 up and ≥1 down regime. Publish results verbatim — including any negative read. | **M** | This IS the gate. A forward, timestamped, un-editable record is what every buyer requires; nothing substitutes for elapsed time. |

### LATER (once a real track record exists — and only then)

| # | Action | Effort | Why |
|---|---|---|---|
| L1 | **Test the measured mean-reversion tilt** (per-symbol 1d IC median −0.046 — high pressure → slight pullback) as a *separate* gated signal, walk-forward net of cost. | **M** | The data already points here; the momentum-signed score bets against it. Cheapest place to look for a real (if small) edge, on data you already have. |
| L2 | **Replace the 8-feature batch logit** with gradient-boosted trees / online FTRL on the feature store (same `Evaluate`/Lift honesty gate), adding crypto microstructure features (order-book imbalance, signed volume, spread) — **crypto 1h is the one clean evaluation channel** (BTC/USD 1273 independent 1h windows, up-rate 0.518). Add **free cross-asset context**: FRED VIXCLS (public-domain, free API) level + term structure. | **L** | Linear logit on 8 TA features is *why* AUC=0.504. Nonlinearity + microstructure + vol-regime is where any real 1h crypto edge would appear. Gated so it only ships if it beats OOS. |
| L3 | **Point-in-time universe with delistings** (survivorship-free) for any cross-sectional/ranking/CAGR claim — or explicitly scope all claims to *"currently-listed liquid names, last ~2y"* and label the survivorship limitation on every performance surface. Add **multiple-testing deflation** (deflated Sharpe / White reality-check across the 500×3×7 grid). | **L** | Today every breadth/CAGR number is structurally optimistic (all 503 symbols `active=1`, ~2yr Alpaca ceiling, zero delisted) and nothing discounts for 500-way selection. Both are due-diligence killers. |
| L4 | **Launch a channel** — impersonal newsletter (§3 #2/#5) or a Collective2 forward record (#4) — **only after ≥3 months of resolved, costed, positive OOS results**, with cost sensitivity shown at 10/25/50/100 bps. For crypto specifically, move off Coinbase/Kraken-derived scores or drop crypto from any paid offering (Coinbase's Derived-Works clause is explicit). | **XL** | This is the payoff — but strictly conditional on skill materializing, which is unproven and may never happen. |

---

## 6. The bottom line

**Is it sellable, and how good is it?**

- **How good is the signal today?** *Unproven — no demonstrated edge.* The forecast is honestly gated off (mean Lift −0.02 to −0.04, AUC 0.504 ≈ coin flip). The scary "IC −0.515" is a two-day, eight-symbol artifact, not negative skill. The calibrated product has **23 of 1,955 predictions resolved** — statistically nothing. There is **no live winrate, and it is honest to say so.** The earliest a real read exists is **8–12+ weeks out.**

- **What's genuinely valuable right now?** The **honesty machinery itself** — no-lookahead backtester, per-side costs, lift-gating, feature store joined to outcomes, edgeless-leg dropping, calibration gates, the no-advice charter. It is well-engineered and rare. But machinery that correctly reports *"no edge yet"* is a **research/monitoring platform**, not alpha.

- **What can you sell, legally, this quarter?** The **platform as software/SaaS** (customer brings their own data license) and a **transparency-first "honesty lab" analytics tool** ($15–25/mo, undercutting Danelfin/TrendSpider) — under the **publisher's exclusion**, with a ToS/LoL cap and backtest labeling. Realistic near-term revenue: **low-four-figures/mo at best for 12+ months.** You **cannot** legally or honestly sell the raw data, the crypto-derived scores, or "alpha" today.

**The single most honest next move:** ship the **platform/SaaS** now (the only clean revenue path), and in parallel **fix the /honesty page (N1) and build the end-to-end signal backtester + hash-chained ledger (X1–X4)** so a real, verifiable, costed, out-of-sample track record starts accruing. Then let it run untouched for a full regime cycle and **publish whatever it shows — including if the answer is "no edge."** That discipline is not a constraint on the product; **it is the product.** The one thing that can kill this business isn't a weak signal — it's a premature skill claim that turns out to be an artifact. Don't make it.

---

*Grounding note: all live figures verified 2026-07-03 against the running daemon (`/api/honesty`, `/api/calibration`, `/api/backtest`) and `data/signaldeck.db` (score_outcomes: 2098 resolved 1d = 8 symbols × 2 days = 48 distinct pairs; prediction_outcomes: 23/1955 resolved 1d, 0/1955 1w). Code paths cited from `daemon/internal/`. Market/legal claims cited inline via fcrawl.*

Report file (markdown, if the parent wants to render it): `/private/tmp/claude-501/-Users-natalienyaung-claude-code/f2608549-e527-4eb8-8003-9afe64661b0f/scratchpad/signaldeck_report.md`",