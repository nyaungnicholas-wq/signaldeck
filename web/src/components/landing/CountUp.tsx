"use client";

import { useEffect, useRef, useState } from "react";

// Counts a real figure up from zero the first time it scrolls into view. The
// server renders the final value, so no-JS readers and crawlers get the number.

const fmt = (v: number, decimals: number) =>
  v.toLocaleString("en-US", { minimumFractionDigits: decimals, maximumFractionDigits: decimals });

export default function CountUp({
  value,
  decimals = 0,
  prefix = "",
  suffix = "",
  duration = 1600,
  className = "",
}: {
  value: number;
  decimals?: number;
  prefix?: string;
  suffix?: string;
  duration?: number;
  className?: string;
}) {
  const ref = useRef<HTMLSpanElement | null>(null);
  const [display, setDisplay] = useState<number | null>(null); // null = show the final value

  useEffect(() => {
    const el = ref.current;
    if (!el || !Number.isFinite(value) || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    let raf = 0;
    const io = new IntersectionObserver(
      ([entry]) => {
        if (!entry.isIntersecting) return;
        io.disconnect();
        const t0 = performance.now();
        const step = (now: number) => {
          const t = Math.min(1, (now - t0) / duration);
          const eased = t === 1 ? 1 : 1 - Math.pow(2, -10 * t);
          setDisplay(t === 1 ? null : eased * value);
          if (t < 1) raf = requestAnimationFrame(step);
        };
        raf = requestAnimationFrame(step);
      },
      { threshold: 0.4 },
    );
    io.observe(el);
    return () => {
      io.disconnect();
      cancelAnimationFrame(raf);
    };
  }, [value, duration]);

  if (!Number.isFinite(value)) return <span className={`tabular-nums ${className}`}>—</span>;
  return (
    <span ref={ref} className={`tabular-nums ${className}`}>
      {prefix}
      {fmt(display ?? value, decimals)}
      {suffix}
    </span>
  );
}
