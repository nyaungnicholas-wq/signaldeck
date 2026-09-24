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
# The guard sources this and now REFUSES without it; the fixture never copied
# it, which is why every dirty-tree assertion below was red.
cp "$(pwd)/ops/lib-portable.sh" "$repo/ops/lib-portable.sh"
# The guard emits a build manifest before it builds, and REFUSES if it cannot --
# an image nothing can bind to its reviewed source is worse than no image. The
# fixture therefore has to carry the tool, exactly as the real repo does.
# Copied BEFORE the commit below, or the tree is instantly dirty and every
# dirty-tree assertion measures the fixture instead of the guard.
mkdir -p "$repo/tools"
cp "$(pwd)/tools/build_manifest.py" "$repo/tools/build_manifest.py"
# ...and the files that tool PINS. It refuses when it can hash none of them --
# a manifest that pins nothing would verify everything, which is the fail-open
# shape the whole gate exists to close -- so the fixture has to stand in for
# them. Contents are irrelevant; only that each exists and hashes to something.
#
# The list is READ from build_manifest.SOURCE_ARTIFACTS, not copied here. A
# hand-kept copy went stale on 2026-09-20 (8b65747 pinned five more files) and
# every good-build case refused "pinned=6 missing=5" -- CI red on every push
# for three days. Same python resolution as the guard.
fx_py=""
for cand in python3 python py; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import sys' >/dev/null 2>&1; then
    fx_py="$cand"; break
  fi
done
pins="$(cd "$repo/tools" && "$fx_py" -c 'import build_manifest; print(" ".join(build_manifest.SOURCE_ARTIFACTS))')"
[ -n "$pins" ] || { echo "FAIL could not read build_manifest.SOURCE_ARTIFACTS"; exit 1; }
# Space-separated: every pinned path is repo-relative and space-free, and a
# word list needs no newline handling across Windows and Linux pythons.
for f in $pins; do
  f="${f%$(printf '\r')}"
  mkdir -p "$repo/$(dirname "$f")"
  echo 'stub for the fixture' > "$repo/$f"
done
printf 'FROM scratch\n' > "$repo/Dockerfile"
# The guard WRITES build-manifest.json into the build context, so without this
# the fixture goes dirty on its own first build and every later case refuses on
# a file the guard itself just created. The real repo gitignores it for exactly
# that reason; the fixture mirrors the repo it stands in for, or it tests a
# condition that cannot occur in practice.
# The commit pack is the same shape of artifact and was added 2026-09-16: the
# guard packs this repository's commit objects into the build context so the
# container grader can resolve historical revisions. The real repo gained
# /build-commits.pack in .gitignore for precisely the reason written above, and
# the fixture went red on every post-first-build case until it mirrored that -
# which is this comment's own warning arriving a second time.
printf '/build-manifest.json\n/build-commits.pack\n' > "$repo/.gitignore"

mkdir -p "$work/bin"
printf '#!/bin/bash\nprintf "%%s\\n" "$*" >> "$ARGV_LOG"\nexit 0\n' > "$work/bin/docker"
chmod +x "$work/bin/docker"
export ARGV_LOG="$work/argv.txt"
export PATH="$work/bin:$PATH"
# These fixtures build throwaway LOCAL images with a stubbed docker, which is
# exactly the case the site-URL guard allows explicitly. Without this every
# test below would refuse on a missing hostname instead of exercising tags and
# the dirty-tree rules it is actually about.
export SIGNALDECK_ALLOW_LOCALHOST_SITE_URL=1

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


# 7. FAIL CLOSED on a missing library. Unchecked sourcing left the dirty-tree
# check silently skipped, so the build stamped a dirty tree (audit id 21).
mv "$repo/ops/lib-portable.sh" "$work/lib-portable.away"
check "missing lib-portable.sh is refused" "$(run)" "1"
check "  and docker never ran" "$(argv)" ""
grep -qi "cannot source" "$work/out" && ok "  refusal names the library" || bad "  refusal names the library"
mv "$work/lib-portable.away" "$repo/ops/lib-portable.sh"

