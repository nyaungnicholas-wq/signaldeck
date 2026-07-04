# SignalDeck — Stage 6 FINAL route audit (2026-07-04)

Harness: isolated daemon (fresh temp DB, first-boot seed watchlist) on :8399 +
`next start` of the production build on :8329 (and a `next dev` on :8331 pointed
at :8399 via SIGNALDECK_DAEMON for logged-in checks). Live daemon :8322 and live
web :8323 were only ever read — never restarted. All harness processes killed
after the audit; launch.json restored.

Builds at audit time: `go build` / `go vet` clean, **993 tests / 54 packages**
(972 baseline + Stage-3/5 additions + 2 new regression tests below);
`npm run build` green, **31 routes** (29 baseline + /lab/system + /lab/signal-backtest
as first-class sub-tab routes).

## Old flat URLs → hub sub-tabs (all 307, query string preserved)

| Old URL | Redirects to | Status | Notes |
|---|---|---|---|
| /screener | /markets/screener | 307 → 200 | OK |
| /trends | /markets/trends | 307 → 200 | OK |
| /regime | /markets/regimes | 307 → 200 | OK |
| /macro | /markets/macro | 307 → 200 | OK |
| /predict | /signals/predictions | 307 → 200 | OK |
| /forecast | /signals/forecasts | 307 → 200 | OK |
| /insights | /signals/insights | 307 → 200 | OK |
| /alerts | /signals/alerts | 307 → 200 | OK (bell deep-link keeps working) |
| /news | /intel/news | 307 → 200 | OK |
| /filings | /intel/filings | 307 → 200 | OK |
| /insiders | /intel/insiders | 307 → 200 | OK |
| /institutions | /intel/institutions | 307 → 200 | OK — `?manager=CIK` preserved (tested) |
| /congress | /intel/congress | 307 → 200 | OK |
| /backtest | /lab/backtest | 307 → 200 | OK |
| /signal-backtest | /lab/signal-backtest | 307 → 200 | OK |
| /risk | /lab/risk | 307 → 200 | OK |
| /portfolio | /lab/portfolio | 307 → 200 | OK |
| /paper | /lab/paper | 307 → 200 | OK |
| /track-record | /lab/track-record | 307 → 200 | OK |
| /honesty | /lab/honesty | 307 → 200 | OK |
| /quality | /lab/system/quality | 307 → 200 | OK |
| /agents | /lab/system/agents | 307 → 200 | OK |
| /ai | /lab/system/ai | 307 → 200 | OK |
| /markets | /markets/screener | 307 → 200 | hub index → default sub-tab |
| /signals | /signals/predictions | 307 → 200 | hub index → default sub-tab |
| /intel | /intel/news | 307 → 200 | hub index → default sub-tab |
| /lab | /lab/backtest | 307 → 200 | hub index → default sub-tab |

All 27 redirects verified with curl: correct 307 + exact Location. Sub-tab
preselection confirmed in rendered HTML (`aria-current="page"` on the right
hub AND the right sub-tab, e.g. /markets/regimes → MARKETS + REGIMES active;
/lab/system/ai → LAB + SYSTEM + AI, three levels).

## Hub pages (all 200, no "Application error" markers, 6-hub nav present)

