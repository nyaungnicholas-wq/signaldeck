#!/usr/bin/env bash
set -uo pipefail

# Why this script exists:
# A hash stored only on the operator's own disk proves nothing to anyone else;
# the operator could rewrite it after learning an outcome and no external party
# could detect the change. OpenTimestamps anchors that hash into the Bitcoin
# blockchain by creating a timestamp attestation that proves the hash existed
# at or before a certain Bitcoin block. This does NOT retroactively prove
# anything about records created before the first stamp; anteriority before the
# first stamp still relies on the operator's word. The script only ever publishes
# a hash, never the source data.

# Configuration (overridable via environment)
API="${SIGNALDECK_API:-http://127.0.0.1:8322}"
OTS_DIR="${SIGNALDECK_OTS_DIR:-/data/ots}"   # on the box; proofs/ots/ is the tracked copy
LOG="${SIGNALDECK_OTS_LOG:-/data/logs/ots-stamp.log}"
TOKEN="${SIGNALDECK_API_TOKEN:-}"

# Logging function: ISO-8601 UTC timestamp, writes to stdout and log file
log() {
    local ts
    ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    printf '[%s] %s\n' "$ts" "$*" | tee -a "$LOG"
}

# Ensure required directories exist
mkdir -p "$OTS_DIR"
mkdir -p "$(dirname "$LOG")"

# 1. Verify ots command is available
if ! command -v ots >/dev/null 2>&1; then
    log "Error: 'ots' command not found. Install with: pip install opentimestamps-client"
    exit 1
fi

# 2. Fetch pre-registration state
CURL_ARGS=(-fsS --max-time 30)
if [[ -n "$TOKEN" ]]; then
    CURL_ARGS+=(-H "Authorization: Bearer $TOKEN")
fi
response=$(curl "${CURL_ARGS[@]}" "$API/api/prereg") || {
    log "Error: Failed to fetch preregistration state from $API/api/prereg"
    exit 1
}

# 3. Extract newest entryHash, seq, and chainVerified status (no jq)
#
# Match the FIELD, not just "any 64 hex characters". The naive version took the
# last 64-hex string anywhere in the document, which happens to be the newest
# entryHash today only because the Record struct orders specHash, prevHash,
# entryHash and the records are listed ascending. Add one hex-shaped field
# after EntryHash, or reorder them, and it would silently stamp the WRONG hash
# -- and an external timestamp of the wrong hash is worse than none, because it
# looks like proof.
entryHash=$(echo "$response" | grep -oE '"entryHash":[[:space:]]*"[0-9a-f]{64}"'     | tail -1 | grep -oE '[0-9a-f]{64}')
seq=$(echo "$response" | grep -oE '"seq":[[:space:]]*[0-9]+' | grep -oE '[0-9]+' | tail -1)
if echo "$response" | grep -q '"chainVerified":.*true'; then
    chainVerified=true
else
    chainVerified=false
fi

# 4. Refuse to stamp a broken chain
if [[ "$chainVerified" != true ]]; then
    log "Error: Chain does not verify (chainVerified is not true). Stamping would create a permanent record of a broken chain."
    exit 1
fi

# 5. Validate entryHash format
if [[ ! "$entryHash" =~ ^[0-9a-f]{64}$ ]]; then
    log "Error: entryHash '$entryHash' is not a 64-character lowercase hex string."
    exit 1
fi

# 6. Prepare stamp file; check idempotency
date_str=$(date -u +"%Y-%m-%d")
file="$OTS_DIR/head-${date_str}.txt"
ots_file="${file}.ots"
if [[ -f "$file" && -f "$ots_file" ]]; then
    log "Info: Today's stamp already exists ($ots_file). Nothing to do."
    exit 0
fi

# Write the three-line file
{
    echo "prereg-chain-head $entryHash"
    echo "prereg-chain-seq $seq"
    echo "stamped-at $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
} > "$file"

# 7. Perform OpenTimestamps stamp
if ! ots stamp "$file"; then
    log "Error: ots stamp failed on $file"
    exit 1
fi

# 8. Log success and verification command
log "Success: stamped $file -> $ots_file"
log "To verify, run: ots verify $ots_file"

# 9. Upgrade any existing .ots files (non-failing)
for ots in "$OTS_DIR"/*.ots; do
    if [[ -e "$ots" ]]; then
        if ots upgrade "$ots"; then
            log "Info: Upgrade succeeded for $ots"
        else
            log "Warning: Upgrade failed or not ready for $ots (ignored)"
        fi
    fi
done

exit 0