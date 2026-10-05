"use client";

import { useEffect, useMemo, useRef, useState } from "react";

// A decorative, self-drawing line behind the hero. It is generated from a
// seeded PRNG and has no axes, labels or numbers on purpose: it must never be
// mistaken for a real series on a site whose whole point is honest data.

function mulberry32(a: number) {
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function smooth(pts: [number, number][]): string {
  let d = `M${pts[0][0]},${pts[0][1]}`;
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[Math.max(0, i - 1)], p1 = pts[i], p2 = pts[i + 1], p3 = pts[Math.min(pts.length - 1, i + 2)];
    const c1x = p1[0] + (p2[0] - p0[0]) / 6, c1y = p1[1] + (p2[1] - p0[1]) / 6;
    const c2x = p2[0] - (p3[0] - p1[0]) / 6, c2y = p2[1] - (p3[1] - p1[1]) / 6;
    d += ` C${c1x.toFixed(1)},${c1y.toFixed(1)} ${c2x.toFixed(1)},${c2y.toFixed(1)} ${p2[0].toFixed(1)},${p2[1].toFixed(1)}`;
  }
  return d;
}

function series(seed: number) {
  const rnd = mulberry32(seed);
  const n = 90;
  const ys: number[] = [];
  let y = 0.5;
  for (let i = 0; i < n; i++) {
    y += (0.5 - y) * 0.08 + (rnd() - 0.5) * 0.09;
    y = Math.min(0.85, Math.max(0.15, y));
    ys.push(y);
  }
  const pts: [number, number][] = ys.map((v, i) => [(i / (n - 1)) * 1000, 300 - v * 300]);
  const ma = ys.map((_, i) => {
    const w = ys.slice(Math.max(0, i - 6), i + 1);
    return w.reduce((a, b) => a + b, 0) / w.length;
  });
  const up: [number, number][] = ma.map((v, i) => [(i / (n - 1)) * 1000, 300 - (v + 0.06) * 300]);
  const dn: [number, number][] = ma.map((v, i) => [(i / (n - 1)) * 1000, 300 - (v - 0.06) * 300]);
  const band = `${smooth(up)} L${dn[dn.length - 1][0]},${dn[dn.length - 1][1]} ${smooth([...dn].reverse()).slice(1)} Z`;
  return { line: smooth(pts), band };
}

const ease = (t: number) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);

export default function DrawnChart({ seed = 7, className = "" }: { seed?: number; className?: string }) {
  const [s, setS] = useState(seed);
  const { line, band } = useMemo(() => series(s), [s]);
  const svgRef = useRef<SVGSVGElement | null>(null);
  const lineRef = useRef<SVGPathElement | null>(null);
  const headRef = useRef<SVGCircleElement | null>(null);

  useEffect(() => {
    const svg = svgRef.current, path = lineRef.current, head = headRef.current;
    if (!svg || !path || !head) return;
    const total = path.getTotalLength();
    const place = (p: number) => {
      path.style.strokeDashoffset = String(1 - p);
      const pt = path.getPointAtLength(p * total);
      head.setAttribute("cx", String(pt.x));
      head.setAttribute("cy", String(pt.y));
    };
    path.style.strokeDasharray = "1";
    path.style.opacity = "1";
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      place(1);
      return;
    }
    let raf = 0, timer = 0, visible = true, t0 = 0, paused = 0;
    const DRAW = 3200;
    const frame = (now: number) => {
      if (!t0) t0 = now;
      const p = Math.min(1, (now - t0) / DRAW);
      place(ease(p));
      if (p < 1) raf = requestAnimationFrame(frame);
      else
        timer = window.setTimeout(() => {
          path.style.transition = "opacity 600ms";
          path.style.opacity = "0";
          timer = window.setTimeout(() => {
            path.style.transition = "";
            setS((x) => x + 1);
          }, 650);
        }, 2200);
    };
    const run = () => {
      if (raf || !visible || document.hidden) return;
      if (paused) t0 += performance.now() - paused;
      paused = 0;
      raf = requestAnimationFrame(frame);
    };
    const halt = () => {
      if (raf) cancelAnimationFrame(raf);
      raf = 0;
      paused = performance.now();
    };
    place(0);
    const io = new IntersectionObserver(([e]) => {
      visible = e.isIntersecting;
      if (visible) run();
      else halt();
    });
    io.observe(svg);
    const onVis = () => (document.hidden ? halt() : run());
    document.addEventListener("visibilitychange", onVis);
    return () => {
      halt();
      window.clearTimeout(timer);
      io.disconnect();
      document.removeEventListener("visibilitychange", onVis);
    };
  }, [line]);

  return (
    <svg ref={svgRef} className={className} viewBox="0 0 1000 300" preserveAspectRatio="none" aria-hidden="true">
      <defs>
        <linearGradient id="dc-stroke" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0" stopColor="#fbbf24" />
          <stop offset="1" stopColor="#38bdf8" />
        </linearGradient>
        <linearGradient id="dc-band" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#38bdf8" stopOpacity="0.18" />
          <stop offset="1" stopColor="#38bdf8" stopOpacity="0" />
        </linearGradient>
        <filter id="dc-glow" x="-10%" y="-10%" width="120%" height="120%">
          <feGaussianBlur stdDeviation="4" result="b" />
          <feMerge>
            <feMergeNode in="b" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>
      <path key={`b${s}`} d={band} fill="url(#dc-band)" className="dc-band" />
      <path
        ref={lineRef}
        d={line}
        fill="none"
        stroke="url(#dc-stroke)"
        strokeWidth={2.2}
        filter="url(#dc-glow)"
        className="dc-line"
        pathLength={1}
        vectorEffect="non-scaling-stroke"
      />
      <circle ref={headRef} r={4} fill="#fbbf24" className="dc-head" />
    </svg>
  );
}
