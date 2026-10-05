"use client";
import { useEffect, useRef, useState, useId, useMemo } from "react";

// Reveal fades its children in when they first scroll into view.
//
// TWO RULES HERE ARE LOAD-BEARING, and breaking either one hid real content:
//
//  1. children render ALWAYS, never `{visible && children}`. Gating the render
//     on visibility made the wrapper zero-height until it revealed — and a
//     zero-AREA target can never report an intersectionRatio above 0, so with
//     the old `threshold: 0.08` the observer never fired, the box never gained
//     height, and the panel stayed blank FOREVER. Measured on
//     /signals/report/stocks/CEG: four panels (incl. "Why this signal fired")
//     were still height:0/opacity:0 after scrolling to the end of the page.
//     Always-rendering also puts the content in the server HTML, so Ctrl+F,
//     screen readers and no-JS clients can reach it.
//  2. threshold stays 0. Any intersection at all is the trigger; nothing here
//     needs a fraction of the box on screen, and a fraction is exactly what a
//     short row cannot supply.
//
// prefers-reduced-motion starts visible: the fade is decoration, and a reader
// who asked for less motion should not have to wait on an animation to read.
export function Reveal({ children, className }: { children: React.ReactNode; className?: string }) {
  const [visible, setVisible] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (visible) return;
    const el = ref.current;
    const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
    if (!el || typeof IntersectionObserver === 'undefined' || reducedMotion) return setVisible(true);
    const obs = new IntersectionObserver(([e]) => { if (e.isIntersecting) { setVisible(true); obs.disconnect(); } }, { threshold: 0 });
    obs.observe(el);
    return () => obs.disconnect();
  }, [visible]);

  return <div ref={ref} className={className} style={{ opacity: visible ? 1 : 0, transition: 'opacity 0.3s' }}>{children}</div>;
}

export function AnimatedNumber({ value, decimals = 0, prefix = "", suffix = "", className }: { value: number; decimals?: number; prefix?: string; suffix?: string; className?: string }) {
  const [display, setDisplay] = useState(value);
  const [flash, setFlash] = useState('');
  const prev = useRef(value);
  const raf = useRef(0);

  useEffect(() => {
    if (isNaN(value) || value == null) return;
    const start = prev.current;
    const diff = value - start;
    if (diff === 0) return;
    const dir = diff > 0 ? 'tick-up' : 'tick-down';
    setFlash(dir);
    const timeout = setTimeout(() => setFlash(''), 900);
    const startTime = performance.now();
    const duration = 650;
    const ease = (t: number) => 1 - Math.pow(1 - t, 3);
    const tick = (now: number) => {
      const progress = Math.min((now - startTime) / duration, 1);
      setDisplay(start + diff * ease(progress));
      if (progress < 1) raf.current = requestAnimationFrame(tick);
    };
    raf.current = requestAnimationFrame(tick);
    prev.current = value;
    return () => { cancelAnimationFrame(raf.current); clearTimeout(timeout); };
  }, [value]);

  if (isNaN(value) || value == null) return <span className={`tnum ${className ?? ''}`}>{"—"}</span>;
  const formatted = display.toLocaleString(undefined, { minimumFractionDigits: decimals, maximumFractionDigits: decimals });
  return <span className={`tnum ${flash} ${className ?? ''}`}>{prefix}{formatted}{suffix}</span>;
}

export function Spark({ data, width = 120, height = 36, color = "var(--hud)", fill = true, className }: { data: number[]; width?: number; height?: number; color?: string; fill?: boolean; className?: string }) {
  const id = useId().replace(/:/g, '');
  const path = useMemo(() => {
    if (data.length < 2) return "";
    const min = Math.min(...data), max = Math.max(...data);
    const range = max - min || 1;
    const step = width / (data.length - 1);
    return data.map((v, i) => `${i * step},${height - ((v - min) / range) * (height * 0.8) - height * 0.1}`).join(' ');
  }, [data, width, height]);

  return (
    <svg viewBox={`0 0 ${width} ${height}`} className={className} style={{ width, height }}>
      {fill && data.length >= 2 && (
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.25" />
            <stop offset="100%" stopColor="transparent" stopOpacity="0" />
          </linearGradient>
        </defs>
      )}
      {data.length >= 2 ? (
        <>
          {fill && <polygon points={`0,${height} ${path} ${width},${height}`} fill={`url(#${id})`} />}
          <polyline points={path} fill="none" stroke={color} strokeWidth="1.5" vectorEffect="non-scaling-stroke" className="draw-path" />
        </>
      ) : (
        <line x1="0" y1={height / 2} x2={width} y2={height / 2} stroke="var(--dim)" strokeWidth="1" />
      )}
    </svg>
  );
}

