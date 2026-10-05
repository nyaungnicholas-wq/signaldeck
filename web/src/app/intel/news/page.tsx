"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, pollMs, POLL_SLOW, type NewsItem, type WatchRow, type Market } from "@/lib/api";
import { ago, fmtScore } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import { useIntelSymbol } from "@/components/intel/IntelShared";
import { Reveal, StatTile, PageHero, MiniBar } from "@/components/ui/Kit";

type Tone = "all" | "bullish" | "bearish" | "neutral";
type SortKey = "ts" | "symbol" | "score";

const TONES: { key: Tone; label: string }[] = [
  { key: "all", label: "all" },
  { key: "bullish", label: "bullish" },
  { key: "bearish", label: "bearish" },
  { key: "neutral", label: "neutral" },
];

function inferMarket(symbol: string): Market {
  return symbol.includes("/") ? "crypto" : "stocks";
}

function sentimentColor(sentiment: string): string {
  switch (sentiment) {
    case "bullish": return "var(--bid)";
    case "bearish": return "var(--ask)";
    case "neutral": return "var(--dim)";
    default: return "var(--faint)";
  }
}

function NewsRow({ item, maxScore }: { item: NewsItem; maxScore: number }) {
  const sym = item.symbol ?? "";
  const sColor = sentimentColor(item.sentiment);
  const rated = item.sentiment === "bullish" || item.sentiment === "bearish" || item.sentiment === "neutral";
  const scoreVal = rated ? Number(item.score) : 0;

  return (
    <article
      className="reveal-item px-4 py-3 border-l-2 transition-colors hover:bg-[var(--panel2)]"
      style={{ borderColor: sColor, borderBottom: "1px solid var(--border)" }}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="chip tnum shrink-0" style={{ color: sColor, borderColor: sColor }}>
          {item.sentiment}
          {rated && <span className="ml-1.5">{fmtScore(item.score)}</span>}
        </span>
        {sym && (
          <Link
            href={`/s/${inferMarket(sym)}/${encodeURIComponent(sym)}`}
            className="chip mono shrink-0 cursor-pointer font-bold hover:text-[var(--accent)] hover:border-[var(--accent)]"
            style={{ color: "var(--text)" }}
          >
            {sym}
          </Link>
        )}
        {item.source && (
          <span className="text-[0.75rem] truncate max-w-[120px] title-attr" title={item.source} style={{ color: "var(--faint)" }}>
            {item.source}
          </span>
        )}
        <span className="tnum ml-auto shrink-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {ago(item.ts)}
        </span>
      </div>
      <div className="mt-2 flex items-start gap-3">
        <div className="flex-1 min-w-0">
          {item.url ? (
            <a href={item.url} target="_blank" rel="noopener noreferrer" className="block group">
              <h2 className="text-sm font-bold leading-snug truncate title-attr group-hover:text-[var(--accent)]" title={item.headline || "(untitled headline)"}>
                {item.headline || "(untitled headline)"}
              </h2>
            </a>
          ) : (
            <h2 className="text-sm font-bold leading-snug truncate title-attr" title={item.headline || "(untitled headline)"}>
              {item.headline || "(untitled headline)"}
            </h2>
          )}
          {item.rationale && (
            <p className="mt-1 text-[0.75rem] italic leading-relaxed truncate title-attr" title={item.rationale} style={{ color: "var(--dim)" }}>
              {item.rationale}
            </p>
          )}
        </div>
        {rated && (
          <div className="w-16 shrink-0">
            <MiniBar value={scoreVal} max={maxScore} height={6} />
          </div>
        )}
      </div>
    </article>
  );
}

