#!/bin/bash
# Self-test for sd_check_publication_posture (ops/lib-deploy.sh), the deploy
# preflight for AUD-09 (2026-10-05): a build must not go in front of a running
# tunnel when it reads the daemon as unpublished, because the public web tier
# forwards every visitor with the local-proxy key and published() is all that
# keeps them from operator rights. The .env reading itself is the binary's
# (config.Published, pinned by TestPublishedReadsDotEnvLikeTheDaemon); this
# pins the shell side with stand-in binaries.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
. ops/lib-deploy.sh

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
fail=0
mkbin() { printf '#!/bin/bash
[ "$1" = "-publication-posture" ] || exit 9
echo "posture says %s"
exit %s
' "$2" "$2" > "$W/$1"; chmod +x "$W/$1"; }
mkbin published 0
mkbin unpublished 3
mkbin oldbinary 2
expect() { # name bin tunnel want
  sd_check_publication_posture "$W/$2" "$W" "$3" >/dev/null
  local got=$?
  if [ "$got" -ne "$4" ]; then echo "FAIL: $1 (rc $got, want $4)"; fail=1; fi
}

expect "tunnel, published build: allow"        published   yes 0
expect "tunnel, unpublished build: refuse"     unpublished yes 1
expect "tunnel, build without the flag: refuse" oldbinary  yes 1
expect "tunnel, missing binary: refuse"        missing     yes 1
expect "no tunnel, unpublished build: allow"   unpublished no  0
case "$(sd_tunnel_running)" in yes|no) ;; *) echo "FAIL: sd_tunnel_running printed neither yes nor no"; fail=1 ;; esac

if [ "$fail" -eq 0 ]; then echo "publication-posture self-test OK (6 cases)"; else exit 1; fi
