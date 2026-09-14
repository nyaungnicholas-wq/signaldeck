# OWNER ACTIONS — only things I could not do, and why

Nothing here is a task I skipped for convenience. Each is an external boundary,
a decision that is yours, or an action the instruction withheld authorisation
for. Ordered by what blocks the most.

---

## 1. Deploy the daemon — the `/proof` registration section depends on it

**Why not me.** A daemon restart is a DEPLOY action under `ops/CHARTER.md`
(needs a rollback plan), the instruction did not authorise deployment, and
`CLAUDE.md` forbids restarting a live forecast-producing daemon on a HEAD move.
This candidate contains a **real daemon change**, not a web-only one, so the
standing "don't restart for web commits" rule does not settle it either way —
it is a deliberate deploy decision and it is yours.

**What changes.** `daemon/internal/api/security.go` adds `/api/prereg` to the
narrow receipts exemption, so `/proof` can render the pre-registration chain for
anonymous visitors. Until it is deployed, that section shows its honest
*unavailable* state. Also in the candidate: the `REFUSED_UNAVAILABLE` status and
the collapse-gate fail-closed change.

**Consequence of not doing it.** `/proof` keeps promising a registration it
cannot show to a stranger — the exact defect F13 was about.

**Before deploying:** the running daemon is revision `5483350`; check
`worker_runs.revision` matches HEAD afterwards (CLAUDE.md pre-flight).

---

## 2. Confirm the competition, and the pre-existing-project rule

**Why not me.** These are facts about you and a rulebook clause I could not
extract.

- Which event — CAC or FirstCommit? The instruction named neither.
- **Does the event admit pre-existing projects?** SignalDeck was not started for
  it. If prior work is excluded, nothing engineering can do makes it eligible,
  and relabelling it as new work is not an option.
- Your grade/school, district, and whether that district is participating.
- Team composition.

Confirmed already: deadline **2026-10-26**, video **1–3 min** public on
YouTube/Vimeo, **AI permitted with full disclosure**, teams up to 4.
See `COMPETITION_PACKET.md`.

---

## 3. Write your own AI-disclosure and contribution statement

**Why not me.** CAC requires AI usage to be fully disclosed, and a successful
overnight agent pass cannot establish what *you* contributed. Writing a
first-person accomplishment for you would be a fabricated claim, in a project
whose entire argument is that it does not make those.

`COMPETITION_PACKET.md` has a draft with every personal clause left as
`[NICHOLAS TO CONFIRM]`. Do not sign "I can explain how every component works"
unless it is true — the Technical Q&A section is where to start if it is not yet.

---

## 4. Decide the two red scheduled tasks

`SignalDeck Check-Grader-Health` and `SignalDeck Check-Task-Health` both report
`LastTaskResult 1`. **Not diagnosed** — I found them and ran out of night. They
are the checks that would tell you when the grader stops, so a red health-checker
is worth more attention than it looks.

```
Get-ScheduledTaskInfo -TaskName 'SignalDeck Check-Grader-Health'
Get-Content logs\*grader-health*.log -Tail 40
```

---

## 5. Public deployment, when you want it — NOT done, NOT authorised

Not attempted: no image built, no host, no DNS, no repository-visibility change,
no external check. The instruction excluded all of it.

**Build command, once you have chosen an audience** (the wrapper now refuses a
hostname-carrying image that has not):

```bash
NEXT_PUBLIC_SITE_URL=https://<your-host> NEXT_PUBLIC_SIGNALDECK_PUBLIC=1 ops/docker-build.sh
```

Before calling any public URL working, fetch it **anonymously from outside this
machine**. Keep the private repository private — it has held database backup
assets.

**Open gap to close first:** F06 — the container applies one gate fewer than the
dev box (`deployment_drift` needs a git checkout the image does not have), so a
stale binary the dev-box publish would refuse is still graded there.

---

## 6. Verify provider terms before anything is public

**Why not me.** This is a licensing judgement with legal consequences, against
each provider's *current* terms. Publicly available data is not automatically
redistributable and derived analytics are not automatically exempt.

Work through `DATA_SOURCES.md` and record, per provider: dataset, entitlement,
which field or output is exposed, to which audience. Where it is unclear,
withhold that output and record the unresolved permission rather than guessing.

---

## 7. Decide the waitlist's data lifecycle

`/` collects email addresses via `/api/waitlist` and there is no visible
privacy, retention or removal link. For a demo, **disabling public collection is
a legitimate choice** and the cheapest one. Otherwise it needs a truthful
collection notice, retention policy and a removal mechanism.

I did not change it either way: the right answer depends on whether you intend
to email anyone, which is your call. No test email was sent to any real address.

---

## Explicitly NOT done, by instruction

- `LIVE_ARMED` untouched; no real-money or broker path enabled
- Go research-loop cadence unchanged
- No sealed holdout opened, no settled refuted search re-run
- `tools/accuracy_registry.py` not edited (sha256 pinned in the prereg chain)
- No git history rewritten, no repository reset, no unknown process stopped
- No historical verdict altered to implement atomic publication
- No ledger entry backdated or retrofitted
