"use client";

// THIS SYMBOL'S AGENT — the model learned from THIS symbol's OWN resolved
// outcomes: its plain-English personality, per-signal skill bars, the active
// evidence tier, and an honest learning-progress bar. Every stock behaves
// differently, so every stock gets its own agent; until a symbol has enough of
// its own resolved calls it stays on the GLOBAL model and this panel says so.

import { useEffect, useState } from "react";
import { symbolAgent, type Horizon, type Market, type SymbolAgent } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

// Horizons the per-symbol learner models (matches the daemon's predHorizons).
type AgentHorizon = "1d" | "1w";
const AGENT_HORIZONS: AgentHorizon[] = ["1d", "1w"];

const LEG_LABEL: Record<string, string> = {
  pressure: "Pressure",
  expectancy: "Expectancy",
  forecast: "Forecast",
  sentiment: "Sentiment",
};

function tierBadge(tier: SymbolAgent["tier"]): { text: string; color: string; bg: string } {
  switch (tier) {
    case "personal":
      return { text: "personal model", color: "var(--bid)", bg: "var(--panel2)" };
    case "regime":
      return { text: "global · per-regime", color: "var(--warn)", bg: "var(--panel2)" };
    case "global":
      return { text: "global model", color: "var(--warn)", bg: "var(--panel2)" };
    default:
      return { text: "static prior", color: "var(--dim)", bg: "var(--panel2)" };
  }
}

// Colour a directional hit-rate: >0.5 is edge over a coin flip.
function hrColor(hr: number): string {
  if (hr >= 0.58) return "var(--bid)";
  if (hr >= 0.52) return "var(--warn)";
  return "var(--dim)";
}

function SkillBar({ label, hitRate, hasHR, ic, hasIC, n }: {
  label: string;
  hitRate: number;
  hasHR: boolean;
  ic: number;
  hasIC: boolean;
  n: number;
}) {
  const pct = hasHR ? Math.max(0, Math.min(1, hitRate)) * 100 : 0;
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-2 text-[0.75rem]">
        <span style={{ color: "var(--text)" }}>{label}</span>
        <span className="tnum" style={{ color: "var(--faint)" }}>
          {hasHR ? `${(hitRate * 100).toFixed(0)}% hit` : "no directional calls"}
          {hasIC ? ` · IC ${ic >= 0 ? "+" : ""}${ic.toFixed(2)}` : ""}
          {` · n=${n}`}
        </span>
      </div>
      {/* Hit-rate track with a 50% coin-flip marker. */}
      <div className="relative h-2 w-full overflow-hidden rounded-full" style={{ background: "var(--panel2)" }}>
        {hasHR && (
          <div
            className="absolute inset-y-0 left-0 rounded-full transition-[width] duration-300"
            style={{ width: `${pct}%`, background: hrColor(hitRate) }}
          />
        )}
        {/* coin-flip reference at 50% */}
        <div className="absolute inset-y-0" style={{ left: "50%", width: 1, background: "var(--border)" }} />
      </div>
    </div>
  );
}

