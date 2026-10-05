#!/bin/bash
# Install beside the destination first: a failed copy must not remove the
# working executable. Callers stop the service and hold the maintenance lock.
sd_install_binary() {
  local source="$1" target="$2" staged
  staged="$(mktemp "${target}.next.XXXXXX")" || return 1
  if ! install -m 755 "$source" "$staged"; then
    rm -f "$staged"
    return 1
  fi
  if [ -f "$target" ]; then
    if ! mv -f "$target" "$target.prev"; then
      echo "deploy REFUSED: cannot preserve the previous executable" >&2
      rm -f "$staged"
      return 1
    fi
  fi
  if ! mv -f "$staged" "$target"; then
    echo "deploy FAILED: restoring the previous executable" >&2
    if [ -f "$target.prev" ]; then
      cp -p "$target.prev" "$target" || echo "deploy RECOVERY FAILED: previous executable remains at $target.prev" >&2
    fi
    rm -f "$staged"
    return 1
  fi
}

# sd_check_publication_posture ENVFILE TUNNEL_RUNNING: refuse a restart that
# would leave a running tunnel in front of a daemon with no publication
# evidence (2026-10-05 audit, AUD-09). The public web tier forwards every
# visitor with the local-proxy key, so published() is the only thing keeping
# operator authority and raw data closed, and it rests on daemon/.env naming a
# public URL, the quick-tunnel log, or a public surface. TUNNEL_RUNNING is
# "yes" or "no" (the caller decides how to detect it). Prints why on refusal.
sd_check_publication_posture() {
  local env="$1" tunnel="$2"
  [ "$tunnel" = "yes" ] || return 0
  if grep -qE '^(SIGNALDECK_PUBLIC_URL|SIGNALDECK_TUNNEL_LOG)=.+' "$env" 2>/dev/null; then
    return 0
  fi
  if grep -qiE '^SIGNALDECK_PUBLIC_SURFACE=(1|true|yes)' "$env" 2>/dev/null; then
    return 0
  fi
  echo "deploy REFUSED: a tunnel is running but $env names no SIGNALDECK_PUBLIC_URL, SIGNALDECK_TUNNEL_LOG or SIGNALDECK_PUBLIC_SURFACE=1, so the daemon would treat every tunnel visitor as local (ops/CLOUDFLARE_TUNNEL.md section 3). Set one first."
  return 1
}