export function Gauge({ value, min = 0, max = 100, label, color = "var(--accent)", size = 120 }: { value: number; min?: number; max?: number; label?: string; color?: string; size?: number }) {
  const [mounted, setMounted] = useState(false);
  const r = 40, cx = 50, cy = 50;
  const circumference = 2 * Math.PI * r;
  const arcLength = circumference * (240 / 360);
  const progress = Math.max(0, Math.min(1, (value - min) / (max - min)));
  const offset = arcLength * (1 - progress);
  const decimals = (max - min) < 10 ? 1 : 0;

  useEffect(() => { const t = setTimeout(() => setMounted(true), 50); return () => clearTimeout(t); }, []);

  return (
    <div className="relative flex flex-col items-center" style={{ width: size, height: size }}>
      <svg viewBox="0 0 100 100" style={{ width: size, height: size }}>
        <circle cx={cx} cy={cy} r={r} fill="none" stroke="rgba(255,255,255,0.07)" strokeWidth="10" strokeLinecap="round" strokeDasharray={`${arcLength} ${circumference}`} strokeDashoffset={0} transform={`rotate(150 ${cx} ${cy})`} />
        <circle cx={cx} cy={cy} r={r} fill="none" stroke={color} strokeWidth="10" strokeLinecap="round" strokeDasharray={`${arcLength} ${circumference}`} strokeDashoffset={mounted ? offset : arcLength} style={{ transition: 'stroke-dashoffset 900ms cubic-bezier(0.22,1,0.36,1)' }} transform={`rotate(150 ${cx} ${cy})`} />
      </svg>
      <div className="absolute flex flex-col items-center justify-center" style={{ top: '50%', left: '50%', transform: 'translate(-50%, -50%)' }}>
        <AnimatedNumber value={value} decimals={decimals} className="num-hero text-xl" />
        {label && <span className="text-[0.75rem] text-[color:var(--dim)] uppercase tracking-wider">{label}</span>}
      </div>
    </div>
  );
}

// unit is a PROP because this badge used to hardcode '%' onto whatever number
// it was handed, and two callers were not handing it a percentage move: the
// macro tiles pass (percentile - 50), which is a distance from the midpoint in
// POINTS, and the news tile passed a raw headline tally. A '%' on either is a
// unit error rendered in the house style, next to a real percentage, with an
// up-arrow that reads as a change over time.
//
// title carries the sentence that makes a non-obvious unit legible on hover,
// since "pp" alone does not say pp-from-what.
export function DeltaBadge({ value, decimals = 2, unit = "%", title, className }: { value: number; decimals?: number; unit?: string; title?: string; className?: string }) {
  if (value === 0) return <span title={title} className={`rounded-full border px-2 py-0.5 text-[0.75rem] tnum ${className ?? ''}`} style={{ color: 'var(--dim)', borderColor: 'var(--dim)' }}><svg width="8" height="8" viewBox="0 0 8 8"><line x1="2" y1="4" x2="6" y2="4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/></svg></span>;
  const positive = value > 0;
  const colorVar = positive ? '--bid' : '--ask';
  return <span title={title} className={`rounded-full border px-2 py-0.5 text-[0.75rem] tnum ${className ?? ''}`} style={{ color: `var(${colorVar})`, borderColor: `var(${colorVar})`, backgroundColor: `color-mix(in srgb, var(${colorVar}) 10%, transparent)` }}>
    {positive ? <svg width="8" height="8" viewBox="0 0 8 8" className="inline mr-0.5"><polygon points="4,1 7,6 1,6" fill="currentColor"/></svg> : <svg width="8" height="8" viewBox="0 0 8 8" className="inline mr-0.5"><polygon points="4,7 7,2 1,2" fill="currentColor"/></svg>}
    {Math.abs(value).toFixed(decimals)}{unit}
  </span>;
}

