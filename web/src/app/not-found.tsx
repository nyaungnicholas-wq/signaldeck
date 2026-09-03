import Link from "next/link";

/**
 * A real 404. Without this file Next serves its stock white page, outside the
 * app's own dark chrome, which on a public deployment reads as a broken site
 * rather than a wrong URL.
 */
export const metadata = { title: "Not found - SignalDeck" };

export default function NotFound() {
  return (
    <div className="flex flex-col gap-4 pt-10">
      <div
        className="mono text-[0.7rem] uppercase tracking-[0.2em]"
        style={{ color: "var(--accent)" }}
      >
        404
      </div>
      <h1 className="m-0 text-[1.6rem] font-bold">That page does not exist.</h1>
      <p className="m-0 max-w-[56ch] text-sm leading-relaxed" style={{ color: "var(--dim)" }}>
        The link may be out of date. Everything published here is reachable from the
        front page.
      </p>
      <div className="flex flex-wrap gap-3 pt-1">
        <Link href="/" className="chip" style={{ padding: "0.55rem 0.9rem" }}>
          Front page
        </Link>
        <Link href="/accuracy" className="chip" style={{ padding: "0.55rem 0.9rem" }}>
          The grades
        </Link>
        <Link href="/proof" className="chip" style={{ padding: "0.55rem 0.9rem" }}>
          The receipts
        </Link>
      </div>
    </div>
  );
}
