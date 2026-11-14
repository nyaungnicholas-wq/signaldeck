"use client";

// PUBLIC FORECASTS (/forecasts). The current rule-based readings for every
// covered stock, for a visitor with no account. It exists only on an instance
// whose daemon is fed by a price source that may be shown publicly; on any
// other instance the daemon refuses the read and this page says so.
//
// It deliberately shows NO accuracy figure. This record is new, nothing has
// been measured on this data yet, and the backtest tables measured on other
// data are not shown here.

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  KIND_LABEL,
  STALE_AFTER_DAYS,
  daysSince,
  pivotForecasts,
  strengthText,
  type PublicForecasts,
} from "@/lib/publicForecasts";

type State =
  | { kind: "loading" }
  | { kind: "ready"; data: PublicForecasts }
  | { kind: "unavailable"; why: string };

function tone(regime: string): string {
  const r = regime.toLowerCase();
  if (r.includes("up") || r === "calm" || r.includes("rising")) return "var(--bid)";
  if (r.includes("down") || r === "elevated" || r.includes("falling")) return "var(--ask)";
  return "var(--dim)";
}

export default function ForecastsPage() {
  const [state, setState] = useState<State>({ kind: "loading" });
  const [query, setQuery] = useState("");

  useEffect(() => {
    let alive = true;
    fetch("/api/public/forecasts", { headers: { "X-Signaldeck": "1" } })
      .then(async (res) => {
        if (!alive) return;
        if (res.status === 401 || res.status === 451) {
          setState({
            kind: "unavailable",
            why: "Forecasts are not shown publicly on this site yet. The grades and receipts pages need no account.",
          });
          return;
        }
        if (!res.ok) {
          setState({ kind: "unavailable", why: `The forecast service answered ${res.status}. Try again in a minute.` });
          return;
        }
        const data = (await res.json()) as PublicForecasts;
        setState({ kind: "ready", data });
      })
      .catch(() => {
        if (alive) setState({ kind: "unavailable", why: "The forecast service could not be reached. Try again in a minute." });
      });
    return () => {
      alive = false;
    };
  }, []);

  const table = useMemo(
    () => (state.kind === "ready" ? pivotForecasts(state.data.rows ?? []) : { kinds: [], lines: [] }),
    [state],
  );
  const q = query.trim().toUpperCase();
  const lines = q ? table.lines.filter((l) => l.symbol.includes(q)) : table.lines;

  return (
    <div className="mx-auto flex w-full max-w-[1100px] flex-col gap-5">
      <header className="flex flex-col gap-2">
        <h1 className="text-[1.4rem] font-extrabold tracking-tight">Forecasts</h1>
        <p className="text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          For each stock: is its price trend, its volatility and its trading activity expected to be
          higher or lower than usual over the coming weeks. Each reading comes from a fixed rule
          applied to recent prices and volumes. Descriptive, not advice.
        </p>
      </header>

      {state.kind === "loading" && (
        <div className="panel px-5 py-6 text-[0.85rem]" style={{ color: "var(--dim)" }} role="status">
          Loading forecasts...
        </div>
      )}

      {state.kind === "unavailable" && (
        <div className="panel px-5 py-6 text-[0.85rem] leading-relaxed" data-testid="forecasts-unavailable">
          <p>{state.why}</p>
          <p className="mt-2">
            <Link href="/accuracy" className="hover:underline" style={{ color: "var(--accent)" }}>
              See the grades
            </Link>
          </p>
        </div>
      )}

      {state.kind === "ready" && (
        <>
          <section className="panel px-5 py-4 text-[0.8rem] leading-relaxed" aria-label="about this data">
            {state.data.citation && (
              <p data-testid="forecasts-citation">
                <a
                  href="https://www.iex.io/legal/hist-data-terms"
                  target="_blank"
                  rel="noreferrer"
                  className="font-semibold hover:underline"
                >
                  Data provided for free by IEX.
                </a>{" "}
                {state.data.coverage}
              </p>
            )}
            <p className="mt-1" style={{ color: "var(--dim)" }}>
              {state.data.note}
            </p>
            {state.data.dataThrough && (
              <p className="mt-1 font-semibold" data-testid="forecasts-data-through">
                Built from trading data through {state.data.dataThrough}.
              </p>
            )}
            {(daysSince(state.data.dataThrough) ?? 0) > STALE_AFTER_DAYS && (
              <p className="mt-1" style={{ color: "var(--warn)" }} data-testid="forecasts-stale">
                No newer data has arrived since then, so these readings are not up to date. IEX has
                paused downloads of its historical data; the readings will update again when it
                resumes.
              </p>
            )}
            <p className="mt-1" style={{ color: "var(--faint)" }}>
              {table.lines.length.toLocaleString("en-US")} stocks
              {" · "}the percentage is how far the reading sits from its threshold, not a probability.
            </p>
          </section>

          <section className="panel" aria-label="forecast table">
            <div className="panel-h flex flex-wrap items-center justify-between gap-3">
              <span style={{ color: "var(--dim)" }}>CURRENT READINGS</span>
              <label className="flex items-center gap-2 text-[0.75rem]" style={{ color: "var(--faint)" }}>
                <span>Find a symbol</span>
                <input
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="AAPL"
                  className="rounded border bg-transparent px-2 py-1 text-[0.8rem]"
                  style={{ borderColor: "var(--border)", color: "var(--text)", width: "7rem" }}
                />
              </label>
            </div>
            {lines.length === 0 ? (
              <p className="px-5 py-5 text-[0.85rem]" style={{ color: "var(--dim)" }}>
                {table.lines.length === 0
                  ? "No readings yet. They appear after the first daily update."
                  : `No covered stock matches ${query.trim()}.`}
              </p>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-[0.8rem]">
                  <thead>
                    <tr style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}>
                      <th scope="col" className="px-4 py-2 font-medium">
                        symbol
                      </th>
                      {table.kinds.map((k) => (
                        <th key={k} scope="col" className="px-3 py-2 font-medium">
                          {KIND_LABEL[k] ?? k}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="tnum">
                    {lines.map((l) => (
                      <tr key={l.symbol} style={{ borderBottom: "1px solid var(--border)" }}>
                        <th scope="row" className="px-4 py-2 font-bold" style={{ color: "var(--text)" }}>
                          {l.symbol}
                        </th>
                        {table.kinds.map((k) => {
                          const c = l.cells[k];
                          return (
                            <td key={k} className="px-3 py-2">
                              {c ? (
                                <>
                                  <span style={{ color: tone(c.regime) }}>{c.regime}</span>
                                  <span className="ml-2" style={{ color: "var(--faint)" }}>
                                    {strengthText(c.conviction)}
                                  </span>
                                </>
                              ) : (
                                <span style={{ color: "var(--faint)" }} title="not enough history for this reading yet">
                                  —
                                </span>
                              )}
                            </td>
                          );
                        })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </div>
  );
}