// StatTile accepts null/undefined and renders an em-dash for it, the same
// no-data contract <Gauge> already honours. WHY it has to live here: the tile
// only took `number | string`, so every call site with a nullable metric wrote
// `?? 0` to satisfy the type — and a 0 in a hero tile is not "no data", it is a
// measurement. That produced "Days to cover 0.0 — FINRA short interest" for
// symbols with no short-interest row at all, and "Best Quintile 0.00%" on the
// honesty page, whose whole purpose is not inventing forward returns. Widening
// the type is what lets those call sites stop lying; a `?? 0` reaching this
// component is now a bug with a fix rather than the only way to compile.
export function StatTile({ label, value, decimals = 0, prefix = "", suffix = "", sub, delta, deltaUnit, deltaTitle, spark, glow, i = 0 }: { label: string; value: number | string | null | undefined; decimals?: number; prefix?: string; suffix?: string; sub?: string; delta?: number; deltaUnit?: string; deltaTitle?: string; spark?: number[]; glow?: "up" | "down" | "accent" | "hud"; i?: number }) {
  const glowClass = glow === 'up' ? 'glow-up' : glow === 'down' ? 'glow-down' : glow === 'accent' ? 'glow-text' : glow === 'hud' ? 'glow-hud' : '';
  const hasData = value != null && value !== '';
  return (
    <div className="panel reveal-item relative p-4" style={{ "--i": i } as React.CSSProperties}>
      <div className="flex justify-between items-start mb-2">
        <span className="text-[0.75rem] uppercase tracking-wider" style={{ color: 'var(--dim)' }}>{label}</span>
        {delta != null && <DeltaBadge value={delta} unit={deltaUnit} title={deltaTitle} />}
      </div>
      <div className="flex items-baseline gap-2">
        {/* prefix/suffix are dropped with the value: "$—" and "—%" read as a
            formatted zero, which is the thing this branch exists to avoid. */}
        {!hasData ? <span className="num-hero text-2xl tnum" style={{ color: 'var(--faint)' }}>—</span>
          : typeof value === 'number' ? <AnimatedNumber value={value} decimals={decimals} prefix={prefix} suffix={suffix} className={`num-hero text-2xl ${glowClass}`} /> : <span className={`num-hero text-2xl tnum ${glowClass}`}>{prefix}{value}{suffix}</span>}
      </div>
      {sub && <div className="mt-1 text-[0.75rem]" style={{ color: 'var(--faint)' }}>{sub}</div>}
      {spark && <div className="absolute bottom-2 right-2"><Spark data={spark} width={100} height={28} /></div>}
    </div>
  );
}

export function PageHero({ title, subtitle, right, live }: { title: string; subtitle?: string; right?: React.ReactNode; live?: boolean }) {
  return (
    <div className="flex flex-col sm:flex-row sm:justify-between sm:items-end gap-4 mb-4">
      <div>
        <h1 className="hero-title text-2xl sm:text-3xl font-bold flex items-center gap-2">
          {live && <span className="live-dot" />}
          {title}
          {live && <span className="mono text-sm font-normal" style={{ color: 'var(--hud)' }}>LIVE</span>}
        </h1>
        {/* A hero subtitle IS the page's purpose line — it says what the page
            is for, above the fold, in the reader's language. Marked so the UX
            audit counts it instead of only counting <PagePurpose>. */}
        {subtitle && <p data-purpose="hero" className="mt-1 text-sm max-w-3xl" style={{ color: 'var(--dim)' }}>{subtitle}</p>}
      </div>
      {/* min-w-0 + shrink, NOT flex-shrink-0. The right slot holds each page's
          filter bar, and a slot that refuses to shrink cannot fit beside a long
          title: /intel/companies pushed the DOCUMENT to 1331px in a 1280px
          viewport (measured 2026-08-14), giving the whole app horizontal scroll
          on a desktop screen. min-w-0 lets a flex child shrink below its
          content width so its own flex-wrap can do the wrapping — the page body
          must never scroll sideways. */}
      {right && <div className="min-w-0 shrink">{right}</div>}
    </div>
  );
}

export function MiniBar({ value, max, color = "var(--accent)", i = 0, height = 6 }: { value: number; max: number; color?: string; i?: number; height?: number }) {
  const pct = Math.max(0, Math.min(100, (value / max) * 100));
  return (
    <div className="w-full rounded" style={{ height, backgroundColor: 'rgba(255,255,255,0.06)' }}>
      <div className="bar-animate h-full rounded" style={{ width: `${pct}%`, backgroundColor: color, "--i": i } as React.CSSProperties} />
    </div>
  );
}
