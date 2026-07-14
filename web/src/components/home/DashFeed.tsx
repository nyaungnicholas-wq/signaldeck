"use client";

// WHAT CHANGED TODAY feed panel — extracted from the old monolithic page.tsx
// (pure refactor; behavior identical). The kind-filter chips, the pinned
// today-only briefing card and the merged stream live here. The load-bearing
// tag caveats (sentiment ≠ recommendation, anomaly z ≠ prediction, filings
// lag) are VISIBLE text now — no hover required; the badge title attributes
// stay as decorative reinforcement.

import Link from "next/link";
import { useMemo, useState } from "react";
import type { DashboardResponse, DashFeedItem, Market } from "@/lib/api";
import { ago } from "@/lib/format";
import EmptyState from "@/components/EmptyState";
import {
  KIND_FILTERS,
  feedSymbolMarket,
  nyDayOf,
  nyToday,
  sentimentColor,
  type KindFilter,
} from "@/components/home/helpers";

function SymbolChip({ symbol, market }: { symbol?: string; market: Market }) {
  if (!symbol) return null;
  return (
    <Link
      href={`/s/${market}/${encodeURIComponent(symbol)}`}
      className="chip mono cursor-pointer px-2 py-[1px] text-[0.75rem] font-bold tracking-wide transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
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
        className="chip px-2 py-[1px] text-[0.75rem] tracking-wider"
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
        className="chip tnum px-2 py-[1px] text-[0.75rem] tracking-wider"
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
        className="chip px-2 py-[1px] text-[0.75rem] tracking-wider"
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
        className="chip px-2 py-[1px] text-[0.75rem] tracking-wider"
        style={{ color: sentimentColor(it.sentiment), borderColor: sentimentColor(it.sentiment) }}
        title="model-rated sentiment tag — not a recommendation"
      >
        {it.sentiment}
      </span>
    );
  }
  return (
    <span className="chip px-2 py-[1px] text-[0.75rem] tracking-wider" style={{ color: "var(--faint)" }}>
      news
    </span>
  );
}

function FeedRow({ it }: { it: DashFeedItem }) {
  return (
    <li
      className="flex flex-wrap items-baseline gap-x-2 gap-y-1 border-t px-4 py-2 text-[0.75rem]"
      style={{ borderColor: "var(--border)" }}
    >
      <SymbolChip symbol={it.symbol} market={feedSymbolMarket(it)} />
      <KindBadge it={it} />
      <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
        {ago(it.ts)}
      </span>
      <span className="w-full leading-relaxed">
        {it.url ? (
          <a
            href={it.url}
            target="_blank"
            rel="noopener noreferrer"
            className="cursor-pointer text-[var(--text)] transition-colors duration-150 hover:text-[var(--accent)]"
          >
            {it.title}
          </a>
        ) : (
          it.title
        )}
        {it.detail && it.kind !== "briefing" && (
          <span className="ml-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
            {it.detail}
          </span>
        )}
      </span>
      {it.detail && it.kind === "briefing" && (
        <span className="w-full text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
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
          className="chip px-2 py-[2px] text-[0.75rem] tracking-wider"
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
        <p className="mt-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          {it.detail}
        </p>
      )}
      <Link
        href="/signals/insights"
        className="mt-2 inline-flex min-h-[40px] cursor-pointer items-center text-[0.75rem] tracking-wider text-[var(--faint)] transition-colors duration-150 hover:text-[var(--accent)]"
      >
        Investigate all insights →
      </Link>
    </section>
  );
}

export default function DashFeed({ feed }: { feed: DashboardResponse["feed"] }) {
  const [kindFilter, setKindFilter] = useState<KindFilter>("all");

  const feedItems = useMemo(() => feed.items ?? [], [feed]);
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
    <section className="panel lg:col-span-2" aria-label="merged market feed">
      <div className="panel-h flex-wrap gap-2">
        <span>FEED</span>
        <span
          className="text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          news + filings + anomalies + briefings, newest first
        </span>
        {feedItems.length > 0 && (
          <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
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
              className="chip tnum min-h-[36px] cursor-pointer text-[0.75rem] transition-colors duration-150 hover:border-[var(--border-strong)] hover:text-[var(--text)]"
              style={isOn ? { color: "var(--text)", borderColor: "var(--accent)" } : undefined}
            >
              {f.label} {n}
            </button>
          );
        })}
      </div>

      {/* the tag caveats, visible — not hidden behind hover tooltips */}
      <p
        className="m-0 border-b px-3 py-1.5 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: "var(--faint)" }}
      >
        Tags: sentiment is model-rated, not a recommendation · anomaly z-scores are descriptive,
        not predictions · SEC filings lag by law/process (Form 4 ~2 business days; 13F quarterly).
      </p>

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
        className="border-t px-4 py-2 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--border)", color: "var(--faint)" }}
      >
        {feed.note}
      </p>
    </section>
  );
}
