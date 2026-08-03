"use client";

import useLive from "@/hooks/useLive";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import PipelineStatus from "@/components/live/PipelineStatus";
import TvFeedPanel from "@/components/live/TvFeedPanel";
import SystemHealth from "@/components/live/SystemHealth";
import { PageHero, StatTile, Reveal } from "@/components/ui/Kit";

export default function LivePage() {
  const { status, signals, workers, health, stats, error, loading, refresh } = useLive();
  const hasAny = !!(status || signals || workers || health || stats);

  const pipelineActive = status?.secretConfigured;
  const workerCount = workers?.length || 0;
  const signalCount = signals?.length || 0;
  const lastSignal = signals?.[0]?.ts;

  return (
    <div className="page-enter space-y-4">
      <PageHero
        title="Live Pipeline"
        subtitle="Real-time status of the daemon, workers, and TradingView webhook feed."
        live={pipelineActive}
      />

      {error && !hasAny && (
        <ErrorState
          message={error}
          retry={refresh}
          hint="Is the daemon running? Start signaldeckd and this page recovers on its own."
        />
      )}
      {loading && !hasAny && <Skeleton lines={4} label="loading live pipeline status" />}
      {error && hasAny && (
        <div className="px-1 text-[0.75rem]" style={{ color: "var(--bad)" }}>
          connection lost — showing last known data · {error}
        </div>
      )}

      {hasAny && (
        <>
          <Reveal className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <StatTile
              label="Daemon"
              value={status?.secretConfigured ? "ONLINE" : "OFFLINE"}
              sub={status?.lastAt ? new Date(status.lastAt).toLocaleString() : undefined}
              glow={status?.secretConfigured ? "hud" : undefined}
              i={0}
            />
            <StatTile
              label="Workers"
              value={workerCount}
              delta={status?.secretConfigured ? 0 : undefined}
              i={1}
            />
            <StatTile
              label="Signals"
              value={signalCount}
              delta={status?.secretConfigured ? 0 : undefined}
              i={2}
            />
            <StatTile
              label="Last Signal"
              value={lastSignal ? new Date(lastSignal).toLocaleTimeString() : "N/A"}
              sub={lastSignal ? new Date(lastSignal).toLocaleDateString() : undefined}
              i={3}
            />
          </Reveal>

          <div className="grid gap-4 md:grid-cols-2">
            <div className="hud-panel">
              <PipelineStatus status={status} health={health} error={error} loading={loading} />
            </div>
            <div className="panel">
              <TvFeedPanel status={status} signals={signals} />
            </div>
          </div>

          <div className="panel">
            <SystemHealth health={health} workers={workers} stats={stats} />
          </div>
        </>
      )}
    </div>
  );
}
