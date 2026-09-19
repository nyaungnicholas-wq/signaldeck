#!/bin/bash
set -eu
REPO="$(cd "$(dirname "$0")/.." && pwd)"
. "$REPO/ops/lib-deploy.sh"
tmp="$(mktemp -d)"
# Delete only the individual fixture files we create.
trap 'rm -f "$tmp/new" "$tmp/daemon" "$tmp/daemon.prev"; rmdir "$tmp"' EXIT
printf old > "$tmp/daemon"
printf new > "$tmp/new"
install() { return 1; }
if sd_install_binary "$tmp/new" "$tmp/daemon"; then exit 1; fi
[ "$(cat "$tmp/daemon")" = old ]
unset -f install
# Inject a rename failure AFTER the previous binary was moved aside.
mv() { case "$1 $2" in *'.next.'*) return 1;; esac; command mv "$@"; }
if sd_install_binary "$tmp/new" "$tmp/daemon"; then exit 1; fi
[ "$(cat "$tmp/daemon")" = old ]
[ "$(cat "$tmp/daemon.prev")" = old ]
unset -f mv
sd_install_binary "$tmp/new" "$tmp/daemon"
[ "$(cat "$tmp/daemon")" = new ]
[ "$(cat "$tmp/daemon.prev")" = old ]
echo 'deploy rollback: copy failure, rename failure, and success passed'
