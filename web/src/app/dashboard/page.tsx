"use client";

// DASHBOARD — the decisive home page, thin composition only. The old
// ~1,190-line monolith is split into src/components/home/* (view pieces) and
// src/hooks/useDashboard*.ts (state + polling); everything still runs off ONE
// api.dashboard() roundup (server caches shared sections 60s and says so).
//
// ORDER IS NO LONGER FIXED. The page is a map of named blocks; which ones lead
// and which fold into the bottom disclosure comes from the reader's goal via
// lib/goal.ts. See DASHBOARD_LAYOUTS there for what each goal changes and why.
// Nothing is ever removed — a goal only demotes, so switching is not
// destructive and needs no warning.
//
// HONESTY: nothing is fabricated to fill a panel — gauges keep their gate
// captions in BOTH simple and pro modes, every price is a stored daily close on
// worker cadence, and every empty state says WHY it is empty. The opening hero
// leads with a VALIDATED structural regime call (measured skill on a balanced
// label); the directional P(up) is demoted inside it to a labelled experimental
// footnote that states its own negative live record. See TodaysRead.tsx for the
// measurements behind that ordering.

import Link from "next/link";
import { useMemo } from "react";
import type { Market } from "@/lib/api";
import { ago } from "@/lib/format";
import useDashboard from "@/hooks/useDashboard";
import { foldedBlocks, layoutFor, useGoal, type BlockId } from "@/lib/goal";
import PagePurpose from "@/components/PagePurpose";
import StorySection from "@/components/StorySection";
import TickerTape from "@/components/TickerTape";
import FreshnessBadge from "@/components/FreshnessBadge";
import HelpTip from "@/components/HelpTip";
import Heatmap, { type HeatmapItem } from "@/components/viz/Heatmap";
import BigCandle, { type ChipSym } from "@/components/viz/BigCandle";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import TodaysRead from "@/components/home/TodaysRead";
import InsightSpotlight from "@/components/home/InsightSpotlight";
import VolRegimeLead from "@/components/home/VolRegimeLead";
import SetupChecklist from "@/components/home/SetupChecklist";
import GoalBanner from "@/components/home/GoalBanner";
import GaugeRow from "@/components/home/GaugeRow";
import DashFeed from "@/components/home/DashFeed";
import WatchlistPanel from "@/components/home/WatchlistPanel";
import AlertsPanel from "@/components/home/AlertsPanel";
import MoversMini from "@/components/home/MoversMini";
import ProofStrip from "@/components/home/ProofStrip";

