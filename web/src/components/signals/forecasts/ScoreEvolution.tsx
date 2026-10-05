"use client";

// SCORE EVOLUTION — carried over from the pre-race forecasts page: the
// mechanical composite score's stored path (last 30 days) for the selected
// symbol + horizon, drawn with the shared <Sparkline/>. This is descriptive
// history of the screener score, NOT an out-of-sample model grade — that
// caveat rides on the panel as a visible chip, per house rules. A failed poll
// degrades to a chip over the last data instead of blanking the panel.

import { useEffect, useState } from "react";
import { api, pollMs, POLL_SLOW, type Horizon, type Market, type Score } from "@/lib/api";
import { ago } from "@/lib/format";
import Sparkline from "@/components/viz/Sparkline";
import Skeleton from "@/components/Skeleton";
import { useViewMode } from "@/components/Plain";

/** History keyed by symbol|market|horizon so switching never shows a stale line. */
interface Hist {
  key: string;
  rows: Score[];
  err: string | null;
}

export default function ScoreEvolution({
  symbol,
  market,
  horizon,
}: {
  symbol: string;
  market: Market;
  horizon: Horizon;
}) {
  const mode = useViewMode();
  const key = `${symbol}|${market}|${horizon}`;
  const [hist, setHist] = useState<Hist | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api
        .scoreHistory(symbol, market, horizon, 30)
        .then((rows) => {
          if (!alive) return;
          setHist({ key, rows: [...rows].sort((a, b) => a.ts - b.ts), err: null });
        })
        .catch((e: unknown) => {
          if (!alive) return;
          const msg = e instanceof Error ? e.message : String(e);
          setHist((cur) =>
            cur && cur.key === key ? { ...cur, err: msg } : { key, rows: [], err: msg },
          );
        });
    load();
    // POLL_SLOW tier — a 30-day stored path moves on score-pass cadence.
    const stop = pollMs(load, POLL_SLOW);
    return () => {
      alive = false;
      stop();
    };
  }, [symbol, market, horizon, key]);

  const data = hist && hist.key === key ? hist : null;
  const rows = data?.rows ?? [];
  const latest = rows.length > 0 ? rows[rows.length - 1] : null;
  const first = rows.length > 0 ? rows[0] : null;
  const delta = latest && first ? latest.score - first.score : 0;

  return (
    <section className="panel">
      <div className="panel-h">
        SCORE EVOLUTION
        <span className="flex flex-wrap items-center gap-2">
          <span className="chip tnum" style={{ color: "var(--faint)" }}>
            {horizon} · 30d window
          </span>
          <span className="chip" style={{ color: "var(--dim)" }}>
            {mode === "simple"
              ? "mechanical score history — not a model grade"
              : "descriptive screener-score path — not an OOS grade"}
          </span>
          {data?.err && (
            <span
              className="chip"
              style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
              title={data.err}
            >
              poll failed — showing last data
            </span>
          )}
        </span>
      </div>

      {data === null && <Skeleton lines={2} label={`loading score history for ${symbol}`} className="m-4" />}

      {data !== null && rows.length === 0 && (
        <div className="px-4 py-3 text-[0.75rem]" style={{ color: "var(--dim)" }}>
          no stored score history for {symbol} on {horizon} yet — points accrue as the
          scorer runs; an honest blank, not a flat line.
        </div>
      )}

      {data !== null && rows.length > 0 && (
        <div className="flex flex-wrap items-center gap-4 overflow-x-auto px-4 py-3">
          <Sparkline
            closes={rows.map((r) => r.score)}
            width={420}
            height={64}
            label={`${symbol} ${horizon} score path, ${rows.length} points over 30 days`}
          />
          <div className="flex flex-col gap-1 text-[0.75rem]" style={{ color: "var(--dim)" }}>
            {latest && (
              <span className="tnum" style={{ color: "var(--text)" }}>
                latest score {latest.score.toFixed(1)}
                <span className="ml-2" style={{ color: delta >= 0 ? "var(--bid)" : "var(--ask)" }}>
                  {delta >= 0 ? "+" : ""}
                  {delta.toFixed(1)} over the window
                </span>
              </span>
            )}
            <span className="tnum" style={{ color: "var(--faint)" }}>
              {rows.length} points{latest ? ` · last ${ago(latest.ts)}` : ""}
            </span>
            <span style={{ color: "var(--faint)" }}>
              {mode === "simple"
                ? "how the mechanical score has drifted — context for the race above, not a prediction."
                : "screener composite over stored snapshots — context for the model race, not a forecast."}
            </span>
          </div>
        </div>
      )}
    </section>
  );
}
