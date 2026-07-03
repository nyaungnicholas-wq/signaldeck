"use client";

/** Renders macro.push20Macro — an opaque object synced from the stock-trader
    monitor (trader-hud). We don't control its schema, so render defensively:
    pull out a few known-ish keys (gate, score, top_risk) with semantic color,
    then list the remaining scalar fields as plain chips. */

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Coerce a scalar value to a short display string; null for non-scalars. */
function scalar(v: unknown): string | null {
  if (v === null || v === undefined) return null;
  if (typeof v === "boolean") return v ? "true" : "false";
  if (typeof v === "number") return isFinite(v) ? String(v) : "—";
  if (typeof v === "string") return v;
  return null;
}

/** Color a gate value: OPEN → ok, FROZEN/CLOSED → bad, else accent. */
function gateColor(raw: string): string {
  const g = raw.toUpperCase();
  if (g.includes("OPEN")) return "var(--ok)";
  if (g.includes("FROZEN") || g.includes("CLOSED") || g.includes("HALT")) return "var(--bad)";
  return "var(--accent)";
}

/** Find a value across a few candidate key spellings, case-insensitively. */
function pick(obj: Record<string, unknown>, keys: string[]): unknown {
  const lower = new Map(Object.keys(obj).map((k) => [k.toLowerCase(), k]));
  for (const want of keys) {
    const actual = lower.get(want.toLowerCase());
    if (actual !== undefined) return obj[actual];
  }
  return undefined;
}

const HIGHLIGHT_KEYS = new Set(
  ["gate", "score", "top_risk", "toprisk", "risk"].map((k) => k.toLowerCase()),
);

export default function Push20Macro({ data }: { data: unknown }) {
  if (data === null || data === undefined) {
    return (
      <p className="px-4 py-4 text-[0.76rem]" style={{ color: "var(--faint)" }}>
        PUSH-20 macro not synced (start trader-hud).
      </p>
    );
  }

  if (!isRecord(data)) {
    // Some primitive/array — show it verbatim so nothing is silently dropped.
    return (
      <p className="tnum px-4 py-4 text-[0.78rem]" style={{ color: "var(--dim)" }}>
        {scalar(data) ?? JSON.stringify(data)}
      </p>
    );
  }

  const gate = pick(data, ["gate"]);
  const gateStr = scalar(gate);
  const score = pick(data, ["score"]);
  const scoreStr = scalar(score);
  const topRisk = pick(data, ["top_risk", "topRisk", "risk"]);
  const topRiskStr = scalar(topRisk);

  // Remaining scalar fields not already surfaced as highlights.
  const rest = Object.entries(data)
    .filter(([k]) => !HIGHLIGHT_KEYS.has(k.toLowerCase()))
    .map(([k, v]) => [k, scalar(v)] as const)
    .filter((e): e is readonly [string, string] => e[1] !== null);

  const empty = !gateStr && !scoreStr && !topRiskStr && rest.length === 0;

  if (empty) {
    return (
      <p className="px-4 py-4 text-[0.76rem]" style={{ color: "var(--faint)" }}>
        PUSH-20 macro synced but carried no readable fields.
      </p>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2 px-4 py-4">
      {gateStr && (
        <span
          className="chip"
          style={{ color: gateColor(gateStr), borderColor: gateColor(gateStr) }}
          aria-label={`gate ${gateStr}`}
        >
          gate <span className="tnum font-bold">{gateStr}</span>
        </span>
      )}
      {scoreStr && (
        <span className="chip tnum" aria-label={`score ${scoreStr}`}>
          score <span className="font-bold" style={{ color: "var(--text)" }}>{scoreStr}</span>
        </span>
      )}
      {topRiskStr && (
        <span
          className="chip"
          style={{ color: "var(--warn)", borderColor: "var(--warn)" }}
          aria-label={`top risk ${topRiskStr}`}
        >
          top risk <span className="font-bold">{topRiskStr}</span>
        </span>
      )}
      {rest.map(([k, v]) => (
        <span key={k} className="chip tnum" aria-label={`${k} ${v}`}>
          {k} <span style={{ color: "var(--text)" }}>{v}</span>
        </span>
      ))}
    </div>
  );
}
