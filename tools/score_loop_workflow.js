export const meta = {
  name: 'signaldeck-score-loop',
  description: 'Drive all 10 SignalDeck rubric boards to >=80 on METHOD RIGOR (never on predictive accuracy), looping until every board is either >=80 or provably BLOCKED',
  phases: [
    { title: 'Research' },
    { title: 'Plan' },
    { title: 'Fix' },
    { title: 'Verify' },
    { title: 'Score' },
  ],
}

// Categories from HOSTILE_REVIEW_FIX_SUPERPROMPT.md Section 4 / audits/2026-07-26-reaudit.md Section 3
const CATEGORIES = [
  'Overall architecture',
  'Research quality',
  'Statistical validity',
  'Software engineering',
  'Production readiness',
  'Institutional readiness',
  'Competitive moat',
  'Scalability',
  'Explainability',
  'Scientific credibility',
]

const TARGET = 80
const MAX_ROUNDS = 8
const REPO = '~/claude code/signaldeck'

// The single most important constraint in this workflow. SignalDeck's research
// results are uniformly null by design and by proof: 1d direction is capped
// ~55% (arcsin bound + exhaustive empirical test), the live prequential record
// is 46.7% with negative Brier skill, and trend21's most-accurate band (97.2%)
// carries a NEGATIVE mean forward return. A rubric score here therefore grades
// the RIGOR OF THE METHOD, never the profitability or hit-rate of the signal.
const RIGOR_CREED = `
SCORING DEFINITION — read this before doing anything else.
These rubric scores grade METHOD RIGOR, never predictive accuracy or profitability.
An honest, well-powered NULL RESULT is TOP-TIER evidence and must score HIGH.
A high accuracy number with an unmatched null, overlapping samples, or survivorship
contamination is WORTHLESS and must score LOW.
You must NEVER propose, and must actively reject, any change that:
  - tunes a threshold/parameter to lift a reported accuracy number,
  - re-labels or re-bands data so a number clears a bar,
  - searches for a new predictor purely because current ones show no edge,
  - reworks disclosure wording so a weakness "reads better" without fixing the mechanism.
Raising a score means the underlying METHOD, CODE, DATA HANDLING, or PROCESS
actually got more rigorous. Nothing else counts. Score theater is the primary
failure mode of this loop — you are expected to refuse it out loud.
`.trim()

// OmniRoute (local gateway, localhost:20128) is a stateless text-completion
// call with NO file access and NO tools, so it can never do Research, Fix or
// Score work — those all read or edit the repo. It IS well suited to pure-text
// judgment on a payload someone else already gathered. Measured 2026-07-27:
// correctly classified both a cosmetic disclosure-only diff and a real
// non-overlapping-windows fix, ~1.2s each, free. Used here as an independent
// adversarial vote, which also buys perspective diversity, not just cost.
const OMNIROUTE = `
DELEGATION — use the free local model pool for this step's pure-text judgment.
Write your payload to a UNIQUE temp file (include your label so parallel agents
never collide), then call:
  omniroute chat --file <path> -m auto
Rules:
  - ALWAYS -m auto. Never pin a provider/model: pinned requests do not fall back,
    so one flaky upstream fails the call. auto self-adapts and is ~800ms warm.
  - Use --file, never shell-interpolate a diff or JSON into the command line.
  - Treat the reply as ONE INDEPENDENT VOTE from a fast free helper, never as
    the final answer. You own the verdict.
  - If the call errors or returns nothing usable, proceed on your own judgment
    and say the delegation failed. Never block on it, never retry more than once.
`.trim()

