"use client";

// HOME FEED (Signal8 wave, Stage 4): signal8-style "Top stories / Latest" —
// ONE time-ordered stream merging news headlines (existing news table, with
// sentiment chips) and NEW SEC filings rendered in plain English (label from
// the daemon's form-type map, tagged `sec-filing`). Symbol chips link to the
// symbol pages; headlines link out to the source / EDGAR document.
//
// HONESTY: filings are public-domain SEC data but LAG by law/process (Form 4
// ~2 business days; 13F quarterly + ≤45 days) — the API's note is rendered
// verbatim in the footer. News sentiment chips are the LLM's tag, shown as a
// tag, not a recommendation.

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, filings, type Filing, type NewsItem } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";

interface FeedItem {
  key: string;
  ts: number;
  kind: "news" | "filing";
  symbol?: string;
  headline: string;
  url: string;
  source: string; // news source or "sec-filing"
  sentiment?: string; // news only: bullish | bearish | neutral | unrated
}

function sentimentColor(s?: string): string {
  if (s === "bullish") return "var(--bid)";
  if (s === "bearish") return "var(--ask)";
  return "var(--faint)";
}

function toFeed(news: NewsItem[], fil: Filing[]): FeedItem[] {
  const out: FeedItem[] = [];
  for (const n of news) {
    out.push({
      key: `n:${n.id}`,
      ts: n.ts,
      kind: "news",
      symbol: n.symbol,
      headline: n.headline,
      url: n.url,
      source: n.source || "news",
      sentiment: n.sentiment,
    });
  }
  for (const f of fil) {
    out.push({
      key: `f:${f.id}`,
      ts: f.filedTs,
      kind: "filing",
      symbol: f.symbol,
      // signal8-style plain-English rendering: "SEM: S-8 POS — acquisition merger"
      headline: f.label || `${f.form} filing`,
      url: f.url,
      source: "sec-filing",
    });
  }
  out.sort((a, b) => b.ts - a.ts);
  return out;
}

function SymbolChip({ symbol }: { symbol?: string }) {
  if (!symbol) return null;
  // Feed symbols are equities (news + SEC filings both track stocks here);
  // crypto news rows carry the crypto symbol and link to its page.
  const market = symbol.includes("/") ? "crypto" : "stocks";
  return (
    <Link
      href={`/s/${market}/${encodeURIComponent(symbol)}`}
      className="chip cursor-pointer px-2 py-[1px] text-[0.68rem] font-bold tracking-wide transition-colors duration-150 hover:border-[var(--accent)] hover:text-[var(--accent)]"
    >
      {symbol}
    </Link>
  );
}

function Row({ it }: { it: FeedItem }) {
  return (
    <div
      className="flex flex-wrap items-baseline gap-x-2 gap-y-1 border-t px-4 py-2 text-[0.78rem]"
      style={{ borderColor: "var(--border)" }}
    >
      <SymbolChip symbol={it.symbol} />
      {it.kind === "filing" ? (
        <span
          className="chip px-2 py-[1px] text-[0.65rem] tracking-wider"
          style={{ color: "var(--accent)", borderColor: "var(--accent)" }}
        >
          sec-filing
        </span>
      ) : (
        it.sentiment &&
        it.sentiment !== "unrated" && (
          <span
            className="chip px-2 py-[1px] text-[0.65rem] tracking-wider"
            style={{ color: sentimentColor(it.sentiment), borderColor: sentimentColor(it.sentiment) }}
          >
            {it.sentiment}
          </span>
        )
      )}
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
            {it.headline}
          </a>
        ) : (
          it.headline
        )}
        <span className="ml-2 text-[0.68rem]" style={{ color: "var(--faint)" }}>
          {it.kind === "news" ? it.source : "SEC EDGAR"}
        </span>
      </span>
    </div>
  );
}

export default function HomeFeed({ limit = 40 }: { limit?: number }) {
  const [items, setItems] = useState<FeedItem[] | null>(null);
  const [filingsNote, setFilingsNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      Promise.all([api.news(), filings(undefined, undefined, limit)])
        .then(([news, fil]) => {
          if (!alive) return;
          setItems(toFeed(news ?? [], fil.filings ?? []).slice(0, limit));
          setFilingsNote(fil.note);
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
  }, [limit, retryTick]);

  // Top stories = the 3 freshest LLM-rated (non-neutral) headlines; an
  // honest empty when nothing is rated yet.
  const top = (items ?? [])
    .filter((i) => i.kind === "news" && (i.sentiment === "bullish" || i.sentiment === "bearish"))
    .slice(0, 3);

  return (
    <section className="panel" aria-label="top stories and latest feed">
      <div className="panel-h flex-wrap gap-2">
        <span>TOP STORIES / LATEST</span>
        <span
          className="text-[0.68rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          news + SEC filings in plain English, one time-ordered stream
        </span>
        {items !== null && (
          <span className="tnum ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
            {items.length} items
          </span>
        )}
      </div>

      {items === null && !error && (
        <div className="p-3">
          <Skeleton lines={5} label="loading feed" />
        </div>
      )}
      {items === null && error && (
        <div className="p-3">
          <ErrorState
            message={error}
            hint="Is the daemon running? The feed merges /api/news and /api/filings."
            retry={() => {
              setError(null);
              setRetryTick((t) => t + 1);
            }}
          />
        </div>
      )}
      {items !== null && items.length === 0 && (
        <EmptyState
          message="No stories or filings yet"
          detail="The news fetcher and the EDGAR filings poller fill this feed on their own cadence — nothing is fabricated in the meantime."
        />
      )}

      {top.length > 0 && (
        <div className="grid grid-cols-1 gap-2 border-t p-3 sm:grid-cols-3" style={{ borderColor: "var(--border)" }}>
          {top.map((it) => (
            <div key={`top:${it.key}`} className="rounded border p-3" style={{ borderColor: "var(--border)", background: "var(--panel2)" }}>
              <div className="flex items-center gap-2">
                <SymbolChip symbol={it.symbol} />
                <span
                  className="text-[0.65rem] font-bold tracking-wider"
                  style={{ color: sentimentColor(it.sentiment) }}
                >
                  {it.sentiment?.toUpperCase()}
                </span>
                <span className="tnum ml-auto text-[0.65rem]" style={{ color: "var(--faint)" }}>
                  {ago(it.ts)}
                </span>
              </div>
              <a
                href={it.url}
                target="_blank"
                rel="noopener noreferrer"
                className="mt-1.5 block cursor-pointer text-[0.78rem] font-bold leading-snug transition-colors duration-150 hover:text-[var(--accent)]"
              >
                {it.headline}
              </a>
            </div>
          ))}
        </div>
      )}

      {items !== null && items.length > 0 && (
        <div className="flex flex-col">
          {items.map((it) => (
            <Row key={it.key} it={it} />
          ))}
        </div>
      )}

      {filingsNote && (
        <p
          className="border-t px-4 py-2 text-[0.68rem] leading-relaxed"
          style={{ borderColor: "var(--border)", color: "var(--faint)" }}
        >
          {filingsNote}
        </p>
      )}
    </section>
  );
}
