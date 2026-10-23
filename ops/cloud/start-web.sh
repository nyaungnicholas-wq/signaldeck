#!/bin/bash
# start-web.sh [PORT] - ExecStart of signaldeck-web.service; the Linux form of
# ops/start-local-workspace.ps1. The public web tier is the KEYED local proxy:
# SIGNALDECK_LOCAL_ONLY_PROXY=1 plus SIGNALDECK_LOCAL_PROXY_KEY, read here as
# that ONE line of daemon/.env (an EnvironmentFile= would hand the web process
# every secret in the file). See ops/CLOUDFLARE_TUNNEL.md "Why the order matters".
set -u
SD="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
port="${1:-8323}"
export SIGNALDECK_LOCAL_ONLY_PROXY=1
key="$(grep -m1 '^SIGNALDECK_LOCAL_PROXY_KEY=' "$SD/daemon/.env" 2>/dev/null | cut -d= -f2- | tr -d '\r' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
[ -n "$key" ] && export SIGNALDECK_LOCAL_PROXY_KEY="$key"
cd "$SD/web" || exit 1
exec node "$SD/web/node_modules/next/dist/bin/next" start -H 127.0.0.1 -p "$port"