// Splitting research by code area is the core fix: the previous single
// 400-word agent had to summarize 10 categories across the whole repo and
// returned unusable mush. Each cluster now owns a narrow surface and names
// the files it must actually open.
const RESEARCH_CLUSTERS = [
  {
    key: 'statistical',
    categories: ['Statistical validity', 'Research quality', 'Scientific credibility'],
    surface: `daemon/internal/researchx, daemon/internal/pipeline/researchloop.go, daemon/internal/pipeline/researchledger.go, daemon/internal/structregime, daemon/internal/volregime, daemon/internal/prereg, tools/accuracy_registry.py, PREDICTION_PROCESS.md, PREREGISTRATION.md, REPRODUCE.md`,
    focus: `Sampling and inference correctness: non-overlapping windows, matched nulls, multiple-testing correction, block-clustered CIs, walk-forward causality, survivorship bias, look-ahead, per-band (not population-average) accuracy attribution, pre-registration integrity, and whether rejections are ledgered as durably as winners. ALSO: research_loop_hypotheses currently has ZERO rows despite the loop being built — determine whether the ResearchLoop worker is actually running, erroring, gated, or silently refusing (it refuses below 2,000 obs by design), and treat a research engine that has never emitted a hypothesis as a first-class Research quality defect.`,
  },
  {
    key: 'engineering',
    categories: ['Overall architecture', 'Software engineering', 'Scalability', 'Production readiness'],
    surface: `daemon/ (Go services, workers, store), web/, ops/, bin/, STORAGE.md, ARCHITECTURE_EV.md, DATA_SOURCES.md`,
    focus: `Build/test health, error handling and fail-closed behavior, worker lifecycle and idempotency, DB schema and migration safety, known perf defects (e.g. /track-record taking ~12s because VerifyLedger scans 203k rows per request), split-adjustment/backfill correctness, secrets handling, and what breaks at 10x data volume.`,
  },
  {
    key: 'institutional',
    categories: ['Institutional readiness', 'Competitive moat', 'Explainability'],
    surface: `README.md, SHIP_READINESS.md, INSTITUTIONAL_GAP.md, PREDICTION_PROCESS.md, LICENSE, audits/, ROADMAP.md`,
    focus: `What an institutional reviewer would demand and not find: reproducibility from a cold clone, data licensing and provenance, audit trail completeness, disclosure that matches mechanism, and whether claimed differentiation is defensible or just asserted. Explainability is already near target — do NOT spend effort polishing it; report it and move on.`,
  },
]

const RESEARCH_SCHEMA = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          category: { type: 'string' },
          current_score_estimate: { type: 'number' },
          weakest_evidence: { type: 'string' },
          movable: {
            type: 'boolean',
            description: 'true if a concrete code/doc/process change could raise this score; false if the gap is structural (no edge exists to find, or it needs data/credentials/a human this repo cannot supply)',
          },
          blocked_reason: {
            type: 'string',
            description: 'Required when movable is false. State plainly WHY no code change can move it.',
          },
          candidate_fixes: {
            type: 'array',
            items: {
              type: 'object',
              properties: {
                title: { type: 'string' },
                files: { type: 'array', items: { type: 'string' } },
                change: { type: 'string' },
                rigor_justification: {
                  type: 'string',
                  description: 'How this makes the METHOD more rigorous. If the only justification is "raises the number", omit the fix entirely.',
                },
              },
              required: ['title', 'files', 'change', 'rigor_justification'],
            },
          },
        },
        required: ['category', 'current_score_estimate', 'weakest_evidence', 'movable', 'candidate_fixes'],
      },
    },
  },
  required: ['findings'],
}

const PLAN_SCHEMA = {
  type: 'object',
  properties: {
    fixes: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          category: { type: 'string' },
          title: { type: 'string' },
          files: { type: 'array', items: { type: 'string' } },
          action: { type: 'string' },
          rigor_justification: { type: 'string' },
        },
        required: ['category', 'title', 'files', 'action', 'rigor_justification'],
      },
    },
    rejected: {
      type: 'array',
      description: 'Candidate fixes refused as score theater. Recording these is mandatory.',
      items: {
        type: 'object',
        properties: {
          title: { type: 'string' },
          why_rejected: { type: 'string' },
        },
        required: ['title', 'why_rejected'],
      },
    },
  },
  required: ['fixes'],
}

