# SignalDeck MCP Server — Superprompt

Build an MCP server that lets any AI client (Claude Code, Codex, Cursor, or a third party's own agent) consult SignalDeck for **methodology guidance and current regime verdicts**, while making the underlying dataset and research machinery non-extractable.

**Location:** new Go package `daemon/internal/mcp/`, served by the existing daemon. Reuse what is already there — do not rebuild it.

---

## READ FIRST — two constraints that govern every decision

### 1. This reverses a prior decision. Handle it deliberately.
SignalDeck has been **deliberately unpublished**: never deployed, loopback-only, and the LLM council explicitly rejected public exposure on the grounds that a live money-adjacent predictor "reads as compliance-unaware." Exposing an MCP endpoint to third parties re-opens exactly that.

It is defensible **only** in a strictly advisory shape, and the build must enforce that shape rather than rely on wording:
- The server **never** returns a trade recommendation, target price, entry/exit, position size, or anything framable as a signal.
- Every response carrying a number also carries its conviction band, sample size, and caveat. A bare accuracy figure is a bug.
- Every response includes a machine-readable `disclaimer` field: research output, not investment advice.
- **Never headline "83% accurate."** That is the base rate — the naive "it persists" answer scores the same. The honest and stronger claim is **discrimination: ~73% at low conviction vs ~98% at very-high, a ~24.7pp spread that held across 24 quarters and survived a survivorship-clean universe of 54,969 observations.**
- The live directional record (**46.7%, negative Brier skill**) must be retrievable and must never be hidden.

### 2. You cannot make a useful advisory interface provably leak-free.
Any interface that answers questions about data is a channel that leaks it; enough queries reconstruct it. The goal is **bounded, auditable, expensive-to-abuse disclosure** — not zero. Design honestly against that, and say so in the README rather than claiming a guarantee the architecture cannot deliver.

---

## What already exists — reuse, don't reinvent

| Asset | Path | Use for |
|---|---|---|
| Data-license classifier | `daemon/internal/datalicense/datalicense.go` | The raw-vs-derived boundary, already enforced on `/api/bars` (HTTP 451) |
| Per-client token bucket | `daemon/internal/api/ratelimit.go` | In-memory, two-tier (read/write), lazily evicted — extend, don't replace |
| Body caps + CSRF | `daemon/internal/api/security.go` | `maxBodyBytes` 128 KB pattern |
| Auth | `daemon/internal/api/auth.go` | Existing login/session primitives |
| Bind-derived exposure | `daemon/internal/config` (`loopbackOnly`, `PublicReads`) | Safe-by-default posture; MCP must inherit it |

**The boundary already drawn in `datalicense/` is the right one and the MCP must inherit it verbatim:** raw records are protected, derived insight is shareable. Licensed sources (alpaca, cryptohist, cryptolive, hyperliquid, news) are use-yes/redistribute-no. Restricted sources (tvscanner, stocktwits) are personal-use only and must never reach a third party through any tool. Public sources (edgar, fred, finra, cftc, cboe, congress, wikimedia) are free to redistribute.

Note: a root `Dockerfile` and Render blueprint are referenced in older notes but are **not present in the repo** — verify before assuming any deployment path exists.

---

## Six layers of defense

Each layer must fail **closed** and be independently testable. A layer that can only be verified by reading the code is not a layer.

**Layer 1 — Identity and transport.** Streamable HTTP transport (stdio also supported for local use). TLS required for any non-loopback bind. Every client holds a distinct, revocable credential — OAuth 2.1 client credentials per the MCP authorization spec, or signed API keys with an expiry. No anonymous access when not on loopback; inherit `loopbackOnly()` so a reachable bind cannot silently serve openly.

**Layer 2 — Authorization and a closed tool surface.** Default-deny scopes per client. **No tool accepts free-form code, SQL, expressions, file paths, or arbitrary query structures** — every parameter is a typed enum or a validated symbol, rejected on anything unrecognized. A connected AI must not be able to use this server as a compute or code-execution surface; it may only invoke the fixed, enumerated tools below.

**Layer 3 — Data boundary (the important one).** A single serialization chokepoint every response passes through, built as an **allowlist**: fields not explicitly permitted are dropped, not passed. Raw bars, per-symbol historical series, ledger row dumps, and anything classified Licensed or Restricted must be structurally unrepresentable in an MCP response type — enforced by the type system where possible, by a validated schema otherwise. Add a test that fails the build if a new field reaches a response without an allowlist entry.

**Layer 4 — Budget, with latency as a hard requirement.** Extend the existing token bucket: per-client request rate, a per-response result cap, and a rolling daily aggregate budget. **Rate limiting must not cost latency** — keep it in-memory and lock-cheap, and serve verdicts from a precomputed cache refreshed by the existing workers rather than computing per request. Target **p95 < 200ms**, and make the limiter's own overhead sub-millisecond. Return proper MCP errors with retry-after rather than hanging.

**Layer 5 — Audit and anomaly detection.** Log every call: client identity, tool, parameter hash, response size, timestamp. Detect extraction patterns — systematic enumeration across symbols, monotonic parameter sweeps, sustained max-rate usage — and auto-throttle plus flag. Append-only, in the same spirit as the prediction ledger.

**Layer 6 — Provenance.** Per-client response watermarking, so output that surfaces elsewhere is traceable to the client that pulled it. Keep it semantically neutral: vary insignificant formatting or ordering, never the numbers themselves. Document that it exists — deterrence is most of the value.

---

## Tool surface (start here; add only with a written reason)

**Advisory — no dataset access, effectively zero extraction risk:**
- `explain_methodology(topic)` — matched nulls, non-overlapping sampling, conviction banding, walk-forward, survivorship, pre-registration. Pure exposition.
- `critique_research_design(description)` — reviews a user's *own* proposed study for the failure modes SignalDeck has already hit: overlapping windows, unmatched nulls, day-0 conditioning, population-average accuracy attribution, survivorship contamination.

**Current verdicts — banded, capped, caveated, never historical:**
- `get_regime_verdict(symbol)` — today's structural verdict with conviction band, band-specific accuracy, sample size, caveat. Never a series, never a forecast history.
- `get_track_record()` — the honest aggregate, including the negative-skill directional record.
- `list_validated_findings()` — what survived and what was killed (52-week-high magnet negative vs matched null; gap-fill pulled for the day-0 conditioning bug). Rejections carry equal weight.
- `get_preregistration()` — the six predictors frozen twelve days before first grade, with chain verification status.

**Explicitly forbidden — do not implement:** `query`, `sql`, `eval`, `run_backtest` with a caller-supplied spec, `get_bars`, `get_history`, `export`, or any bulk/list-all accessor over symbols or dates.

---

## Deliverables

- `daemon/internal/mcp/` — server, tool definitions, allowlist serializer, per-client budget, audit sink.
- Tests per layer, including: the allowlist drops an unlisted field; a Licensed/Restricted source cannot reach a response; a non-loopback bind refuses anonymous access; the limiter's overhead stays sub-millisecond; enumeration is detected.
- `MCP_SERVER.md` — how to connect from Claude Code and Codex, the scope model, and an honest statement of what this design does and does not guarantee.
- Do **not** `git commit`, do **not** deploy, and do **not** expose a public endpoint. Leave the diff for review — going live is a separate decision with compliance implications.

## Verification before claiming done

1. `go build ./...` and the full daemon suite green.
2. Connect a real MCP client over stdio and exercise every tool; paste actual responses.
3. Attempt extraction adversarially: enumerate symbols, sweep parameters, request raw bars through each tool, and try to smuggle a query/expression through every parameter. Show it failing closed each time.
4. Measure p95 latency under rate limiting and report the number.
5. Confirm no response contains a Licensed or Restricted raw record.
