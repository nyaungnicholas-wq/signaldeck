"use client";

// STAGE 4 — DASHBOARD REBUILD. The user's hybrid layout on ONE roundup call:
//   ticker tape (fed from the roundup — no second /api/tape fetch)
//   → VISUAL BAND: universe heatmap (left) + featured BigCandle (right;
//     featured = first watchlist symbol, else top mover, else SPY)
//   → GAUGE ROW: breadth · VIX regime · unusual-activity 24h · prediction
//     confidence — every dial renders its honesty gate caption verbatim
//   → MAIN: merged feed (news/filings/anomalies/briefings, kind filter chips,
//     daily-briefing card pinned when today's exists) 2/3 + SIDEBAR 1/3
//     (watchlist w/ sparklines + day% + 1d score chip + add/unwatch, unread
//     alerts, top-movers mini-table). Mobile (<lg): band stacks heatmap →
//     candle → gauges 2×2; sidebar stacks after the feed (DOM order).
//
// DATA: ONE api.dashboard() roundup polled every 60s (server caches shared
// sections 60s and says so) + the existing session-scoped api.alerts fetch
// when logged in + BigCandle's own lazy bars/overlays fetch. No waterfalls.
//
// HONESTY: nothing is fabricated to fill a panel — gauges keep their gate
// captions, the heatmap's unknown-mcap cells stay uniform and labeled, every
// price is a stored daily close on worker cadence, and every empty state says
// WHY it is empty and when data arrives.

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import {
  api,
  dashboard,
  type AlertRow,
  type DashboardResponse,
  type DashFeedItem,
  type DashWatchSpark,
  type Market,
} from "@/lib/api";
import { ago, fmtPct, fmtScore, scoreColor } from "@/lib/format";
import TickerTape from "@/components/TickerTape";
import Heatmap, { type HeatmapItem } from "@/components/viz/Heatmap";
import BigCandle, { type ChipSym } from "@/components/viz/BigCandle";
import Gauge from "@/components/viz/Gauge";
import Sparkline from "@/components/viz/Sparkline";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

type FeedKind = DashFeedItem["kind"];
type KindFilter = "all" | FeedKind;

const KIND_FILTERS: { k: KindFilter; label: string }[] = [
  { k: "all", label: "all" },
  { k: "news", label: "news" },
  { k: "filing", label: "filings" },
  { k: "anomaly", label: "anomalies" },
  { k: "briefing", label: "briefings" },
];

/** Today's date key in America/New_York (the daily-briefing worker's clock). */
function nyToday(): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/New_York" }).format(new Date());
}

/** NY-day key for a unix ts — pins the briefing card only for TODAY's brief. */
function nyDayOf(ts: number): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "America/New_York" }).format(
    new Date(ts * 1000),
  );
}

function changeColor(v: number): string {
  if (!Number.isFinite(v) || v === 0) return "var(--dim)";
  return v > 0 ? "var(--bid)" : "var(--ask)";
}

function sentimentColor(s?: string): string {
  if (s === "bullish") return "var(--bid)";
  if (s === "bearish") return "var(--ask)";
  return "var(--faint)";
}

function feedSymbolMarket(it: DashFeedItem): Market {
  if (it.market === "crypto" || it.market === "stocks") return it.market;
  return it.symbol?.includes("/") ? "crypto" : "stocks";
}

// ── feed pieces ──────────────────────────────────────────────────────────

function SymbolChip({ symbol, market }: { symbol?: string; market: Market }) {
  if (!symbol) return null;
  return (
    <Link
      href={`/s/${market}/${encodeURIComponent(symbol)}`}
      className="chip cursor-pointer px-2 py-[1px] text-[0.68rem] font-bold tracking-wide transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
    >
      {symbol}
    </Link>
  );
}

