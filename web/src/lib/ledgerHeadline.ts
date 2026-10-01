// The /proof ledger headline and the tone it is shown in.
//
// Lives here rather than inside proof/page.tsx so node --test can exercise it
// (type stripping, no JSX). The panel used to be coloured by `intact` alone, so
// a chain whose signed anchors had stopped reproducing -- positive evidence
// that history was rewritten after signing -- read green at the headline.
// `intact` only says the stored rows are internally consistent; a regenerated
// chain is consistent too, and only the failing anchor tells them apart.

export function ledgerHeadline(lv: {
  intact: boolean;
  brokenAtSeq?: number;
  tamperEvidence?: { failingAnchors?: number };
}): { tone: "ok" | "bad"; text: string } {
  if (!lv.intact) return { tone: "bad", text: `BROKEN at #${lv.brokenAtSeq}` };
  const failing = lv.tamperEvidence?.failingAnchors ?? 0;
  if (failing > 0) {
    const one = failing === 1;
    return {
      tone: "bad",
      text: `Chain consistent, but ${failing} signed anchor${one ? "" : "s"} no longer reproduce${one ? "s" : ""}`,
    };
  }
  return { tone: "ok", text: "Chain intact" };
}
