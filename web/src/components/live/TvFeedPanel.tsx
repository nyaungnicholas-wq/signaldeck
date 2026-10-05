"use client";

// TRADINGVIEW FEED panel — the webhook's own vital signs: how long since the
// last alert, lifetime + 24h counts, the public webhook URL(s) as read-only
// copyable text (never a live link that would leak the endpoint on click), the
// per-streamed-symbol firing grid, and the recent raw-alert feed. A <HelpTip>
// carries the honest caveats: crossing alerts fire sporadically, so a quiet
// feed is not necessarily a fault, and the shared secret is required for an
// alert to be accepted.

import { useEffect, useRef, useState } from "react";
import { ago, fmtPrice, fmtTs } from "@/lib/format";
import EmptyState from "@/components/EmptyState";
import HelpTip from "@/components/HelpTip";
import TvSymbolGrid from "@/components/live/TvSymbolGrid";
import type { TVSignal, TvStatus } from "@/lib/api";

/** Read-only copyable webhook URL — copy button, NOT an anchor (a click must
 *  never navigate to / leak the endpoint). Keyboard-operable, 44px target. */
function CopyField({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  const copy = () => {
    navigator.clipboard
      ?.writeText(value)
      .then(() => {
        setCopied(true);
        if (timer.current) clearTimeout(timer.current);
        timer.current = setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => {});
  };
  return (
    <div className="flex items-center gap-2">
      <code
        className="mono min-w-0 flex-1 rounded-lg border px-2 py-1.5 text-[0.75rem] break-all"
        style={{ borderColor: "var(--border)", background: "var(--panel2)", color: "var(--text)" }}
      >
        {value}
      </code>
      <button
        type="button"
        onClick={copy}
        aria-label={copied ? "webhook URL copied" : "copy webhook URL"}
        className="chip inline-flex min-h-[40px] min-w-[40px] shrink-0 cursor-pointer items-center justify-center transition-colors duration-150 hover:text-[var(--text)]"
        style={copied ? { color: "var(--ok)", borderColor: "var(--ok)" } : undefined}
      >
        {copied ? (
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M3 8.5l3.5 3.5L13 4.5" />
          </svg>
        ) : (
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
            <path d="M10.5 5.5V4a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 4v5A1.5 1.5 0 0 0 4 10.5h1.5" />
          </svg>
        )}
      </button>
    </div>
  );
}

export default function TvFeedPanel({
  status,
  signals,
}: {
  status: TvStatus | null;
  signals: TVSignal[] | null;
}) {
  // status === null means the tv-status fetch has not succeeded — an honest
  // "unavailable", never a confident empty-feed claim (which the null-symbol /
  // no-URL branches below also avoid).
  const headline = status
    ? `${status.lastAt ? `last signal ${ago(status.lastAt)}` : "no signal yet"} · ${status.total} total · ${status.last24h} in 24h`
    : "status unavailable";

  const urls =
    status && status.publicHosts.length > 0
      ? status.publicHosts.map((h) => `https://${h}${status.webhookPath}`)
      : [];

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        <h2 className="text-[0.75rem] font-medium tracking-[0.16em]">TRADINGVIEW FEED</h2>
        <HelpTip label="about the TradingView webhook feed">
          TradingView crossing alerts fire <strong>sporadically</strong> — only when a price
          actually crosses your alert condition. A quiet feed (&ldquo;no recent signal&rdquo;) is
          normal and not necessarily a fault. Every alert must carry the shared secret
          (SIGNALDECK_TV_WEBHOOK_SECRET) or the daemon rejects it, so the webhook URL alone is
          not enough to post a signal.
        </HelpTip>
        <span className="ml-auto tnum text-[0.75rem] font-normal normal-case tracking-normal" style={{ color: "var(--faint)" }}>
          {headline}
        </span>
      </div>

      <div className="flex flex-col gap-4 p-4">
        {/* Webhook URL(s) — read-only, copyable */}
        <div className="flex flex-col gap-2">
          <div className="text-[0.75rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
            WEBHOOK URL
          </div>
          {status === null ? (
            <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
              Webhook status is currently unavailable — the daemon did not answer the last
              tv-status request. This is not a confirmation that no tunnel is configured; the
              panel refreshes on its own once the daemon responds.
            </p>
          ) : urls.length > 0 ? (
            urls.map((u) => <CopyField key={u} value={u} />)
          ) : (
            <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
              No public tunnel is configured, so there is no external webhook URL to show. The
              daemon still accepts alerts on <code className="mono" style={{ color: "var(--dim)" }}>{status.webhookPath}</code>{" "}
              over localhost; expose a tunnel (its host appears in AllowedHosts) to receive
              TradingView alerts from the internet.
            </p>
          )}
        </div>

        {/* Per-symbol firing grid */}
        <div className="flex flex-col gap-2">
          <div className="text-[0.75rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
            STREAMED SYMBOLS
          </div>
          {status === null ? (
            <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              Streamed-symbol status is unavailable while the daemon is unreachable.
            </p>
          ) : (
            <TvSymbolGrid symbols={status.symbols} />
          )}
        </div>

        {/* Recent raw-alert feed */}
        <div className="flex flex-col gap-2">
          <div className="text-[0.75rem] tracking-[0.12em]" style={{ color: "var(--dim)" }}>
            RECENT ALERTS
          </div>
          {signals && signals.length === 0 && (
            <EmptyState
              message="No TradingView alerts received yet"
              detail="Alerts appear here the moment the daemon accepts a webhook. A quiet feed is expected between crossings."
            />
          )}
          {signals && signals.length > 0 && (
            <div className="table-wrap max-h-[22rem] overflow-y-auto rounded-lg border" style={{ borderColor: "var(--border)" }}>
              <table className="w-full text-[0.75rem]">
                <thead className="sticky top-0" style={{ background: "var(--panel2)" }}>
                  <tr style={{ color: "var(--dim)" }}>
                    <th className="px-3 py-2 text-left font-medium">time</th>
                    <th className="px-3 py-2 text-left font-medium">symbol</th>
                    <th className="px-3 py-2 text-left font-medium">action</th>
                    <th className="px-3 py-2 text-right font-medium">price</th>
                    <th className="px-3 py-2 text-left font-medium">message</th>
                  </tr>
                </thead>
                <tbody>
                  {signals.map((s) => (
                    <tr key={s.id} style={{ borderTop: "1px solid var(--border)" }}>
                      <td className="tnum whitespace-nowrap px-3 py-2" style={{ color: "var(--faint)" }} title={fmtTs(s.ts)}>
                        {fmtTs(s.ts)}
                      </td>
                      <td className="mono whitespace-nowrap px-3 py-2 font-bold" style={{ color: "var(--text)" }}>
                        {s.symbol || s.ticker}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2" style={{ color: "var(--dim)" }}>
                        {s.action || "—"}
                      </td>
                      <td className="tnum whitespace-nowrap px-3 py-2 text-right" style={{ color: "var(--dim)" }}>
                        {s.price != null ? fmtPrice(s.price) : "—"}
                      </td>
                      <td className="px-3 py-2" style={{ color: "var(--faint)" }}>
                        {s.message || "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
