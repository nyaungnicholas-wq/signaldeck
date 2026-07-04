# SUPER PROMPT — Visual, Compact, Everything-Works SignalDeck

User intent (their words, distilled): "Make it more visual — I want to SEE trends
and news. Make it compact so I don't have to look at each tab — combine things
into a couple of hubs. Make every tab work. Combine every single stock-related
thing into one, but DON'T combine the live trading thing (HUD)."

User decisions (asked + answered):
- Layout: HYBRID of "feed + sidebar" and "big visuals first" — a big visual band
  on top (heatmap, featured candlestick w/ signals, gauges), then a main feed
  with a persistent watchlist/alerts sidebar.
- Consolidation: ~5 hubs with sub-tabs (nothing deleted, nav 25→6).
- Visuals wanted (ALL): sparkline trends per symbol everywhere · market heatmap
  · big candlestick with signal markers · gauges/badges for breadth/VIX/confidence.
- "Broken tabs": insiders/institutions are empty → ROOT CAUSE FOUND: SEC 403s
  our EDGAR requests (User-Agent rejected by SEC's WAF; also 13f-poller has
  never completed a run). Fix the data, don't just restyle the empty page.

## Target information architecture (6 nav items)
1. **DASHBOARD** (/) — the one screen: ticker tape → visual band (market heatmap
   of the universe; featured big candle w/ score/regime/breakout markers; gauge
   row: breadth, VIX regime, market pulse, prediction confidence) → main feed
   (news + filings + anomalies + daily briefing merged, filterable chips) with
   right sidebar (watchlist w/ sparklines, unread alerts, top movers). Mobile:
   sidebar stacks below.
2. **MARKETS** — sub-tabs: Screener(+discover) · Trends · Regimes · Macro.
   Universe table gets sparklines + regime chips + rank; heatmap view toggle.
3. **SIGNALS** — sub-tabs: Predictions(+calibration+model self-knowledge) ·
   Forecasts · Insights · Alerts center · Unusual activity.
4. **INTEL** (ALL stock intel in ONE place) — sub-tabs: News · Filings ·
   Insiders · Institutions · Congress. Per-symbol intel stays on symbol pages.
5. **LAB** — sub-tabs: Backtest · Signal-backtest · Risk · Portfolio · Paper ·
   Track record · Honesty · System (quality+agents+AI chat).
6. **HUD** — the live trading surface (PUSH-20 sync). NOT combined, per user.
Old URLs 301/redirect (or render the hub with the right sub-tab preselected).

## Build stages (sequential; keep 972 daemon tests + web build green)
S1 DATA FIX + AUDIT: SEC-compliant User-Agent ("SignalDeck/0.1 <contact email
   from daemon/.env or SIGNALDECK_EDGAR_UA env>") on every EDGAR request; find
   why 13f-poller never ran; verify against live SEC with ONE polite request
   in a temp harness, then trigger one real sweep; curl-audit all ~29 routes
   logged-in/out, write audit table to scratchpad for later stages; fix any
   hard 5xx/crash found.
S2 NAV/HUBS: Shell nav → 6 hubs; sub-tab component; route consolidation with
   redirects; every old page's content mounted under its hub.
S3 VISUAL KIT (consult ui-ux-pro-max skill for the design pass; keep the dark
   terminal identity + reading mode + a11y): <Sparkline> (SVG, 60d closes,
   colored by trend) · <Heatmap> (universe grid, %chg color, mcap-weighted
   sizing when available, click→symbol) · <Gauge> (semicircular dial + label,
   honest n/gate captions) · <BigCandle> (lightweight-charts + existing signal
   overlays, symbol switcher from watchlist/movers).
S4 DASHBOARD rebuild per the hybrid layout above; one API roundup endpoint
   (/api/dashboard) to avoid 10 client fetches; 60s cache.
S5 HUB PAGES: Markets/Signals/Intel/Lab assembled from existing components +
   sparklines in every symbol table; empty states must say WHY + when data
   arrives (e.g. congress dead-mirror note stays honest).
S6 REVIEW/VERIFY: full builds; route-by-route table (status, has-data,
   empty-reason); fix findings; report.

Honesty rules: no fabricated data to fill panels; gauges/badges carry their
gate captions; heatmap greys symbols lacking bars; every merged surface keeps
its lag/proxy notes.