/** Kind badge per feed row — color is never the only signal (text label). */
function KindBadge({ it }: { it: DashFeedItem }) {
  if (it.kind === "filing") {
    return (
      <span
        className="chip px-2 py-[1px] text-[0.65rem] tracking-wider"
        style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
        title="SEC EDGAR filing — filings lag by law/process (Form 4 ~2 business days; 13F quarterly)"
      >
        {it.sub ? `sec-filing · ${it.sub}` : "sec-filing"}
      </span>
    );
  }
  if (it.kind === "anomaly") {
    return (
      <span
        className="chip tnum px-2 py-[1px] text-[0.65rem] tracking-wider"
        style={{ color: "var(--crossed)", borderColor: "var(--crossed)" }}
        title="descriptive z-score vs the symbol's own baseline — NOT a prediction"
      >
        anomaly{it.sub ? ` · ${it.sub}` : ""}
        {typeof it.z === "number" && it.z !== 0 ? ` · z=${it.z.toFixed(1)}` : ""}
      </span>
    );
  }
  if (it.kind === "briefing") {
    return (
      <span
        className="chip px-2 py-[1px] text-[0.65rem] tracking-wider"
        style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
      >
        briefing
      </span>
    );
  }
  // news: the LLM's sentiment tag, shown as a tag — not a recommendation.
  if (it.sentiment && it.sentiment !== "unrated" && it.sentiment !== "neutral") {
    return (
      <span
        className="chip px-2 py-[1px] text-[0.65rem] tracking-wider"
        style={{ color: sentimentColor(it.sentiment), borderColor: sentimentColor(it.sentiment) }}
        title="model-rated sentiment tag — not a recommendation"
      >
        {it.sentiment}
      </span>
    );
  }
  return (
    <span className="chip px-2 py-[1px] text-[0.65rem] tracking-wider" style={{ color: "var(--faint)" }}>
      news
    </span>
  );
}

function FeedRow({ it }: { it: DashFeedItem }) {
  return (
    <li
      className="flex flex-wrap items-baseline gap-x-2 gap-y-1 border-t px-4 py-2 text-[0.78rem]"
      style={{ borderColor: "var(--border)" }}
    >
      <SymbolChip symbol={it.symbol} market={feedSymbolMarket(it)} />
      <KindBadge it={it} />
      <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
        {ago(it.ts)}
      </span>
      <span className="w-full leading-relaxed">
        {it.url ? (
          <a
            href={it.url}
            target="_blank"
            rel="noopener noreferrer"
            className="cursor-pointer transition-colors duration-150 hover:text-[var(--accent)]"
            style={{ color: "var(--text)" }}
          >
            {it.title}
          </a>
        ) : (
          it.title
        )}
        {it.detail && it.kind !== "briefing" && (
          <span className="ml-2 text-[0.68rem]" style={{ color: "var(--faint)" }}>
            {it.detail}
          </span>
        )}
      </span>
      {it.detail && it.kind === "briefing" && (
        <span className="w-full text-[0.72rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {it.detail}
        </span>
      )}
    </li>
  );
}

/** Pinned daily-briefing card — only when TODAY's briefing is in the feed. */
function PinnedBriefing({ it }: { it: DashFeedItem }) {
  return (
    <section
      aria-label="daily briefing"
      className="border-b px-4 py-3 sm:px-5"
      style={{ borderColor: "var(--accent)", background: "var(--panel2)" }}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span
          className="chip px-2 py-[2px] text-[0.72rem] tracking-wider"
          style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
        >
          DAILY BRIEFING
        </span>
        <span className="text-[0.85rem] font-bold tracking-wide">{it.title}</span>
        <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {ago(it.ts)}
        </span>
      </div>
      {it.detail && (
        <p className="mt-2 text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {it.detail}
        </p>
      )}
      <Link
        href="/signals/insights"
        className="mt-2 inline-flex min-h-[40px] cursor-pointer items-center text-[0.75rem] tracking-wider transition-colors duration-150 hover:text-[var(--accent)]"
        style={{ color: "var(--faint)" }}
      >
        all insights →
      </Link>
    </section>
  );
}

// ── gauge row pieces ─────────────────────────────────────────────────────

/** Count tile matching the Gauge footprint — a dial needs a bounded scale and
 *  a 24h anomaly count has none, so the honest shape is a plain number. */
function StatTile({
  value,
  label,
  sub,
  caption,
}: {
  value: string;
  label: string;
  sub: string;
  caption: string;
}) {
  return (
    <figure
      className="m-0 flex flex-col items-center"
      style={{ width: 168 }}
      role="img"
      aria-label={`${label}: ${value} ${sub}. ${caption}`}
    >
      <div className="flex h-[99px] flex-col items-center justify-center gap-0.5">
        <span className="tnum text-[1.7rem] font-bold leading-none">{value}</span>
        <span className="text-[0.68rem]" style={{ color: "var(--dim)" }}>
          {sub}
        </span>
      </div>
      <figcaption className="flex flex-col items-center gap-0.5 text-center">
        <span className="text-[0.68rem] font-medium tracking-[0.14em]" style={{ color: "var(--dim)" }}>
          {label}
        </span>
        <span className="text-[0.62rem] leading-snug" style={{ color: "var(--faint)" }}>
          {caption}
        </span>
      </figcaption>
    </figure>
  );
}

