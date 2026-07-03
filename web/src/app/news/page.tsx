"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { api, type NewsItem, type WatchRow, type Market } from "@/lib/api";
import { ago, fmtScore } from "@/lib/format";

const POLL_MS = 10000;

type Tone = "all" | "bullish" | "bearish" | "neutral";

const TONES: { key: Tone; label: string }[] = [
  { key: "all", label: "all" },
  { key: "bullish", label: "bullish" },
  { key: "bearish", label: "bearish" },
  { key: "neutral", label: "neutral" },
];

/** News rows don't always carry a market. Pairs like BTC/USD contain "/" →
    crypto; bare tickers → stocks. Mirrors the insights page idiom. */
function inferMarket(symbol: string): Market {
  return symbol.includes("/") ? "crypto" : "stocks";
}

/** Color + label for a sentiment tag. Unknown/missing → "unrated". */
function sentimentStyle(sentiment: string): { color: string; label: string } {
  switch (sentiment) {
    case "bullish":
      return { color: "var(--bid)", label: "bullish" };
    case "bearish":
      return { color: "var(--ask)", label: "bearish" };
    case "neutral":
      return { color: "var(--dim)", label: "neutral" };
    default:
      return { color: "var(--faint)", label: "unrated" };
  }
}

function NewsRow({ item }: { item: NewsItem }) {
  const sym = item.symbol ?? "";
  const s = sentimentStyle(item.sentiment);
  const rated = item.sentiment === "bullish" || item.sentiment === "bearish" || item.sentiment === "neutral";

  return (
    <article
      className="px-4 py-3.5 transition-colors duration-150 hover:bg-[var(--panel2)]"
      style={{ borderBottom: "1px solid var(--border)" }}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span
          className="chip tnum shrink-0"
          aria-label={`sentiment ${s.label}${rated ? `, score ${fmtScore(item.score)}` : ""}`}
          style={{ color: s.color, borderColor: s.color }}
        >
          {s.label}
          {rated && (
            <span className="ml-1.5" style={{ color: s.color }}>
              {fmtScore(item.score)}
            </span>
          )}
        </span>

        {sym && (
          <Link
            href={`/s/${inferMarket(sym)}/${encodeURIComponent(sym)}`}
            className="chip shrink-0 cursor-pointer font-bold transition-colors duration-150 hover:text-[var(--accent)] hover:border-[var(--accent)]"
            style={{ color: "var(--text)" }}
            aria-label={`open ${sym} detail`}
          >
            {sym}
          </Link>
        )}

        {item.source && (
          <span className="text-[0.68rem]" style={{ color: "var(--faint)" }}>
            {item.source}
          </span>
        )}

        <span className="tnum ml-auto shrink-0 text-[0.68rem]" style={{ color: "var(--faint)" }}>
          {ago(item.ts)}
        </span>
      </div>

      {item.url ? (
        <a
          href={item.url}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-2 block cursor-pointer text-[0.86rem] font-bold leading-snug transition-colors duration-150 hover:text-[var(--accent)]"
          style={{ color: "var(--text)" }}
        >
          {item.headline || "(untitled headline)"}
        </a>
      ) : (
        <h2 className="mt-2 text-[0.86rem] font-bold leading-snug" style={{ color: "var(--text)" }}>
          {item.headline || "(untitled headline)"}
        </h2>
      )}

      {item.rationale && (
        <p className="mt-1.5 text-[0.75rem] italic leading-relaxed" style={{ color: "var(--dim)" }}>
          {item.rationale}
        </p>
      )}
    </article>
  );
}

