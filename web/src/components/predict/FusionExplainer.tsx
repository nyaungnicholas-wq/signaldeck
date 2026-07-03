"use client";

/** How the fused, calibrated probability is built — and why the diagram is the honesty check. */
export default function FusionExplainer() {
  return (
    <section className="panel">
      <div className="panel-h">HOW THE PREDICTION IS BUILT</div>
      <div
        className="space-y-2 px-4 py-3 text-[0.75rem] leading-relaxed"
        style={{ color: "var(--dim)" }}
      >
        <p>
          This fuses the{" "}
          <span style={{ color: "var(--text)" }}>pressure score</span>, the measured{" "}
          <span style={{ color: "var(--text)" }}>expectancy tendency</span>, and the{" "}
          <span style={{ color: "var(--text)" }}>backtested forecast</span> — the last one only when
          it has proven out-of-sample edge — into ONE probability, then{" "}
          <span style={{ color: "var(--text)" }}>calibrates</span> it against what actually happened
          on this data.
        </p>
        <p>
          The reliability diagram is the honesty check: when we say 70%, does it happen ~70% of the
          time? Points on the diagonal mean the forecast is honest; drift above or below shows where
          it is under- or over-confident. Nothing else here shows you this.
        </p>
        <p style={{ color: "var(--faint)" }}>
          Every probability carries its blend depth and resolved-sample size so you can see how much
          to trust it — a confident-looking number with few resolved outcomes is flagged, not hidden.
        </p>
      </div>
    </section>
  );
}
