"use client";

import MemberJournal from "@/components/MemberJournal";

// The member call journal (daemon plan step 8): a member's OWN calls, graded by
// the rules SignalDeck grades itself with. Per-user; never a SignalDeck forecast.
export default function JournalPage() {
  return <MemberJournal />;
}
