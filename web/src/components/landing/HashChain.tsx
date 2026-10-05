"use client";

import { Fragment, useEffect, useRef, useState } from "react";

// The newest pre-registration records, animated as a chain: each block's hash
// scrambles and resolves, then its link lights. The data is REAL (passed from
// /api/prereg); only the reveal is theatre. SSR renders every block in its
// final state so no-JS readers and crawlers see the actual hashes.

type Block = { seq: number; kind: string; hash: string; when: string };
type State = "hidden" | "resolving" | "done";

const HEX = "0123456789abcdef";
const STEP_MS = 420; // between block starts
const CHAR_MS = 40; // per resolved character

// chainOk is the daemon's own recomputation (/api/prereg chainVerified). The
// check line states it; the animation never decides it.
export default function HashChain({
  blocks,
  chainOk,
  brokenAt,
}: {
  blocks: Block[];
  chainOk: boolean;
  brokenAt: number;
}) {
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [states, setStates] = useState<State[]>(() => blocks.map(() => "done"));
  const [shown, setShown] = useState<string[]>(() => blocks.map((b) => b.hash.slice(0, 16)));
  const [verified, setVerified] = useState(true);

  useEffect(() => {
    const root = rootRef.current;
    if (!root || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    const timers: number[] = [];
    let tick = 0;
    const prefixes = blocks.map((b) => b.hash.slice(0, 16));
    const scramble = (prefix: string, revealed: number) =>
      prefix.slice(0, revealed) +
      Array.from({ length: 16 - revealed }, () => HEX[Math.floor(Math.random() * 16)]).join("");

    const run = () => {
      setStates(blocks.map(() => "hidden"));
      setShown(prefixes.map((p) => scramble(p, 0)));
      setVerified(false);
      const t0 = performance.now();
      tick = window.setInterval(() => {
        const el = performance.now() - t0;
        const nextStates: State[] = [];
        const nextShown: string[] = [];
        prefixes.forEach((p, i) => {
          const local = el - i * STEP_MS;
          if (local < 0) {
            nextStates.push("hidden");
            nextShown.push(scramble(p, 0));
            return;
          }
          const revealed = Math.min(16, Math.floor(local / CHAR_MS));
          nextStates.push(revealed >= 16 ? "done" : "resolving");
          nextShown.push(revealed >= 16 ? p : scramble(p, revealed));
        });
        setStates(nextStates);
        setShown(nextShown);
        if (nextStates.every((s) => s === "done")) {
          window.clearInterval(tick);
          tick = 0;
          setVerified(true);
        }
      }, CHAR_MS);
    };

    const io = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          io.disconnect();
          timers.push(window.setTimeout(run, 150));
        }
      },
      { threshold: 0.3 },
    );
    io.observe(root);
    return () => {
      io.disconnect();
      timers.forEach((t) => window.clearTimeout(t));
      if (tick) window.clearInterval(tick);
    };
  }, [blocks]);

  return (
    <div ref={rootRef} className="hc-root" data-verified={verified ? "true" : "false"}>
      <div className="flex flex-col items-stretch sm:flex-row">
        {blocks.map((b, i) => (
          <Fragment key={b.seq}>
            {i > 0 && (
              <div
                className="hc-link"
                aria-hidden="true"
                data-on={states[i - 1] === "done" && states[i] === "done" ? "true" : "false"}
              >
                <span className="hc-pulse" />
              </div>
            )}
            <div
              className="hc-block panel relative flex min-w-0 flex-1 flex-col gap-1 px-3 py-3"
              data-state={states[i]}
            >
              <div className="mono text-[0.7rem] tracking-[0.18em]" style={{ color: "var(--accent)" }}>
                #{b.seq}
              </div>
              <div className="text-sm font-semibold">{b.kind}</div>
              <code className="hc-hash mono block truncate text-[0.72rem]" title={b.hash}>
                {shown[i]}…
              </code>
              <div className="text-[0.68rem]" style={{ color: "var(--faint)" }}>
                {b.when}
              </div>
            </div>
          </Fragment>
        ))}
      </div>
      {chainOk ? (
        <div className="hc-verified mono mt-3 text-[0.75rem]" data-show={verified ? "true" : "false"}>
          ✓ chain recomputed by the daemon — every link matches
        </div>
      ) : (
        <div className="mono mt-3 text-[0.75rem]" style={{ color: "var(--bad)" }} role="alert">
          ✗ the daemon&rsquo;s recomputation found a broken link{brokenAt > 0 ? ` at #${brokenAt}` : ""}
        </div>
      )}
    </div>
  );
}
