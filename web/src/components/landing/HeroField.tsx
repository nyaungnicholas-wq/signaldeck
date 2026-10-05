"use client";

import { useEffect, useRef } from "react";

// The hero's "data globe": a rotating Fibonacci point sphere with orbit rings
// and pulses, projected into canvas 2D. Decorative only (aria-hidden). No
// WebGL and no dependency — 1,400 points sorted per frame is well inside a
// frame budget, and the loop stops whenever the hero is off screen or the tab
// is hidden.

type V3 = [number, number, number];

const N_POINTS = 1400;
const GOLDEN = Math.PI * (3 - Math.sqrt(5));
const AMBER: V3 = [251, 191, 36];
const SKY: V3 = [56, 189, 248];

function fibonacciSphere(n: number): V3[] {
  const pts: V3[] = [];
  for (let i = 0; i < n; i++) {
    const y = 1 - (i / (n - 1)) * 2;
    const r = Math.sqrt(1 - y * y);
    const th = GOLDEN * i;
    pts.push([Math.cos(th) * r, y, Math.sin(th) * r]);
  }
  return pts;
}

function ring(n: number, radius: number, tiltX: number, tiltZ: number): V3[] {
  const pts: V3[] = [];
  const cx = Math.cos(tiltX), sx = Math.sin(tiltX);
  const cz = Math.cos(tiltZ), sz = Math.sin(tiltZ);
  for (let i = 0; i < n; i++) {
    const a = (i / n) * Math.PI * 2;
    const x0 = Math.cos(a) * radius;
    const z0 = Math.sin(a) * radius;
    // tilt around X (y0 = 0), then around Z
    const y1 = -z0 * sx;
    const z1 = z0 * cx;
    const x2 = x0 * cz - y1 * sz;
    const y2 = x0 * sz + y1 * cz;
    pts.push([x2, y2, z1]);
  }
  return pts;
}

function rotate(p: V3, yaw: number, pitch: number): V3 {
  const cy = Math.cos(yaw), sy = Math.sin(yaw);
  const x1 = p[0] * cy + p[2] * sy;
  const z1 = -p[0] * sy + p[2] * cy;
  const cp = Math.cos(pitch), sp = Math.sin(pitch);
  const y2 = p[1] * cp - z1 * sp;
  const z2 = p[1] * sp + z1 * cp;
  return [x1, y2, z2];
}

function mix(a: V3, b: V3, t: number): string {
  return `${Math.round(a[0] + (b[0] - a[0]) * t)},${Math.round(a[1] + (b[1] - a[1]) * t)},${Math.round(a[2] + (b[2] - a[2]) * t)}`;
}

type Pulse = { idx: number; born: number };

