// /login — server layout so the client sign-in page can still ship metadata
// (mirrors welcome/layout.tsx; without this the route inherits the root
// "Dashboard - SignalDeck" title).
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Sign In",
};

export default function LoginLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
