"use client";

// WORLD MODEL — how the economy connects. Two honest halves:
//   (1) LIVE DRIVERS — the real macro dials (rates, vol, credit, growth …),
//       each a chip colored by its state (calm green, stressed amber/red,
//       unknown a faint dashed outline) with its current value and series;
//   (2) SHOCK EXPLORER — pick a hypothetical shock and the graph traces the
//       CAUSAL CHAIN (Oil ↑ → Inflation ↑ → Rates ↑ → Banks ↑ / Homebuilders ↓)
//       and lists the AFFECTED SYMBOLS with the path that reaches each.
// The `note` — that these are CURATED relationships, not learned — rides the
// footnote, because a hand-built causal graph presented as discovered truth
// would be a lie. Self-contained: fetches its own world-model, shock list, and
// on-demand propagations.

import { useEffect, useState } from "react";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";

// ── shapes (local; the desk does not import from lib/api) ──
interface WMDriver {
  key: string;
  label: string;
  state: string;
  series: string;
  value: number | string;
  known: boolean;
  note: string;
}
interface WorldModel {
  asOf: number;
  note: string;
  drivers: WMDriver[];
  nodes: { id: string; label: string; kind: "driver" | "sector" | "symbol" | string }[];
  edges: { from: string; to: string; sign: 1 | -1; rationale: string }[];
}
interface PropStep {
  node: string;
  label: string;
  effect: "up" | "down" | string;
}
interface PropAffected {
  symbol: string;
  market: string;
  effect: "up" | "down" | string;
  path: string;
}
interface Propagate {
  shock: string;
  label: string;
  note: string;
  chain: PropStep[];
  affected: PropAffected[];
}

const j = <T,>(p: string): Promise<T> =>
  fetch(p, { credentials: "include", headers: { "X-Signaldeck": "1" } }).then((r) =>
    r.ok ? (r.json() as Promise<T>) : Promise.reject(new Error("API " + r.status)),
  );

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** Driver state → color. Stressed macro states warn/alarm; calm states read
 *  green; an unknown/ungathered dial is faint (and drawn dashed by the chip). */
function driverStateColor(state: string, known: boolean): string {
  if (!known) return "var(--faint)";
  const s = (state || "").toLowerCase();
  if (["inverted", "wide", "stress", "high", "elevated"].includes(s)) {
    return s === "inverted" || s === "wide" || s === "stress" ? "var(--bad)" : "var(--warn)";
  }
  if (["tight", "rising"].includes(s)) return "var(--warn)";
  if (["low", "normal", "loose", "calm", "easing"].includes(s)) return "var(--ok)";
  if (s === "unknown" || s === "") return "var(--faint)";
  return "var(--dim)";
}

/** Humanize a shock key ("oil_up" → "Oil ↑", "credit_stress" → "Credit stress"). */
function shockLabel(key: string): string {
  const parts = (key || "").split("_");
  const dir = parts[parts.length - 1];
  const head = parts.slice(0, dir === "up" || dir === "down" ? -1 : parts.length).join(" ");
  const cap = head ? head[0].toUpperCase() + head.slice(1) : key;
  if (dir === "up") return `${cap} ↑`;
  if (dir === "down") return `${cap} ↓`;
  return cap;
}

/** A colored up/down arrow for a propagated effect. */
function EffectArrow({ effect }: { effect: string }) {
  const up = effect === "up";
  return (
    <span aria-hidden="true" className="font-bold" style={{ color: up ? "var(--ok)" : "var(--bad)" }}>
      {up ? "↑" : "↓"}
    </span>
  );
}

