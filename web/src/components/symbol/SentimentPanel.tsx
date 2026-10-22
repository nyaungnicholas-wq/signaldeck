"use client";

// PUBLIC ATTENTION (why-it's-moving wave): /api/wiki-attention, daily
// Wikipedia article views plus a DESCRIPTIVE z of the latest day against the
// symbol's own baseline. Attention is not direction: a spike says people are
// looking, nothing about which way price goes. The StockTwits half was removed
// when StockTwits was retired (22 October 2026, licence review).
//
// When nothing is stored, this panel says so. It never renders a fabricated
// neutral reading: a made-up value reads as a measurement.

import { useEffect, useState } from "react";
import {
  pollMs,
  POLL_SLOW,
  wikiAttention,
  type Market,
  type WikiAttentionResponse,
} from "@/lib/api";
import HelpTip from "@/components/HelpTip";

/** Direction-neutral attention sparkline — deliberately not green/red. */
function AttentionSpark({ values }: { values: number[] }) {
  const w = 140;
  const h = 30;
  const pts = (values ?? []).filter((v) => Number.isFinite(v));
  if (pts.length < 5) {
    return (
      <svg width={w} height={h} role="img" aria-label="not enough stored days for an attention trend yet (needs 5+)">
        <title>not enough stored days yet (needs 5+)</title>
        <line x1={2} y1={h / 2} x2={w - 2} y2={h / 2} stroke="var(--border)" strokeDasharray="2 4" />
      </svg>
    );
  }
  const min = Math.min(...pts);
  const max = Math.max(...pts);
  const span = max - min || 1;
  const path = pts
    .map((v, i) => {
      const x = 2 + (i / (pts.length - 1)) * (w - 4);
      const y = h - 3 - ((v - min) / span) * (h - 6);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <svg width={w} height={h} role="img" aria-label={`page views over ${pts.length} days (attention, not direction)`}>
      <polyline points={path} fill="none" stroke="var(--accent)" strokeWidth="1.5" />
    </svg>
  );
}

export default function SentimentPanel({
  symbol,
  market,
}: {
  symbol: string;
  market: Market;
}) {
  // Every slice is keyed by symbol|market so a symbol switch invalidates it
  // without a bare setState in the effect body. `loading` is derived, not
  // stored: the first pass has landed once either slice carries this key.
  const key = `${symbol}|${market}`;
  const [wikiState, setWikiState] = useState<{ key: string; r: WikiAttentionResponse } | null>(null);
  const [errState, setErrState] = useState<{ key: string; msg: string | null } | null>(null);
  const wiki = wikiState && wikiState.key === key ? wikiState.r : null;
  const err = errState && errState.key === key ? errState.msg : null;
  const loading = errState?.key !== key;

  useEffect(() => {
    let alive = true;
    const k = `${symbol}|${market}`;
    const load = async () => {
      const [wv] = await Promise.allSettled([wikiAttention(symbol, market, 90)]);
      if (!alive) return;
      let firstErr: string | null = null;
      const note = (r: PromiseSettledResult<unknown>) => {
        if (r.status === "rejected" && firstErr === null) {
          firstErr = r.reason instanceof Error ? r.reason.message : String(r.reason);
        }
      };
      if (wv.status === "fulfilled") setWikiState({ key: k, r: wv.value });
      else note(wv);
      // Written last: this is also the "first pass landed" marker for `loading`.
      setErrState({ key: k, msg: firstErr });
    };
    void load();
    // POLL_SLOW: page views land once a day.
    const stop = pollMs(() => void load(), POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market]);

  const wikiSeries = wiki?.series ?? [];
  const wikiLatest = wikiSeries.length > 0 ? wikiSeries[wikiSeries.length - 1] : null;

  const nothing = wikiLatest === null;

  return (
    <section className="panel" aria-label={`public attention for ${symbol}`}>
      <div className="panel-h flex-wrap gap-2">
        <span>PUBLIC ATTENTION · {symbol}</span>
        <HelpTip label="what this is (and is not)">
          A free external CONTEXT dataset, not a scored factor and not a
          prediction. Wikipedia views measure attention, which says nothing about
          direction — a spike means people are looking, not that price will rise.
        </HelpTip>
      </div>

      {err !== null && nothing && !loading && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          {err}
        </p>
      )}
      {loading && nothing && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          loading public attention…
        </p>
      )}

      {/* ── Wikipedia attention ── */}
      {wikiLatest !== null ? (
        <div className="border-t px-4 py-3" style={{ borderColor: "var(--border)" }}>
          <div className="flex flex-wrap items-center gap-4">
            <div>
              <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
                WIKIPEDIA VIEWS · {wikiLatest.day}
              </div>
              <div className="tnum text-lg font-bold">{wikiLatest.views.toLocaleString("en-US")}</div>
            </div>
            <div>
              <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
                LAST {wikiSeries.length}D
              </div>
              <AttentionSpark values={wikiSeries.map((p) => p.views)} />
            </div>
            {wiki?.latestZ != null && (
              <div>
                <div className="flex items-center gap-1 text-[0.75rem]" style={{ color: "var(--dim)" }}>
                  Z (DESCRIPTIVE)
                  <HelpTip label="what this z-score means">{wiki.zNote}</HelpTip>
                </div>
                <div className="tnum text-[0.85rem]">{wiki.latestZ.toFixed(2)}</div>
              </div>
            )}
            {wiki?.article && (
              <span className="chip" style={{ color: "var(--faint)" }}>
                article: {wiki.article}
              </span>
            )}
          </div>
          {/* the attention caveat — verbatim */}
          <p className="mt-2 text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
            {wiki?.note}
          </p>
          {wiki?.resolutionNote && (
            <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--warn)" }}>
              {wiki.resolutionNote}
            </p>
          )}
        </div>
      ) : (
        wiki !== null && (
          <p className="border-t px-4 py-3 text-[0.75rem] leading-relaxed" style={{ borderColor: "var(--border)", color: "var(--faint)" }}>
            {wiki.resolutionNote ??
              wiki.emptyNote ??
              "no Wikipedia page views stored for this symbol yet — honest absence, not a neutral reading."}
          </p>
        )
      )}
    </section>
  );
}
