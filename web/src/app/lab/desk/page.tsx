"use client";

import { useEffect, useState } from "react";
import PagePurpose from "@/components/PagePurpose";
import OpportunityList, { type TopRow } from "@/components/desk/OpportunityList";
import RecommendationCard, { type Recommendation } from "@/components/desk/RecommendationCard";
import AgentPanel from "@/components/desk/AgentPanel";
import AuditTrail from "@/components/desk/AuditTrail";
import WorldModelPanel from "@/components/desk/WorldModelPanel";
import { PageHero, StatTile } from "@/components/ui/Kit";
import { fmtTs } from "@/lib/format";

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
  // shownOpportunities is the PAGE SIZE, not a universe count: the fetch above
  // hardcodes limit=12 and the response carries no total, so this can never
  // exceed 12. Named and labelled for what it is.
  const shownOpportunities = topRows.length;
  const bestOpportunity = topRows[0];
  // /api/recommendation/top returns best-score-first over the full set, so the
  // last row of a top-12 is the 12th BEST, not the worst in the universe.
  // Labelling it "Worst" and glowing it red inverted its meaning in a trading UI.
  const lowestShown = topRows[shownOpportunities - 1];
  // reco.asOf is unix SECONDS; RecommendationCard on this same page already
  // renders it with ago(). A bare new Date() read it as ms and showed 1970.
  const latestUpdate = updatedTs ? fmtTs(updatedTs) : "—";

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="AI RESEARCH DESK"
        subtitle="An always-on research desk: live world model, explainable recommendations, multi-agent panel, and reproducible audit trail — measured, not advice."
        live={!!updatedTs}
      />

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile
          label="Opportunities Shown"
          value={shownOpportunities}
          sub="top 12 by score"
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
          label="Lowest of Top 12"
          value={lowestShown?.symbol || "—"}
          sub={lowestShown?.score ? `Score: ${lowestShown.score.toFixed(2)}` : undefined}
          glow="hud"
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
