"use client";

// AI RESEARCH DESK — OVERVIEW. The desk in one view: the ranked opportunity
// shortlist re-aims a selected-symbol column holding that symbol's explainable
// recommendation, its independent multi-agent panel, and its reproducible audit
// trail; the world model spans the full width beneath. This page only fetches +
// composes — every panel keeps its own honesty (loading skeletons, inline
// errors, and available:false reasons rendered verbatim). Polls every 30s.

import { useEffect, useState } from "react";
import { ago } from "@/lib/format";
import PagePurpose from "@/components/PagePurpose";
import OpportunityList, { type TopRow } from "@/components/desk/OpportunityList";
import RecommendationCard, { type Recommendation } from "@/components/desk/RecommendationCard";
import AgentPanel from "@/components/desk/AgentPanel";
import AuditTrail from "@/components/desk/AuditTrail";
import WorldModelPanel from "@/components/desk/WorldModelPanel";

interface TopResponse {
  note: string;
  rows: TopRow[];
}

const j = <T,>(p: string): Promise<T> =>
  fetch(p, { credentials: "include", headers: { "X-Signaldeck": "1" } }).then((r) =>
    r.ok ? (r.json() as Promise<T>) : Promise.reject(new Error("API " + r.status)),
  );

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

export default function DeskOverviewPage() {
  const [top, setTop] = useState<TopResponse | null>(null);
  const [topErr, setTopErr] = useState<string | null>(null);
  const [activeRaw, setActiveRaw] = useState<{ symbol: string; market: string } | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  // Effective active symbol, derived in render (no setState-in-effect seeding):
  // an explicit pick wins; otherwise the first opportunity row; otherwise an
  // MSFT fallback once the shortlist has resolved (or failed), so the detail
  // column always loads.
  const rows = top?.rows;
  const firstRow = Array.isArray(rows) && rows.length > 0 ? rows[0] : null;
  const active =
    activeRaw ??
    (firstRow
      ? { symbol: firstRow.symbol, market: firstRow.market }
      : top || topErr
        ? { symbol: "MSFT", market: "stocks" }
        : null);

  // Recommendation is keyed to this string so switching picks shows a loading
  // state without a synchronous setState inside the effect (the app's idiom),
  // and the fetch effect can depend on the primitive key rather than the object.
  const activeKey = active ? `${active.symbol}|${active.market}` : "";
  const [recoState, setRecoState] = useState<{ key: string; d: Recommendation } | null>(null);
  const [recoErrState, setRecoErrState] = useState<{ key: string; msg: string } | null>(null);
  const reco = recoState && recoState.key === activeKey ? recoState.d : null;
  const recoErr = recoErrState && recoErrState.key === activeKey ? recoErrState.msg : null;

  // Opportunity shortlist — fetch + 30s poll.
  useEffect(() => {
    let alive = true;
    const load = () =>
      j<TopResponse>("/api/recommendation/top?limit=12")
        .then((d) => {
          if (alive) {
            setTop(d);
            setTopErr(null);
          }
        })
        .catch((e) => {
          if (alive) setTopErr(msg(e));
        });
    load();
    const id = setInterval(load, 30000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  // Recommendation for the active symbol — fetch + 30s poll; retryTick re-kicks.
  // Depends on the primitive activeKey so it re-runs only when the symbol truly
  // changes, not on every shortlist poll that hands back a fresh object.
  useEffect(() => {
    if (!activeKey) return;
    let alive = true;
    const [symbol, market] = activeKey.split("|");
    const load = () =>
      j<Recommendation>(
        `/api/recommendation?symbol=${encodeURIComponent(symbol)}&market=${market}`,
      )
        .then((d) => {
          if (alive) setRecoState({ key: activeKey, d });
        })
        .catch((e) => {
          if (alive) setRecoErrState({ key: activeKey, msg: msg(e) });
        });
    load();
    const id = setInterval(load, 30000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [activeKey, retryTick]);

  const updatedTs = reco?.available ? reco.asOf : 0;

  return (
    <div className="flex flex-col gap-4">
      {/* header */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-1">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">AI RESEARCH DESK</h1>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          measured research, not advice
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {active ? (
            <span className="chip tnum">
              {active.symbol} <span style={{ color: "var(--faint)" }}>{active.market}</span>
            </span>
          ) : null}
          <span className="chip tnum">{updatedTs ? `updated ${ago(updatedTs)}` : "—"}</span>
        </div>
      </div>

      {/* what this page answers, in plain English */}
      <PagePurpose
        id="desk-overview"
        text="An always-on research desk: a live world model, explainable recommendations, a multi-agent panel, and a reproducible audit trail — measured, not advice."
      />

      {/* opportunity shortlist (top) + the selected-symbol detail column */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.7fr)]">
        <OpportunityList
          rows={top?.rows ?? null}
          note={top?.note ?? ""}
          err={topErr}
          activeKey={activeKey}
          onSelect={(symbol, market) => setActiveRaw({ symbol, market })}
        />

        <div className="flex flex-col gap-4">
          {active ? (
            <RecommendationCard
              symbol={active.symbol}
              market={active.market}
              data={reco}
              err={recoErr}
              retry={() => {
                setRecoErrState(null);
                setRetryTick((t) => t + 1);
              }}
            />
          ) : null}
          {reco?.available ? <AgentPanel agents={reco.agents ?? []} /> : null}
          {reco?.available ? <AuditTrail audit={reco.audit} /> : null}
        </div>
      </div>

      {/* world model — full width */}
      <WorldModelPanel />
    </div>
  );
}
