#!/bin/bash
# Pre-publication scan — run this BEFORE anything from this repo goes public.
#
# The council review that prompted this made the point sharply: "open the repo"
# is usually stated as a near-free move, and it isn't. This repo holds provider
# credentials in daemon/.env, and it consumes market data under agreements that
# forbid redistributing raw records. A writeup that quotes ledger rows or bar
# data can breach those terms even when the code is fine to show.
#
# So this checks four things, in the order they can hurt:
#   1. secrets that would leak on push (tracked working tree),
#   2. secrets that leaked in the PAST and are still in git history forever,
#   3. raw provider data committed as files,
#   4. whether anything in the working tree is untracked-but-about-to-be-added.
#
# A13 hostile-review finding (2026-07-26): this scan used to `grep -E` with no
# `-i`, while every env var this project actually uses is UPPERCASE
# (SIGNALDECK_NVIDIA_KEY, SIGNALDECK_TV_WEBHOOK_SECRET, ...). That made the
# secret check structurally blind to its own project's credential shapes — it
# would say SAFE TO PUBLISH on a committed daemon/.env and be silently wrong.
# It also never looked at git history, so a secret committed once and later
# deleted was permanently exposed while the scan kept passing. Fixed here; see
# ops/pre-publish-scan-test.sh for the regression test that pins this.
#
# Exit 0 = safe to publish. Non-zero = do not publish; read the output.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

fail=0
say() { printf '%s\n' "$*"; }
bad() { fail=1; say "  ✗ $*"; }
ok()  { say "  ✓ $*"; }

# Secret patterns — case-INsensitive throughout (that's the fix: env var
# names in this repo are SIGNALDECK_NVIDIA_KEY, SIGNALDECK_TV_WEBHOOK_SECRET,
# not nvidia_key/tv_webhook_secret). Split into two tiers:
#
#   VENDOR_PATTERN  — specific token shapes (AWS AKIA, OpenAI/Anthropic sk-,
#     GitHub ghp_, Slack xox., NVIDIA nvapi-, webhook whsec_, PEM private
#     keys). These are unambiguous by shape alone; always flag.
#
#   GENERIC_PATTERN — the "NAME-CONTAINING-key/secret/token/password =
#     value" assignment shape, which is what actually catches project-
#     specific vars like SIGNALDECK_NVIDIA_KEY or SIGNALDECK_TV_WEBHOOK_SECRET.
#     This shape alone over-matches (a header-name constant like
#     `tvSecretHeader = "X-Signaldeck-TV-Secret"`, or a variable-to-variable
#     assignment like `maxTokens = maxOutputTokens`, both matched during
#     testing), so a GENERIC_PATTERN hit only counts if the value also
#     contains a digit — real generated secrets are high-entropy and
#     essentially always do; English header names and bare identifiers don't.
#     This is a heuristic, not a proof: a hand-typed all-letters secret would
#     be missed by this tier (vendor-shaped secrets above are unaffected).
VENDOR_PATTERN='(AKIA[0-9A-Z]{16}|sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]+|nvapi-[A-Za-z0-9_-]{16,}|whsec_[A-Za-z0-9]{16,}|BEGIN [A-Z ]*PRIVATE KEY)'
GENERIC_PATTERN='(api[_-]?key|apikey|secret|password|passwd|token|authtoken|auth[_-]?token|access[_-]?key|client[_-]?secret|private[_-]?key)[A-Za-z0-9_]*["'"'"' ]*[:=]["'"'"' ]*[A-Za-z0-9/+_.-]{12,}'
# Lines/paths that are known-benign and would otherwise false-positive: code
# that reads a secret from the environment rather than embedding one, example
# templates, and test/e2e fixtures with fake credentials (e.g. "hunter2secret"
# in daemon/internal/api/auth_test.go, daemon/e2e/e2e_test.go) — those are
# real, deliberately fake test data, not a leak.
EXCLUDE_PATTERN='os\.Getenv|process\.env|\.env\.example|placeholder|example|_test\.go|e2e/|csrf'

say "── 1. Secrets in the tracked working tree ─────────────────────────────"
# Tracked files only: an ignored .env is fine on disk and fatal in a commit.
tracked=$(git ls-files)
vendor_hits=$(printf '%s\n' "$tracked" | xargs grep -InE "$VENDOR_PATTERN" 2>/dev/null \
  | grep -viE "$EXCLUDE_PATTERN")
generic_hits=$(printf '%s\n' "$tracked" | xargs grep -InE "$GENERIC_PATTERN" 2>/dev/null \
  | grep -viE "$EXCLUDE_PATTERN" \
  | awk -F: '{content=$0; sub(/^[^:]*:[0-9]+:/, "", content); if (content ~ /[0-9]/) print}')
hits=$(printf '%s\n%s\n' "$vendor_hits" "$generic_hits" | grep -v '^$' | sort -u | head -20)
if [ -n "$hits" ]; then
  bad "possible secrets in TRACKED files:"
  printf '%s\n' "$hits" | sed 's/^/      /'
else
  ok "no secret-shaped strings in tracked files"
fi

env_files=$(printf '%s\n' "$tracked" | grep -E '(^|/)\.env($|\.[^.]*$)' | grep -viE '\.env\.example$')
if [ -n "$env_files" ]; then
  bad "tracked .env-shaped file(s) — these hold provider credentials:"
  printf '%s\n' "$env_files" | sed 's/^/      /'