export default function WorldModelPanel() {
  const [wm, setWm] = useState<WorldModel | null>(null);
  const [wmErr, setWmErr] = useState<string | null>(null);
  const [shocks, setShocks] = useState<string[]>([]);
  // An explicit pick wins; otherwise default to the flagship shock so a causal
  // chain is visible the moment the shock list loads. Derived in render — no
  // setState-in-effect seeding.
  const [selectedRaw, setSelectedRaw] = useState<string | null>(null);
  const selected = selectedRaw ?? (shocks.includes("oil_up") ? "oil_up" : (shocks[0] ?? null));

  // Propagation keyed to the selected shock so switching shows a loading state
  // without a synchronous setState inside the effect.
  const [propState, setPropState] = useState<{ key: string; d: Propagate } | null>(null);
  const [propErrState, setPropErrState] = useState<{ key: string; msg: string } | null>(null);
  const prop = propState && propState.key === selected ? propState.d : null;
  const propErr = propErrState && propErrState.key === selected ? propErrState.msg : null;

  // world-model (30s poll) + one-shot shock list
  useEffect(() => {
    let alive = true;
    j<{ shocks: string[] }>("/api/world-model/shocks")
      .then((d) => {
        if (alive && Array.isArray(d?.shocks)) setShocks(d.shocks);
      })
      .catch(() => {
        /* the drivers strip still renders; the explorer just has no buttons */
      });

    const load = () =>
      j<WorldModel>("/api/world-model")
        .then((d) => {
          if (alive) {
            setWm(d);
            setWmErr(null);
          }
        })
        .catch((e) => {
          if (alive) setWmErr(msg(e));
        });
    load();
    const id = setInterval(load, 30000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  // Propagate the selected shock on demand.
  useEffect(() => {
    if (!selected) return;
    let alive = true;
    const key = selected;
    j<Propagate>(`/api/world-model/propagate?shock=${encodeURIComponent(selected)}`)
      .then((d) => {
        if (alive) setPropState({ key, d });
      })
      .catch((e) => {
        if (alive) setPropErrState({ key, msg: msg(e) });
      });
    return () => {
      alive = false;
    };
  }, [selected]);

  const drivers = Array.isArray(wm?.drivers) ? wm.drivers : [];
  const chain = Array.isArray(prop?.chain) ? prop.chain : [];
  const affected = Array.isArray(prop?.affected) ? prop.affected : [];

  return (
    <section className="panel">
      <div className="panel-h flex-wrap gap-2">
        WORLD MODEL
        <span className="normal-case" style={{ color: "var(--faint)", letterSpacing: "normal" }}>
          — how the economy connects
        </span>
      </div>

      <div className="flex flex-col gap-6 px-4 py-4">
        {/* ── (1) LIVE DRIVERS ── */}
        <div className="flex flex-col gap-2">
          <span className="text-[0.75rem] font-medium tracking-wide" style={{ color: "var(--faint)" }}>
            LIVE DRIVERS
          </span>
          {wmErr && !wm ? (
            <ErrorState message={wmErr} className="border-0" />
          ) : !wm ? (
            <Skeleton lines={2} label="loading world model" className="border-0 p-0" />
          ) : drivers.length === 0 ? (
            <p className="text-[0.75rem]" style={{ color: "var(--dim)" }}>
              No macro drivers gathered yet.
            </p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {drivers.map((dr) => {
                const color = driverStateColor(dr.state, dr.known);
                const dashed = !dr.known || (dr.state || "").toLowerCase() === "unknown";
                return (
                  <div
                    key={dr.key || dr.label}
                    className="flex min-w-[8rem] flex-col gap-0.5 rounded-lg px-3 py-2"
                    style={{
                      background: "var(--panel2)",
                      border: `1px ${dashed ? "dashed" : "solid"} ${dashed ? "var(--border-strong)" : "var(--border)"}`,
                    }}
                    title={dr.note || undefined}
                  >
                    <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                      {dr.label}
                    </span>
                    <div className="flex items-baseline gap-2">
                      <span className="text-[0.82rem] font-bold" style={{ color }}>
                        {dr.known ? dr.state || "—" : "unknown"}
                      </span>
                      <span className="tnum text-[0.75rem]" style={{ color: "var(--dim)" }}>
                        {dr.value !== undefined && dr.value !== null && dr.value !== "" ? String(dr.value) : "—"}
                      </span>
                    </div>
                    {dr.series ? (
                      <span className="mono text-[0.75rem]" style={{ color: "var(--faint)" }}>
                        {String(dr.series)}
                      </span>
                    ) : null}
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* ── (2) SHOCK EXPLORER ── */}
        <div className="flex flex-col gap-3">
          <span className="text-[0.75rem] font-medium tracking-wide" style={{ color: "var(--faint)" }}>
            SHOCK EXPLORER — trace a hypothetical through the graph
          </span>

          {shocks.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {shocks.map((s) => {
                const active = s === selected;
                return (
                  <button
                    key={s}
                    type="button"
                    onClick={() => setSelectedRaw(s)}
                    aria-pressed={active}
                    className="chip min-h-[36px] cursor-pointer px-3 transition-colors duration-150 hover:text-[var(--text)]"
                    style={active ? { color: "var(--accent)", borderColor: "var(--accent)" } : undefined}
                  >
                    {shockLabel(s)}
                  </button>
                );
              })}
            </div>
          ) : (
            <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
              No shock scenarios available.
            </p>
          )}

          {/* propagation result */}
          {propErr && !prop ? (
            <ErrorState message={propErr} className="border-0" />
          ) : selected && !prop ? (
            <Skeleton lines={2} label="propagating shock" className="border-0 p-0" />
          ) : prop ? (
            <div className="flex flex-col gap-4">
              {/* CAUSAL CHAIN */}
              <div className="flex flex-col gap-1.5">
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  CAUSAL CHAIN
                  {prop.label ? <span style={{ color: "var(--dim)" }}> · {prop.label}</span> : null}
                </span>
                {chain.length > 0 ? (
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-2 overflow-x-auto">
                    {chain.map((step, i) => (
                      <span key={`${step.node}-${i}`} className="flex items-center gap-2">
                        {i > 0 ? (
                          <span aria-hidden="true" style={{ color: "var(--faint)" }}>
                            →
                          </span>
                        ) : null}
                        <span
                          className="flex items-center gap-1 rounded-md px-2 py-1 text-[0.75rem]"
                          style={{ background: "var(--panel2)", border: "1px solid var(--border)", color: "var(--text)" }}
                        >
                          {step.label} <EffectArrow effect={step.effect} />
                        </span>
                      </span>
                    ))}
                  </div>
                ) : (
                  <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    no chain for this shock
                  </p>
                )}
              </div>

              {/* AFFECTED SYMBOLS */}
              <div className="flex flex-col gap-1.5">
                <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                  AFFECTED SYMBOLS
                </span>
                {affected.length > 0 ? (
                  <div className="flex flex-col gap-1.5">
                    {affected.map((af, i) => (
                      <div
                        key={`${af.symbol}-${i}`}
                        className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[0.75rem]"
                      >
                        <span className="mono font-bold" style={{ color: "var(--text)" }}>
                          {af.symbol}
                        </span>
                        <span style={{ color: "var(--faint)" }}>{af.market}</span>
                        <EffectArrow effect={af.effect} />
                        {af.path ? (
                          <span style={{ color: "var(--dim)" }}>{af.path}</span>
                        ) : null}
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
                    no symbols mapped for this shock
                  </p>
                )}
              </div>
            </div>
          ) : null}
        </div>

        {/* honesty footnote — curated relationships, not learned */}
        {wm?.note ? (
          <p className="text-[0.75rem] leading-relaxed" style={{ color: "var(--faint)" }}>
            {wm.note}
          </p>
        ) : null}
      </div>
    </section>
  );
}