| Route | Hub / sub-tab | Status | Data / empty-reason (fresh-DB harness) |
|---|---|---|---|
| / | DASHBOARD | 200 | Tape + heatmap + gauges + merged feed + sidebar off ONE /api/dashboard call; on a fresh DB: feed has briefing+news, heatmap/breadth honestly empty ("no fresh bars" — nothing grey-faked), gauges carry gate captions (breadth n, confidence minResolvedN, VIX hasData). Anonymous: watchlist=null → UI says log in. |
| /hud | HUD (kept separate) | 200 | PUSH-20 sync surface; empty state explains when trader-hud (:8787) is offline. |
| /markets/screener | MARKETS / SCREENER | 200 | Universe table + sparklines + regime chips + rank; heatmap view toggle. Empty rows say why (no bars yet). |
| /markets/trends | MARKETS / TRENDS | 200 | Renders; per-symbol trend sparklines once bars exist. |
| /markets/regimes | MARKETS / REGIMES | 200 | Regime table; honest "insufficient history" gates. |
| /markets/macro | MARKETS / MACRO | 200 | FRED series + VIX gauge with caption; empty until FRED poll. |
| /signals/predictions | SIGNALS / PREDICTIONS | 200 | Confidence badges + calibration; gated "backtested-not-live" captions intact. |
| /signals/forecasts | SIGNALS / FORECASTS | 200 | Renders; per-horizon forecast cards. |
| /signals/insights | SIGNALS / INSIGHTS | 200 | Insight feed (works with legacy rows post-fix). |
| /signals/alerts | SIGNALS / ALERTS | 200 | Session-scoped; logged-out → explains login needed. |
| /signals/unusual | SIGNALS / UNUSUAL | 200 | UnusualActivityPanel + kind filters; empty says why. |
| /intel/news | INTEL / NEWS | 200 | News feed (harness had live Benzinga rows minutes after boot). |
| /intel/filings | INTEL / FILINGS | 200 | EDGAR filings; SEC-compliant UA fix from S1; empty state says when sweep lands. |
| /intel/insiders | INTEL / INSIDERS | 200 | Form-4 intel; honest sweep empty state (S1 root-cause fix). |
| /intel/institutions | INTEL / INSTITUTIONS | 200 | 13F holdings; ?manager= filter preserved via redirect. |
| /intel/congress | INTEL / CONGRESS | 200 | Dead-mirror note stays honest. |
| /lab/backtest | LAB / BACKTEST | 200 | Strategy backtests. |
| /lab/signal-backtest | LAB / SIGNAL-BACKTEST | 200 | Own-signal OOS panel; honest insufficient-data state. |
| /lab/risk | LAB / RISK | 200 | Risk surface. |
| /lab/portfolio | LAB / PORTFOLIO | 200 | Session-scoped ledger. |
| /lab/paper | LAB / PAPER | 200 | Paper trading. |
| /lab/track-record | LAB / TRACK RECORD | 200 | Resolved-scores record with sample sizes. |
| /lab/honesty | LAB / HONESTY | 200 | Honesty grades; IC-artifact labeling intact. |
| /lab/system | LAB / SYSTEM (all-in-one) | 200 | Quality + agents + AI mounted together (3-level tabs verified). |
| /lab/system/quality | LAB / SYSTEM / QUALITY | 200 | Data-quality stats. |
| /lab/system/agents | LAB / SYSTEM / AGENTS | 200 | Worker-run status. |
| /lab/system/ai | LAB / SYSTEM / AI | 200 | AI chat (spend cap in DB). |
| /login | (chrome only, no hub active) | 200 | Sign-in form; AuthChip/bell intentionally absent here. |
| /s/stocks/AAPL | symbol deep-dive | 200 | Candle + overlays + panels; logged-out safe. |
| /s/crypto/BTC%2FUSD | symbol deep-dive | 200 | Crypto variant (micro panel). |

## Nav (real browser, production build)

Header = logo + exactly **6 hubs** (DASHBOARD · MARKETS · SIGNALS · INTEL · LAB ·
HUD — verified in live DOM and in every page's SSR HTML) + AlertsBell + AuthChip +
reading-mode toggle + daemon dot. Bell and auth chip are session-conditional by
design: logged out, AuthGate redirects to /login (verified in browser) and the
bell hides (alerts are session-scoped 401); logged in (verified via API session
on the isolated daemon), /api/dashboard returns the per-user watchlist section
(sparks/sparkPoints/unseenAlerts) and the alerts endpoint feeds the bell count.

## /api/dashboard contract (isolated NEW daemon)

- 200, `Content-Type: application/json`, `Cache-Control: no-store` (browser never
  caches; freshness is server-owned).
- Top-level keys: asOf, cacheTtlS(=60), note, tape, heatmap, movers, gauges, feed,
  watchlist (null anonymous; object with a session).
- Server-side 60s TTL confirmed: identical `asOf` across calls 2s apart; cached
  responses ~3ms.
- All four gauges ship honesty captions (breadth n/advancers/decliners,
  confidence gated+minResolvedN, VIX hasData+regime, anomalies windowH).

## Bug found in verify + fixed inline (daemon)

**/api/dashboard 500 "SQL logic error: malformed JSON (1)".** Root cause: the
Risk-watcher agent persists insights without an evidence blob; `Insight.Data`'s
Go zero value `''` was inserted verbatim, and `InsightsByKind`'s bare
`json_extract(i.data,…)` fails the WHOLE query on the first non-JSON row —
one risk-watcher run would take down the new dashboard feed in production.
Fixes:
1. `internal/store/briefing.go` — `WHERE json_valid(i.data) AND json_extract(…)`
   (reader tolerates legacy `''` rows already on disk — the live DB likely has them).
2. `internal/store/store.go` — `InsertInsight` normalizes unset Data `""` → `'{}'`
   (the schema's own NOT NULL DEFAULT; valid JSON, still not a kind-match).
3. `internal/store/insights_malformed_test.go` — 2 regression tests (writer
   normalization + reader tolerance of a seeded legacy `''` row).
Re-verified against the harness DB that contained two real malformed rows:
/api/dashboard now 200.

## Deployment note (not a code issue)

The RUNNING daemon on :8322 is the pre-Stage-3 binary — it has no /api/dashboard
(404 via the live proxy). The new dashboard shows its ErrorState until the daemon
binary is redeployed (rebuild + restart, which this verify stage was not allowed
to do). Redeploy also delivers the malformed-JSON fix. Web rewrites bake
SIGNALDECK_DAEMON at build time (next start reads routes-manifest), so production
web needs no rebuild — target stays 127.0.0.1:8322.
