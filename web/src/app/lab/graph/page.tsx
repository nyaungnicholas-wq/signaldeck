"use client";
import { useEffect, useMemo, useState } from "react";
import { api, type GraphResult } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import EmptyState from "@/components/EmptyState";
import RippleGraph, { kindColor, kindLabel, otherEndpoint } from "@/components/graph/RippleGraph";
import { PageHero, StatTile } from "@/components/ui/Kit";

const MIN_CORR_MIN = 0.3;
const MIN_CORR_MAX = 0.9;

interface Query {
  symbol: string;
  minCorr: number;
}

export default function GraphPage() {
  const [draftSymbol, setDraftSymbol] = useState("NVDA");
  const [minCorr, setMinCorr] = useState(0.5);
  const [query, setQuery] = useState<Query>({ symbol: "NVDA", minCorr: 0.5 });
  const [result, setResult] = useState<{ key: string; data: GraphResult } | null>(null);
  const [errState, setErrState] = useState<{ key: string; msg: string } | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  const queryKey = `${query.symbol}|${query.minCorr}`;

  useEffect(() => {
    let alive = true;
    const key = `${query.symbol}|${query.minCorr}`;
    api
      .knowledgeGraph(query.symbol, query.minCorr)
      .then((r) => {
        if (!alive) return;
        setResult({ key, data: r });
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setErrState({ key, msg: e instanceof Error ? e.message : String(e) });
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

  const data = result && result.key === queryKey ? result.data : null;
  const err = errState && errState.key === queryKey ? errState.msg : null;

  const edges = useMemo(() => data?.neighborhood.Edges ?? [], [data]);
  const comparedWith = data?.comparedWith ?? [];
  const center = data?.center || data?.neighborhood.Center || query.symbol;

  const rows = useMemo(() => [...edges].sort((a, b) => b.Weight - a.Weight), [edges]);
  const drawnSet = useMemo(
    () => new Set(edges.map((e) => otherEndpoint(center, e).toUpperCase())),
    [edges, center],
  );
  const belowThreshold = useMemo(() => {
    const nbrs = data?.neighborhood.Neighbors ?? [];
    return nbrs.filter((nb) => !drawnSet.has(nb.toUpperCase())).length;
  }, [data, drawnSet]);

  const loading = data === null && err === null;
  const hardError = data === null && err !== null;
  const empty = data !== null && edges.length === 0;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Knowledge-Graph Ripple"
        subtitle="Explore the neighborhood around a symbol: price correlation and 13F co-ownership links."
      />

      <div className="panel">
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
      </div>

      {loading && <Skeleton lines={6} label={`mapping the ripple around ${query.symbol}`} />}

      {hardError && (
        <ErrorState
          message={err ?? "graph unavailable"}
          hint="Is the daemon running? Start it with signaldeckd."
          retry={() => {
            setErrState(null);
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

      {data !== null && !empty && (
        <>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <StatTile label="LINKS" value={rows.length} i={0} />
            <StatTile label="NEIGHBORS" value={drawnSet.size} i={1} />
            {belowThreshold > 0 && <StatTile label="BELOW THRESHOLD" value={belowThreshold} i={2} />}
            {comparedWith.length > 0 && <StatTile label="COMPARED WITH" value={comparedWith.length} i={3} />}
          </div>

          <section className="hud-panel">
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

            <div className="mx-auto w-full max-w-[720px] px-3 py-4">
              <RippleGraph center={center} edges={edges} />
            </div>

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
                {Object.entries(data.edgeKinds).map(([k, c]) => (
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

          <div
            className="flex flex-col gap-1 px-1 text-[0.75rem] leading-relaxed"
            style={{ color: "var(--faint)" }}
          >
            {data.note ? <p>{data.note}</p> : null}
            {data.neighborhood.Note && data.neighborhood.Note !== data.note ? (
              <p>{data.neighborhood.Note}</p>
            ) : null}
          </div>
        </>
      )}
    </div>
  );
}