export default function NewsPage() {
  const [news, setNews] = useState<NewsItem[] | null>(null);
  const [watch, setWatch] = useState<WatchRow[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [tone, setTone] = useState<Tone>("all");
  const [sym, setSym] = useState<string | null>(null);
  const [sortKey, setSortKey] = useState<SortKey>("ts");
  const [sortAsc, setSortAsc] = useState(false);
  const { symbol: sharedSym } = useIntelSymbol();
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      Promise.all([api.news(), api.watchlist().catch(() => [] as WatchRow[])])
        .then(([rows, wl]) => {
          if (!alive) return;
          setNews(Array.isArray(rows) ? rows : []);
          setWatch(Array.isArray(wl) ? wl : []);
          setErr(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErr(e instanceof Error ? e.message : String(e));
        });
    load();
    const stop = pollMs(load, POLL_SLOW);
    return () => { alive = false; stop(); };
  }, [retryTick]);

  const stats = useMemo(() => {
    const rows = news ?? [];
    let bullish = 0, bearish = 0;
    for (const n of rows) {
      if (n.sentiment === "bullish") bullish++;
      else if (n.sentiment === "bearish") bearish++;
    }
    const latest = rows.length > 0 ? rows.reduce((a, b) => (b.ts ?? 0) > (a.ts ?? 0) ? b : a).ts : 0;
    return { count: rows.length, bullish, bearish, net: bullish - bearish, latest };
  }, [news]);

  const sorted = useMemo(() => {
    const base = [...(news ?? [])].sort((a, b) => {
      const av = sortKey === "ts" ? (a.ts ?? 0) : sortKey === "symbol" ? (a.symbol ?? "") : Number(a.score ?? 0);
      const bv = sortKey === "ts" ? (b.ts ?? 0) : sortKey === "symbol" ? (b.symbol ?? "") : Number(b.score ?? 0);
      if (av < bv) return sortAsc ? -1 : 1;
      if (av > bv) return sortAsc ? 1 : -1;
      return 0;
    });
    return base;
  }, [news, sortKey, sortAsc]);

  const symbolChips = useMemo(() => {
    const present = new Set(sorted.map((n) => n.symbol).filter(Boolean) as string[]);
    return watch.map((w) => w.symbol).filter((s) => present.has(s));
  }, [watch, sorted]);

  const toneCounts = useMemo(() => {
    let bullish = 0, bearish = 0, neutral = 0;
    for (const n of sorted) {
      if (n.sentiment === "bullish") bullish++;
      else if (n.sentiment === "bearish") bearish++;
      else if (n.sentiment === "neutral") neutral++;
    }
    return { all: sorted.length, bullish, bearish, neutral };
  }, [sorted]);

  const visible = useMemo(() => {
    let rows = sorted;
    if (sharedSym) rows = rows.filter((n) => (n.symbol ?? "").toUpperCase().startsWith(sharedSym));
    if (sym) rows = rows.filter((n) => n.symbol === sym);
    if (tone !== "all") rows = rows.filter((n) => n.sentiment === tone);
    return rows;
  }, [sorted, tone, sym, sharedSym]);

  const maxScore = useMemo(() => {
    let max = 0;
    for (const n of visible) {
      const s = Math.abs(Number(n.score ?? 0));
      if (s > max) max = s;
    }
    return max || 1;
  }, [visible]);

  const loading = news === null && err === null;
  const hardError = news === null && err !== null;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="News"
        subtitle="Market-moving headlines with sentiment - newest and most relevant first."
        right={
          news !== null && (
            <div className="flex flex-wrap items-center gap-1.5">
              {TONES.map((t) => (
                <button key={t.key} type="button" aria-pressed={tone === t.key}
                  onClick={() => setTone(t.key)}
                  className="chip min-h-[32px] cursor-pointer hover:text-[var(--text)]"
                  style={{ borderColor: tone === t.key ? "var(--accent)" : undefined }}>
                  {t.label} <span className="tnum">{toneCounts[t.key]}</span>
                </button>
              ))}
            </div>
          )
        }
      />

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="Headlines" value={stats.count} i={0} />
        <StatTile label="Bullish" value={stats.bullish} glow="up" i={1} />
        <StatTile label="Bearish" value={stats.bearish} glow="down" i={2} />
        {/* delta removed rather than relabelled. stats.net is bullish minus
            bearish -- a headline COUNT -- so the badge rendered e.g. "7.00%"
            for seven headlines, and it was the same number the tile already
            shows. There is nothing for a delta to mean here. */}
        <StatTile label="Net Tone" value={stats.net} i={3} />
      </div>

      <div className="panel hud-panel">
        <div className="panel-h flex flex-wrap items-center justify-between gap-2">
          <span>FEED</span>
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>Sort:</span>
            {([["ts", "date"], ["symbol", "symbol"], ["score", "sentiment"]] as const).map(([key, label]) => (
              <button key={key} type="button" onClick={() => { if (sortKey === key) setSortAsc(!sortAsc); else { setSortKey(key); setSortAsc(key === "symbol"); } }}
                className="chip min-h-[28px] flex items-center gap-1 cursor-pointer hover:text-[var(--text)]"
                style={{ borderColor: sortKey === key ? "var(--accent)" : undefined }}>
                {label}
                {sortKey === key && (
                  <svg width="10" height="10" viewBox="0 0 10 10" className="inline">
                    <path d={sortAsc ? "M5 2L8 7H2Z" : "M5 8L8 3H2Z"} fill="currentColor" />
                  </svg>
                )}
              </button>
            ))}
          </div>
        </div>

        {symbolChips.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 px-4 py-2" style={{ borderBottom: "1px solid var(--border)" }}>
            <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>symbol:</span>
            <button type="button" aria-pressed={sym === null} onClick={() => setSym(null)}
              className="chip min-h-[28px] cursor-pointer hover:text-[var(--text)]"
              style={{ borderColor: sym === null ? "var(--accent)" : undefined }}>all</button>
            {symbolChips.map((s) => (
              <button key={s} type="button" aria-pressed={sym === s} onClick={() => setSym(sym === s ? null : s)}
                className="chip mono min-h-[28px] cursor-pointer font-bold hover:text-[var(--text)]"
                style={{ borderColor: sym === s ? "var(--accent)" : undefined }}>{s}</button>
            ))}
          </div>
        )}

        {loading && <Skeleton lines={4} label="loading news" />}

        {hardError && (
          <ErrorState message={err ?? "news unavailable"} hint="Is the daemon running? Start it with signaldeckd."
            retry={() => { setErr(null); setRetryTick((t) => t + 1); }} />
        )}

        {news !== null && (
          visible.length === 0 ? (
            <EmptyState className="m-4" message={sorted.length === 0 ? "No news yet" : "Nothing matches this filter"}
              detail={sorted.length === 0 ? "The news-fetcher pulls headlines every 20 min." : "Try the 'all' tone, clear symbol chips, or clear the shared intel filter."} />
          ) : (
            <Reveal>
              <div>
                {visible.map((item) => (
                  <NewsRow key={item.id} item={item} maxScore={maxScore} />
                ))}
              </div>
            </Reveal>
          )
        )}
      </div>
    </div>
  );
}
