"use client";

// /live — the LIVE tab. One page, no sub-tabs: is the whole pipeline live?
// Daemon + workers + the TradingView webhook and its public tunnel, at a
// glance. Fast-tier polling via useLive(); last-good is kept on error so a
// blip shows a "connection lost" strip, never a blank page.

import useLive from "@/hooks/useLive";
import PagePurpose from "@/components/PagePurpose";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import PipelineStatus from "@/components/live/PipelineStatus";
import TvFeedPanel from "@/components/live/TvFeedPanel";
import SystemHealth from "@/components/live/SystemHealth";

export default function LivePage() {
  const { status, signals, workers, health, stats, error, loading, refresh } = useLive();
  const hasAny = !!(status || signals || workers || health || stats);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-sm font-bold tracking-[0.16em]">LIVE</h1>
      </div>

      <PagePurpose
        id="live"
        text="Is the whole pipeline live? Daemon + workers + the TradingView webhook and its public tunnel, in one place."
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
          <PipelineStatus status={status} health={health} error={error} loading={loading} />
          <TvFeedPanel status={status} signals={signals} />
          <SystemHealth health={health} workers={workers} stats={stats} />
        </>
      )}
    </div>
  );
}
