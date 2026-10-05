"use client";
export type PaperProcess = {
  worker: string;
  verdict: "abstaining" | "active" | "stalled" | "failing" | "unknown";
  note: string;
  longThresh: number;
  windowDays: number;
  lastRun?: { worker: string; startedAt: number; finishedAt: number; status: string; detail: string; revision: string } | null;
  lastRunAgeS?: number;
  lastRunError?: string;
  abstention?: { sinceTs: number; forecasts: number; aboveLong: number; maxCalProb: number; decisions: number; doNothing: number; lastDecisionTs: number } | null;
  abstentionError?: string;
};
function getColor(verdict: PaperProcess['verdict']): string {
  switch (verdict) {
    case 'abstaining':
    case 'active':
      return 'var(--ok)';
    case 'stalled':
    case 'unknown':
      return 'var(--warn)';
    case 'failing':
      return 'var(--ask)';
    default:
      return 'var(--text)';
  }
}
export default function SimulatorStatus({ process }: { process: PaperProcess | null | undefined }) {
  if (!process) {
    return (
      <section className="panel">
        <div className="panel-h">SIMULATOR STATUS</div>
        <div className="px-4 py-3 text-[0.8rem] leading-snug" style={{ color: 'var(--faint)' }}>
          Process status not reported by this daemon build.
        </div>
      </section>
    );
  }
  const verdictColor = getColor(process.verdict);
  let agoText = '';
  if (process.lastRunError) {
    agoText = `Last run: unavailable (${process.lastRunError})`;
  } else if (typeof process.lastRunAgeS === 'number') {
    const secs = process.lastRunAgeS;
    let ago: string;
    if (secs < 90) {
      ago = `${String(Math.floor(secs))}s ago`;
    } else if (secs < 5400) {
      ago = `${String(Math.floor(secs / 60))}m ago`;
    } else {
      ago = `${String(Math.floor(secs / 3600))}h ago`;
    }
    agoText = `Last run: ${ago}`;
    if (process.lastRun) {
      agoText += ` · status ${process.lastRun.status}`;
      agoText += ` · rev ${process.lastRun.revision.slice(0, 10)}`;
    }
  } else {
    agoText = 'Last run: unknown';
  }
  let abstLine = '';
  if (process.abstentionError) {
    abstLine = `Abstention stats unavailable (${process.abstentionError})`;
  } else if (process.abstention) {
    const a = process.abstention;
    abstLine = `${String(a.aboveLong)} of ${String(a.forecasts)} calibrated forecasts ≥ ${String(process.longThresh.toFixed(2))} in the last ${String(process.windowDays)} days (max ${String(a.maxCalProb.toFixed(3))}); ${String(a.doNothing)} of ${String(a.decisions)} EV decisions were DO_NOTHING.`;
  }
  return (
    <section className="panel">
      <div className="panel-h">SIMULATOR STATUS</div>
      <div className="px-4 py-3 text-[0.8rem] leading-snug">
        <span className="chip" style={{ color: verdictColor }}>{process.verdict.toUpperCase()}</span>
        <span style={{ color: 'var(--text)' }}>{process.note}</span>
      </div>
      <div className="px-4 py-3 text-[0.8rem] leading-snug" style={{ color: 'var(--faint)' }}>
        {agoText}
      </div>
      {abstLine && (
        <div className="px-4 py-3 text-[0.8rem] leading-snug" style={{ color: 'var(--faint)' }}>
          {abstLine}
        </div>
      )}
      <div className="px-4 py-3 text-[0.7rem] leading-snug" style={{ color: 'var(--faint)' }}>
        A healthy simulator that is abstaining is not a stalled worker: it checks hourly and trades only when a calibrated forecast clears the entry threshold on a settled daily bar.
      </div>
    </section>
  );
}