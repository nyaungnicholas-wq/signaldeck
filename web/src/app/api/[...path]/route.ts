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
const MAX_BODY_BYTES = 128 * 1024; // mirrors maxBodyBytes in daemon/internal/api/security.go; the two must move together

// Allowlists — nothing else crosses the boundary in either direction.
// x-forwarded-for: without it the daemon saw 127.0.0.1 for every browser
// request, so SIGNALDECK_TRUST_PROXY was a knob that could not work — turning
// it on changed nothing because the header never arrived, and every
// unauthenticated visitor shared one rate-limit bucket keyed "ip:127.0.0.1".
// The daemon still ignores this header unless TRUST_PROXY is explicitly set,
// which is correct: a spoofable header must only be believed when the daemon
// is unreachable except through this proxy.
const REQUEST_HEADERS = [
  "content-type",
  "x-signaldeck",
  "cookie",
  "origin",
  "accept",
  "x-forwarded-for",
] as const;
// location: fetch() runs with redirect:"manual", so an upstream 3xx must
// carry its Location through or the browser gets an unfollowable redirect.
const RESPONSE_HEADERS = ["content-type", "retry-after", "cache-control", "location"] as const;

function cappedBody(body: ReadableStream<Uint8Array>, onOverflow: () => void): ReadableStream<Uint8Array> {
  // A Content-Length check alone cannot do this job, because a chunked upload declares no length
  // and a dishonest one declares the wrong one, so the only limit that holds is the one that counts
  // the bytes as they arrive.
  let total = 0;
  // pipeThrough, not `new TransformStream(...).readable`: the readable side of a
  // TransformStream nobody writes into never yields a byte and never closes, so
  // the upstream fetch sits there until its own 60s timeout fires. Measured —
  // the request reached the backend as nothing at all and the log said
  // TimeoutError, which reads like a slow daemon rather than a wiring bug.
  return body.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, controller) {
        total += chunk.byteLength;
        if (total > MAX_BODY_BYTES) {
          onOverflow();
          controller.error(new Error(`request body exceeds ${MAX_BODY_BYTES} bytes`));
        } else {
          controller.enqueue(chunk);
        }
      },
    }),
  );
}

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
  // Private local workspace (ops/start-local-workspace.ps1, bound to 127.0.0.1):
  // present the shared key so the daemon can grant loopback-only raw data.
  // The daemon honours it only from a loopback socket, an unset key matches
  // nothing, and x-signaldeck-local is NOT in REQUEST_HEADERS so a client copy
  // never passes through. x-forwarded-for stays intact: deleting it (the old
  // scheme) made the exception copy-pasteable onto a wildcard bind.
  if (process.env.SIGNALDECK_LOCAL_ONLY_PROXY === "1" && process.env.SIGNALDECK_LOCAL_PROXY_KEY) {
    headers.set("x-signaldeck-local", process.env.SIGNALDECK_LOCAL_PROXY_KEY);
  }
  // Deliberately do NOT attach SIGNALDECK_API_TOKEN here. The daemon's
  // resolveUser() checks the session cookie first and falls back to the bearer
  // token, which maps to the ADMIN user. Attaching the bearer unconditionally
  // therefore inverted the precedence: a logged-in visitor got their own
  // identity, but an ANONYMOUS visitor was handed admin — reading and mutating
  // the admin's watchlist/portfolio and burning LLM budget, which also defeated
  // SIGNALDECK_PUBLIC_READS=false. The browser's credential is the session
  // cookie, and it is already forwarded via REQUEST_HEADERS above. The bearer
  // token remains for non-browser clients that call the daemon directly.

  const hasBody = req.method !== "GET" && req.method !== "HEAD";

  // Cheap case: a client that declares an oversized upload is refused before a byte of it is read.
  if (hasBody) {
    const lengthHeader = req.headers.get("content-length");
    if (lengthHeader !== null) {
      const length = Number.parseInt(lengthHeader, 10);
      if (Number.isFinite(length) && length > MAX_BODY_BYTES) {
        return Response.json({ error: "request body too large" }, { status: 413 });
      }
    }
  }

  // Bound the upstream wait — a hung daemon connection must not pin this route
  // forever. 60s leaves headroom for slow AI chat/filing.
  //
  // Created HERE, before anything touches the body. It used to sit in the fetch
  // object literal, which is evaluated only after `await req.arrayBuffer()`
  // returns — so the one phase it could not bound was the upload.
  const signal = AbortSignal.timeout(60_000);

  let overflowed = false;
  let bodyToSend: undefined | ReadableStream<Uint8Array> = undefined;
  if (hasBody && req.body !== null) {
    // The body is STREAMED rather than buffered, so an upload is bounded as it arrives
    // instead of after it has already been held in memory.
    bodyToSend = cappedBody(req.body, () => { overflowed = true; });
  }

  // duplex: "half" is the opt-in a streamed request body requires. It is not in
  // the DOM RequestInit type, hence the intersection.
  const init: RequestInit & { duplex?: "half" } = {
    method: req.method,
    headers,
    redirect: "manual",
    cache: "no-store",
    signal,
    ...(bodyToSend !== undefined ? { body: bodyToSend, duplex: "half" as const } : {}),
  };

  let res: Response;
  try {
    res = await fetch(upstream, init);
  } catch (err) {
    // A body the cap cut off aborts this fetch. Reporting that as an
    // unreachable daemon would blame the backend for a request THIS tier
    // refused, and would hide the refusal from anyone reading the logs.
    if (overflowed) {
      return Response.json({ error: "request body too large" }, { status: 413 });
    }
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
