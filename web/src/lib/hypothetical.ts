// Publisher-guardrail copy (plan step 6), kept as plain strings so node --test
// can pin the wording (it strips types but cannot load JSX). Every surface that
// shows a member or visitor a paper P&L or a backtest-accuracy number renders
// one of these through components/HypotheticalNote.tsx; change the words here,
// once, never inline.

export const HYPOTHETICAL_NOTE =
  "Hypothetical performance. Simulated paper results, not trades in a real account; costs are " +
  "estimated. Hypothetical results have inherent limitations and do not predict future results.";

/** The compact form, for a line sitting right next to a backtest-accuracy figure. */
export const HYPOTHETICAL_SHORT =
  "Backtest accuracy is hypothetical: measured on past data, not trades in a real account, and it does not predict future results.";

export const WHAT_SIGNALDECK_IS =
  "SignalDeck publishes the same statistical forecasts to every member. It does not know your " +
  "portfolio and does not tell you what to buy or sell.";

/** How the per-symbol classifier is named to members: what it is, not a persona. */
export const SYMBOL_MODEL_LABEL = "This symbol's model";
export const SYMBOL_MODEL_DESCRIPTION =
  "a statistical classifier on its own price and volume history";
