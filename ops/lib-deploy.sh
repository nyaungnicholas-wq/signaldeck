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
