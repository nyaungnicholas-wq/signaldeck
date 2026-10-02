import { HYPOTHETICAL_NOTE, HYPOTHETICAL_SHORT } from "@/lib/hypothetical";

/** The one hypothetical-performance disclosure. `short` for a line beside a backtest figure. */
export default function HypotheticalNote({ short = false, className = "" }: { short?: boolean; className?: string }) {
  return (
    <p
      className={`m-0 text-[0.72rem] leading-relaxed ${className}`}
      style={{ color: "var(--faint)" }}
      data-testid="hypothetical-note"
    >
      {short ? HYPOTHETICAL_SHORT : HYPOTHETICAL_NOTE}
    </p>
  );
}
