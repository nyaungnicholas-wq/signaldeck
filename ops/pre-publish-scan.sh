#!/bin/bash
# Pre-publication scan — run this BEFORE anything from this repo goes public.
#
# The council review that prompted this made the point sharply: "open the repo"
# is usually stated as a near-free move, and it isn't. This repo holds provider
# credentials in daemon/.env, and it consumes market data under agreements that
# forbid redistributing raw records. A writeup that quotes ledger rows or bar
# data can breach those terms even when the code is fine to show.
#
# So this checks three things, in the order they can hurt:
#   1. secrets that would leak on push,
#   2. raw provider data committed as files,
#   3. whether anything in the working tree is untracked-but-about-to-be-added.
#
# Exit 0 = safe to publish. Non-zero = do not publish; read the output.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2

fail=0
say() { printf '%s\n' "$*"; }
bad() { fail=1; say "  ✗ $*"; }
ok()  { say "  ✓ $*"; }

say "── 1. Secrets ─────────────────────────────────────────────────────────"
# Tracked files only: an ignored .env is fine on disk and fatal in a commit.
tracked=$(git ls-files)
hits=$(printf '%s\n' "$tracked" | xargs grep -InE \
  '(AKIA[0-9A-Z]{16}|sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{20,}|xox[baprs]-|BEGIN [A-Z ]*PRIVATE KEY|(api[_-]?key|apikey|secret|password|token)["'"'"' ]*[:=]["'"'"' ]*[A-Za-z0-9/+_-]{16,})' \
  2>/dev/null | grep -viE 'os\.Getenv|process\.env|\.env\.example|placeholder|example|_test\.go|e2e/|csrf' | head -20)
if [ -n "$hits" ]; then
  bad "possible secrets in TRACKED files:"
  printf '%s\n' "$hits" | sed 's/^/      /'
else
  ok "no secret-shaped strings in tracked files"
fi

if git ls-files --error-unmatch daemon/.env >/dev/null 2>&1; then
  bad "daemon/.env is TRACKED — it holds provider credentials"
else
  ok "daemon/.env is not tracked"
fi

say "── 2. Provider data ───────────────────────────────────────────────────"
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

say "── 3. Working tree ────────────────────────────────────────────────────"
untracked=$(git status --porcelain | grep '^??' | wc -l | tr -d ' ')
if [ "$untracked" != "0" ]; then
  say "  ! $untracked untracked path(s) — none are published unless you add them:"
  git status --porcelain | grep '^??' | head -10 | sed 's/^/      /'
else
  ok "working tree has no untracked paths"
fi

say ""
if [ "$fail" = "0" ]; then
  say "SAFE TO PUBLISH — secrets and raw-data checks passed."
  say "Still your call: derived analytics may be published freely, raw provider"
  say "records may not. Quoting ledger rows or bars in a writeup is the case to"
  say "think about; aggregate statistics are not."
else
  say "DO NOT PUBLISH — fix the ✗ items above first."
fi
exit "$fail"
