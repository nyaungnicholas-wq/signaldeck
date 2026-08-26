import type { Metadata } from "next";

// /proof is a CLIENT component, so it cannot export metadata itself and fell
// through to the root layout's default — every tab, bookmark and link preview
// for the one URL built to be shared read "Dashboard — SignalDeck". This is the
// same sibling-layout pattern the other client routes already use; proof was
// simply the one segment missing it.
//
// The bare name relies on the root template ("%s — SignalDeck"), so the tab
// reads "The receipts — SignalDeck" and matches the page's own heading rather
// than inventing a second name for it.
export const metadata: Metadata = {
  title: "The receipts",
  description:
    "SignalDeck's live, out-of-sample record: a tamper-evident hash chain of every committed prediction, recomputed on load, graded over independent symbol-day resolutions — and withheld outright when the sample is too thin to claim skill.",
};

export default function ProofLayout({ children }: { children: React.ReactNode }) {
  return children;
}
