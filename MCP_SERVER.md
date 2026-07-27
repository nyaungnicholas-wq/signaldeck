# MCP_SERVER.md — connecting to SignalDeck over the Model Context Protocol

SignalDeck exposes an MCP server so an AI client can consult it for **research
methodology** and **today's structural regime verdicts**. It is deliberately
advisory: six tools, no dataset access, no query surface, no recommendations.

**Status: built, not deployed.** The HTTP transport is off unless
`SIGNALDECK_MCP_ENABLED=true`, no endpoint is published, and going live is a
separate decision with compliance implications. Nothing in this document should
be read as a statement that the endpoint is running.

---

## What this design does and does not guarantee

Start here, because the honest version is shorter than the reassuring one.

**It does not guarantee zero leakage.** Any interface that answers questions
about data is a channel that leaks it, and enough queries reconstruct some of
it. A server that answers "what is AAPL's regime today" a thousand times has
disclosed a thousand facts about the dataset, and no amount of layering changes
that arithmetic. Claiming otherwise would be the dishonest part of an
architecture whose entire pitch is honesty about what it measures.

**What it does guarantee — because each is enforced in code and tested:**

- Raw records cannot be represented in a response. Not "are not returned":
  cannot be. The serialization chokepoint accepts only plain maps of
  allowlisted field paths, drops any Go struct wholesale, drops any object
  carrying three or more bar fields, and drops any numeric list long enough to
  be a series.
- No tool accepts code, SQL, an expression, a path, a projection, a date range,
  a limit, or any other query structure. Unknown arguments are refused, not
  ignored.
- Licensed and Restricted sources (per `internal/datalicense`) cannot surface,
  including inside free text.
- Every number ships with its conviction band, its sample size, and its caveat.
  A bare accuracy figure fails a test.
- The platform's failing live directional record is retrievable and cannot be
  hidden — it appears even on a daemon whose grading worker has never run.
- Every call is attributable and recorded append-only; extraction-shaped usage
  is detected and auto-throttled; output is watermarked per client.

**What it buys, then, is bounded, auditable, expensive-to-abuse disclosure.**
Verdicts arrive banded rather than continuous, so the finest thing a client can
learn about a symbol is which of four buckets it falls in. There is no listing
of the universe, so a client must already know a ticker to ask about it. And a
client that walks tickers is flagged after 20 distinct symbols and throttled to
a quarter of its budget.

**Known residual risks, stated rather than buried:**

- A determined client with 500 calls a day can learn 500 banded verdicts a day.
  Over months that is a meaningful shadow of the regime table. The daily budget
  sets the exchange rate; it does not set it to zero.
- Watermarking is deterrence, not prevention. It identifies whose copy leaked;
  it cannot stop the copy.
- A signed stateless key cannot be pinned to one machine. Two colluding
  clients sharing a key look like one busy client — which is why the anomaly
  detector watches shape, not just volume.
- The `critique_research_design` checklist is shallow keyword matching. Its
  response says so. A clean result is not evidence a design is sound.

---

## The six tools

| Tool | Scope | What it returns |
|---|---|---|
| `explain_methodology(topic)` | `methodology` | Fixed exposition on one of: matched nulls, non-overlapping sampling, conviction banding, walk-forward, survivorship, pre-registration. No data is consulted. |
| `critique_research_design(description)` | `methodology` | Your own study description matched against a fixed checklist of failure modes this platform has hit. Never executed, never stored, never echoed back. |
| `get_regime_verdict(symbol)` | `verdicts` | Today's structural verdict for one symbol: regime, conviction **band**, that band's measured accuracy, sample size, horizon, caveat, tradeability. Never a series, a history, a price, or a forecast for another date. |
| `get_track_record()` | `record` | The honest aggregate, including the retired directional ensemble's negative live result and negative Brier skill. |
| `list_validated_findings()` | `record` | What survived validation and what was killed, with the reason for each kill. Rejections carry equal weight. |
| `get_preregistration()` | `record` | The frozen, hash-chained per-predictor claims plus the chain's verification status. |

**Deliberately not implemented, and refused by name:** `query`, `sql`, `eval`,
`exec`, `run_backtest`, `get_bars`, `get_history`, `export`, `list_symbols`,
`get_series`, `read_file`, `search`, `dump`, and any bulk accessor over symbols
or dates. A test asserts the surface is exactly the six above.

### How to read what comes back

- **Never quote the 83% aggregate.** It is the persistence base rate; the naive
  "the regime continues" answer scores the same. The real claim is
  **discrimination**: 72.9% at low conviction against 97.6% at very-high — a
  24.7pp spread across 24 quarters on a survivorship-clean universe of 54,969
  independent observations. Every response carrying the claim also carries this
  as `headlineGuard`.
- **Accuracy is not return, and at the top trend band they are inverted.** The
  most accurate band (97%+) has a *negative* mean forward 21-day return, because
  high conviction means price is already far from its 200-day average and
  extended names mean-revert. `tradeability` says so on every trend row.
- **Every structural number is a backtest** until the first gradable date,
  2026-08-07. `evidence` and `evidenceCaveat` say so on every verdict.
- **The one live result is a failure.** 46.7% over 8,191 independent
  symbol-days with negative Brier skill, auto-retired by the health gate.
  Anyone quoting SignalDeck as predictive of price is quoting the part that was
  already switched off.

---

## Connecting

### Claude Code (stdio, local)

