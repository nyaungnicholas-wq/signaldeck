"use client";

import { useEffect, useState } from "react";
import PagePurpose from "@/components/PagePurpose";
import OpportunityList, { type TopRow } from "@/components/desk/OpportunityList";
import RecommendationCard, { type Recommendation } from "@/components/desk/RecommendationCard";
import AgentPanel from "@/components/desk/AgentPanel";
import AuditTrail from "@/components/desk/AuditTrail";
import WorldModelPanel from "@/components/desk/WorldModelPanel";
import { PageHero, StatTile } from "@/components/ui/Kit";

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

  const rows = top?.rows;
  const firstRow = Array.isArray(rows) && rows.length > 0 ? rows[0] : null;
  // No hardcoded fallback symbol. `topErr` — the opportunity feed FAILING — used
  // to select MSFT, so an outage produced a full RecommendationCard, AgentPanel
  // and AuditTrail for a symbol the user never picked and that was in no list,
  // on a page whose subtitle promises "measured, not advice". An empty selection
  // is the honest state; the panels below already handle it.
  const active =
    activeRaw ?? (firstRow ? { symbol: firstRow.symbol, market: firstRow.market } : null);

  const activeKey = active ? `${active.symbol}|${active.market}` : "";
  const [recoState, setRecoState] = useState<{ key: string; d: Recommendation } | null>(null);
  const [recoErrState, setRecoErrState] = useState<{ key: string; msg: string } | null>(null);
  const reco = recoState && recoState.key === activeKey ? recoState.d : null;
  const recoErr = recoErrState && recoErrState.key === activeKey ? recoErrState.msg : null;

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
  const topRows = top?.rows || [];
  const totalOpportunities = topRows.length;
  const bestOpportunity = topRows[0];
  const worstOpportunity = topRows[totalOpportunities - 1];
  const latestUpdate = updatedTs ? new Date(updatedTs).toLocaleString() : "—";

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="AI RESEARCH DESK"
        subtitle="An always-on research desk: live world model, explainable recommendations, multi-agent panel, and reproducible audit trail — measured, not advice."
        live={!!updatedTs}
      />

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile
          label="Total Opportunities"
          value={totalOpportunities}
          glow="hud"
          i={0}
        />
        <StatTile
          label="Best Opportunity"
          value={bestOpportunity?.symbol || "—"}
          sub={bestOpportunity?.score ? `Score: ${bestOpportunity.score.toFixed(2)}` : undefined}
          glow="up"
          i={1}
        />
        <StatTile
          label="Worst Opportunity"
          value={worstOpportunity?.symbol || "—"}
          sub={worstOpportunity?.score ? `Score: ${worstOpportunity.score.toFixed(2)}` : undefined}
          glow="down"
          i={2}
        />
        <StatTile
          label="Last Updated"
          value={latestUpdate}
          glow="hud"
          i={3}
        />
      </div>

      <PagePurpose
        id="desk-overview"
        text="An always-on research desk: a live world model, explainable recommendations, a multi-agent panel, and a reproducible audit trail — measured, not advice."
      />

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.7fr)]">
        <div className="hud-panel">
          <OpportunityList
            rows={top?.rows ?? null}
            note={top?.note ?? ""}
            err={topErr}
            activeKey={activeKey}
            onSelect={(symbol, market) => setActiveRaw({ symbol, market })}
          />
        </div>

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

      <WorldModelPanel />
    </div>
  );
}