function vixRegimeColor(regime?: string): string {
  if (regime === "calm") return "var(--bid)";
  if (regime === "elevated") return "var(--warn)";
  if (regime === "stressed") return "var(--ask)";
  return "var(--dim)";
}

function GaugeRow({ dash }: { dash: DashboardResponse }) {
  const g = dash.gauges;
  return (
    <section className="panel" aria-label="market gauges">
      <div className="panel-h">
        <span>MARKET GAUGES</span>
        <span
          className="text-[0.65rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          stored data on worker cadence — each dial keeps its gate caption
        </span>
      </div>
      <div className="grid grid-cols-2 justify-items-center gap-x-2 gap-y-5 px-3 py-4 lg:grid-cols-4">
        <div className="flex flex-col items-center gap-1">
          <Gauge
            value={g.breadth.pct}
            min={0}
            max={100}
            label="BREADTH"
            caption={g.breadth.caption}
            hasData={g.breadth.hasData}
            format={(v) => `${v.toFixed(0)}%`}
            zones={[
              { from: 0, to: 45, color: "var(--ask)" },
              { from: 55, to: 100, color: "var(--bid)" },
            ]}
          />
          {g.breadth.hasData && (
            <span className="tnum text-[0.68rem]" style={{ color: "var(--dim)" }}>
              <span style={{ color: "var(--bid)" }}>{g.breadth.advancers} adv</span>
              {" · "}
              <span style={{ color: "var(--ask)" }}>{g.breadth.decliners} dec</span>
            </span>
          )}
        </div>

        <div className="flex flex-col items-center gap-1">
          <Gauge
            value={g.vix.level ?? 0}
            min={10}
            max={40}
            label="VIX REGIME"
            caption={g.vix.caption}
            hasData={g.vix.hasData}
            format={(v) => v.toFixed(1)}
            zones={[
              { from: 10, to: 15, color: "var(--bid)" },
              { from: 20, to: 30, color: "var(--warn)" },
              { from: 30, to: 40, color: "var(--ask)" },
            ]}
          />
          {g.vix.hasData && g.vix.regime && (
            <span className="text-[0.68rem] font-bold tracking-wider" style={{ color: vixRegimeColor(g.vix.regime) }}>
              {g.vix.regime.toUpperCase()}
              {typeof g.vix.dayChangePct === "number" && (
                <span className="tnum ml-1 font-normal" style={{ color: changeColor(g.vix.dayChangePct) }}>
                  {fmtPct(g.vix.dayChangePct)}
                </span>
              )}
            </span>
          )}
        </div>

        <StatTile
          value={String(g.anomalies.count)}
          label="UNUSUAL ACTIVITY"
          sub={`events, last ${g.anomalies.windowH}h`}
          caption={g.anomalies.caption}
        />

        <Gauge
          value={g.confidence.avg}
          min={0}
          max={1}
          label="PREDICTION CONFIDENCE"
          caption={g.confidence.caption}
          hasData={g.confidence.hasData}
          format={(v) => `${(v * 100).toFixed(0)}%`}
        />
      </div>
    </section>
  );
}

// ── sidebar pieces ───────────────────────────────────────────────────────

