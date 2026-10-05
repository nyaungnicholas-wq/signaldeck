#!/bin/bash
# Self-test for sd_check_publication_posture (ops/lib-deploy.sh), the deploy
# preflight added for AUD-09 (2026-10-05): a restart must not leave a running
# tunnel in front of a daemon whose env names no public URL, tunnel log or
# public surface, because the public web tier forwards every visitor with the
# local-proxy key and published() is all that keeps them from operator rights.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
. ops/lib-deploy.sh

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
fail=0
expect() { # name env tunnel want
  sd_check_publication_posture "$W/$2" "$3" >/dev/null
  local got=$?
  if [ "$got" -ne "$4" ]; then echo "FAIL: $1 (rc $got, want $4)"; fail=1; fi
}
printf 'SIGNALDECK_OPEN_SIGNUP=true\n' > "$W/none.env"
printf 'SIGNALDECK_TUNNEL_LOG=logs/quicktunnel.log\n' > "$W/log.env"
printf 'SIGNALDECK_PUBLIC_URL=https://signaldeck.example\n' > "$W/url.env"
printf 'SIGNALDECK_PUBLIC_SURFACE=1\n' > "$W/surface.env"
printf 'SIGNALDECK_PUBLIC_URL=\n' > "$W/blank.env"

expect "tunnel, no evidence: refuse"       none.env    yes 1
expect "tunnel, blank public URL: refuse"  blank.env   yes 1
expect "tunnel, tunnel log: allow"         log.env     yes 0
expect "tunnel, public URL: allow"         url.env     yes 0
expect "tunnel, public surface: allow"     surface.env yes 0
expect "no tunnel, no evidence: allow"     none.env    no  0
expect "tunnel, missing env file: refuse"  missing.env yes 1

if [ "$fail" -eq 0 ]; then echo "publication-posture self-test OK (7 cases)"; else exit 1; fi
