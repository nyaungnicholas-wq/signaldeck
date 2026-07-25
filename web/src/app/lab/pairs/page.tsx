"use client";

// PAIRS — a published DO-NOT-SHIP result.
//
// Every other Lab surface shows something the platform believes. This one shows
// something it stopped believing, and the layout is built around the single
// comparison that killed it: three arms — pairs chosen by cointegration, pairs
// chosen at random inside the same sector, and the LEAST cointegrated pairs —
// traded on identical rules. If the selection rule carried information the arms
// would separate. They do not, and the worst-selected arm has the highest point
// estimate, so the arms are shown side by side before any single mean return is
// shown at all.
//
// The mechanism panel is the part worth keeping: correlation rank persists hard
// while cointegration rank does not persist at all, which is why a statistic
// this well estimated could still have nothing in it.

import { useEffect, useState } from "react";
import { api, type PairsStudyPayload, type PairsArm, type PairsCostLevel } from "@/lib/api";
import Skeleton from "@/components/Skeleton";
import ErrorState from "@/components/ErrorState";
import PagePurpose from "@/components/PagePurpose";
import HelpTip from "@/components/HelpTip";
import ProOnly from "@/components/ProOnly";

const pct = (v: number) => `${(v * 100 >= 0 ? "+" : "") + (v * 100).toFixed(3)}%`;
const pctPlain = (v: number) => `${(v * 100).toFixed(1)}%`;
const rho = (v: number) => (v >= 0 ? "+" : "") + v.toFixed(3);

