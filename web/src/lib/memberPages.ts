import { isPublicRoute } from "@/lib/publicRoutes";

/**
 * The pages a MEMBER may open. Each is built only on routes in the daemon's
 * memberRoutes (daemon/internal/api/accounts.go), which is the authority: this
 * list only keeps the browser from walking into operator pages that would
 * render a wall of 403s. Shell sends members home from anything else, and
 * HubTabs hides the tabs that lead there. /ask renders the daemon's refusal
 * while SIGNALDECK_MEMBER_COPILOT is off, and the Shell hides its nav link.
 */
const MEMBER_PAGES = ["/today", "/market/regimes", "/market/breadth", "/watchlist", "/journal", "/ask", "/account"];

export function memberMayVisit(pathname: string): boolean {
  return isPublicRoute(pathname) || MEMBER_PAGES.includes(pathname) || pathname.startsWith("/s/");
}
