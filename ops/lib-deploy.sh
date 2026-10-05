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

# sd_tunnel_running: prints "yes" when a cloudflared process runs on this host.
sd_tunnel_running() {
  if command -v tasklist >/dev/null 2>&1; then
    tasklist //FI "IMAGENAME eq cloudflared.exe" 2>/dev/null | grep -qi cloudflared && { echo yes; return; }
  elif command -v pgrep >/dev/null 2>&1; then pgrep -x cloudflared >/dev/null 2>&1 && { echo yes; return; }
  else
    echo "WARNING: neither tasklist nor pgrep exists; assuming no tunnel (the daemon still checks at startup)" >&2
  fi
  echo no
}

# sd_check_publication_posture BIN DAEMON_DIR TUNNEL_RUNNING: refuse to put a
# binary in front of a running tunnel when it would read this daemon as
# unpublished (2026-10-05 audit, AUD-09). The public web tier forwards every
# visitor with the local-proxy key, so published() is all that keeps operator
# authority and raw data closed. BIN answers itself (-publication-posture: 0
# published, 3 not), run from DAEMON_DIR so it finds the same daemon/.env the
# installed binary will: a grep here was a second .env parser and disagreed
# with the daemon (2026-10-05 review). Callers run this BEFORE stopping
# anything. TUNNEL_RUNNING is "yes" or "no". Prints why on refusal.
sd_check_publication_posture() {
  local bin="$1" dir="$2" tunnel="$3" out rc
  [ "$tunnel" = "yes" ] || return 0
  out="$(cd "$dir" && env -u SIGNALDECK_ROOT "$bin" -publication-posture 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] && return 0
  echo "REFUSED: a tunnel is running but this build reads the daemon as unpublished (rc $rc), so it would treat every tunnel visitor as local (ops/CLOUDFLARE_TUNNEL.md section 3). Set SIGNALDECK_PUBLIC_URL, SIGNALDECK_TUNNEL_LOG or SIGNALDECK_PUBLIC_SURFACE=1 in daemon/.env first. Nothing was stopped. The binary said: $(printf '%s' "$out" | tail -1)"
  return 1
}
