"use client";

// SIGNALS → UNUSUAL — fleet-wide unusual-activity feed (Stage 2 hub
// consolidation; Stage 5 adds the kind filters). This surface previously
// existed only as a home-page panel; here it gets a full sub-tab. All honesty
// framing (descriptive z-scores vs each symbol's own baseline, PROXY chips,
// "not predictions" note) lives in UnusualActivityPanel itself and renders
// unchanged — the kind filter is passed straight to the API (server-side),
// never faked by hiding rows.

import { useState } from "react";
import UnusualActivityPanel from "@/components/UnusualActivityPanel";
import type { AnomalyRow } from "@/lib/api";
import PagePurpose from "@/components/PagePurpose";

type KindFilter = AnomalyRow["kind"] | undefined;

const KIND_CHIPS: { kind: KindFilter; label: string; title: string }[] = [
  { kind: undefined, label: "all", title: "every anomaly kind" },
  { kind: "anomaly_imbalance", label: "imbalance", title: "trade imbalance vs own baseline (stocks = volume-side proxy, labeled)" },
  { kind: "anomaly_vol", label: "volatility", title: "true-range / volatility spikes vs own baseline" },
  { kind: "anomaly_volume", label: "volume", title: "volume spikes vs own baseline" },
];

export default function UnusualPage() {
  const [kind, setKind] = useState<KindFilter>(undefined);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 px-1">
        <h1 className="text-sm font-bold tracking-[0.18em]">UNUSUAL ACTIVITY</h1>
        <span className="chip">descriptive anomaly layer · z-scores vs own baseline · not predictions</span>
        <span className="ml-auto flex items-center gap-1" role="tablist" aria-label="anomaly kind filter">
          {KIND_CHIPS.map((c) => {
            const active = kind === c.kind;
            return (
              <button
                key={c.label}
                type="button"
                role="tab"
                aria-selected={active}
                title={c.title}
                onClick={() => setKind(c.kind)}
                className="chip min-h-[40px] cursor-pointer px-3 transition-colors duration-150"
                style={{
                  color: active ? "var(--accent)" : "var(--dim)",
                  borderColor: active ? "var(--accent)" : "var(--border)",
                }}
              >
                {c.label}
              </button>
            );
          })}
        </span>
      </div>

      {/* STAGE 3: what this page answers, in plain English */}
      <PagePurpose
        id="signals-unusual"
        text="Which symbols are behaving unusually versus their own normal? Descriptive flags (volume, volatility, imbalance) — observations, never predictions."
      />
      <UnusualActivityPanel kind={kind} limit={50} />
    </div>
  );
}