function WatchRowItem({
  r,
  onUnwatch,
}: {
  r: DashWatchSpark;
  onUnwatch: (r: DashWatchSpark) => void;
}) {
  const score = typeof r.score1d === "number" && Number.isFinite(r.score1d) ? r.score1d : null;
  return (
    <li
      className="flex items-center gap-2 border-t px-3 py-2"
      style={{ borderColor: "var(--border)" }}
    >
      <Link
        href={`/s/${r.market}/${encodeURIComponent(r.symbol)}`}
        className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 transition-colors duration-150 hover:text-[var(--accent)]"
        aria-label={`open ${r.symbol} (${r.market})`}
      >
        <span className="w-14 shrink-0 truncate text-[0.8rem] font-bold tracking-wide">
          {r.symbol}
        </span>
        <Sparkline closes={r.closes ?? []} width={88} height={26} />
        <span className="tnum ml-auto shrink-0 text-[0.75rem]" style={{ color: changeColor(r.dayChangePct) }}>
          {(r.closes?.length ?? 0) > 1 ? fmtPct(r.dayChangePct) : "—"}
        </span>
      </Link>
      {score !== null ? (
        <span
          className="chip tnum shrink-0 px-2 py-[1px] text-[0.68rem]"
          style={{ color: scoreColor(score) }}
          title="latest 1d ensemble score, [-1,+1] — backtested calibration, not a live track record"
        >
          {fmtScore(score)}
        </span>
      ) : (
        <span
          className="chip shrink-0 px-2 py-[1px] text-[0.68rem]"
          style={{ color: "var(--faint)" }}
          title="1d score pending — the scorer needs more stored history for this symbol"
        >
          —
        </span>
      )}
      <button
        type="button"
        onClick={() => onUnwatch(r)}
        aria-label={`stop watching ${r.symbol}`}
        className="shrink-0 cursor-pointer px-1 text-[0.8rem] transition-colors duration-150 hover:text-[var(--bad)]"
        style={{ color: "var(--faint)" }}
      >
        ×
      </button>
    </li>
  );
}

function WatchlistPanel({
  wl,
  onChanged,
}: {
  wl: NonNullable<DashboardResponse["watchlist"]>;
  onChanged: () => void;
}) {
  const [sym, setSym] = useState("");
  const [mkt, setMkt] = useState<Market>("stocks");
  const [adding, setAdding] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  async function onAdd(e: React.FormEvent) {
    e.preventDefault();
    const s = sym.trim().toUpperCase();
    if (!s || adding) return;
    setAdding(true);
    setActionError(null);
    try {
      await api.subscribe(s, mkt);
      setSym("");
      onChanged();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setAdding(false);
    }
  }

  function onUnwatch(r: DashWatchSpark) {
    if (!window.confirm(`Stop watching ${r.symbol} (${r.market})?`)) return;
    setActionError(null);
    api
      .unsubscribe(r.symbol, r.market)
      .then(onChanged)
      .catch((err) => setActionError(err instanceof Error ? err.message : String(err)));
  }

  const sparks = wl.sparks ?? [];
  return (
    <section className="panel" aria-label="watchlist">
      <div className="panel-h">
        <span>WATCHLIST</span>
        <span className="chip tnum ml-auto px-2 py-[1px] text-[0.68rem]">{sparks.length} tracked</span>
      </div>

      {sparks.length === 0 ? (
        <EmptyState
          message="No symbols tracked yet"
          detail="Add one below — e.g. AAPL (stocks) or BTC/USD (crypto). The daemon backfills history and scores on its own cadence."
        />
      ) : (
        <ul className="m-0 list-none p-0">
          {sparks.map((r) => (
            <WatchRowItem key={`${r.market}:${r.symbol}`} r={r} onUnwatch={onUnwatch} />
          ))}
        </ul>
      )}

      <form
        className="flex flex-wrap items-center gap-2 border-t px-3 py-2"
        style={{ borderColor: "var(--border)" }}
        onSubmit={onAdd}
      >
        <label htmlFor="wl-add-sym" className="sr-only">
          symbol to add
        </label>
        <input
          id="wl-add-sym"
          className="dash-input tnum w-24 min-w-0 flex-1 uppercase"
          value={sym}
          onChange={(e) => {
            setSym(e.target.value);
            if (actionError) setActionError(null);
          }}
          placeholder={mkt === "crypto" ? "BTC/USD" : "AAPL"}
          spellCheck={false}
          autoComplete="off"
        />
        <select
          className="dash-input cursor-pointer"
          value={mkt}
          onChange={(e) => setMkt(e.target.value as Market)}
          aria-label="market"
        >
          <option value="stocks">stocks</option>
          <option value="crypto">crypto</option>
        </select>
        <button
          type="submit"
          disabled={adding || sym.trim() === ""}
          className="dash-input dash-add cursor-pointer transition-colors duration-150"
          style={{ color: "var(--dim)" }}
        >
          {adding ? "adding…" : "add"}
        </button>
      </form>
      {actionError && (
        <p role="alert" className="px-3 pb-2 text-[0.72rem]" style={{ color: "var(--bad)" }}>
          {actionError}
        </p>
      )}
      <p
        className="border-t px-3 py-2 text-[0.65rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: "var(--faint)" }}
      >
        {wl.note}
      </p>
    </section>
  );
}

