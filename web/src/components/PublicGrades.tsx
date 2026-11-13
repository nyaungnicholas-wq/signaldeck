import Link from "next/link";
import { KIND_LABEL } from "@/lib/publicForecasts";

// The Grades page of the public (IEX-fed) instance. It reads the daemon's
// allowlisted /api/public/grades: per kind of forecast, how many are still
// open, how many have been graded, and, once enough distinct days exist, how
// often the forecast was right next to how often "nothing changes" was right.
// Until then it says exactly that. No interval, no verdict, no accuracy claim.

const DAEMON = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";

type Grade = {
  kind: string;
  open: number;
  graded: number;
  days: number;
  accuracy?: number;
  baseline?: number;
  compared?: number;
  status: string;
};
type Grades = { kinds?: Grade[]; minDays?: number; note?: string };

async function load(): Promise<Grades | null> {
  try {
    const res = await fetch(`${DAEMON}/api/public/grades`, {
      cache: "no-store",
      headers: { Accept: "application/json" },
      signal: AbortSignal.timeout(6_000),
    });
    if (!res.ok) return null;
    return (await res.json()) as Grades;
  } catch {
    return null;
  }
}

const pct = (v: number | undefined) => (v == null || !Number.isFinite(v) ? "" : `${(v * 100).toFixed(1)}%`);

export default async function PublicGrades() {
  const data = await load();
  const kinds = data?.kinds ?? [];
  const anyRate = kinds.some((k) => k.accuracy != null);
  return (
    <div className="mx-auto flex w-full max-w-[900px] flex-col gap-5" data-testid="public-grades">
      <header className="flex flex-col gap-2">
        <h1 className="text-[1.4rem] font-extrabold tracking-tight">Grades</h1>
        <p className="m-0 max-w-[68ch] text-[0.85rem] leading-relaxed" style={{ color: "var(--dim)" }}>
          How often each kind of forecast turned out right, next to how often simply assuming
          &ldquo;nothing changes&rdquo; would have been right on the same forecasts.
        </p>
      </header>

      <section className="panel px-5 py-5 text-[0.85rem] leading-relaxed">
        <p className="m-0 font-semibold">
          {data === null
            ? "The grades could not be read just now. Try again in a minute."
            : anyRate
              ? "Results so far, with their sample sizes."
              : "No grades yet: this public record is new."}
        </p>
        <p className="mt-2" style={{ color: "var(--dim)" }}>
          A forecast is graded once its window has passed, about a month for one-month forecasts. A
          rate is shown for a kind only after it has graded forecasts from at least{" "}
          {data?.minDays ?? 10} different days; before that a percentage would be an anecdote. These
          are raw tallies on overlapping windows, not a test of skill.
        </p>
      </section>

      {kinds.length > 0 && (
        <section className="panel" aria-label="grades by forecast kind">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-[0.8rem]">
              <thead>
                <tr style={{ color: "var(--faint)", borderBottom: "1px solid var(--border)" }}>
                  <th scope="col" className="px-4 py-2 font-medium">forecast</th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">still open</th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">graded</th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">days</th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">compared</th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">right</th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">baseline right</th>
                </tr>
              </thead>
              <tbody className="tnum">
                {kinds.map((k) => (
                  <tr key={k.kind} style={{ borderBottom: "1px solid var(--border)" }}>
                    <th scope="row" className="px-4 py-2 font-semibold">{KIND_LABEL[k.kind] ?? k.kind}</th>
                    <td className="px-3 py-2 text-right">{k.open.toLocaleString("en-US")}</td>
                    <td className="px-3 py-2 text-right">{k.graded.toLocaleString("en-US")}</td>
                    <td className="px-3 py-2 text-right">{k.days.toLocaleString("en-US")}</td>
                    {k.accuracy != null ? (
                      <>
                        <td className="px-3 py-2 text-right">{(k.compared ?? k.graded).toLocaleString("en-US")}</td>
                        <td className="px-3 py-2 text-right">{pct(k.accuracy)}</td>
                        <td className="px-3 py-2 text-right">{pct(k.baseline) || "—"}</td>
                      </>
                    ) : (
                      <td colSpan={3} className="px-3 py-2 text-right" style={{ color: "var(--faint)" }}>
                        {k.status}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      <section className="panel px-5 py-4 text-[0.8rem] leading-relaxed" style={{ color: "var(--dim)" }}>
        Built only from IEX exchange data.{" "}
        <a href="https://www.iex.io/legal/hist-data-terms" target="_blank" rel="noreferrer" className="hover:underline">
          Data provided for free by IEX.
        </a>{" "}
        SignalDeck also keeps an older research record measured on licensed market data; its figures
        are not shown on this site because that licence does not allow it.{" "}
        <Link href="/forecasts" className="hover:underline" style={{ color: "var(--accent)" }}>
          See the current forecasts
        </Link>
      </section>
    </div>
  );
}
