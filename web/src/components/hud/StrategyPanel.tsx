"use client";

import { fmtPct } from "@/lib/format";
import type { HudSlippage, HudStrategy } from "./types";

function Label({ children }: { children: React.ReactNode }) {
  return (
    <div className="mb-1.5 text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
      {children}
    </div>
  );
}

/** STRATEGY — identity, backtest expectations, live config, flags, slippage. */
export default function StrategyPanel({
  strategy,
  slippage,
}: {
  strategy?: HudStrategy;
  slippage?: HudSlippage;
}) {
  const s = strategy;
  const cfg = s?.config && typeof s.config === "object" ? Object.entries(s.config) : [];
  const flags = s?.flags && typeof s.flags === "object" ? Object.entries(s.flags) : [];
  return (
    <section className="panel h-full">
      <div className="panel-h">STRATEGY</div>
      <div className="flex flex-col gap-4 p-4 text-[0.8rem]">
        {!s ? (
          <div className="text-[0.78rem]" style={{ color: "var(--faint)" }}>
            no strategy block in the last sync
          </div>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-bold" style={{ color: "var(--text)" }}>
                {s.name ?? "—"}
              </span>
              {s.deployed && <span className="chip tnum">deployed {s.deployed}</span>}
              {s.last_rebalance && (
                <span className="chip tnum">last rebalance {s.last_rebalance}</span>
              )}
            </div>

            <div>
              <Label>BACKTEST EXPECTATIONS</Label>
              <div className="flex flex-wrap gap-2">
                <span
                  className="chip tnum"
                  style={{ color: "var(--bid)" }}
                  title="Compound annual growth rate (backtested)"
                >
                  CAGR {s.cagr != null && isFinite(s.cagr) ? fmtPct(s.cagr * 100) : "—"}
                </span>
                <span
                  className="chip tnum"
                  style={{ color: "var(--ask)" }}
                  title="Maximum drawdown — worst peak-to-trough loss (backtested)"
                >
                  max drawdown {s.mdd != null && isFinite(s.mdd) ? fmtPct(s.mdd * 100) : "—"}
                </span>
                <span className="chip tnum" title="Share of months that ended positive (backtested)">
                  monthly win{" "}
                  {s.monthly_win != null && isFinite(s.monthly_win)
                    ? fmtPct(s.monthly_win * 100, false)
                    : "—"}
                </span>
              </div>
            </div>

            {cfg.length > 0 && (
              <div>
                <Label>CONFIG</Label>
                <div className="grid grid-cols-1 gap-x-4 gap-y-1 text-[0.76rem] sm:grid-cols-2">
                  {cfg.map(([k, v]) => (
                    <div
                      key={k}
                      className="flex justify-between gap-2 border-b border-dashed pb-0.5"
                      style={{ borderColor: "var(--border)" }}
                    >
                      <span style={{ color: "var(--dim)" }}>{k}</span>
                      <span className="tnum" style={{ color: "var(--text)" }}>
                        {String(v)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {flags.length > 0 && (
              <div>
                <Label>LIVE FLAGS</Label>
                <div className="flex flex-wrap gap-1.5">
                  {flags.map(([k, v]) => (
                    <span key={k} className="chip tnum text-[0.75rem]">
                      {k}={v}
                    </span>
                  ))}
                </div>
              </div>
            )}

            <div
              className="text-[0.78rem] tnum"
              style={{ color: "var(--dim)" }}
              title="Slippage — price paid vs expected, in basis points (1 bp = 0.01%)"
            >
              {slippage && (slippage.n ?? 0) > 0
                ? `slippage · ${slippage.n} fills · avg ${slippage.avg_bps ?? "—"} bps · worst ${
                    slippage.worst_bps ?? "—"
                  } bps (assumed ${slippage.assumed_bps ?? 5})`
                : `slippage · no measured fills yet (assumed ${slippage?.assumed_bps ?? 5} bps)`}
            </div>
          </>
        )}
      </div>
    </section>
  );
}