```bash
claude mcp add signaldeck -- /Users/you/claude\ code/signaldeck/daemon/bin/signaldeck-mcp
```

Build the binary first:

```bash
cd ~/claude\ code/signaldeck/daemon && go build -o bin/signaldeck-mcp ./cmd/signaldeck-mcp
```

A stdio client is the operator on their own machine, so it runs as
`stdio-local` with all three scopes — still budgeted, still audited.

### Codex (stdio, local)

Add to `~/.codex/config.toml`:

```toml
[mcp_servers.signaldeck]
command = "/Users/you/claude code/signaldeck/daemon/bin/signaldeck-mcp"
```

### Any MCP client over Streamable HTTP

Off by default. To enable on the daemon:

```bash
SIGNALDECK_MCP_ENABLED=true SIGNALDECK_MCP_SECRET=$(signaldeck-mcp new-secret) signaldeckd
```

Then mint a per-client key:

```bash
signaldeck-mcp mint -client partner-a -scopes methodology,record -ttl 720h
```

The client sends `POST /mcp` with `Authorization: Bearer sdmcp_...`. POST only —
this server never initiates a message, so `GET` returns 405 rather than an
empty stream, and JSON-RPC batches are refused (one admission check per
request).

### Verifying the connection

```bash
npx @modelcontextprotocol/inspector --cli --transport stdio ./bin/signaldeck-mcp --method tools/list
```

---

## The scope model

Three scopes, default-deny. A key carries exactly what it was minted with; a
key with no scopes can call nothing, and a scope this build does not recognise
is dropped rather than carried forward to a future tool.

| Scope | Grants |
|---|---|
| `methodology` | `explain_methodology`, `critique_research_design` |
| `verdicts` | `get_regime_verdict` |
| `record` | `get_track_record`, `list_validated_findings`, `get_preregistration` |

Keys are signed (HMAC-SHA256), self-describing, and **must** expire. Revocation
is by client **identity**, not by key string, so a leaked key cannot be
resurrected by re-minting it and an operator can revoke without holding the key:

```bash
SIGNALDECK_MCP_REVOKED=partner-a,partner-b
```

Anonymous access is permitted **only** when the daemon is bound to loopback
*and* no reverse-tunnel LaunchAgent is configured (`config.ReachablePrivately`).
A tunnel publishes the process without changing the bind, and every tunnelled
request presents a loopback `RemoteAddr` — so the decision comes from the
daemon's configuration, never from the connection in front of it. **On the
current machine a tunnel agent exists, so anonymous HTTP access is already
refused and a key is required.**

---

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `SIGNALDECK_MCP_ENABLED` | `false` | Mounts `POST /mcp`. Never inherits an "open on loopback" default. |
| `SIGNALDECK_MCP_SECRET` | *(empty)* | HMAC key signing client keys. Empty ⇒ **no key can verify**. |
| `SIGNALDECK_MCP_AUDIT` | `logs/mcp_audit.jsonl` | Append-only audit sink. |
| `SIGNALDECK_MCP_DAILY_CALLS` | `500` | Per-client calls per UTC day. |
| `SIGNALDECK_MCP_REVOKED` | *(empty)* | Revoked client ids. |

Budgets: 500 calls **and** 2 MB per client per UTC day (a few large responses
and many small ones are the same extraction at different pacing), 25 items per
response, and the daemon's shared token bucket per second — the *same* limiter
instance the rest of the API uses, so a client cannot collect two budgets by
arriving through two doors.

---

## The six layers, and where each is tested

| Layer | Code | Tests |
|---|---|---|
| 1 identity + transport | `internal/mcp/auth.go`, `http.go` | `auth_test.go`, `attack_test.go` |
| 2 authorization + closed surface | `internal/mcp/tools.go`, `params.go` | `tools_test.go` |
| 3 data boundary (allowlist) | `internal/mcp/allowlist.go` | `allowlist_test.go` |
| 4 budget + latency | `internal/mcp/budget.go`, `cache.go` | `budget_test.go` |
| 5 audit + anomaly detection | `internal/mcp/audit.go` | `audit_test.go` |
| 6 provenance (watermark) | `internal/mcp/watermark.go` | `server_test.go` |

`allowlist_test.go` contains the build gate: it runs every tool for real and
fails if any emitted field lacks an allowlist entry. A new field either gets a
deliberate entry or the build goes red.

Watermarking varies only which of several equivalent closing sentences the
`disclaimer` ends with, and the rotation of order-insignificant lists. It never
varies a number, a band, a sample size, a verdict, or a caveat — a test compares
two clients' verdict arrays byte-for-byte. It is documented here because
deterrence is most of its value.

Measured on this machine (2026-07-27): **p95 484µs, p99 673µs** for
`get_regime_verdict` over 1000 HTTP round trips with the limiter and budget
engaged, against a 200ms requirement. The limiter's own admission cost is under
0.1µs per call.

---

## Reusing rather than rebuilding

`internal/mcp` imports neither `internal/api` nor `internal/config`. The two
primitives it reuses from them — the per-client token bucket
(`api/ratelimit.go`) and the constant-time credential compare (`api/auth.go`) —
are injected as functions by `internal/api/mcpmount.go`, which keeps the reuse
real without an import cycle and leaves the package testable with no HTTP server
and no database. The raw-versus-derived boundary is `internal/datalicense`'s,
imported directly, so the classification has exactly one home.
