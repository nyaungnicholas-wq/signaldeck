"use client";

// DELIVERY STATUS — one row of transport chips from /api/notify-status.
// Each configured transport now also states its last SUCCESSFUL delivery
// ("delivered 12m ago") from NotifyTransport.lastOk — visible proof the
// pipe works, not just that an env var is set. Off transports say which
// env enables them; last errors surface (redacted) on hover.

import { ago } from "@/lib/format";
import type { NotifyStatusResponse } from "@/lib/api";

export default function DeliveryStatus({ status }: { status: NotifyStatusResponse }) {
  return (
    <div
      className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[0.75rem]"
      style={{ color: "var(--faint)" }}
    >
      <span>deliveries:</span>
      {status.transports.map((t) => (
        <span
          key={t.name}
          className="chip px-2 py-[2px] text-[0.75rem]"
          style={
            t.configured ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined
          }
          title={
            t.configured
              ? (t.note ??
                (t.lastError
                  ? `last error (redacted): ${t.lastError}${t.lastErrorTs ? ` — ${ago(t.lastErrorTs)}` : ""}`
                  : t.lastOk
                    ? `last successful delivery ${ago(t.lastOk)}`
                    : "configured — no delivery recorded yet"))
              : `off — set ${t.env} in daemon/.env to enable`
          }
        >
          {t.name} {t.configured ? "✓" : "—"}
          {t.configured && t.lastOk ? (
            <span className="tnum" style={{ color: "var(--dim)" }}>
              {" "}
              · delivered {ago(t.lastOk)}
            </span>
          ) : null}
          {t.configured && !t.lastOk && t.lastError ? (
            <span style={{ color: "var(--warn)" }}> · last try failed</span>
          ) : null}
        </span>
      ))}
      {status.transports.some((t) => !t.configured) && (
        <span>
          — off transports: set the env shown on hover in daemon/.env (see .env.example). Email:{" "}
          {status.email}.
        </span>
      )}
    </div>
  );
}