# 8. FAIL CLOSED on an unset site URL. The web bundle inlines it at build time,
# so an image built without it serves localhost robots/sitemap/og forever.
check "unset NEXT_PUBLIC_SITE_URL is refused" "$(env -u SIGNALDECK_ALLOW_LOCALHOST_SITE_URL bash ops/docker-build.sh >"$work/out" 2>&1; echo $?)" "1"
grep -qi "NEXT_PUBLIC_SITE_URL is unset" "$work/out" && ok "  refusal names the variable" || bad "  refusal names the variable"
check "  explicit hostname builds" "$(NEXT_PUBLIC_SITE_URL=https://x.test NEXT_PUBLIC_SIGNALDECK_PUBLIC=1 run)" "0"

# 9. A HOSTNAME WITHOUT AN AUDIENCE. The guard above it was added with the
# NEXT_PUBLIC_SIGNALDECK_PUBLIC work and nothing here exercised it, which is how
# it broke case 8: that case passed only the hostname, the new guard correctly
# refused it, and the suite went red on a contract change nobody had told it
# about. The two cases now pin both sides of the same rule.
check "hostname without an audience is refused" "$(NEXT_PUBLIC_SITE_URL=https://x.test run)" "1"
grep -qi "hostname but no audience" "$work/out" && ok "  refusal names the missing choice" || bad "  refusal names the missing choice"
check "  private audience builds too" "$(NEXT_PUBLIC_SITE_URL=https://x.test NEXT_PUBLIC_SIGNALDECK_PUBLIC=0 run)" "0"
# 10. THE BUILD MANIFEST. ops/docker-build.sh emits it on the host, where git
# exists, so the image can be bound to its reviewed source by content hash
# rather than by the GIT_REV label the file itself calls "TRUSTED, not checked".
# Two halves, both worth pinning: it is actually produced on a good build, and
# a build that cannot produce it is refused rather than shipped unbindable.
rm -f "$repo/build-manifest.json"
check "a good build emits the manifest" "$(run)" "0"
if [ -s "$repo/build-manifest.json" ]; then
  ok "  manifest lands in the build context"
else
  bad "  manifest lands in the build context"
fi
grep -q '"source_artifacts"' "$repo/build-manifest.json" 2>/dev/null \
  && ok "  and pins the files that decide a verdict" \
  || bad "  and pins the files that decide a verdict"

# The COMMIT PACK, the manifest's counterpart. The manifest binds the bytes; this
# carries the commit objects so the pinned grader can resolve the revision on
# every historical forecast row inside the image, where there is otherwise no
# git. Same contract as above: it must land in the build context, and it must be
# a real pack rather than an empty file, because an empty one would unpack to a
# store that resolves nothing and strip every verdict silently.
rm -f "$repo/build-commits.pack"
check "a good build emits the commit pack" "$(run)" "0"
if [ -s "$repo/build-commits.pack" ]; then
  ok "  commit pack lands in the build context"
else
  bad "  commit pack lands in the build context"
fi
head -c 4 "$repo/build-commits.pack" 2>/dev/null | grep -q 'PACK' \
  && ok "  and it is a real git pack, not an empty file" \
  || bad "  and it is a real git pack, not an empty file"

# The removal is COMMITTED, not just moved. build_manifest.py is a tracked file
# here, so moving it aside leaves the tree dirty and the dirty-tree guard --
# which runs first -- refuses for that reason instead. The assertion would still
# see exit 1 and pass while measuring the wrong guard entirely, which is the
# most expensive kind of green.
cp "$repo/tools/build_manifest.py" "$work/build_manifest.keep"
git -c core.autocrlf=false rm -q "$repo/tools/build_manifest.py"
git -c user.name=t -c user.email=t@t commit -qm "drop the manifest tool"
check "missing manifest tool is refused" "$(run)" "1"
grep -qi "build manifest" "$work/out" && ok "  refusal names the manifest" || bad "  refusal names the manifest"
case "$(argv)" in
  "") ok "  and docker never ran" ;;
  *)  bad "  and docker never ran (argv: $(argv))" ;;
esac
cp "$work/build_manifest.keep" "$repo/tools/build_manifest.py"
git -c core.autocrlf=false add -A
git -c user.name=t -c user.email=t@t commit -qm "restore the manifest tool"

echo ""
echo "docker-build self-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