export default function PairsPage() {
  const [data, setData] = useState<PairsStudyPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [retryTick, setRetryTick] = useState(0);

  useEffect(() => {
    let alive = true;
    api
      .pairsStudy()
      .then((d) => alive && (setData(d), setErr(null)))
      .catch((e: unknown) => alive && setErr(e instanceof Error ? e.message : String(e)));
    return () => {
      alive = false;
    };
    // The study is frozen — there is nothing to poll for.
  }, [retryTick]);

  if (err) return <ErrorState message={err} retry={() => setRetryTick((t) => t + 1)} />;
  if (!data) return <Skeleton lines={8} />;

  const s = data.study;
  const zero = s.costs.find((c) => c.costBps === 0) ?? s.costs[0];
  const p = s.persistence;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <h1 className="text-sm font-extrabold tracking-[0.18em]">PAIRS</h1>
        <span className="text-[0.75rem]" style={{ color: "var(--faint)" }}>
          the test that resolved H018 — and killed it
        </span>
        <span className="ml-auto text-[0.7rem]" style={{ color: "var(--faint)" }}>
          frozen run · {s.ranOn}
        </span>
      </div>

      <PagePurpose
        id="lab-pairs"
        text={
          "A hypothesis sat parked for eight days at 73.1% because its confidence interval straddled the product bar. " +
          "More data would never have settled it, because a rank-persistence statistic is not a decision anyone can act on. " +
          "So it was resolved the other way: by building the position that would have to earn the money. This page is that " +
          "test and its verdict, published in full because a negative result is the only kind that retires a belief."
        }
      />

      {/* The verdict, and the one number behind it. */}
      <section
        className="rounded border p-3"
        style={{ borderColor: "var(--line)", background: "var(--panel)" }}
      >
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <h2 className="text-[0.8rem] font-bold tracking-[0.12em]">VERDICT</h2>
          <span
            className="rounded px-1.5 py-0.5 text-[0.65rem] font-bold"
            style={{ background: "var(--line)", color: "var(--fg)" }}
          >
            DO NOT SHIP
          </span>
          <span className="ml-auto text-[0.68rem]" style={{ color: "var(--faint)" }}>
            ledger tag {data.ledgerTag}
          </span>
        </div>

        <div className="mb-3 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat
            label="selection edge / trade"
            value={pct(zero.selectionEdge)}
            strong
            hint="Cointegration-selected pairs minus random same-sector pairs, at zero cost. This is all the selection rule is worth."
          />
          <Stat
            label="selected arm, 0 bps"
            value={pct(zero.coint.meanRet)}
            hint="Before a cent of friction — and pairs trading is cost-dominated."
          />
          <Stat
            label="its 95% interval"
            value={`[${pct(zero.coint.meanLo)}, ${pct(zero.coint.meanHi)}]`}
            hint="Block bootstrap: one cluster per walk-forward window, because trades opened in the same quarter share a regime."
          />
          <Stat
            label="excludes zero?"
            value={zero.coint.excludesZero ? "yes" : "no"}
            hint="At zero cost. The answer at every cost level in the sweep below is the same."
          />
        </div>

        <p className="text-[0.8rem] leading-relaxed">{s.verdict}</p>
      </section>

      {/* The comparison that decides it — arms first, before any single number. */}
      <section
        className="rounded border p-3"
        style={{ borderColor: "var(--line)", background: "var(--panel)" }}
      >
        <h2
          className="mb-1 text-[0.7rem] font-bold tracking-[0.16em]"
          style={{ color: "var(--faint)" }}
        >
          THREE ARMS, IDENTICAL RULES — {zero.costBps} BPS
        </h2>
        <p className="mb-2 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {data.readThisFirst}
        </p>
        <ArmTable arms={[zero.coint, zero.random, zero.worst]} />
        <p className="mt-2 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          Entry {s.rules.entry}, exit {s.rules.exit}, stop {s.rules.stop}, force close{" "}
          {s.rules.forceClose}. Top {s.rules.pairsPerFold} pairs per block.
        </p>
      </section>

      {/* The mechanism — the finding that outlives the verdict. */}
      <section
        className="rounded border p-3"
        style={{ borderColor: "var(--line)", background: "var(--panel)" }}
      >
        <h2
          className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]"
          style={{ color: "var(--faint)" }}
        >
          WHY — WHAT PERSISTS AND WHAT DOES NOT
        </h2>
        <div className="mb-3 grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat
            label="correlation rank persists"
            value={rho(p.corrRho)}
            strong
            hint="Spearman rho, formation-window rank vs next-window rank. Pairs that moved together keep moving together."
          />
          <Stat
            label="its 95% interval"
            value={
              p.corrRhoLo == null || p.corrRhoHi == null
                ? "withheld"
                : `[${rho(p.corrRhoLo)}, ${rho(p.corrRhoHi)}]`
            }
          />
          <Stat
            label="cointegration rank persists"
            value={rho(p.cointRho)}
            strong
            hint="The same statistic for the property a spread trade actually needs. Indistinguishable from zero."
          />
          <Stat label="walk-forward blocks" value={String(p.blocks)} />
        </div>
        <p className="text-[0.8rem] leading-relaxed">{s.mechanism}</p>
        <p className="mt-2 text-[0.78rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {s.why}
        </p>

        <div className="mt-3 grid grid-cols-3 gap-3">
          <Stat label="stays above forward median" value={pctPlain(p.aboveMedian)} hint="Null: 50%." />
          <Stat label="stays in top tercile" value={pctPlain(p.topTercile)} hint="Null: 33%." />
          <Stat label="stays in top decile" value={pctPlain(p.topDecile)} hint="Null: 10%." />
        </div>
        <p className="mt-1 text-[0.7rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          Binary framings of forward co-movement persistence, over{" "}
          {p.pairsEvaluated.toLocaleString()} pairs. They corroborate the mechanism; they are NOT
          restatements of the ledgered 73.1%, which measured SPY-correlation tiering — a different
          quantity.
        </p>
      </section>

      {/* Cost sweep: the break-even level is the finding, not a footnote. */}
      <section
        className="rounded border p-3"
        style={{ borderColor: "var(--line)", background: "var(--panel)" }}
      >
        <h2
          className="mb-1 text-[0.7rem] font-bold tracking-[0.16em]"
          style={{ color: "var(--faint)" }}
        >
          COST SWEEP — SELECTED ARM
        </h2>
        <p className="mb-2 text-[0.72rem] leading-relaxed" style={{ color: "var(--faint)" }}>
          {s.method.costSweepReason}
        </p>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[30rem] text-[0.75rem]">
            <thead>
              <tr style={{ color: "var(--faint)" }}>
                <th className="text-left font-normal">cost / side</th>
                <th className="text-right font-normal">mean / trade</th>
                <th className="text-right font-normal">95% CI</th>
                <th className="text-right font-normal">Sharpe</th>
                <th className="text-right font-normal">selection edge</th>
                <th className="text-right font-normal">excludes zero</th>
              </tr>
            </thead>
            <tbody>
              {s.costs.map((c: PairsCostLevel) => (
                <tr key={c.costBps} style={{ borderTop: "1px solid var(--line)" }}>
                  <td className="py-1">{c.costBps.toFixed(1)} bps</td>
                  <td className="py-1 text-right">{pct(c.coint.meanRet)}</td>
                  <td className="py-1 text-right">
                    [{pct(c.coint.meanLo)}, {pct(c.coint.meanHi)}]
                  </td>
                  <td className="py-1 text-right">{c.coint.sharpe.toFixed(2)}</td>
                  <td className="py-1 text-right">{pct(c.selectionEdge)}</td>
                  <td className="py-1 text-right">{c.coint.excludesZero ? "yes" : "no"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <ProOnly>
        <section
          className="rounded border p-3"
          style={{ borderColor: "var(--line)", background: "var(--panel)" }}
        >
          <h2
            className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]"
            style={{ color: "var(--faint)" }}
          >
            METHOD
          </h2>
          <dl className="flex flex-col gap-2 text-[0.75rem] leading-relaxed">
            <Note term="Walk" text={s.method.window} />
            <Note term="Universe" text={s.method.universe} />
            <Note term="Frozen parameters" text={s.method.frozenParams} />
            <Note term="Critical values" text={s.method.criticalValues} />
            <Note term="Matched nulls" text={s.method.matchedNulls} />
            <Note term="Why the interval is clustered" text={s.method.blockBootstrap} />
            <Note term="Why the numbers do not move" text={data.whyFrozen} />
          </dl>
          <p className="mt-2 text-[0.7rem]" style={{ color: "var(--faint)" }}>
            Reproduce: <code>{data.reproduce}</code> · full writeup in {data.writeup}
          </p>
        </section>
      </ProOnly>

      <section
        className="rounded border p-3"
        style={{ borderColor: "var(--line)", background: "var(--panel)" }}
      >
        <h2
          className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]"
          style={{ color: "var(--faint)" }}
        >
          WHAT THIS TEST STILL CANNOT SEE
        </h2>
        <ul className="flex list-disc flex-col gap-2 pl-4 text-[0.75rem] leading-relaxed">
          {s.limitations.map((l) => (
            <li key={l}>{l}</li>
          ))}
        </ul>
      </section>

      <section
        className="rounded border p-3"
        style={{ borderColor: "var(--line)", background: "var(--panel)" }}
      >
        <h2
          className="mb-2 text-[0.7rem] font-bold tracking-[0.16em]"
          style={{ color: "var(--faint)" }}
        >
          CARRIED FORWARD
        </h2>
        <ul className="flex list-disc flex-col gap-2 pl-4 text-[0.75rem] leading-relaxed">
          {s.lessons.map((l) => (
            <li key={l}>{l}</li>
          ))}
        </ul>
      </section>

      <p
        className="rounded border p-3 text-[0.75rem] leading-relaxed"
        style={{ borderColor: "var(--line)", color: "var(--faint)" }}
      >
        {s.hypothesis}
      </p>
    </div>
  );
}

