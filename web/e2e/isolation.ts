/**
 * The specs that sign in WRITE: an account, paper orders, watchlist rows. The
 * web tier proxies /api to SIGNALDECK_DAEMON and defaults to 127.0.0.1:8322,
 * which on this host is PRODUCTION, so `npm run e2e` used to write test data
 * into the live database. They now refuse to run unless SIGNALDECK_DAEMON names
 * an isolated daemon on an explicit port other than 8322. (release-smoke mocks
 * every /api call and does not sign in, so the release gate is unaffected.)
 */
export const PRODUCTION_DAEMON_PORT = "8322";

export function isolatedDaemon(env: Record<string, string | undefined> = process.env): string {
  const raw = (env.SIGNALDECK_DAEMON ?? "").trim();
  let port = "";
  try {
    port = new URL(raw).port;
  } catch {
    port = "";
  }
  if (!raw || !port || port === PRODUCTION_DAEMON_PORT) {
    throw new Error(
      "e2e REFUSED: these specs write to the daemon. Set SIGNALDECK_DAEMON to an isolated daemon " +
        `on an explicit port other than ${PRODUCTION_DAEMON_PORT} (got ${raw ? JSON.stringify(raw) : "nothing, which means production"}).`,
    );
  }
  return raw;
}
