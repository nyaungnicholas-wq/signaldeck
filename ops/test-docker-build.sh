#!/bin/bash
# Self-test for ops/docker-build.sh, in the shape ops/test-ledger-provenance.sh
# and ops/test-pre-push-hook.sh already use: if the guard cannot prove it still
# refuses, the guard is not evidence.
#
# Everything runs in a THROWAWAY git repo. The live tree has a scheduled job
# writing research scripts continuously, so a clean-tree precondition can never
# be relied on there, and a test that depends on the timing of an unrelated
# background task is a flake waiting to happen.
#
# docker is stubbed on PATH and records its argv, so this needs no engine — the
# question is what the guard DECIDES, not whether an image builds.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 2
GUARD="$(pwd)/ops/docker-build.sh"

pass=0; fail=0
ok()   { printf '  ok    %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
repo="$work/repo"; mkdir -p "$repo/ops"
cp "$GUARD" "$repo/ops/docker-build.sh"
printf 'FROM scratch\n' > "$repo/Dockerfile"

mkdir -p "$work/bin"
printf '#!/bin/bash\nprintf "%%s\\n" "$*" >> "$ARGV_LOG"\nexit 0\n' > "$work/bin/docker"
chmod +x "$work/bin/docker"
export ARGV_LOG="$work/argv.txt"
export PATH="$work/bin:$PATH"

cd "$repo" || exit 2
git init -q
# The operator's global core.autocrlf would rewrite line endings on commit and
# leave this fixture instantly "dirty" — a property of the machine, not of the
# guard under test.
git -c core.autocrlf=false add -A
git -c user.name=t -c user.email=t@t commit -qm init
rev="$(git rev-parse HEAD)"

run(){ : > "$ARGV_LOG"; bash ops/docker-build.sh "$@" >"$work/out" 2>&1; echo "$?"; }
argv(){ cat "$ARGV_LOG" 2>/dev/null; }

echo "── ops/docker-build.sh ────────────────────────────────────────────────"

# 1. A clean tree builds, and stamps the commit the tree actually is.
check "clean tree builds" "$(run)" "0"
case "$(argv)" in
  *"--build-arg GIT_REV=$rev"*) ok "stamps the real HEAD" ;;
  *) bad "stamps the real HEAD (argv: $(argv))" ;;
esac
case "$(argv)" in
  *" .") ok "build context '.' stays last" ;;
  *) bad "build context '.' stays last (argv: $(argv))" ;;
esac
case "$(argv)" in
  *"-t signaldeck"*) ok "defaults to the signaldeck tag" ;;
  *) bad "defaults to the signaldeck tag (argv: $(argv))" ;;
esac

# 2. An UNTRACKED file is dirt. This is the case that matters here: the eighty
#    loop drops new research scripts into the tree between commits.
printf 'probe\n' > "$repo/untracked.tmp"
check "untracked file is refused" "$(run)" "1"
check "  and docker never ran" "$(argv)" ""
grep -qi 'not clean' "$work/out" && ok "  refusal names the reason" \
  || bad "  refusal names the reason"
rm -f "$repo/untracked.tmp"

# 3. A MODIFIED TRACKED file is dirt too — the case that would ship an image
#    built from edits that exist in no commit.
printf '# touched\n' >> "$repo/Dockerfile"
check "modified tracked file is refused" "$(run)" "1"
check "  and docker never ran" "$(argv)" ""
git checkout -- Dockerfile

# 4. A STAGED-BUT-UNCOMMITTED change is dirt: staging is not committing, and the
#    container would still be built from code no commit contains.
printf 'staged\n' > "$repo/staged.txt"
git add staged.txt
check "staged change is refused" "$(run)" "1"
git rm -q --cached staged.txt; rm -f "$repo/staged.txt"

# 5. Arguments pass through: first positional is the tag, the rest reach docker.
check "custom tag accepted" "$(run myimage:test --no-cache)" "0"
case "$(argv)" in
  *"-t myimage:test"*"--no-cache"*|*"--no-cache"*"-t myimage:test"*)
    ok "  tag and extra flags both forwarded" ;;
  *) bad "  tag and extra flags both forwarded (argv: $(argv))" ;;
esac

echo ""
echo "docker-build self-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
