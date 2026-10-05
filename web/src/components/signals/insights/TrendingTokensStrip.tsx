"use client";

// TRENDING IN HEADLINES — the fleet's top headline tokens over the last 24h
// (/api/news-trends), shown as a context strip above the insights feed.
// Chips scale font size/weight by count so the eye reads relative attention;
// the API's note renders verbatim (descriptive attention, not a forecast).
//
// The endpoint is per-symbol (it also carries that symbol's volume series),
// but fleetTokens is fleet-wide and identical whichever symbol anchors the
// call — so we anchor on the first active watchlist symbol, falling back to
// the public universe screener when signed out / unsubscribed (same chain as
// the MODEL RACE picker). This is a CONTEXT strip, not the page's backbone:
// when no anchor symbol or no tokens exist it renders nothing — the feed
// below is unaffected (absent beats a dead panel).

import { useEffect, useState } from "react";
import {
  api,
  newsTrends,
  pollMs,
  POLL_DEFAULT,
  screenerRows,
  type FleetToken,
  type Market,
} from "@/lib/api";

interface Anchor {
  symbol: string;
  market: Market;
}

/** Loaded tokens keyed by the anchor so an anchor change never flashes a
 *  stale board (and no synchronous setState inside the effect body). */
interface TokenSlot {
  key: string;
  tokens: FleetToken[];
  note: string;
}

/** count → font-size/weight scale, relative to the board's max count. */
function chipScale(count: number, maxCount: number): { fontSize: string; fontWeight: number } {
  const t = maxCount > 0 ? Math.max(0, Math.min(1, count / maxCount)) : 0;
  return {
    fontSize: `${(0.72 + t * 0.33).toFixed(2)}rem`,
    fontWeight: 500 + Math.round(t * 3) * 100, // 500 → 800
  };
}

export default function TrendingTokensStrip() {
  const [anchor, setAnchor] = useState<Anchor | null>(null);
  const [slot, setSlot] = useState<TokenSlot | null>(null);
  const [stale, setStale] = useState(false); // last poll failed — data kept

  // Resolve the anchor symbol once (watchlist → screener fallback). Errors
  // leave the anchor null and the strip simply absent — context, not backbone.
  useEffect(() => {
    let alive = true;
    const applyFirst = (rows: { symbol: string; market: Market; active?: boolean }[]) => {
      const first = rows.find((r) => r.active !== false) ?? rows[0];
      if (first) setAnchor({ symbol: first.symbol, market: first.market });
    };
    api
      .watchlist()
      .then((r) => {
        if (!alive) return;
        const active = r.filter((x) => x.active);
        if (active.length > 0) {
          applyFirst(active);
          return;
        }
        return screenerRows().then((all) => {
          if (alive) applyFirst(all);
        });
      })
      .catch(() => {
        if (!alive) return;
        return screenerRows()
          .then((all) => {
            if (alive) applyFirst(all);
          })
          .catch(() => {
            /* no anchor available — the strip stays absent */
          });
      });
    return () => {
      alive = false;
    };
  }, []);

  // Poll the token board on the page's default cadence once anchored.
  useEffect(() => {
    if (!anchor) return;
    let alive = true;
    const key = `${anchor.market}:${anchor.symbol}`;
    const load = () =>
      newsTrends(anchor.symbol, anchor.market)
        .then((d) => {
          if (!alive) return;
          // Go nil slices arrive as JSON null — normalize before storing.
          setSlot({ key, tokens: d.fleetTokens ?? [], note: d.note });
          setStale(false);
        })
        .catch(() => {
          if (!alive) return;
          setStale(true); // keep the last board, flag it honestly
        });
    load();
    // POLL_DEFAULT tier — same cadence as the insights feed around it.
    const stop = pollMs(load, POLL_DEFAULT);
    return () => {
      alive = false;
      stop();
    };
  }, [anchor]);

  const key = anchor ? `${anchor.market}:${anchor.symbol}` : "";
  const board = slot && slot.key === key ? slot : null;
  if (!board || board.tokens.length === 0) return null;

  const maxCount = board.tokens.reduce((m, t) => Math.max(m, t.count), 0);

  return (
    <section className="panel">
      <div className="panel-h">
        TRENDING IN HEADLINES
        <span
          className="text-[0.75rem] font-normal normal-case tracking-normal"
          style={{ color: "var(--faint)" }}
        >
          last 24h · across every tracked symbol
        </span>
        {stale && (
          <span
            className="chip ml-auto"
            style={{ color: "var(--bad)", borderColor: "var(--bad)" }}
          >
            poll failed — showing last data
          </span>
        )}
      </div>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-2 px-4 py-3">
        {board.tokens.map((t) => {
          const s = chipScale(t.count, maxCount);
          return (
            <span
              key={t.token}
              className="chip"
              style={{ fontSize: s.fontSize, fontWeight: s.fontWeight }}
              title={`${t.token} (${t.count} across ${t.symbols} symbols)`}
            >
              {t.token}
              <span className="tnum ml-1.5 text-[0.7rem] font-normal" style={{ color: "var(--faint)" }}>
                {t.count}
              </span>
            </span>
          );
        })}
      </div>
      {/* API note — verbatim, never softened */}
      <p className="px-4 pb-3 text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
        {board.note}
      </p>
    </section>
  );
}
