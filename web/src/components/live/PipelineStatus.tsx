"use client";

// PIPELINE STATUS strip — the one-glance answer to "is the whole pipeline
// live?". A big overall verdict word (LIVE / DEGRADED) plus four status chips,
// each a coloured dot PAIRED WITH A WORD (never colour alone) + an aria-label:
// DAEMON, TUNNEL, WEBHOOK SECRET, LAST SIGNAL. Honesty: an unknown/unconfigured
// state renders grey with the literal word, never green.

import { ago } from "@/lib/format";
import type { Health } from "@/hooks/useLive";
import type { TvStatus } from "@/lib/api";

type Tone = "ok" | "bad" | "warn" | "info" | "unknown";

function toneColor(t: Tone): string {
  if (t === "ok") return "var(--ok)";
  if (t === "bad") return "var(--bad)";
  if (t === "warn") return "var(--warn)";
  if (t === "info") return "var(--accent)";
  return "var(--faint)";
}

function StatusChip({
  label,
  tone,
  state,
  ariaLabel,
}: {
  label: string;
  tone: Tone;
  state: string;
  ariaLabel: string;
}) {
  const color = toneColor(tone);
  return (
    <div className="panel flex flex-col gap-1.5 p-3">
      <div className="flex items-center gap-2">
        <span
          role="img"
          aria-label={ariaLabel}
          className="inline-block h-2.5 w-2.5 shrink-0 rounded-full"
          style={{
            background: color,
            boxShadow: tone === "ok" ? "0 0 8px rgba(52,211,153,.55)" : undefined,
          }}
        />
        <span className="text-[0.75rem] tracking-[0.14em]" style={{ color: "var(--dim)" }}>
          {label}
        </span>
      </div>
      <span className="tnum text-sm font-bold" style={{ color }}>
        {state}
      </span>
    </div>
  );
}

export default function PipelineStatus({
  status,
  health,
  error,
  loading,
}: {
  status: TvStatus | null;
  health: Health | null;
  error: string | null;
  loading: boolean;
}) {
  // Daemon: no health payload at all ⇒ offline; last-good but the latest poll
  // errored ⇒ reconnecting (honest amber); a clean payload ⇒ live.
  const daemonLive = health !== null && !error;
  const daemonTone: Tone = health === null ? "bad" : error ? "warn" : "ok";
  const daemonState = health === null ? "offline" : error ? "reconnecting" : "live";

  const hasPublic = (status?.publicHosts.length ?? 0) > 0;

  // Tunnel: not configured (no public host) is a valid, non-error state — grey,
  // never green. Configured-but-unprobed (null) is "unknown", not "reachable".
  let tunnelTone: Tone = "unknown";
  let tunnelState = "—";
  let tunnelAria = "tunnel status unknown";
  if (status) {
    if (!hasPublic) {
      tunnelTone = "unknown";
      tunnelState = "not configured";
      tunnelAria = "public tunnel not configured";
    } else if (status.tunnelReachable === true) {
      tunnelTone = "ok";
      tunnelState = "reachable";
      tunnelAria = "public tunnel reachable";
    } else if (status.tunnelReachable === false) {
      tunnelTone = "bad";
      tunnelState = "unreachable";
      tunnelAria = "public tunnel unreachable";
    } else {
      tunnelTone = "unknown";
      tunnelState = "unknown";
      tunnelAria = "public tunnel configured but not yet probed";
    }
  }

  const secretTone: Tone = !status ? "unknown" : status.secretConfigured ? "ok" : "bad";
  const secretState = !status ? "—" : status.secretConfigured ? "set" : "missing";
  const secretAria = !status
    ? "webhook secret status unknown"
    : status.secretConfigured
      ? "webhook secret configured"
      : "webhook secret not configured";

  let signalTone: Tone = "unknown";
  let signalState = "—";
  let signalAria = "last signal time unknown";
  if (status) {
    if (status.lastAt) {
      signalTone = "info";
      signalState = ago(status.lastAt);
      signalAria = `last signal ${ago(status.lastAt)}`;
    } else {
      signalTone = "unknown";
      signalState = "none yet";
      signalAria = "no TradingView signal received yet";
    }
  }

  // Overall verdict — committed once the first poll pass has completed. A source
  // still null after that pass is treated as DOWN (its chip already says so),
  // never left gating the verdict on "…" forever while its sibling chip
  // contradicts it.
  const ready = !loading;
  const isLive = daemonLive && (!hasPublic || status?.tunnelReachable === true) && !!status?.secretConfigured;
  const verdict = !ready ? "…" : isLive ? "LIVE" : "DEGRADED";
  const verdictColor = !ready ? "var(--faint)" : isLive ? "var(--ok)" : "var(--warn)";

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline gap-3">
        <h2 className="text-sm font-bold tracking-[0.16em]">PIPELINE</h2>
        <span
          className="text-2xl font-black tracking-[0.1em]"
          style={{ color: verdictColor }}
          aria-label={`pipeline verdict: ${ready ? verdict : "checking"}`}
        >
          {verdict}
        </span>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          {ready
            ? isLive
              ? "daemon up, webhook secured, tunnel reachable"
              : "one or more pipeline parts are not confirmed live — see chips"
            : "checking…"}
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatusChip label="DAEMON" tone={daemonTone} state={daemonState} ariaLabel={`daemon ${daemonState}`} />
        <StatusChip label="TUNNEL" tone={tunnelTone} state={tunnelState} ariaLabel={tunnelAria} />
        <StatusChip label="WEBHOOK SECRET" tone={secretTone} state={secretState} ariaLabel={secretAria} />
        <StatusChip label="LAST SIGNAL" tone={signalTone} state={signalState} ariaLabel={signalAria} />
      </div>
    </section>
  );
}
