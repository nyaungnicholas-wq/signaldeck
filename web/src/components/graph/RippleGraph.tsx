"use client";

// KNOWLEDGE-GRAPH RIPPLE viz — a radial network of ONE symbol's neighborhood,
// pure inline SVG (no chart library, the same dependency-free approach as
// <Sparkline/>). The center node is the queried symbol; each neighbor sits
// evenly around a ring, linked to the center by one line per relationship
// KIND. Line THICKNESS encodes the link weight (0..1) and COLOR encodes the
// kind: amber = price correlation, blue = 13F co-ownership. A neighbor that is
// BOTH correlated and co-owned gets two slightly-offset lines. Hover any node
// or line for its ticker / kind / weight (native SVG <title>).
//
// This graphic is decorative-with-meaning: the adjacency TABLE on the page is
// the accessible source of truth (network graphs read poorly to screen
// readers), so the SVG is role="img" with a summarizing aria-label, and colour
// is never the only carrier of a kind — the table labels it in words.

import type { GraphEdge } from "@/lib/api";

const BLUE = "#60a5fa"; // co-ownership (13F) — reads clearly on OLED-dark, distinct from the amber accent

/** Shared kind → colour/label map, reused by the page legend + adjacency table. */
export const KIND_META: Record<string, { color: string; label: string }> = {
  correlation: { color: "var(--accent)", label: "correlation" },
  coowned: { color: BLUE, label: "co-owned (13F)" },
};

export function kindColor(kind: string): string {
  return KIND_META[kind]?.color ?? "var(--dim)";
}
export function kindLabel(kind: string): string {
  return KIND_META[kind]?.label ?? kind;
}

/** The neighbor endpoint of an edge incident to `center` (case-insensitive). */
export function otherEndpoint(center: string, e: GraphEdge): string {
  return e.A.toUpperCase() === center.toUpperCase() ? e.B : e.A;
}

// Geometry in viewBox units. Capped by max-w on the page so it renders ~1:1 on
// desktop (labels ≈ 12px); it scales down responsively on narrow screens, where
// the adjacency table is the readable fallback.
const W = 680;
const H = 460;
const CX = W / 2;
const CY = H / 2;
const R = Math.min(W, H) / 2 - 92; // neighbor ring radius (room left for radial labels)
const CENTER_R = 24;
const NODE_R = 7;
const LABEL_GAP = 16;
const OFFSET = 4; // perpendicular gap between a neighbor's two kind-lines

export default function RippleGraph({
  center,
  edges,
}: {
  center: string;
  edges: GraphEdge[];
}) {
  // Group edges by neighbor so a symbol linked by BOTH kinds becomes one node
  // with two lines. Strongest links are laid out first (top, going clockwise)
  // for a readable ring.
  const byNeighbor = new Map<string, GraphEdge[]>();
  for (const e of edges) {
    const nb = otherEndpoint(center, e);
    const arr = byNeighbor.get(nb);
    if (arr) arr.push(e);
    else byNeighbor.set(nb, [e]);
  }
  const groups = [...byNeighbor.entries()]
    .map(([neighbor, es]) => ({
      neighbor,
      // stable kind order so the two offset lines never swap between renders
      edges: [...es].sort((a, b) => a.Kind.localeCompare(b.Kind)),
      maxWeight: Math.max(...es.map((e) => e.Weight)),
    }))
    .sort((a, b) => b.maxWeight - a.maxWeight);

  const n = groups.length;
  const laid = groups.map((g, i) => {
    const theta = -Math.PI / 2 + (i / n) * Math.PI * 2; // start at top, clockwise
    const cos = Math.cos(theta);
    const sin = Math.sin(theta);
    return {
      neighbor: g.neighbor,
      edges: g.edges,
      nx: CX + R * cos,
      ny: CY + R * sin,
      lx: CX + (R + LABEL_GAP) * cos,
      ly: CY + (R + LABEL_GAP) * sin,
      anchor: cos > 0.15 ? "start" : cos < -0.15 ? "end" : "middle",
      px: -sin, // perpendicular unit vector — offsets the dual kind-lines
      py: cos,
    };
  });

  const corr = edges.filter((e) => e.Kind === "correlation").length;
  const co = edges.filter((e) => e.Kind === "coowned").length;
  const ariaLabel =
    `Radial ripple graph centered on ${center}, linking ${n} symbol${n === 1 ? "" : "s"}: ` +
    `${corr} price-correlation and ${co} co-ownership link${corr + co === 1 ? "" : "s"}. ` +
    `Thicker lines are stronger links; amber lines are correlation, blue lines are 13F co-ownership. ` +
    `Exact neighbor, kind and weight values are in the adjacency table below.`;

  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      width="100%"
      role="img"
      aria-label={ariaLabel}
      style={{ height: "auto", display: "block", maxHeight: H }}
    >
      {/* faint guide ring the neighbors sit on */}
      <circle
        cx={CX}
        cy={CY}
        r={R}
        fill="none"
        stroke="var(--border)"
        strokeOpacity={0.6}
        strokeDasharray="3 6"
      />

      {/* edges first, so the nodes render on top of the line ends */}
      {laid.flatMap((g) => {
        const m = g.edges.length;
        return g.edges.map((e, k) => {
          const off = (k - (m - 1) / 2) * (OFFSET * 2); // 0 for a lone line; ±OFFSET for a pair
          const x1 = CX + g.px * off;
          const y1 = CY + g.py * off;
          const x2 = g.nx + g.px * off;
          const y2 = g.ny + g.py * off;
          return (
            <line
              key={`${g.neighbor}-${e.Kind}`}
              x1={x1}
              y1={y1}
              x2={x2}
              y2={y2}
              stroke={kindColor(e.Kind)}
              strokeWidth={1 + e.Weight * 5}
              strokeOpacity={0.6}
              strokeLinecap="round"
            >
              <title>{`${g.neighbor} · ${kindLabel(e.Kind)} · weight ${e.Weight.toFixed(2)}`}</title>
            </line>
          );
        });
      })}

      {/* neighbor nodes + radial labels */}
      {laid.map((g) => (
        <g key={g.neighbor}>
          <title>
            {`${g.neighbor}: ` +
              g.edges.map((e) => `${kindLabel(e.Kind)} ${e.Weight.toFixed(2)}`).join(", ")}
          </title>
          <circle
            cx={g.nx}
            cy={g.ny}
            r={NODE_R}
            fill="var(--panel3)"
            stroke="var(--border-strong)"
            strokeWidth={1.5}
          />
          <text
            x={g.lx}
            y={g.ly}
            textAnchor={g.anchor as "start" | "middle" | "end"}
            dominantBaseline="middle"
            className="mono"
            fontSize={12}
            fill="var(--text)"
          >
            {g.neighbor}
          </text>
        </g>
      ))}

      {/* center node — the queried symbol */}
      <g>
        <title>{`${center} — center symbol`}</title>
        <circle
          cx={CX}
          cy={CY}
          r={CENTER_R}
          fill="var(--accent-dim)"
          stroke="var(--accent)"
          strokeWidth={2}
        />
        <text
          x={CX}
          y={CY}
          textAnchor="middle"
          dominantBaseline="central"
          className="mono"
          fontSize={13}
          fontWeight={700}
          fill="var(--accent)"
        >
          {center}
        </text>
      </g>
    </svg>
  );
}
