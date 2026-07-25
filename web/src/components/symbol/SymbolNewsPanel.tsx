"use client";

// SYMBOL NEWS (why-it's-moving wave) — this symbol's own recent headlines.
//
// api.news(symbol, market) has existed in the client for a long time and no
// caller ever passed a symbol, so per-symbol news was invisible on the page
// that most needs it: "why is it moving" usually has a headline attached.
//
// HONESTY: the sentiment tag is the news classifier's label on the HEADLINE,
// not a price forecast and not a scored factor — the panel says so. Every row
// links out to its real source URL, so any claim here is checkable in one click.

import { useEffect, useState } from "react";
import { api, pollMs, POLL_DEFAULT, type Market, type NewsItem } from "@/lib/api";
import { ago } from "@/lib/format";
import HelpTip from "@/components/HelpTip";

/** Sentiment chip color. Neutral/unknown stays dim — never a fake lean. */
function sentColor(s: string): string {
  const v = s.toLowerCase();
  if (v === "bullish" || v === "positive") return "var(--bid)";
  if (v === "bearish" || v === "negative") return "var(--ask)";
  return "var(--dim)";
}

export default function SymbolNewsPanel({
  symbol,
  market,
  limit = 10,
}: {
  symbol: string;
  market: Market;
  limit?: number;
}) {
  // Keyed by symbol|market (the page's own convention) so a symbol switch shows
  // a loading state without a bare setState inside the effect body.
  const key = `${symbol}|${market}`;
  const [state, setState] = useState<{ key: string; items: NewsItem[] } | null>(null);
  const [errState, setErrState] = useState<{ key: string; msg: string } | null>(null);
  const items = state && state.key === key ? state.items : null;
  const err = errState && errState.key === key ? errState.msg : null;

  useEffect(() => {
    let alive = true;
    const k = `${symbol}|${market}`;
    const load = () =>
      api
        .news(symbol, market)
        .then((n) => {
          if (!alive) return;
          // Go nil slices arrive as JSON null — normalize before sorting.
          setState({ key: k, items: [...(n ?? [])].sort((a, b) => b.ts - a.ts) });
          setErrState(null);
        })
        .catch((e: unknown) => {
          if (!alive) return;
          setErrState({ key: k, msg: e instanceof Error ? e.message : String(e) });
        });
    load();
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market]);

  const rows = (items ?? []).slice(0, limit);

  return (
    <section className="panel" aria-label={`recent headlines for ${symbol}`}>
      <div className="panel-h flex-wrap gap-2">
        <span>NEWS · {symbol}</span>
        <HelpTip label="what the sentiment tag is">
          The tag is the news classifier&rsquo;s read of the HEADLINE only — it is
          descriptive context, not a price forecast and not one of the scored
          factors. Every row links to its source so you can check it yourself.
        </HelpTip>
        {items !== null && (
          <span className="tnum ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
            {items.length} stored · newest first
          </span>
        )}
      </div>

      {err !== null && items === null && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          {err}
        </p>
      )}
      {items === null && err === null && (
        <p className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--faint)" }}>
          loading headlines…
        </p>
      )}
      {items !== null && rows.length === 0 && (
        <p className="px-4 py-4 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          no headlines stored for {symbol} — the news worker covers tracked
          symbols on its own cadence. Honest absence, not an error.
        </p>
      )}

      {rows.length > 0 && (
        <ul className="m-0 list-none p-0">
          {rows.map((n) => (
            <li key={n.id} className="border-t px-4 py-2" style={{ borderColor: "var(--border)" }}>
              <div className="flex flex-wrap items-baseline gap-2">
                <a
                  href={n.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-[0.8rem] leading-relaxed transition-colors duration-150 hover:text-[var(--accent)]"
                  style={{ color: "var(--text)" }}
                >
                  {n.headline}
                </a>
                {n.sentiment && (
                  <span
                    className="chip text-[0.75rem]"
                    style={{ color: sentColor(n.sentiment), borderColor: sentColor(n.sentiment) }}
                  >
                    {n.sentiment}
                  </span>
                )}
              </div>
              <div className="mt-0.5 flex flex-wrap items-center gap-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                <span>{n.source}</span>
                <span className="tnum">{ago(n.ts)}</span>
                {n.rationale && <span>· {n.rationale}</span>}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
