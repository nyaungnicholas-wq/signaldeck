"use client";

// KNOWLEDGE-GRAPH RIPPLE — the neighborhood around ONE symbol: what it
// co-moves with (price correlation) and who holds it alongside other names
// (13F co-ownership). A radial <RippleGraph/> shows the shape; the adjacency
// table below is the accessible, exact readout (network graphs read poorly to
// screen readers). Honesty rule: the daemon's caveat is rendered verbatim —
// co-movement is not causation, pairs need ≥30 shared trading days, and
// supplier/customer edges aren't in this data.

import { useEffect, useMemo, useState } from "react";
import { api, type GraphResult } from "@/lib/api";
import PagePurpose from "@/components/PagePurpose";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import RippleGraph, {
  kindColor,
  kindLabel,
  otherEndpoint,
} from "@/components/graph/RippleGraph";

const MIN_CORR_MIN = 0.3;
const MIN_CORR_MAX = 0.9;

interface Query {
  symbol: string;
  minCorr: number;
}

export default function GraphPage() {
  // Draft controls vs. the committed query: the "Map" button (or Enter in the
  // symbol box) commits BOTH the symbol and the threshold in one go, so the
  // graph never re-fetches mid-drag on the slider.
  const [draftSymbol, setDraftSymbol] = useState("NVDA");
  const [minCorr, setMinCorr] = useState(0.5);
  const [query, setQuery] = useState<Query>({ symbol: "NVDA", minCorr: 0.5 });

  const [result, setResult] = useState<GraphResult | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    setResult(null);
    setErr(null);
    api
      .knowledgeGraph(query.symbol, query.minCorr)
      .then((r) => {
        if (!alive) return;
        setResult(r);
        setErr(null);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setErr(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [query, retryTick]);

  const submit = () => {
    const s = draftSymbol.trim().toUpperCase();
    if (!s) return;
    setQuery({ symbol: s, minCorr });
  };

  // Guarded reads — the daemon serializes empty Go slices as JSON null.
  const edges = useMemo(() => result?.neighborhood.Edges ?? [], [result]);
  const neighbors = result?.neighborhood.Neighbors ?? [];
  const comparedWith = result?.comparedWith ?? [];
  const center = result?.center || result?.neighborhood.Center || query.symbol;

  // Rows for the adjacency table + the two derived counts the header chips show.
  // Neighbors are derived from the drawn edges so the count matches the viz.
  const rows = useMemo(() => [...edges].sort((a, b) => b.Weight - a.Weight), [edges]);
  const drawnSet = useMemo(
    () => new Set(edges.map((e) => otherEndpoint(center, e).toUpperCase())),
    [edges, center],
  );
  // Symbols the daemon lists in the neighborhood but which have no drawn link at
  // the current threshold — surfaced honestly rather than hidden.
  const belowThreshold = useMemo(
    () => neighbors.filter((nb) => !drawnSet.has(nb.toUpperCase())).length,
    [neighbors, drawnSet],
  );

  const loading = result === null && err === null;
  const hardError = result === null && err !== null;
  const empty = result !== null && edges.length === 0;

  return (
    <div className="flex flex-col gap-4">
      {/* header */}
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">KNOWLEDGE-GRAPH RIPPLE</h1>
        <span className="chip mono">{center}</span>
        <span className="chip tnum" style={{ color: "var(--dim)" }}>
          min corr ≥ {query.minCorr.toFixed(2)}
        </span>
      </div>

      <PagePurpose
        id="markets-graph"
        text="The ripple around one symbol — what it co-moves with (correlation) and who holds it alongside others (13F co-ownership)."
      />

      {/* controls: symbol + min-correlation + Map */}
      <section className="panel">
        <div className="panel-h">MAP A SYMBOL</div>
        <div className="flex flex-wrap items-end gap-x-5 gap-y-3 px-4 py-3 text-[0.75rem]">
          <label className="flex flex-col gap-1">
            <span style={{ color: "var(--faint)" }}>symbol</span>
            <input
              value={draftSymbol}
              onChange={(e) => setDraftSymbol(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") submit();
              }}
              placeholder="e.g. NVDA"
              aria-label="symbol to map"
              className="mono min-h-[40px] w-40 rounded-lg border px-3 uppercase outline-none"
              style={{ background: "var(--panel2)", borderColor: "var(--border)", color: "var(--text)" }}
            />
          </label>

          <label className="flex flex-col gap-1">
            <span style={{ color: "var(--faint)" }}>min correlation</span>
            <span className="flex items-center gap-2">
              <input
                type="range"
                min={MIN_CORR_MIN}
                max={MIN_CORR_MAX}
                step={0.05}
                value={minCorr}
                onChange={(e) => setMinCorr(Number(e.target.value))}
                className="w-40 cursor-pointer"
                style={{ accentColor: "var(--accent)" }}
                aria-label="minimum correlation threshold"
              />
              <span className="chip tnum">≥ {minCorr.toFixed(2)}</span>
            </span>
          </label>

          <button
            type="button"
            onClick={submit}
            disabled={draftSymbol.trim() === ""}
            className="chip min-h-[40px] cursor-pointer px-5 font-bold transition-colors duration-150 hover:brightness-125 disabled:cursor-not-allowed disabled:opacity-40"
            style={{
              color: "var(--accent)",
              borderColor: "var(--accent)",
              background: "rgba(251,191,36,.08)",
            }}
          >
            Map
          </button>

          <span className="ml-auto text-[0.75rem]" style={{ color: "var(--faint)" }}>
            higher threshold = fewer, tighter correlation links
          </span>
        </div>
      </section>

      {loading && <Skeleton lines={6} label={`mapping the ripple around ${query.symbol}`} />}

      {hardError && (
        <ErrorState
          message={err ?? "graph unavailable"}
          hint="Is the daemon running? Start it with signaldeckd."
          retry={() => {
            setErr(null);
            setRetryTick((t) => t + 1);
          }}
        />
      )}

      {empty && (
        <EmptyState
          message={`No strong links for ${center} yet — thin overlapping history.`}
          detail="Correlation needs ≥30 shared trading days; co-ownership needs stored 13F holdings. Try a lower min-correlation threshold or a more widely-held symbol."
        />
      )}

      {result !== null && !empty && (
        <>
          {/* the viz */}
          <section className="panel">
            <div className="panel-h flex-wrap gap-2">
              RIPPLE
              <span className="chip tnum">
                {rows.length} link{rows.length === 1 ? "" : "s"}
              </span>
              <span className="chip tnum">
                {drawnSet.size} neighbor{drawnSet.size === 1 ? "" : "s"}
              </span>
              {belowThreshold > 0 && (
                <span
                  className="chip tnum"
                  style={{ color: "var(--faint)" }}
                  title="listed in the neighborhood but no link met the current threshold to draw"
                >
                  +{belowThreshold} below threshold
                </span>
              )}
              {comparedWith.length > 0 && (
                <span
                  className="chip tnum"
                  style={{ color: "var(--faint)" }}
                  title="symbols with enough overlapping history to be compared for correlation"
                >
                  compared with {comparedWith.length}
                </span>
              )}
            </div>

            <div className="mx-auto max-w-[720px] px-3 py-4">
              <RippleGraph center={center} edges={edges} />
            </div>

            {/* legend + edge-kind counts (colour is labelled, never alone) */}
            <div
              className="flex flex-wrap items-center gap-x-5 gap-y-2 px-4 py-3 text-[0.75rem]"
              style={{ borderTop: "1px solid var(--border)", color: "var(--dim)" }}
            >
              <span className="flex items-center gap-2">
                <svg width={22} height={10} aria-hidden="true">
                  <line
                    x1={1}
                    y1={5}
                    x2={21}
                    y2={5}
                    stroke={kindColor("correlation")}
                    strokeWidth={3}
                    strokeLinecap="round"
                  />
                </svg>
                correlation
              </span>
              <span className="flex items-center gap-2">
                <svg width={22} height={10} aria-hidden="true">
                  <line
                    x1={1}
                    y1={5}
                    x2={21}
                    y2={5}
                    stroke={kindColor("coowned")}
                    strokeWidth={3}
                    strokeLinecap="round"
                  />
                </svg>
                co-owned (13F)
              </span>
              <span style={{ color: "var(--faint)" }}>line thickness ∝ weight</span>
              <span className="ml-auto flex flex-wrap gap-1.5">
                {Object.entries(result.edgeKinds).map(([k, c]) => (
                  <span
                    key={k}
                    className="chip tnum"
                    style={{ color: kindColor(k), borderColor: kindColor(k) }}
                  >
                    {kindLabel(k)} {c}
                  </span>
                ))}
              </span>
            </div>
          </section>

          {/* adjacency table — the accessible source of truth */}
          <section className="panel">
            <div className="panel-h flex-wrap gap-2">
              ADJACENCY
              <span
                className="text-[0.75rem] font-normal normal-case tracking-normal"
                style={{ color: "var(--faint)" }}
              >
                the accessible readout — sorted by link weight
              </span>
            </div>
            <div className="table-wrap">
              <table className="w-full text-[0.75rem]">
                <thead>
                  <tr
                    className="text-left tracking-[0.12em]"
                    style={{ color: "var(--dim)", borderBottom: "1px solid var(--border)" }}
                  >
                    <th className="px-3 py-2">NEIGHBOR</th>
                    <th className="px-3 py-2">LINK</th>
                    <th className="px-3 py-2 text-right">WEIGHT</th>
                  </tr>
                </thead>
                <tbody className="tnum">
                  {rows.map((e, i) => {
                    const nb = otherEndpoint(center, e);
                    return (
                      <tr
                        key={`${nb}-${e.Kind}-${i}`}
                        style={{ borderBottom: "1px solid var(--border)" }}
                      >
                        <td className="mono px-3 py-2 font-bold" style={{ color: "var(--text)" }}>
                          {nb}
                        </td>
                        <td className="px-3 py-2">
                          <span
                            className="inline-flex items-center gap-2"
                            style={{ color: "var(--dim)" }}
                          >
                            <span
                              aria-hidden="true"
                              style={{
                                width: 8,
                                height: 8,
                                borderRadius: 9999,
                                background: kindColor(e.Kind),
                                display: "inline-block",
                              }}
                            />
                            {kindLabel(e.Kind)}
                          </span>
                        </td>
                        <td className="px-3 py-2 text-right">{e.Weight.toFixed(2)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </section>

          {/* honest caveat — rendered verbatim from the daemon */}
          <div
            className="flex flex-col gap-1 px-1 text-[0.75rem] leading-relaxed"
            style={{ color: "var(--faint)" }}
          >
            {result.note ? <p>{result.note}</p> : null}
            {result.neighborhood.Note && result.neighborhood.Note !== result.note ? (
              <p>{result.neighborhood.Note}</p>
            ) : null}
          </div>
        </>
      )}
    </div>
  );
}