else
  ok "no tracked .env-shaped files (daemon/.env, web/.env, ...)"
fi

say "── 2. Secrets in git HISTORY ──────────────────────────────────────────"
# A secret deleted in a later commit is still in every clone forever — the
# working-tree check above cannot see it. This walks the added ("+") lines of
# every commit reachable from any local ref and applies the same pattern,
# with per-file path context so _test.go / .env.example fixtures don't
# false-positive the way a filename-blind history grep would.
#
# COST/COVERAGE TRADEOFF (documented per A13, not silently chosen):
#   - Full `git log -p --all` over this repo's 95 commits takes ~35s. Below
#     HIST_COMMIT_LIMIT we do the full walk — bounded and cheap enough to run
#     on every push.
#   - Past that limit we fall back to HEAD~<limit>..HEAD plus anything not
#     yet on origin/main, and print which strategy ran, so nobody mistakes a
#     partial scan for a full one.
#   - What this does NOT cover even in "full" mode: refs that were force-
#     pushed away, reflog-only/dangling commits, or remote branches never
#     fetched locally (`--all` only sees local + already-fetched refs). If a
#     secret was ever pushed and then history-rewritten away, this scan will
#     not find it — that requires scanning the *remote's* reflog/GC state,
#     which is out of reach from a local clone. Rotate the credential instead
#     of relying on a scan to prove it's gone.
HIST_COMMIT_LIMIT=3000
total_commits=$(git rev-list --all --count 2>/dev/null || echo 0)
if [ "$total_commits" -gt "$HIST_COMMIT_LIMIT" ] 2>/dev/null; then
  base=$(git merge-base HEAD origin/main 2>/dev/null || echo "")
  if [ -n "$base" ]; then
    hist_range="$base..HEAD"
  else
    hist_range="HEAD~${HIST_COMMIT_LIMIT}..HEAD"
  fi
  say "  ! $total_commits commits > $HIST_COMMIT_LIMIT — scanning $hist_range only (not full history)."
  hist_log=$(git log -p "$hist_range" -- . 2>/dev/null)
else
  hist_log=$(git log -p --all -- . 2>/dev/null)
fi

hist_hits=$(printf '%s\n' "$hist_log" | VENDOR_PATTERN="$VENDOR_PATTERN" GENERIC_PATTERN="$GENERIC_PATTERN" EXCLUDE_PATTERN="$EXCLUDE_PATTERN" perl -ne '
  BEGIN { $vendor = $ENV{VENDOR_PATTERN}; $generic = $ENV{GENERIC_PATTERN}; $excl = $ENV{EXCLUDE_PATTERN}; $file = "(unknown)"; }
  if (/^\+\+\+ b\/(.*)$/) { $file = $1; next }
  next unless /^\+/;
  next if $file =~ /(_test\.go|\.env\.example|\/e2e\/|testdata\/)/;
  next if /$excl/i;
  if (/$vendor/i || (/$generic/i && /[0-9]/)) {
    print "$file: $_";
  }
' 2>/dev/null | head -20)
if [ -n "$hist_hits" ]; then
  bad "possible secrets committed at some point in history (still present in every clone):"
  printf '%s\n' "$hist_hits" | sed 's/^/      /'
else
  ok "no secret-shaped strings added in scanned git history"
fi

say "── 3. Provider data ───────────────────────────────────────────────────"
# Raw records are the licensed thing; derived analytics are explicitly fine
# (see DATA_SOURCES.md). Committed .db/.csv files are the usual accident.
#
# This is a REVIEW flag, not a hard failure, and the distinction matters: a
# scan that fails on every CSV gets ignored within a week, and an ignored scan
# is worse than no scan. Known-benign example: the prune audit log holds symbol
# IDs and tickers, which are public facts, not provider records.
data_hits=$(printf '%s\n' "$tracked" | grep -iE '\.(db|sqlite3?|csv|parquet)$' | head -10)
if [ -n "$data_hits" ]; then
  say "  ! data files are tracked — eyeball each against DATA_SOURCES.md."
  say "    Bars, quotes and news bodies are licensed and may NOT be published."
  say "    Tickers, symbol ids and aggregate statistics are fine."
  printf '%s\n' "$data_hits" | sed 's/^/      /'
else
  ok "no data files tracked"
fi

say "── 4. Working tree ────────────────────────────────────────────────────"
untracked=$(git status --porcelain | grep '^??' | wc -l | tr -d ' ')
if [ "$untracked" != "0" ]; then
  say "  ! $untracked untracked path(s) — none are published unless you add them:"
  git status --porcelain | grep '^??' | head -10 | sed 's/^/      /'
else
  ok "working tree has no untracked paths"
fi

say ""
if [ "$fail" = "0" ]; then
  say "SAFE TO PUBLISH — secrets (working tree + history) and raw-data checks passed."
  say "Still your call: derived analytics may be published freely, raw provider"
  say "records may not. Quoting ledger rows or bars in a writeup is the case to"
  say "think about; aggregate statistics are not."
else
  say "DO NOT PUBLISH — fix the ✗ items above first."
fi
exit "$fail"
