import type { Metadata } from "next";
import PagePurpose from "@/components/PagePurpose";
import HealthBoard from "@/components/health/HealthBoard";

// Bare page name — the root layout appends " — SignalDeck" via its template.
export const metadata: Metadata = { title: "Health" };

// SignalDeck already grades its forecasts in public on /lab/track-record. This
// page applies the same rule to the product itself: measure it, publish the
// number, name the weakest part, and say what the fix is.
export default function HealthPage() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-[0.9rem] font-extrabold tracking-[0.14em]">
        How usable is SignalDeck?
      </h1>
      <PagePurpose
        id="health"
        text="SignalDeck checks itself the same way it checks its forecasts. This page shows the score, what it measured, which part is weakest, and the one change that would help most."
      />
      <HealthBoard />
    </div>
  );
}