/** The three arms, so the eye compares them before it reads any one of them. */
function ArmTable({ arms }: { arms: PairsArm[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[34rem] text-[0.75rem]">
        <thead>
          <tr style={{ color: "var(--faint)" }}>
            <th className="text-left font-normal">arm</th>
            <th className="text-right font-normal">trades</th>
            <th className="text-right font-normal">mean / trade</th>
            <th className="text-right font-normal">95% CI</th>
            <th className="text-right font-normal">win rate</th>
            <th className="text-right font-normal">Sharpe</th>
            <th className="text-right font-normal">stopped out</th>
          </tr>
        </thead>
        <tbody>
          {arms.map((a) => (
            <tr key={a.arm} style={{ borderTop: "1px solid var(--line)" }}>
              <td className="py-1">{a.arm}</td>
              <td className="py-1 text-right">{a.trades.toLocaleString()}</td>
              <td className="py-1 text-right">{pct(a.meanRet)}</td>
              <td className="py-1 text-right">
                [{pct(a.meanLo)}, {pct(a.meanHi)}]
              </td>
              <td className="py-1 text-right">{pctPlain(a.winRate)}</td>
              <td className="py-1 text-right">{a.sharpe.toFixed(2)}</td>
              <td className="py-1 text-right">
                {a.trades > 0 ? pctPlain(a.exits.stop / a.trades) : "—"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Stat({
  label,
  value,
  hint,
  strong,
}: {
  label: string;
  value: string;
  hint?: string;
  strong?: boolean;
}) {
  return (
    <div>
      <div
        className="flex items-center gap-1 text-[0.65rem] tracking-wide"
        style={{ color: "var(--faint)" }}
      >
        <span>{label}</span>
        {hint && <HelpTip label={label}>{hint}</HelpTip>}
      </div>
      <div className={strong ? "text-[0.95rem] font-bold" : "text-[0.85rem]"}>{value}</div>
    </div>
  );
}

function Note({ term, text }: { term: string; text: string }) {
  return (
    <div>
      <dt className="text-[0.68rem] font-bold tracking-wide" style={{ color: "var(--faint)" }}>
        {term}
      </dt>
      <dd>{text}</dd>
    </div>
  );
}
