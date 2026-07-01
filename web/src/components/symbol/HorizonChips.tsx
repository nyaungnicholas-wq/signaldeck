"use client";

// Small horizon selector (1h/1d/1w). Horizons with no data are disabled.

import { HORIZONS, type Horizon } from "@/lib/api";

export default function HorizonChips({
  value,
  onChange,
  available,
}: {
  value: Horizon;
  onChange: (h: Horizon) => void;
  available?: Horizon[];
}) {
  return (
    <div className="flex items-center gap-1" role="tablist" aria-label="horizon">
      {HORIZONS.map((h) => {
        const enabled = !available || available.includes(h);
        const active = h === value;
        return (
          <button
            key={h}
            type="button"
            role="tab"
            aria-selected={active}
            disabled={!enabled}
            onClick={() => enabled && onChange(h)}
            className="chip transition-colors duration-150"
            style={{
              cursor: enabled ? "pointer" : "not-allowed",
              opacity: enabled ? 1 : 0.4,
              color: active ? "var(--accent)" : "var(--dim)",
              borderColor: active ? "var(--accent)" : "var(--border)",
            }}
          >
            {h}
          </button>
        );
      })}
    </div>
  );
}
