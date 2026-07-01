"use client";

/** Inline SVG sparkline; colored by first→last direction. */
export default function Spark({
  values,
  width = 120,
  height = 32,
}: {
  values: number[];
  width?: number;
  height?: number;
}) {
  if (!values || values.length < 2) {
    return (
      <svg width={width} height={height} aria-hidden="true">
        <line x1="0" y1={height / 2} x2={width} y2={height / 2} stroke="var(--border)" />
      </svg>
    );
  }
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const pts = values
    .map((v, i) => {
      const x = (i / (values.length - 1)) * (width - 2) + 1;
      const y = height - 3 - ((v - min) / span) * (height - 6);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  const up = values[values.length - 1] >= values[0];
  return (
    <svg width={width} height={height} aria-hidden="true">
      <polyline
        points={pts}
        fill="none"
        stroke={up ? "var(--bid)" : "var(--ask)"}
        strokeWidth="1.5"
      />
    </svg>
  );
}