export default function NewsPage() {
  const [news, setNews] = useState<NewsItem[] | null>(null);
  const [watch, setWatch] = useState<WatchRow[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [tone, setTone] = useState<Tone>("all");
  const [sym, setSym] = useState<string | null>(null);

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
    const t = setInterval(load, POLL_MS);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  const sorted = useMemo(
    () => [...(news ?? [])].sort((a, b) => (b.ts ?? 0) - (a.ts ?? 0)),
    [news],
  );

  // Watchlist symbols that actually have news, in watchlist order.
  const symbolChips = useMemo(() => {
    const present = new Set(sorted.map((n) => n.symbol).filter(Boolean) as string[]);
    return watch.map((w) => w.symbol).filter((s) => present.has(s));
  }, [watch, sorted]);

  const toneCounts = useMemo(() => {
    let bullish = 0;
    let bearish = 0;
    let neutral = 0;
    for (const n of sorted) {
      if (n.sentiment === "bullish") bullish++;
      else if (n.sentiment === "bearish") bearish++;
      else if (n.sentiment === "neutral") neutral++;
    }
    return { all: sorted.length, bullish, bearish, neutral };
  }, [sorted]);

  const visible = useMemo(() => {
    let rows = sorted;
    if (sym) rows = rows.filter((n) => n.symbol === sym);
    if (tone !== "all") rows = rows.filter((n) => n.sentiment === tone);
    return rows;
  }, [sorted, tone, sym]);

  const loading = news === null && err === null;
  const hardError = news === null && err !== null;
  const net = toneCounts.bullish - toneCounts.bearish;

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">NEWS</h1>
        {news !== null && (
          <>
            <span className="chip tnum">{toneCounts.all} headlines</span>
            <span className="chip tnum">
              <span style={{ color: "var(--bid)" }}>{toneCounts.bullish} bullish</span>
              <span style={{ color: "var(--faint)" }}> · </span>
              <span style={{ color: "var(--ask)" }}>{toneCounts.bearish} bearish</span>
            </span>
            {toneCounts.bullish + toneCounts.bearish > 0 && (
              <span
                className="chip tnum"
                aria-label={`net tone ${net >= 0 ? "positive" : "negative"} ${Math.abs(net)}`}
                style={{
                  color: net > 0 ? "var(--bid)" : net < 0 ? "var(--ask)" : "var(--dim)",
                  borderColor: net > 0 ? "var(--bid)" : net < 0 ? "var(--ask)" : undefined,
                }}
              >
                net {net > 0 ? "+" : ""}{net}
              </span>
            )}
          </>
        )}
        {err !== null && news !== null && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {/* what this page is */}
      <section className="panel">
        <div className="panel-h">HOW SENTIMENT IS TAGGED</div>
        <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          Sentiment is tagged by the local AI from the headline text only; &ldquo;unrated&rdquo;
          means it hasn&rsquo;t been processed yet (tagging runs every 10 min).
        </p>
      </section>

      {loading && (
        <div className="panel px-4 py-8 text-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
          loading…
        </div>
      )}

      {hardError && (
        <div className="panel px-4 py-8 text-center text-[0.75rem]">
          <div style={{ color: "var(--bad)" }}>{err}</div>
          <div className="mt-2" style={{ color: "var(--faint)" }}>
            is the daemon running? start it with <span style={{ color: "var(--dim)" }}>signaldeckd</span>
          </div>
        </div>
      )}

      {news !== null && (
        <section className="panel">
          <div className="panel-h flex-wrap">
            FEED
            <div className="ml-auto flex flex-wrap items-center gap-1.5" role="group" aria-label="Filter by sentiment">
              {TONES.map((t) => {
                const active = tone === t.key;
                return (
                  <button
                    key={t.key}
                    type="button"
                    aria-pressed={active}
                    onClick={() => setTone(t.key)}
                    className="chip cursor-pointer transition-colors duration-150 hover:text-[var(--text)]"
                    style={{
                      color: active ? "var(--text)" : undefined,
                      borderColor: active ? "var(--accent)" : undefined,
                    }}
                  >
                    {t.label} <span className="tnum">{toneCounts[t.key]}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {/* symbol filter chips */}
          {symbolChips.length > 0 && (
            <div
              className="flex flex-wrap items-center gap-1.5 px-4 py-2.5"
              style={{ borderBottom: "1px solid var(--border)" }}
              role="group"
              aria-label="Filter by symbol"
            >
              <span className="text-[0.68rem]" style={{ color: "var(--faint)" }}>
                symbol
              </span>
              <button
                type="button"
                aria-pressed={sym === null}
                onClick={() => setSym(null)}
                className="chip cursor-pointer transition-colors duration-150 hover:text-[var(--text)]"
                style={{
                  color: sym === null ? "var(--text)" : undefined,
                  borderColor: sym === null ? "var(--accent)" : undefined,
                }}
              >
                all
              </button>
              {symbolChips.map((s) => {
                const active = sym === s;
                return (
                  <button
                    key={s}
                    type="button"
                    aria-pressed={active}
                    onClick={() => setSym(active ? null : s)}
                    className="chip cursor-pointer font-bold transition-colors duration-150 hover:text-[var(--text)]"
                    style={{
                      color: active ? "var(--text)" : undefined,
                      borderColor: active ? "var(--accent)" : undefined,
                    }}
                  >
                    {s}
                  </button>
                );
              })}
            </div>
          )}

          {visible.length === 0 ? (
            <div className="px-4 py-8 text-center text-[0.75rem]" style={{ color: "var(--faint)" }}>
              {sorted.length === 0
                ? "No news yet — the news-fetcher pulls headlines every 20 min (stocks only; Alpaca doesn't cover crypto)."
                : "nothing matches this filter."}
            </div>
          ) : (
            <div>
              {visible.map((item) => (
                <NewsRow key={item.id} item={item} />
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}
