# SignalDeck — "Best Platform" Integration Plan (2026-07-02)

Folding the quant-suite capabilities (CopilotQuant, RiskLens, FilingMind) +
an AI analyst + a real forecast model + new data feeds + delivery features
into SignalDeck — natively, on top of its growing honest database.

## Decisions (from the interview)
- **AI analyst agent:** YES — LLM agent that reads stored data, writes
  narrative reads + flags setups. (Needs an API key — see blocker.)
- **Prediction depth:** honest expectancy (already built) **+ a real
  backtested forecast model**, each with its own honesty grade.
- **New data:** news+sentiment, macro/econ calendar, options flow,
  fundamentals — all four.
- **Delivery:** alerts, daily briefing, chat-with-your-data, correlation +
  paper portfolio.
- **quant-suite:** bring CopilotQuant + RiskLens + FilingMind in as **native
  SignalDeck features** (not a file copy of the separate TS monorepo, which
  is a different stack and only partly built).

## Blocker
No LLM key present on the machine. LLM-dependent features (analyst, chat,
FilingMind, news-sentiment) are built behind a provider abstraction and stay
in a safe no-op "add a key to activate" state until a key is provided at
`SIGNALDECK_GEMINI_KEY` (or an OpenAI/Anthropic equivalent).

## Architecture fit
Everything hangs off the existing Go daemon + SQLite store + worker fleet +
JSON API + Next.js app. New engines are pure Go packages; new agents are
workers; new views are pages. The honesty brand holds: every model ships its
own out-of-sample grade, every LLM claim cites its source.

## Waves

### Wave 1 — no-key backend engines (START NOW)
- `internal/forecast` — walk-forward logistic model per symbol/horizon over
  stored features; predicts P(up); **graded out-of-sample** (Brier/accuracy),
  never shown without its grade.
- `internal/risklens` — portfolio VaR (historical + parametric), stress
  scenarios, per-holding risk contribution, plain-English driver summary.
- `internal/backtest` — bias-free backtester on stored bars (no lookahead,
  real costs, survivorship-aware as data allows) + a rule-based plain-English
  strategy parser (MA cross, RSI, breakout…); LLM parser upgrades it later.
- `internal/portfolio` — correlation matrix + paper-position tracking that
  logs your reads and grades them.

### Wave 2 — wire Wave 1 into daemon + API + pages
Workers where periodic (forecast trainer, risk recompute), endpoints, and
new pages: /forecast, /risk, /backtest (CopilotQuant), /portfolio.

### Wave 3 — new data feeds (agents)
- macro/econ calendar (free source) → regime layer + "event soon" flags
- fundamentals (Alpaca/other free) → valuation context
- news + sentiment (LLM-tagged — needs key)
- options flow (paid source — flagged; stub until source chosen)

### Wave 4 — LLM layer (needs key)
- `internal/llm` provider abstraction (Gemini/OpenAI/Anthropic)
- analyst agent (narrative market read + per-symbol thesis + watch list)
- chat-with-your-data (NL → SQL/tool calls over the DB, cited)
- FilingMind (SEC filing → cited bull/bear thesis, made-up numbers dropped)

### Wave 5 — delivery
- alerts worker (conditions → macOS notification) + /alerts builder
- daily briefing worker (templated now, LLM-upgraded with key) + /briefing

## Honesty guardrails (non-negotiable)
- Forecast + backtest results always shown with out-of-sample grade + costs.
- No lookahead: features at time t use only data ≤ t; walk-forward only.
- LLM outputs cite sources; unverifiable numbers are dropped, not shown.
- Every new agent is persisted + visible on /agents; failures surface on
  /quality.
