"use client";

// OUTCOMES — the receipts panel (the alerts-tab differentiator). For every
// alert kind that fired in the lookback window, the MEASURED forward return
// after the alert: fired n, 1d/5d median + mean, 1d hit rate. Every cell
// below the daemon's independent-N gate renders "n=X — withheld (min N)" —
// an honest unknown, never a fabricated stat. The payload's `note` and
// `method` strings render VERBATIM under the table, so the definition of
// "hit" and the measurement basis are never implied.

import { useEffect, useState } from "react";
import { api, type AlertOutcomes } from "@/lib/api";
import { useViewMode } from "@/components/Plain";
import HelpTip from "@/components/HelpTip";
import CellBar from "@/components/viz/CellBar";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import { fmtPct } from "@/lib/format";
import { ruleFor, sortKinds } from "./rules";

function fwdColor(v: number): string {
  if (v > 0) return "var(--bid)";
  if (v < 0) return "var(--ask)";
  return "var(--dim)";
}

/** Forward-return cell — exact %, colored by sign; null = no read. */
function FwdCell({ v, title }: { v: number | null; title: string }) {
  return (
    <td
      className="tnum px-3 py-2 text-right"
      style={{ color: v === null ? "var(--faint)" : fwdColor(v) }}
      title={title}
    >
      {v === null ? "—" : fmtPct(v * 100)}
    </td>
  );
}

/** The gated cell — the withheld stat, stated out loud (spans a window's columns). */
function GatedCell({ n, minN, span }: { n: number; minN: number; span: number }) {
  return (
    <td
      colSpan={span}
      className="tnum px-3 py-2 text-center text-[0.75rem]"
      style={{ color: "var(--faint)" }}
      title={`only ${n} resolved outcomes in this window — below the daemon's min ${minN} gate, so the stat is withheld instead of shown on a tiny sample`}
    >
      n={n} — withheld (min {minN})
    </td>
  );
}

