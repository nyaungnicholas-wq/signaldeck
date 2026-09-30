"use client";

import React, { useEffect, useRef, useState } from "react";

// Scroll reveal. The hidden state only exists under html.js (added here), so a
// reader without JavaScript — or a crawler — always sees the content.

type Variant = "up" | "fade" | "scale" | "left" | "right" | "blur";

export default function Reveal({
  children,
  delay = 0,
  variant = "up",
  className = "",
  as = "div",
}: {
  children: React.ReactNode;
  delay?: number;
  variant?: Variant;
  className?: string;
  as?: "div" | "section" | "li";
}) {
  const ref = useRef<HTMLElement | null>(null);
  const [shown, setShown] = useState(false);

  useEffect(() => {
    document.documentElement.classList.add("js");
    const el = ref.current;
    if (!el) return;
    const instant = !("IntersectionObserver" in window) ||
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const io = instant
      ? null
      : new IntersectionObserver(
          ([entry]) => {
            if (entry.isIntersecting) {
              setShown(true);
              io?.disconnect();
            }
          },
          { threshold: 0.12, rootMargin: "0px 0px -8% 0px" },
        );
    if (io) io.observe(el);
    else queueMicrotask(() => setShown(true));
    return () => io?.disconnect();
  }, []);

  const Tag = as as React.ElementType;
  return (
    <Tag
      ref={ref}
      className={`rv rv-${variant} ${className}`}
      data-shown={shown ? "true" : "false"}
      style={{ transitionDelay: `${delay}ms` }}
    >
      {children}
    </Tag>
  );
}

export function RevealStagger({
  children,
  step = 90,
  variant = "up",
  className = "",
}: {
  children: React.ReactNode[];
  step?: number;
  variant?: Variant;
  className?: string;
}) {
  return (
    <div className={className}>
      {children.map((child, i) => (
        <Reveal key={i} delay={i * step} variant={variant}>
          {child}
        </Reveal>
      ))}
    </div>
  );
}
