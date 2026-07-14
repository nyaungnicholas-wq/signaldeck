// Server-side proxy for every /api/* request → the data daemon (:8322).
// Replaces the old next.config rewrite so the daemon port is never exposed
// and the server can attach a bearer token (SIGNALDECK_API_TOKEN — server
// env only, never NEXT_PUBLIC). The daemon enforces CSRF via the
// X-Signaldeck header + Origin allowlist on non-GET and auth via an
// HttpOnly session cookie, so cookie/origin/x-signaldeck MUST be forwarded
// or login and all POSTs break. Daemon rate limits (429 + Retry-After)
// pass through untouched.

import type { NextRequest } from "next/server";

export const dynamic = "force-dynamic";

const DAEMON = process.env.SIGNALDECK_DAEMON || "http://127.0.0.1:8322";

// Allowlists — nothing else crosses the boundary in either direction.
const REQUEST_HEADERS = [
  "content-type",
  "x-signaldeck",
  "cookie",
  "origin",
  "accept",
] as const;
// location: fetch() runs with redirect:"manual", so an upstream 3xx must
// carry its Location through or the browser gets an unfollowable redirect.
const RESPONSE_HEADERS = ["content-type", "retry-after", "cache-control", "location"] as const;

async function proxy(
  req: NextRequest,
  ctx: { params: Promise<{ path: string[] }> }
): Promise<Response> {
  const { path } = await ctx.params;
  const segments = path ?? [];
  // Next decodes params, so %2e%2e arrives as ".." — reject dot segments
  // outright, then re-encode each segment. The URL is built ONLY as
  // base + /api/ + segments + original query: no absolute-URL injection.
  if (segments.some((s) => s === "." || s === "..")) {
    return Response.json({ error: "invalid path" }, { status: 400 });
  }
  const upstream = `${DAEMON}/api/${segments
    .map(encodeURIComponent)
    .join("/")}${req.nextUrl.search}`;

  const headers = new Headers();
  for (const name of REQUEST_HEADERS) {
    const value = req.headers.get(name);
    if (value !== null) headers.set(name, value);
  }
  if (process.env.SIGNALDECK_API_TOKEN) {
    headers.set("Authorization", `Bearer ${process.env.SIGNALDECK_API_TOKEN}`);
  }

  const hasBody = req.method !== "GET" && req.method !== "HEAD";
  let res: Response;
  try {
    res = await fetch(upstream, {
      method: req.method,
      headers,
      body: hasBody ? await req.arrayBuffer() : undefined,
      redirect: "manual",
      cache: "no-store",
      // Bound the upstream wait — a hung daemon connection must not pin
      // this route forever. 60s leaves headroom for slow AI chat/filing.
      signal: AbortSignal.timeout(60_000),
    });
  } catch (err) {
    console.error(`[api-proxy] ${req.method} ${upstream} failed:`, err);
    return Response.json({ error: "daemon unreachable" }, { status: 502 });
  }

  const out = new Headers();
  // set-cookie can repeat (login/logout) — append each one individually.
  for (const cookie of res.headers.getSetCookie()) out.append("set-cookie", cookie);
  for (const name of RESPONSE_HEADERS) {
    const value = res.headers.get(name);
    if (value !== null) out.set(name, value);
  }
  // API responses default to no-store unless the daemon says otherwise —
  // never let the browser heuristically cache market data or auth state.
  if (!out.has("cache-control")) out.set("cache-control", "no-store");
  return new Response(res.body, { status: res.status, headers: out });
}

export {
  proxy as GET,
  proxy as POST,
  proxy as PUT,
  proxy as DELETE,
  proxy as PATCH,
  proxy as OPTIONS,
};