export default function OutcomesPanel({ days = 90 }: { days?: number }) {
  const mode = useViewMode();
  const [data, setData] = useState<AlertOutcomes | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let alive = true;
    api
      .alertOutcomes(days)
      .then((d) => {
        if (!alive) return;
        setData(d);
        setError(null);
      })
      .catch((e) => {
        if (!alive) return;
        setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [days, tick]);

  const simple = mode === "simple";
  const H = {
    kind: simple ? "alert kind" : "KIND",
    fired: simple ? "fired" : "N",
    hit1: simple ? "next-day hit rate" : "HIT 1D",
    med1: simple ? "next-day typical" : "MED 1D",
    mean1: simple ? "next-day average" : "MEAN 1D",
    med5: simple ? "5-day typical" : "MED 5D",
    mean5: simple ? "5-day average" : "MEAN 5D",
  };

  const kinds = data?.kinds ?? [];
  const ordered = sortKinds(kinds.map((k) => k.kind)).map(
    (name) => kinds.find((k) => k.kind === name)!,
  );

  return (
    <section className="panel">
      <div className="panel-h">
        {simple ? "WHAT HAPPENED AFTER PAST ALERTS" : "ALERT OUTCOMES — MEASURED FORWARD RETURNS"}
        {data && (
          <span className="chip px-2 py-[2px] text-[0.75rem] tnum">last {data.days}d</span>
        )}
        {data && (
          <span className="chip tnum px-2 py-[2px] text-[0.75rem]">
            gate: min {data.minN} per cell
          </span>
        )}
        {data && (
          <HelpTip label="How to read this table">
            For each alert kind: N is how many fired in the window; the 1d/5d columns are the
            measured forward returns after those alerts (median and mean), and the hit rate is
            the share counted as hits at 1 day — the exact definition is the method note under
            the table. Cells with fewer than {data.minN} resolved outcomes are withheld, never
            estimated from a tiny sample.
          </HelpTip>
        )}
        <span className="ml-auto text-[0.75rem] normal-case" style={{ color: "var(--faint)" }}>
          measured, not promised
        </span>
      </div>

      {data === null && !error && <Skeleton lines={4} label="loading alert outcomes" />}

      {data === null && error && (
        <div className="p-3">
          {/401/.test(error) ? (
            <p className="m-0 text-[0.75rem]" style={{ color: "var(--faint)" }}>
              sign in to see measured outcomes — this ledger is scoped to your alerts.
            </p>
          ) : (
            <ErrorState
              message={error}
              retry={() => {
                setError(null);
                setTick((t) => t + 1);
              }}
            />
          )}
        </div>
      )}

      {data !== null && (
        <>
          {ordered.length === 0 ? (
            <p className="m-0 px-4 py-4 text-[0.75rem]" style={{ color: "var(--faint)" }}>
              no alerts fired in the last {data.days}d — nothing to measure yet. The table fills
              in as alerts fire and their forward windows resolve.
            </p>
          ) : (
            <div className="table-wrap">
              <table className="w-full text-[0.75rem]">
                <thead>
                  <tr className="text-[0.75rem] tracking-wide" style={{ color: "var(--faint)" }}>
                    <th className="px-3 py-2 text-left font-medium">{H.kind}</th>
                    <th
                      className="tnum px-3 py-2 text-right font-medium"
                      title="alerts of this kind fired in the window"
                    >
                      {H.fired}
                    </th>
                    <th
                      className="px-3 py-2 text-right font-medium"
                      title="share of alerts counted as hits at 1 day — the exact definition is the method note below the table"
                    >
                      {H.hit1}
                    </th>
                    <th
                      className="px-3 py-2 text-right font-medium"
                      title="median 1-day forward return after the alert"
                    >
                      {H.med1}
                    </th>
                    <th
                      className="px-3 py-2 text-right font-medium"
                      title="mean 1-day forward return after the alert"
                    >
                      {H.mean1}
                    </th>
                    <th
                      className="px-3 py-2 text-right font-medium"
                      title="median 5-day forward return after the alert"
                    >
                      {H.med5}
                    </th>
                    <th
                      className="px-3 py-2 text-right font-medium"
                      title="mean 5-day forward return after the alert"
                    >
                      {H.mean5}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {ordered.map((k) => {
                    const r = ruleFor(k.kind);
                    return (
                      <tr key={k.kind} className="border-t" style={{ borderColor: "var(--border)" }}>
                        <td className="px-3 py-2">
                          <span
                            className="chip px-2 py-[2px] text-[0.75rem] tracking-wider"
                            style={{ color: r.color, borderColor: r.color }}
                            title={r.rule}
                          >
                            {r.label}
                          </span>
                        </td>
                        <td
                          className="tnum px-3 py-2 text-right"
                          style={{ color: "var(--dim)" }}
                          title={`${k.n} fired · ${k.n1d} with a resolved 1d return · ${k.n5d} with a resolved 5d return`}
                        >
                          {k.n}
                        </td>
                        {k.gated1d ? (
                          <GatedCell n={k.n1d} minN={data.minN} span={3} />
                        ) : (
                          <>
                            <td className="px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                              <CellBar
                                frac={
                                  k.hitRate1d === null || !Number.isFinite(k.hitRate1d)
                                    ? null
                                    : k.hitRate1d
                                }
                                label={
                                  k.hitRate1d === null || !Number.isFinite(k.hitRate1d)
                                    ? "—"
                                    : fmtPct(k.hitRate1d * 100, false)
                                }
                                color="var(--accent)"
                                title={`1d hit rate over ${k.n1d} resolved alerts — bar on an absolute 0–100% scale; definition of a hit is the method note below`}
                              />
                            </td>
                            <FwdCell
                              v={k.median1d}
                              title={`median 1d forward return over ${k.n1d} resolved alerts`}
                            />
                            <FwdCell
                              v={k.mean1d}
                              title={`mean 1d forward return over ${k.n1d} resolved alerts`}
                            />
                          </>
                        )}
                        {k.gated5d ? (
                          <GatedCell n={k.n5d} minN={data.minN} span={2} />
                        ) : (
                          <>
                            <FwdCell
                              v={k.median5d}
                              title={`median 5d forward return over ${k.n5d} resolved alerts`}
                            />
                            <FwdCell
                              v={k.mean5d}
                              title={`mean 5d forward return over ${k.n5d} resolved alerts`}
                            />
                          </>
                        )}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}

          {/* the payload's own honesty strings — verbatim, always rendered */}
          <div
            className="flex flex-col gap-1 border-t px-4 py-2 text-[0.75rem] leading-relaxed"
            style={{ borderColor: "var(--border)", color: "var(--faint)" }}
          >
            <p className="m-0">{data.note}</p>
            <p className="m-0">{data.method}</p>
          </div>
        </>
      )}
    </section>
  );
}