function alertKindColor(kind: string): string {
  if (kind === "breakout") return "var(--accent)";
  if (kind === "regime_change") return "var(--crossed)";
  if (kind === "prediction_high") return "var(--bid)";
  if (kind === "prediction_low") return "var(--ask)";
  return "var(--dim)";
}

function AlertsPanel({ alerts, unseen }: { alerts: AlertRow[] | null; unseen: number }) {
  return (
    <section className="panel" aria-label="unread alerts">
      <div className="panel-h">
        <span>ALERTS</span>
        {unseen > 0 && (
          <span
            className="chip tnum px-2 py-[1px] text-[0.68rem]"
            style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
          >
            {unseen} unread
          </span>
        )}
        <Link
          href="/signals/alerts"
          className="ml-auto cursor-pointer text-[0.68rem] font-normal normal-case tracking-wider transition-colors duration-150 hover:text-[var(--accent)]"
          style={{ color: "var(--faint)" }}
        >
          all →
        </Link>
      </div>
      {alerts === null && <Skeleton lines={2} label="loading alerts" />}
      {alerts !== null && alerts.length === 0 && (
        <p className="px-3 py-3 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          No unread alerts — the alerts engine writes breakout / regime-change /
          prediction events for your watched symbols as workers detect them.
        </p>
      )}
      {alerts !== null && alerts.length > 0 && (
        <ul className="m-0 list-none p-0">
          {alerts.map((a) => (
            <li
              key={a.id}
              className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 border-t px-3 py-2 text-[0.72rem]"
              style={{ borderColor: "var(--border)" }}
            >
              <span
                className="chip px-1.5 py-[1px] text-[0.62rem] tracking-wider"
                style={{ color: alertKindColor(a.kind), borderColor: alertKindColor(a.kind) }}
              >
                {a.kind.replace("_", " ")}
              </span>
              {a.symbol && (
                <Link
                  href={`/s/${a.market ?? "stocks"}/${encodeURIComponent(a.symbol)}`}
                  className="cursor-pointer font-bold tracking-wide transition-colors duration-150 hover:text-[var(--accent)]"
                >
                  {a.symbol}
                </Link>
              )}
              <span className="tnum ml-auto text-[0.65rem]" style={{ color: "var(--faint)" }}>
                {ago(a.ts)}
              </span>
              <span className="w-full leading-snug" style={{ color: "var(--dim)" }}>
                {a.detail}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function MoversMini({ dash }: { dash: DashboardResponse }) {
  const gainers = dash.movers.gainers ?? [];
  const losers = dash.movers.losers ?? [];
  const col = (title: string, rows: typeof gainers, bordered = false) => (
    <div
      className="min-w-0 flex-1"
      style={bordered ? { borderLeft: "1px solid var(--border)" } : undefined}
    >
      <div className="px-3 pb-1 pt-2 text-[0.65rem] tracking-[0.14em]" style={{ color: "var(--faint)" }}>
        {title}
      </div>
      <ul className="m-0 list-none p-0">
        {rows.map((m) => (
          <li key={m.symbol}>
            <Link
              href={`/s/stocks/${encodeURIComponent(m.symbol)}`}
              className="flex cursor-pointer items-baseline gap-2 px-3 py-1 text-[0.75rem] transition-colors duration-150 hover:text-[var(--accent)]"
            >
              <span className="truncate font-bold tracking-wide">{m.symbol}</span>
              <span className="tnum ml-auto" style={{ color: changeColor(m.changePct) }}>
                {fmtPct(m.changePct)}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
  return (
    <section className="panel" aria-label="top movers" title={dash.movers.note}>
      <div className="panel-h">TOP MOVERS</div>
      {gainers.length === 0 && losers.length === 0 ? (
        <p className="px-3 py-3 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          No fresh universe bars yet — movers appear as the universe poller
          stores daily closes on its own cadence.
        </p>
      ) : (
        <div className="flex pb-2">
          {col("GAINERS", gainers)}
          {col("LOSERS", losers, true)}
        </div>
      )}
    </section>
  );
}

// ── the page ─────────────────────────────────────────────────────────────

export default function DashboardPage() {
  const [dash, setDash] = useState<DashboardResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [alerts, setAlerts] = useState<AlertRow[] | null>(null);
  const [kindFilter, setKindFilter] = useState<KindFilter>("all");
  const [featured, setFeatured] = useState<{ symbol: string; market: Market } | null>(null);

  // ONE roundup, polled on the server's own cache TTL (60s).
  useEffect(() => {
    let alive = true;
    const load = () =>
      dashboard()
        .then((d) => {
          if (!alive) return;
          setDash(d);
          setError(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setError(e instanceof Error ? e.message : String(e));
        });
    load();
    const t = setInterval(load, 60_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [tick]);

  // Featured symbol: first watchlist symbol, else top mover, else SPY.
  // Locked in ONCE so a later poll never yanks the chart from under the user.
  useEffect(() => {
    if (!dash || featured) return;
    const wl = dash.watchlist?.sparks?.[0];
    const mover = dash.movers.gainers?.[0];
    if (wl) setFeatured({ symbol: wl.symbol, market: wl.market });
    else if (mover) setFeatured({ symbol: mover.symbol, market: "stocks" });
    else setFeatured({ symbol: "SPY", market: "stocks" });
  }, [dash, featured]);

  const loggedIn = dash !== null && dash.watchlist !== null;

  // Existing session-scoped alerts fetch — only once we know a session exists.
  useEffect(() => {
    if (!loggedIn) {
      setAlerts(null);
      return;
    }
    let alive = true;
    const load = () =>
      api
        .alerts(true, 8)
        .then((a) => alive && setAlerts(a))
        .catch(() => alive && setAlerts(null));
    load();
    const t = setInterval(load, 60_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [loggedIn]);

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

  // Feed: pinned briefing (today only) + kind-filtered stream.
  const feedItems = useMemo(() => dash?.feed.items ?? [], [dash]);
  const pinned = useMemo(() => {
    const today = nyToday();
    return feedItems.find((i) => i.kind === "briefing" && nyDayOf(i.ts) === today) ?? null;
  }, [feedItems]);
  const stream = useMemo(
    () =>
      feedItems.filter(
        (i) =>
          !(pinned && i.kind === "briefing" && i.ts === pinned.ts) &&
          (kindFilter === "all" || i.kind === kindFilter),
      ),
    [feedItems, pinned, kindFilter],
  );
  const kindCounts = useMemo(() => {
    const m = new Map<KindFilter, number>([["all", feedItems.length]]);
    for (const i of feedItems) m.set(i.kind, (m.get(i.kind) ?? 0) + 1);
    return m;
  }, [feedItems]);

  return (
    <div className="flex flex-col gap-3">
      <style>{`
        .dash-input {
          background: var(--panel2);
          border: 1px solid var(--border);
          border-radius: 6px;
          padding: 6px 10px;
          min-height: 40px;
          font-size: .72rem;
          color: var(--text);
          font-family: inherit;
        }
        .dash-input::placeholder { color: var(--faint); }
        .dash-add:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
        .dash-add:disabled { color: var(--faint); cursor: default; }
      `}</style>

      {/* ticker tape — fed from the roundup (no second /api/tape call);
          hidden while the payload is loading or empty (honest quiet) */}
      <TickerTape items={dash === null ? null : dash.tape.items} note={dash?.tape.note} />

      {/* slim status row */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">DASHBOARD</h1>
        {dash !== null && (
          <span className="chip tnum" title={dash.note}>
            as of {ago(dash.asOf)} · cached ≤{dash.cacheTtlS}s
          </span>
        )}
        {error && dash !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            refresh failed — retrying
          </span>
        )}
      </div>

      {/* first-load states: full-width skeleton band / recoverable error */}
      {dash === null && !error && (
        <>
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
          hint="The SignalDeck daemon looks offline — start signaldeckd (:8322) and this page will recover on its own. (If the daemon predates /api/dashboard, restart it on the current build.)"
          retry={() => {
            setError(null);
            setTick((t) => t + 1);
          }}
        />
      )}

      {dash !== null && (
        <>
          {/* ── VISUAL BAND: heatmap (left ~50%) + featured candle (right) ── */}
          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <section className="panel" aria-label="market heatmap" title={dash.heatmap.note}>
              <div className="panel-h flex-wrap gap-2">
                <span>MARKET HEATMAP</span>
                <span className="chip tnum px-2 py-[1px] text-[0.68rem]">{dash.heatmap.n} symbols</span>
                <span
                  className="chip tnum px-2 py-[1px] text-[0.68rem]"
                  title={dash.heatmap.mcapNote}
                >
                  {dash.heatmap.mcapCovered} mcap-sized
                </span>
                <span
                  className="ml-auto text-[0.65rem] font-normal normal-case tracking-normal"
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

          {/* ── GAUGE ROW ── */}
          <GaugeRow dash={dash} />

          {/* ── MAIN: merged feed 2/3 + sidebar 1/3 (stacks after feed <lg) ── */}
          <div className="grid grid-cols-1 items-start gap-3 lg:grid-cols-3">
            <section className="panel lg:col-span-2" aria-label="merged market feed">
              <div className="panel-h flex-wrap gap-2">
                <span>FEED</span>
                <span
                  className="text-[0.65rem] font-normal normal-case tracking-normal"
                  style={{ color: "var(--faint)" }}
                  title={dash.feed.note}
                >
                  news + filings + anomalies + briefings, newest first
                </span>
                {feedItems.length > 0 && (
                  <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
                    {stream.length} shown
                  </span>
                )}
              </div>

              {pinned && <PinnedBriefing it={pinned} />}

              {/* kind filter chips */}
              <div
                className="flex flex-wrap items-center gap-1.5 border-b px-3 py-2"
                style={{ borderColor: "var(--border)" }}
                role="group"
                aria-label="filter feed by kind"
              >
                {KIND_FILTERS.map((f) => {
                  const n = kindCounts.get(f.k) ?? 0;
                  const isOn = kindFilter === f.k;
                  return (
                    <button
                      key={f.k}
                      type="button"
                      aria-pressed={isOn}
                      onClick={() => setKindFilter(f.k)}
                      className="chip tnum min-h-[36px] cursor-pointer text-[0.7rem] transition-colors duration-150 hover:brightness-125"
                      style={isOn ? { color: "var(--text)", borderColor: "var(--accent)" } : undefined}
                    >
                      {f.label} {n}
                    </button>
                  );
                })}
              </div>

              {stream.length === 0 && (
                <EmptyState
                  message={
                    kindFilter === "all"
                      ? "No stories, filings or anomalies yet"
                      : `No ${kindFilter} items in the current feed window`
                  }
                  detail="The news fetcher, EDGAR poller, anomaly scanner and briefing worker each fill this stream on their own cadence — nothing is fabricated in the meantime."
                />
              )}
              {stream.length > 0 && (
                <ul className="m-0 list-none p-0">
                  {stream.map((it, i) => (
                    <FeedRow key={`${it.kind}:${it.ts}:${it.symbol ?? ""}:${i}`} it={it} />
                  ))}
                </ul>
              )}
              <p
                className="border-t px-4 py-2 text-[0.65rem] leading-relaxed"
                style={{ borderColor: "var(--border)", color: "var(--faint)" }}
              >
                {dash.feed.note}
              </p>
            </section>

            {/* ── SIDEBAR ── */}
            <div className="flex flex-col gap-3">
              {dash.watchlist !== null ? (
                <>
                  <WatchlistPanel wl={dash.watchlist} onChanged={() => setTick((t) => t + 1)} />
                  <AlertsPanel alerts={alerts} unseen={dash.watchlist.unseenAlerts} />
                </>
              ) : (
                <section className="panel" aria-label="watchlist">
                  <div className="panel-h">WATCHLIST</div>
                  <p className="px-4 py-3 text-[0.78rem] leading-relaxed" style={{ color: "var(--dim)" }}>
                    Watchlists and alerts are per-account.{" "}
                    <Link
                      href="/login"
                      className="cursor-pointer font-bold underline transition-colors duration-150 hover:text-[var(--accent)]"
                      style={{ color: "var(--accent)" }}
                    >
                      Sign in
                    </Link>{" "}
                    to track symbols with sparklines, 1d score chips and unread
                    alerts. Everything else on this dashboard is public-read.
                  </p>
                </section>
              )}

              <MoversMini dash={dash} />
            </div>
          </div>
        </>
      )}
    </div>
  );
}