export default function HeroField({ className }: { className?: string }) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const parent = canvas?.parentElement;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !parent || !ctx) return;

    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const sphere = fibonacciSphere(N_POINTS);
    const rings = [
      { pts: ring(180, 1.25, 1.1, 0.3), speed: 0.35 },
      { pts: ring(180, 1.45, 0.6, -0.5), speed: -0.22 },
      { pts: ring(180, 1.7, 1.4, 0.9), speed: 0.15 },
    ];
    const proj = new Float32Array(N_POINTS * 3); // sx, sy, depth01
    const order = new Uint16Array(N_POINTS);
    for (let i = 0; i < N_POINTS; i++) order[i] = i;

    let w = 0, h = 0, dpr = 1;
    const resize = () => {
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      w = parent.clientWidth;
      h = parent.clientHeight;
      canvas.width = Math.max(1, Math.floor(w * dpr));
      canvas.height = Math.max(1, Math.floor(h * dpr));
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };
    resize();

    let targetYaw = 0, targetPitch = 0, parYaw = 0, parPitch = 0;
    const onPointer = (e: PointerEvent) => {
      targetYaw = ((e.clientX / window.innerWidth) * 2 - 1) * 0.25;
      targetPitch = ((e.clientY / window.innerHeight) * 2 - 1) * 0.15;
    };

    const pulses: Pulse[] = [];
    let lastPulse = 0;
    let yaw = 0.6;
    let last = performance.now();

    const draw = (now: number, animate: boolean) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      if (animate) {
        yaw += 0.12 * dt;
        parYaw += (targetYaw - parYaw) * 0.05;
        parPitch += (targetPitch - parPitch) * 0.05;
      }
      const wide = w >= 900;
      const R = (wide ? 0.36 : 0.42) * Math.min(w, h);
      const cx = wide ? 0.72 * w : 0.5 * w;
      const cy = wide ? 0.48 * h : 0.42 * h;
      const D = 2.6;
      const Y = yaw + parYaw;
      const P = 0.38 + parPitch;

      ctx.clearRect(0, 0, w, h);
      const glow = ctx.createRadialGradient(cx, cy, 0, cx, cy, R * 1.3);
      glow.addColorStop(0, "rgba(251,191,36,0.16)");
      glow.addColorStop(1, "rgba(251,191,36,0)");
      ctx.fillStyle = glow;
      ctx.fillRect(0, 0, w, h);

      // Orbit rings behind and in front (depth-faded dots).
      for (let r = 0; r < rings.length; r++) {
        const rg = rings[r];
        const ry = Y + (animate ? now / 1000 : 0) * rg.speed;
        for (const p of rg.pts) {
          const [x, y, z] = rotate(p, ry, P);
          const s = D / (D - z);
          const d01 = (z / 1.7 + 1) / 2;
          ctx.fillStyle = `rgba(56,189,248,${(0.1 + 0.35 * d01).toFixed(3)})`;
          ctx.fillRect(cx + x * R * s, cy + y * R * s, 1.2, 1.2);
        }
      }

      for (let i = 0; i < N_POINTS; i++) {
        const [x, y, z] = rotate(sphere[i], Y, P);
        const s = D / (D - z);
        proj[i * 3] = cx + x * R * s;
        proj[i * 3 + 1] = cy + y * R * s;
        proj[i * 3 + 2] = (z + 1) / 2;
      }
      order.sort((a, b) => proj[a * 3 + 2] - proj[b * 3 + 2]);
      for (let k = 0; k < N_POINTS; k++) {
        const i = order[k];
        const d = proj[i * 3 + 2];
        const size = 0.8 + 2.1 * d;
        ctx.fillStyle = `rgba(${mix(SKY, AMBER, d)},${(0.22 + 0.78 * d).toFixed(3)})`;
        ctx.fillRect(proj[i * 3] - size / 2, proj[i * 3 + 1] - size / 2, size, size);
      }

      if (animate) {
        if (now - lastPulse > 350) {
          lastPulse = now;
          for (let tries = 0; tries < 12; tries++) {
            const idx = Math.floor(Math.random() * N_POINTS);
            if (proj[idx * 3 + 2] > 0.6) {
              pulses.push({ idx, born: now });
              break;
            }
          }
          if (pulses.length > 24) pulses.shift();
        }
        for (let p = pulses.length - 1; p >= 0; p--) {
          const age = (now - pulses[p].born) / 1200;
          if (age >= 1) {
            pulses.splice(p, 1);
            continue;
          }
          const i = pulses[p].idx;
          const px = proj[i * 3], py = proj[i * 3 + 1];
          ctx.strokeStyle = `rgba(251,191,36,${(0.9 * (1 - age)).toFixed(3)})`;
          ctx.lineWidth = 1.2;
          ctx.beginPath();
          ctx.arc(px, py, 22 * age, 0, Math.PI * 2);
          ctx.stroke();
          ctx.fillStyle = `rgba(255,236,170,${(1 - age).toFixed(3)})`;
          ctx.fillRect(px - 2, py - 2, 4, 4);
        }
      }
    };

    if (reduce) {
      draw(performance.now(), false);
      const ro = new ResizeObserver(() => {
        resize();
        draw(performance.now(), false);
      });
      ro.observe(parent);
      return () => ro.disconnect();
    }

    let raf = 0;
    let visible = true;
    const loop = (now: number) => {
      draw(now, true);
      raf = requestAnimationFrame(loop);
    };
    const start = () => {
      if (!raf && visible && !document.hidden) {
        last = performance.now();
        raf = requestAnimationFrame(loop);
      }
    };
    const stop = () => {
      if (raf) cancelAnimationFrame(raf);
      raf = 0;
    };

    const ro = new ResizeObserver(resize);
    ro.observe(parent);
    const io = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting;
      if (visible) start();
      else stop();
    });
    io.observe(canvas);
    const onVis = () => (document.hidden ? stop() : start());
    document.addEventListener("visibilitychange", onVis);
    window.addEventListener("pointermove", onPointer, { passive: true });
    start();

    return () => {
      stop();
      ro.disconnect();
      io.disconnect();
      document.removeEventListener("visibilitychange", onVis);
      window.removeEventListener("pointermove", onPointer);
    };
  }, []);

  return (
    <canvas
      ref={canvasRef}
      aria-hidden="true"
      className={className}
      style={{ position: "absolute", inset: 0, width: "100%", height: "100%" }}
    />
  );
}
