import { HYPOTHETICAL_NOTE, HYPOTHETICAL_SHORT, LIVE_GRADED_NOTE } from "@/lib/hypothetical";
import { COPY } from "@/lib/demoCopy"; // a demo build's own words (its figures are synthetic)

/**
 * The one hypothetical-performance disclosure. `short` for a line beside a
 * backtest figure; `live` beside a live graded number, which is not hypothetical.
 */
export default function HypotheticalNote({
  short = false,
  live = false,
  className = "",
}: {
  short?: boolean;
  live?: boolean;
  className?: string;
}) {
  return (
    <p
      className={`m-0 text-[0.72rem] leading-relaxed ${className}`}
      style={{ color: "var(--faint)" }}
      data-testid="hypothetical-note"
    >
      {COPY.demo
        ? live ? COPY.liveGradedNote : short ? COPY.hypoShort : COPY.hypoNote
        : live ? LIVE_GRADED_NOTE : short ? HYPOTHETICAL_SHORT : HYPOTHETICAL_NOTE}
    </p>
  );
}