export default function DashboardPage() {
  const { dash, error, errorStatus, alerts, featured, refresh } = useDashboard();
  const goal = useGoal();
  const layout = layoutFor(goal);

  // Heatmap items from the roundup (stocks universe; ETFs already excluded).
  const heatItems: HeatmapItem[] = useMemo(
    () =>
      (dash?.heatmap.items ?? []).map((c) => ({
        symbol: c.symbol,
        changePct: c.changePct,
        mcap: c.mcap,
        name: c.name,
        market: "stocks" as Market,
      })),
    [dash],
  );

  // Chart chip strip from the same roundup: watchlist first, then movers.
  const chartChips: ChipSym[] = useMemo(() => {
    if (!dash) return [];
    const acc: ChipSym[] = [];
    for (const r of dash.watchlist?.sparks ?? []) {
      acc.push({ symbol: r.symbol, market: r.market, src: "watchlist" });
    }
    for (const m of [...(dash.movers.gainers ?? []), ...(dash.movers.losers ?? [])]) {
      acc.push({ symbol: m.symbol, market: "stocks", src: "mover" });
    }
    return acc;
  }, [dash]);

  const watchlistEmpty =
    dash !== null && (dash.watchlist === null || (dash.watchlist.sparks ?? []).length === 0);

  // Every block the dashboard can show, keyed by id. Built only when there is
  // data; the loading and error branches below return before this is used.
  const blocks: Record<BlockId, React.ReactNode> = dash === null
    ? ({} as Record<BlockId, React.ReactNode>)
    : {
        // THE OPENING VERDICT: the single best-evidenced read, or an honest
        // "No qualified read today" when nothing clears the gate.
        read: <TodaysRead dash={dash} />,

        // THE VALIDATED FORECAST: promoted out of the tile grid on 2026-07-25.
        // The volatility regime is the only claim here that has been re-tested
        // and independently re-implemented, and the options surface is built
        // on it — presenting it alongside unvalidated signals understated the
        // one thing that holds up.
        vol: <VolRegimeLead />,

        // Secondary hero: what else is notable right now (alerts, regime
        // changes, anomalies) via the deterministic ladder.
        spotlight: <InsightSpotlight dash={dash} alerts={alerts} />,

        mood: (
          <StorySection
            n={1}
            title="MARKET MOOD"
            sub="what the whole market is doing today — stored closes on worker cadence"
          >
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
              <section className="panel" aria-label="market heatmap">
                <div className="panel-h flex-wrap gap-2">
                  <span>MARKET HEATMAP</span>
                  <span className="chip tnum px-2 py-[1px] text-[0.75rem]">
                    {dash.heatmap.n} symbols
                  </span>
                  <span className="chip tnum px-2 py-[1px] text-[0.75rem]">
                    {dash.heatmap.mcapCovered} mcap-sized
                  </span>
                  <HelpTip label="About this heatmap">
                    {dash.heatmap.note} {dash.heatmap.mcapNote} {dash.heatmap.sizeNote}
                  </HelpTip>
                  <span
                    className="ml-auto text-[0.75rem] font-normal normal-case tracking-normal"
                    style={{ color: "var(--faint)" }}
                  >
                    day % change · stored closes, worker cadence
                  </span>
                </div>
                <div className="max-h-[430px] overflow-y-auto p-3">
                  <Heatmap items={heatItems} />
                </div>
              </section>

              {featured ? (
                <BigCandle
                  symbol={featured.symbol}
                  market={featured.market}
                  height={380}
                  presetChips={chartChips}
                />
              ) : (
                <div className="panel p-3">
                  <Skeleton lines={8} label="loading featured chart" />
                </div>
              )}
            </div>
          </StorySection>
        ),

        gauges: (
          <StorySection
            n={2}
            title="MARKET GAUGES"
            sub="breadth, volatility, unusual activity, prediction confidence — each with its honesty caption"
          >
            <GaugeRow dash={dash} />
          </StorySection>
        ),

        changed: (
          <StorySection
            n={3}
            title="WHAT CHANGED TODAY"
            sub="news, filings, unusual activity — with your watchlist and alerts alongside"
          >
            <div className="grid grid-cols-1 items-start gap-3 lg:grid-cols-3">
              <DashFeed feed={dash.feed} />

              {/* sidebar: watchlist (or sign-in), alerts, movers */}
              <div className="flex flex-col gap-3">
                {dash.watchlist !== null ? (
                  <>
                    <WatchlistPanel wl={dash.watchlist} onChanged={refresh} />
                    <AlertsPanel alerts={alerts} unseen={dash.watchlist.unseenAlerts} />
                  </>
                ) : (
                  <section className="panel" aria-label="watchlist">
                    <div className="panel-h">WATCHLIST</div>
                    <p className="px-4 py-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                      Watchlists and alerts are per-account.{" "}
                      <Link
                        href="/login"
                        className="cursor-pointer font-bold text-[var(--accent)] underline transition-colors duration-150 hover:text-[var(--text)]"
                      >
                        Sign in
                      </Link>{" "}
                      to monitor symbols with sparklines, 1d verdict cards and unread
                      alerts.
                    </p>
                  </section>
                )}
                <MoversMini movers={dash.movers} />
              </div>
            </div>
          </StorySection>
        ),

        proof: (
          <StorySection
            n={4}
            title="FORECAST TRACK RECORD"
            sub="is any of this actually right? measured against real outcomes, gates and all"
          >
            <ProofStrip />
          </StorySection>
        ),
      };

  const folded = foldedBlocks(layout);

  return (
    <div className="flex flex-col gap-3">
      {/* ticker tape — fed from the roundup (no second /api/tape call);
          hidden while the payload is loading or empty (honest quiet) */}
      <TickerTape items={dash === null ? null : dash.tape.items} note={dash?.tape.note} />

      {/* slim status row — aria-live so the freshness/error state is announced
          when the 60s poll swaps it, not only when someone happens to look */}
      <div
        className="flex flex-wrap items-center gap-x-3 gap-y-2"
        aria-live="polite"
        aria-atomic="false"
      >
        <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">DASHBOARD</h1>
        {dash !== null && (
          <span className="chip tnum" title={dash.note}>
            as of {ago(dash.asOf)} · cached ≤{dash.cacheTtlS}s
          </span>
        )}
        <FreshnessBadge />
        {error && dash !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            refresh failed — retrying
          </span>
        )}
      </div>

      {/* what this page answers, in plain English */}
      <PagePurpose
        id="dashboard"
        text="What is the market doing right now, and where do your symbols stand? The order below is arranged around what you told us you're here for — change it any time on the strip beneath."
      />

      {/* first-load states: full-width skeleton band / recoverable error */}
      {dash === null && !error && (
        <>
          <div className="panel p-3">
            <Skeleton lines={3} label="loading top insight" />
          </div>
          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <div className="panel p-3">
              <Skeleton lines={8} label="loading market heatmap" />
            </div>
            <div className="panel p-3">
              <Skeleton lines={8} label="loading featured chart" />
            </div>
          </div>
          <div className="panel p-3">
            <Skeleton lines={3} label="loading gauges" />
          </div>
          <div className="grid grid-cols-1 items-start gap-3 lg:grid-cols-3">
            <div className="panel p-3 lg:col-span-2">
              <Skeleton lines={6} label="loading feed" />
            </div>
            <div className="panel p-3">
              <Skeleton lines={4} label="loading sidebar" />
            </div>
          </div>
        </>
      )}
      {dash === null && error && (
        <ErrorState
          message={error}
          hint={
            errorStatus === 401
              ? "Your session has ended — sign in again to load the deck."
              : errorStatus >= 500 || errorStatus === 0
                ? "The SignalDeck daemon looks offline — start signaldeckd (:8322) and this page will recover on its own."
                : "The daemon answered but refused this request; retry, or check /lab/system for the fleet state."
          }
          retry={refresh}
        />
      )}

      {dash !== null && (
        <>
          {/* Setup progress — replaces the old dismissible welcome card. It
              tracks real state and removes itself at 4/4, so there is no
              dismiss button and no stale "remembered" flag. */}
          {/* One onboarding surface at a time: the goal question first, the
              checklist only once a goal has been chosen. */}
          {goal ? <SetupChecklist watchlistEmpty={watchlistEmpty} /> : null}

          {/* Who this arrangement is for, and the control that changes it. */}
          <GoalBanner />

          {layout.lead.map((id) => (
            <div key={id} data-block={id}>
              {blocks[id]}
            </div>
          ))}

          {/* Demoted, never deleted. Native <details>: keyboard- and
              screen-reader-correct with no JavaScript and no state to keep. */}
          {folded.length > 0 && (
            <details className="panel">
              <summary
                className="flex min-h-[44px] cursor-pointer list-none items-center px-4 py-2 text-[0.8rem] sm:px-5"
                style={{ color: "var(--dim)" }}
              >
                Show the rest of the dashboard ({folded.length}{" "}
                {folded.length === 1 ? "panel" : "panels"})
              </summary>
              <div
                className="flex flex-col gap-3 border-t p-3"
                style={{ borderColor: "var(--border)" }}
              >
                {folded.map((id) => (
                  <div key={id} data-block={id}>
                    {blocks[id]}
                  </div>
                ))}
              </div>
            </details>
          )}
        </>
      )}
    </div>
  );
}
