# SignalDeck UX OVERHAUL — SUPER PROMPT (2026-07-18, user-approved)

User goals, verbatim intent: make the whole app interactive and customer-suitable;
merge the duplicated Markets/Signals hubs into ONE; clicking a company anywhere in
market surfaces shows "what's going on within the company"; trends page shows what
kind of trends / candlestick patterns; regimes + macro pages are messy → combine
into one compact page; signals tabs more compact + interactive; glassmorphism dark
design via the ui-ux-pro-max skill. Decisions locked by AskUserQuestion:
ONE merged hub · slide-over AND full-page company view · glassmorphism dark ·
all in one go.

## 1. Design system — GLASSMORPHISM DARK (tokens in web/src/app/globals.css)

Keep the OLED-dark data identity + bid/ask/accent semantics. Transform surfaces:
- Backdrop: deep `#060910` with STRONGER ambient color fields (amber + blue
  radial gradients at higher alpha) so the frosted blur has something to show.
- `.panel`: rgba(255,255,255,0.045) fill + `backdrop-filter: blur(16px)
  saturate(140%)` + 1px rgba(255,255,255,0.09) border + inner top highlight +
  soft deep shadow. Radius 14px.
- `.panel-h`: translucent, hairline divider rgba(255,255,255,0.07).
- `.chip`: frosted pill rgba(255,255,255,0.05), hover brightens + hairline glow.
- Interactions: 150-200ms color/opacity transitions; hover = brighter fill +
  border-strong (never scale/layout shift); visible focus ring (accent).
- `prefers-reduced-motion`: disable transitions.
- Contrast floors unchanged (--faint stays ≥4.5:1); glass fills are LOW alpha
  behind HIGH-contrast text — never translucent text.
- Fallback: `@supports not (backdrop-filter)` → solid --panel.

## 2. Hub merge — MARKETS + SIGNALS → one "MARKET" hub

Nav: DASHBOARD · MARKET · INTEL · LAB. MARKET tabs (each thing exactly once):
- OVERVIEW   = screener + heatmap + movers (old /markets/screener)
- SIGNALS    = predictions leaderboard (old /signals/predictions)
- REGIMES    = validated regime forecasts (old /signals/regimes)
- TRENDS     = trends + candlestick patterns (old /markets/trends, upgraded)
- MACRO      = regime-map + macro combined compact page (replaces
               /markets/regimes AND /markets/macro)
- ACTIVITY   = alerts (old /signals/alerts)
- UNUSUAL    = anomaly tape (old /signals/unusual)
Research-flavored tabs move to LAB: forecasts, confluence, insights, debate,
memory, graph. ALL old URLs 307-redirect (next.config, query-preserving —
established pattern). Implementation: /market/* pages re-export the existing
page components; originals removed from nav; redirects prevent double homes.

## 3. Company slide-over ("what's going on in this company")

Global `CompanyPeek` (context + portal in root layout). Click a ticker on
heatmap tiles / movers rows / screener rows → slide-over from the right:
price + day change + age · 90d mini chart · trade context (52w range, SMAs,
ATR, $vol) · validated signal stack chips (each → /signals/report) · latest
prediction + composite with live-record caveat · recent breakouts/anomalies
with realized fwd moves. ONE data call: GET /api/signal-report?kind=overview
(+ /api/bars for the mini chart). Buttons: "full page →" (/s/...) and
"detail report →". Esc / backdrop click closes; focus-trapped; body scroll
locked.

## 4. TRENDS tab upgrade — what kind of trend + candle patterns

Per-symbol trend classification (existing /api/trend + trends payload) plus a
CANDLESTICK PATTERNS section: detected patterns (doji/engulfing/hammer/etc.
from the existing patterns engine, /api/candle-patterns) with symbol, pattern
name, date, and the honest framing that patterns are DESCRIPTIVE, not graded
predictions. If a fleet-wide "patterns detected recently" store accessor is
missing, add one (bounded query) + endpoint.

## 5. MACRO tab — regimes + macro combined, compact

One page, three compact bands:
- MARKET STATE: breadth %, VIX level+regime, rates (DGS10/T10Y2Y), PUSH-20
  gate — stat tiles, not sprawling cards.
- REGIME MAP: fleet regime distribution (uptrend/downtrend/range/squeeze
  counts), recent regime changes (top 10), link to full REGIMES tab.
- SECTORS: rotation strip (strongest→weakest).

## 6. Signals compaction + interactivity

- Regimes tab: denser rows, per-kind sections collapsible, top-N default.
- Predictions: table default, sticky header, row hover, ReportLink everywhere.
- Every signal row stays one click from its detail report (built 2026-07-18).

## 7. Later phases (recorded, not this pass)

Deploy/access checklist lives in memory (project-signaldeck-deploy-checklist):
Tailscale, Telegram alerts, Cmd+K palette, cold-load precompute, PWA,
start-here home, weekly digest. Also: deeper table virtualization, export.

## Verification bar

`npm run build` green, full daemon suite green if daemon touched, deploy both,
curl-verify moved routes 200 + redirects, screenshot-or-payload proof of the
peek endpoint. Honesty labels NEVER dropped in restyling.