const VERIFY_SCHEMA = {
  type: 'object',
  properties: {
    real_change: { type: 'boolean' },
    verdict: { type: 'string' },
    build_ok: { type: 'boolean' },
    omniroute_verdict: {
      type: 'string',
      description: 'The free-pool second opinion verbatim (REAL/COSMETIC + reason), or "delegation failed" if the call did not return usable output.',
    },
    votes_agreed: {
      type: 'boolean',
      description: 'Whether your own judgment matched the OmniRoute vote. On disagreement real_change MUST be false.',
    },
  },
  required: ['real_change', 'verdict', 'omniroute_verdict'],
}

const SCORE_SCHEMA = {
  type: 'object',
  properties: {
    scores: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          category: { type: 'string' },
          score: { type: 'number' },
          rationale: { type: 'string' },
          weakest_evidence: { type: 'string' },
        },
        required: ['category', 'score', 'rationale', 'weakest_evidence'],
      },
    },
  },
  required: ['scores'],
}

// Carried across rounds so the loop never re-spends effort on an immovable board
// and never re-proposes a fix already refused as theater.
const blocked = new Map() // category -> reason
const rejectedEver = []
let round = 0
let lastScores = null
let history = []

while (round < MAX_ROUNDS) {
  round++
  const active = CATEGORIES.filter((c) => !blocked.has(c))
  if (!active.length) {
    log('Every remaining category is structurally BLOCKED — no code change can move them. Stopping.')
    break
  }
  log(`Round ${round}/${MAX_ROUNDS} — ${active.length} active, ${blocked.size} blocked`)

  const priorContext = history.length
    ? `\n\nPRIOR ROUNDS (do not repeat completed work, do not re-propose rejected items):\n${JSON.stringify(history.slice(-2))}\nAlready REJECTED as score theater: ${rejectedEver.map((r) => r.title).join('; ') || 'none'}\nAlready BLOCKED: ${[...blocked.entries()].map(([c, r]) => `${c} (${r})`).join('; ') || 'none'}`
    : ''

  phase('Research')
  const clusterFindings = await parallel(
    RESEARCH_CLUSTERS.filter((cl) => cl.categories.some((c) => active.includes(c))).map(
      (cl) => () =>
        agent(
          `${RIGOR_CREED}\n\nYou are auditing the SignalDeck repo at ${REPO}.\nYou own EXACTLY these rubric categories: ${cl.categories.join(', ')}.\nIgnore all other categories entirely.\n\nRead these surfaces before concluding anything — open the actual source, do not infer from docs alone:\n${cl.surface}\n\nFocus:\n${cl.focus}\n\nAlso run: python3 tools/accuracy_registry.py --json /tmp/acc-${cl.key}.json (report FAILED verdicts, but remember accuracy numbers are NOT what you are scoring).\n\nFor each of your categories return: an honest current score estimate, the single weakest piece of evidence, whether it is movable by a code/doc/process change, and concrete candidate fixes with exact file paths. If a category cannot be moved by any change to this repo, set movable=false and state the structural reason plainly — that is a valid and valuable answer, not a failure.${priorContext}`,
          { label: `research:${cl.key}`, phase: 'Research', schema: RESEARCH_SCHEMA, effort: 'high' }
        )
    )
  )

  const findings = clusterFindings.filter(Boolean).flatMap((r) => r.findings)
  for (const f of findings) {
    if (f.movable === false && !blocked.has(f.category)) {
      blocked.set(f.category, f.blocked_reason || 'structural, no reason given')
      log(`BLOCKED ${f.category}: ${f.blocked_reason || '(no reason)'}`)
    }
  }

  const actionable = findings.filter((f) => f.movable !== false && f.current_score_estimate < TARGET)
  if (!actionable.length) {
    log('No movable category below target this round — all remaining gaps are blocked or met.')
    history.push({ round, note: 'no actionable findings', blocked: [...blocked.keys()] })
    continue
  }

  phase('Plan')
  const plan = await agent(
    `${RIGOR_CREED}\n\n${OMNIROUTE}\n\nHere are audited findings on SignalDeck (${REPO}):\n\n${JSON.stringify(actionable)}\n\nBEFORE finalizing, screen for score theater using the free pool: write the candidate fix list (title + change + rigor_justification for each) to \`/tmp/sd-plan-r${round}.txt\` with the instruction "For each numbered candidate fix below, answer THEATER or SUBSTANTIVE and one sentence why. THEATER means it would only raise a reported number or reword a disclosure without changing the underlying method." Run \`omniroute chat --file /tmp/sd-plan-r${round}.txt -m auto\`. Treat its answers as one advisory vote: anything IT flags as THEATER that you still want to keep must have an explicit rigor_justification explaining why it is substantive. Anything you both consider theater goes in \`rejected\`.\n\nProduce a concrete fix plan for this round. Rules:\n- Every fix names exact files and the specific change. Reject anything vague.\n- Every fix carries a rigor_justification. If the only justification is "this raises the score", put it in \`rejected\` instead — recording refusals is mandatory.\n- Prioritize by (gap to ${TARGET}) x tractability. Statistical validity and Research quality are the deepest holes; Explainability is at target — do not touch it.\n- Cap at 6 fixes this round so each gets real attention.\n- Fixes touching prediction/backtest/research code paths must be listed CONSECUTIVELY and will be applied sequentially to avoid clobbering.
- HARD CONSTRAINT — the human owns version control. NEVER propose a fix whose action includes \`git commit\`, \`git push\`, staging for commit, or publishing to any remote. Those actions are forbidden in this loop and such a fix will be killed before it runs, wasting the slot. A gap whose ONLY remedy is "commit/push this" is not a fix: record it under \`rejected\` with why_rejected = "requires a human commit/push; out of scope for this loop", and instead propose the working-tree mechanism (script, test, CI file, gate) that will make that commit correct when the human makes it.${priorContext}`,
    { label: 'plan', phase: 'Plan', schema: PLAN_SCHEMA, effort: 'high' }
  )

  for (const r of plan.rejected || []) {
    rejectedEver.push(r)
    log(`REJECTED (theater): ${r.title} — ${r.why_rejected}`)
  }

  if (!plan.fixes.length) {
    log('Zero legitimate fixes this round — continuing rather than stopping; next round re-researches with updated blocked/rejected memory.')
    history.push({ round, note: 'no legitimate fixes', rejected: (plan.rejected || []).length })
    continue
  }

  // Fix -> Verify pipelined per item: each fix is adversarially checked as soon
  // as it lands, so a cosmetic edit is caught in the same round it was made.
  phase('Fix')
  const verified = await pipeline(
    plan.fixes,
    (fix) =>
      agent(
        `${RIGOR_CREED}\n\nApply this fix to the SignalDeck repo at ${REPO}.\nCategory: ${fix.category}\nTitle: ${fix.title}\nFiles: ${(fix.files || []).join(', ')}\nAction: ${fix.action}\nWhy this is a rigor improvement: ${fix.rigor_justification}\n\nHARD CONSTRAINT: never run \`git commit\`, \`git push\`, \`git add\` for commit, or publish to any remote — the human owns version control. Edit the working tree only.\n\nMake the real edit. For Go: run \`go build ./...\` and the tests for the touched package. For Python: run the relevant script/tests. Do NOT git commit. If partway through you conclude this change would only move the number without improving the method, STOP, revert your edit, and say so — that is the correct outcome.\nReport what you actually changed in under 150 words.`,
        { label: `fix:${fix.title.slice(0, 30)}`, phase: 'Fix' }
      ),
    (fixReport, fix, i) =>
      agent(
        `${RIGOR_CREED}\n\n${OMNIROUTE}\n\nAdversarially verify a change just made to ${REPO}.\nClaimed fix: ${fix.title} (${fix.category})\nFiles: ${(fix.files || []).join(', ')}\nAgent's report: ${fixReport}\n\nSteps:\n1. Run \`git diff -- ${(fix.files || []).map((f) => `'${f}'`).join(' ')}\` and read the ACTUAL current file contents. Never judge from the agent's report alone.\n2. Get an independent second vote from the free pool. NEVER send raw source code or a raw git diff to the external gateway — instead write YOUR OWN neutral prose description of what the change mechanically does (no code, no file contents) plus this instruction to \`/tmp/sd-verify-r${round}-${i}.txt\`:\n   "You are a hostile code reviewer. Below is a description of a change submitted as a fix claiming to improve statistical/methodological RIGOR. Decide whether a real MECHANISM changed or this is only wording/disclosure (score theater). Answer exactly one line: VERDICT: REAL or VERDICT: COSMETIC, then one sentence why."\n   then run \`omniroute chat --file /tmp/sd-verify-r${round}-${i}.txt -m auto\` and record the reply verbatim in omniroute_verdict.\n3. Form your OWN verdict from the diff. If you and the free pool DISAGREE, set real_change=false — disagreement means it is not clearly a real change.\n4. Confirm the build/tests still pass.\n\nDefault to real_change=false whenever uncertain. Your job is to catch a fix that only looks like a fix.`,
        { label: `verify:${fix.title.slice(0, 26)}`, phase: 'Verify', schema: VERIFY_SCHEMA, effort: 'high' }
      ).then((v) => ({ fix, fixReport, verdict: v }))
  )

  const kept = verified.filter(Boolean).filter((v) => v.verdict?.real_change)
  const theater = verified.filter(Boolean).filter((v) => !v.verdict?.real_change)
  log(`Fixes: ${kept.length} verified real, ${theater.length} rejected as cosmetic`)
  for (const t of theater) log(`  cosmetic: ${t.fix.title} — ${t.verdict?.verdict}`)

  phase('Score')
  const scoreResult = await agent(
    `${RIGOR_CREED}\n\nAct as a hostile, skeptical institutional reviewer re-scoring SignalDeck at ${REPO}, 0-100 per category, same rubric as HOSTILE_REVIEW_FIX_SUPERPROMPT.md Section 4 / audits/2026-07-26-reaudit.md Section 3.\nCategories: ${CATEGORIES.join(', ')}\n\nRe-derive every number by reading the CURRENT repo. Do not carry forward any prior audit's number unchanged. Changes verified as real this round: ${JSON.stringify(kept.map((k) => ({ c: k.fix.category, t: k.fix.title })))}. Changes rejected as cosmetic (these must NOT raise any score): ${JSON.stringify(theater.map((t) => t.fix.title))}.\n\nScore the rigor of the method, never the accuracy of the predictions. An honest null result with airtight methodology scores HIGH. Be harsh; do not inflate to be encouraging. For each category give score, one-line rationale, and the single weakest remaining piece of evidence.\n\nStructurally BLOCKED categories (score them honestly but note the ceiling): ${[...blocked.entries()].map(([c, r]) => `${c} — ${r}`).join('; ') || 'none'}`,
    { label: 'rescore', phase: 'Score', schema: SCORE_SCHEMA, effort: 'high' }
  )

  lastScores = scoreResult.scores
  history.push({
    round,
    scores: lastScores.map((s) => ({ c: s.category, s: s.score })),
    kept: kept.map((k) => k.fix.title),
    theater: theater.map((t) => t.fix.title),
  })
  log(`Scores: ${lastScores.map((s) => `${s.category}=${s.score}`).join(', ')}`)

  const belowTarget = lastScores.filter((s) => s.score < TARGET && !blocked.has(s.category))
  if (!belowTarget.length) {
    log(`All non-blocked boards >= ${TARGET}. Done.`)
    break
  }
}

return {
  rounds: round,
  scores: lastScores,
  blocked: [...blocked.entries()].map(([category, reason]) => ({ category, reason })),
  rejected_as_theater: rejectedEver,
  history,
}