export default function SymbolAgentPanel({
  symbol,
  market,
}: {
  symbol: string;
  market: Market;
}) {
  const [horizon, setHorizon] = useState<AgentHorizon>("1d");
  const [retryTick, setRetryTick] = useState(0);
  // Keyed by the request so a symbol/horizon change shows a loading state
  // WITHOUT a synchronous setState inside the effect (mirrors the bars pattern
  // on the symbol page). data/err are only "current" when their key matches.
  const reqKey = `${symbol}|${market}|${horizon}|${retryTick}`;
  const [dataState, setDataState] = useState<{ key: string; d: SymbolAgent } | null>(null);
  const [errState, setErrState] = useState<{ key: string; msg: string } | null>(null);
  const data = dataState && dataState.key === reqKey ? dataState.d : null;
  const err = errState && errState.key === reqKey ? errState.msg : null;

  useEffect(() => {
    let alive = true;
    const key = `${symbol}|${market}|${horizon}|${retryTick}`;
    symbolAgent(symbol, market, horizon as Horizon)
      .then((d) => {
        if (alive) setDataState({ key, d });
      })
      .catch((e: unknown) => {
        if (alive) setErrState({ key, msg: e instanceof Error ? e.message : String(e) });
      });
    return () => {
      alive = false;
    };
  }, [symbol, market, horizon, retryTick]);

  const badge = data ? tierBadge(data.tier) : null;
  const progress = data ? Math.max(0, Math.min(1, data.nSamples / Math.max(1, data.threshold))) : 0;

  return (
    <section className="panel">
      <div className="panel-h">
        <span>THIS SYMBOL&rsquo;S AGENT</span>
        {data && badge && (
          <span
            className="chip ml-2"
            style={{ color: badge.color, borderColor: badge.color, background: badge.bg }}
            title="which evidence tier is driving this symbol's blend"
          >
            {badge.text}
          </span>
        )}
        <div role="group" aria-label="Agent horizon" className="ml-auto flex items-center gap-1">
          {AGENT_HORIZONS.map((h) => {
            const active = h === horizon;
            return (
              <button
                key={h}
                type="button"
                onClick={() => setHorizon(h)}
                aria-pressed={active}
                className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
                style={active ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
              >
                {h}
              </button>
            );
          })}
        </div>
      </div>

      {err ? (
        <ErrorState
          message={err}
          retry={() => setRetryTick((t) => t + 1)}
          className="border-0"
        />
      ) : !data ? (
        <div className="px-4 py-4">
          <Skeleton lines={4} label="loading symbol agent" className="border-0 p-0" />
        </div>
      ) : (
        <div className="flex flex-col gap-4 px-4 py-4">
          {/* Personality — the deterministic plain-English read. */}
          <p className="text-[0.82rem] leading-relaxed" style={{ color: "var(--text)" }}>
            {data.personality}
          </p>

          {/* Learning progress — honest n/threshold toward a personal model. */}
          {!data.personal && (
            <div>
              <div className="mb-1 flex items-baseline justify-between text-[0.75rem]" style={{ color: "var(--dim)" }}>
                <span>
                  still learning — using the{" "}
                  {data.tier === "regime"
                    ? "global per-regime model"
                    : data.tier === "global"
                      ? "global model"
                      : "equal-weight prior"}
                </span>
                <span className="tnum" style={{ color: "var(--faint)" }}>
                  {data.nSamples}/{data.threshold} own outcomes
                </span>
              </div>
              <div className="h-2 w-full overflow-hidden rounded-full" style={{ background: "var(--panel2)" }}>
                <div
                  className="h-full rounded-full transition-[width] duration-300"
                  style={{ width: `${progress * 100}%`, background: "var(--warn)" }}
                />
              </div>
            </div>
          )}

          {/* Per-signal skill bars — the measured edge, whatever the tier. */}
          {(data.skill ?? []).length > 0 ? (
            <div className="flex flex-col gap-3">
              <div className="text-[0.75rem] uppercase tracking-wider" style={{ color: "var(--faint)" }}>
                per-signal skill (this symbol&rsquo;s own history)
              </div>
              {(data.skill ?? []).map((s) => (
                <SkillBar
                  key={s.component}
                  label={LEG_LABEL[s.component] ?? s.component}
                  hitRate={s.hitRate}
                  hasHR={s.hasHR}
                  ic={s.ic}
                  hasIC={s.hasIC}
                  n={s.n}
                />
              ))}
            </div>
          ) : (
            <div className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
              No resolved outcomes for this symbol yet — skill fills in as its predictions resolve
              (resolutions begin after each prediction&rsquo;s horizon elapses).
            </div>
          )}

          {/* Active weights — only when a personal model is truly in force. */}
          {data.personal && Object.keys(data.activeWeights).length > 0 && (
            <div className="flex flex-col gap-1">
              <div className="text-[0.75rem] uppercase tracking-wider" style={{ color: "var(--faint)" }}>
                active blend weights (personal)
              </div>
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(data.activeWeights)
                  .sort((a, b) => b[1] - a[1])
                  .map(([leg, w]) => (
                    <span key={leg} className="chip tnum" style={{ color: "var(--text)" }}>
                      {LEG_LABEL[leg] ?? leg} {(w * 100).toFixed(0)}%
                    </span>
                  ))}
              </div>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
