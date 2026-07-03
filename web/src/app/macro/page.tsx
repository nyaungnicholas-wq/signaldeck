"use client";

import { useEffect, useMemo, useState } from "react";
import { api, type Macro, type SectorAgg } from "@/lib/api";
import { ago } from "@/lib/format";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import Bar from "@/components/macro/Bar";
import Push20Macro from "@/components/macro/Push20Macro";
import SectorRow from "@/components/macro/SectorRow";

const POLL_MS = 10000;

/** Breadth bar color: green when broadly positive, red when broadly weak. */
function breadthColor(pct: number): string {
  if (!isFinite(pct)) return "var(--dim)";
  if (pct > 55) return "var(--bid)";
  if (pct < 45) return "var(--ask)";
  return "var(--dim)";
}

/** Volatility label → semantic color, per the daemon's vol buckets. */
function volColor(label: string): string {
  switch ((label || "").toLowerCase()) {
    case "calm":
      return "var(--ok)";
    case "elevated":
      return "var(--warn)";
    case "stressed":
      return "var(--bad)";
    default:
      return "var(--dim)"; // normal / unknown
  }
}

export default function MacroPage() {
  const [macro, setMacro] = useState<Macro | null>(null);
  const [sectors, setSectors] = useState<SectorAgg[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const load = () =>
      Promise.all([api.macro(), api.sectors()])
        .then(([m, s]) => {
          if (!alive) return;
          setMacro(m);
          setSectors(Array.isArray(s) ? s : []);
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
  }, [retryTick]);

  // Strongest sector first, by mean pressure score.
  const sortedSectors = useMemo(
    () =>
      [...(sectors ?? [])].sort(
        (a, b) => (isFinite(b.MeanScore) ? b.MeanScore : -Infinity) - (isFinite(a.MeanScore) ? a.MeanScore : -Infinity),
      ),
    [sectors],
  );

  const loading = macro === null && sectors === null && err === null;
  const hardError = macro === null && sectors === null && err !== null;

  const breadth = macro ? (isFinite(macro.breadthPct) ? macro.breadthPct : 0) : 0;
  const volLabel = macro?.volLabel ?? "";

  return (
    <div className="flex flex-col gap-4">
      {/* header row */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">MACRO</h1>
        {macro !== null && (
          <span className="chip tnum" aria-label={`as of ${ago(macro.asOf)}`}>
            as of {ago(macro.asOf)}
          </span>
        )}
        {err !== null && (macro !== null || sectors !== null) && (
          <span className="chip" style={{ color: "var(--bad)", borderColor: "var(--bad)" }}>
            poll failed — showing last data
          </span>
        )}
      </div>

      {loading && <Skeleton lines={4} label="loading market context" />}

      {hardError && (
        <ErrorState
          message={err ?? "macro data unavailable"}
          hint="Is the daemon running? Start it with signaldeckd."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {/* MARKET CONTEXT */}
      {macro !== null && (
        <section className="panel">
          <div className="panel-h">
            MARKET CONTEXT
            <span className="tnum ml-auto" style={{ color: "var(--faint)" }}>
              breadth · volatility · trader gate
            </span>
          </div>

          <div className="flex flex-col gap-5 px-4 py-4">
            {/* BREADTH */}
            <div>
              <div className="mb-1.5 flex items-baseline justify-between">
                <span className="text-[0.78rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
                  BREADTH
                </span>
                <span
                  className="tnum text-[0.78rem]"
                  style={{ color: breadthColor(breadth) }}
                  aria-label={`${macro.positive} of ${macro.scored} symbols positive`}
                >
                  {macro.positive} of {macro.scored} symbols positive
                </span>
              </div>
              {macro.scored > 0 ? (
                <>
                  <Bar
                    pct={breadth}
                    color={breadthColor(breadth)}
                    label={`market breadth ${breadth.toFixed(0)} percent of scored symbols positive`}
                  />
                  <div className="tnum mt-1 text-right text-[0.78rem]" style={{ color: "var(--faint)" }}>
                    {breadth.toFixed(0)}%
                  </div>
                </>
              ) : (
                <div className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  nothing scored yet — breadth fills in as bar history accrues.
                </div>
              )}
            </div>

            {/* VOLATILITY */}
            <div>
              <div className="mb-1.5 text-[0.78rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
                VOLATILITY
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <span
                  className="chip"
                  style={{ color: volColor(volLabel), borderColor: volColor(volLabel) }}
                  aria-label={`volatility ${volLabel || "unknown"}`}
                >
                  {volLabel || "unknown"}
                </span>
                <span className="chip tnum" aria-label={`annualized volatility ${macro.volPct.toFixed(1)} percent`}>
                  <span style={{ color: "var(--text)" }}>
                    {isFinite(macro.volPct) ? `${macro.volPct.toFixed(1)}%` : "—"}
                  </span>{" "}
                  annualized
                </span>
              </div>
            </div>

            {/* PUSH-20 MACRO */}
            <div>
              <div className="mb-1.5 text-[0.78rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
                PUSH-20 MACRO
              </div>
              <div
                className="rounded-lg border"
                style={{ borderColor: "var(--border)", background: "var(--panel2)" }}
              >
                <Push20Macro data={macro.push20Macro} />
              </div>
            </div>

            {/* honest note, verbatim */}
            {macro.note && (
              <p className="text-[0.78rem] leading-relaxed" style={{ color: "var(--faint)" }}>
                {macro.note}
              </p>
            )}
          </div>
        </section>
      )}

      {/* SECTOR ROTATION */}
      {sectors !== null && (
        <section className="panel">
          <div className="panel-h">
            SECTOR ROTATION
            {sortedSectors.length > 0 && (
              <span className="chip tnum ml-auto">{sortedSectors.length} sectors</span>
            )}
          </div>

          <p className="px-4 py-3 text-[0.76rem] leading-relaxed" style={{ color: "var(--dim)" }}>
            Which sectors are strongest right now — money rotates toward the top.
          </p>

          {sortedSectors.length === 0 ? (
            <EmptyState
              className="m-4"
              message="No sector data yet"
              detail="Sector aggregation runs hourly once ranking data exists."
            />
          ) : (
            <ul style={{ borderTop: "1px solid var(--border)" }}>
              {sortedSectors.map((s) => (
                <SectorRow key={s.Sector} s={s} />
              ))}
            </ul>
          )}
        </section>
      )}
    </div>
  );
}
