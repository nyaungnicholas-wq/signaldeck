"use client";

import AskData from "@/components/AskData";

// Ask the data (daemon plan step 10): answers only from SignalDeck's own
// tables, every claim citing a row. Members reach it only when the daemon says
// it is available to them (SIGNALDECK_MEMBER_COPILOT); otherwise the page shows
// the daemon's refusal.
export default function AskPage() {
  return <AskData />;
}
