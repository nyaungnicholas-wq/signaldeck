// A demo build (NEXT_PUBLIC_DEMO_DATA=1, ops/demo-run.ps1) tells every viewer its
// figures are synthetic. That holds only while every daemon answer comes from
// tools/demo/demo_daemon.py, which stamps `x-signaldeck-demo: synthetic` on each
// reply. Served any other way (SIGNALDECK_DAEMON unset means the live daemon on
// :8322; a cwd of web/ means the real data/accuracy_registry.json) it would show
// REAL figures under that banner, so a demo build refuses anything else. The
// banner is inlined at build time (next.config.ts env): the build decides this,
// not how it is launched. Outside a demo build it is "" and nothing here applies.

export const NOT_DEMO_DATA = "not demo data";

/** True when a demo build got a daemon answer that is not the demo stub's. */
export function isNotDemoData(res: { headers: Headers }, banner = process.env.SIGNALDECK_DEMO_BANNER): boolean {
  return Boolean(banner) && res.headers.get("x-signaldeck-demo") !== "synthetic";
}

/** fetch() for server-side daemon reads. In a demo build a non-stub answer throws. */
export async function daemonFetch(url: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(url, init);
  if (isNotDemoData(res)) {
    await res.body?.cancel();
    throw new Error(`${NOT_DEMO_DATA}: a demo build reads only the demo stub (tools/demo/demo_daemon.py)`);
  }
  return res;
}

/** In a demo build only the stub's own registry file (demo: true) may be read. */
export function isUsableRegistry(reg: unknown, banner = process.env.SIGNALDECK_DEMO_BANNER): boolean {
  return !banner || (typeof reg === "object" && reg !== null && (reg as { demo?: unknown }).demo === true);
}
