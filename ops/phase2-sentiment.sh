#!/bin/bash
# Phase 2 — re-run the sentiment/trend21 conditioning test.
#
# Gated by design: before trend21 outcomes resolve (first 2026-08-08) it reports
# NO RESULT rather than a number. Scheduled weekly so the answer arrives on its
# own the week the data supports one, instead of waiting for someone to remember
# to ask.
set -uo pipefail
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib-portable.sh
. "$SD/ops/lib-portable.sh"
LOG="$SD/logs/phase2-sentiment.log"
{
  echo "──────── $(date '+%Y-%m-%dT%H:%M:%S') ────────"
  # sd_py, not `python3`: the latter is a Store alias stub under Git Bash, so
  # the weekly run died before the test and the VERDICT grep below saw nothing.
  "$(sd_py)" "$SD/tools/sentiment_conditioning.py"
} >> "$LOG" 2>&1

# Notify only when the test actually produced a verdict — a weekly "still no
# data" banner is how a real alert gets ignored later.
if tail -20 "$LOG" | grep -qE "^VERDICT: (REAL SPLIT|NO SPLIT)"; then
  v=$(tail -20 "$LOG" | grep -E "^VERDICT:" | head -1)
  sd_notify "SignalDeck Phase 2" "$v"
fi
