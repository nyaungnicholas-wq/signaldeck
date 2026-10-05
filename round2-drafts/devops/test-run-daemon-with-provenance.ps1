# test-run-daemon-with-provenance.ps1 -- the one runnable check behind the
# wrapper. Every case runs with -CheckOnly, so nothing is ever started.
#
# Most cases run against a THROWAWAY git repo in the temp dir, because the
# interesting branches (commit absent, no stamp, exactly-HEAD) cannot be
# produced from the live repo without rebuilding a binary. The last case runs
# against the real repo read-only, to prove the wrapper agrees with reality.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File test-run-daemon-with-provenance.ps1
#
# ASCII ONLY (same reason as the script under test).

$ErrorActionPreference = 'Continue'
$wrapper = Join-Path $PSScriptRoot 'run-daemon-with-provenance.ps1'
$fails = 0

function Check {
  param([string] $Name, [bool] $Ok, [string] $Detail)
  if ($Ok) { Write-Output ("PASS  {0}" -f $Name) }
  else { Write-Output ("FAIL  {0}`n      {1}" -f $Name, $Detail); $script:fails++ }
}

# Run the wrapper and capture (exit code, joined output).
function Run {
  param([hashtable] $Params)   # not $Args: that is an automatic variable
  $out = & $wrapper @Params -CheckOnly 2>&1 | Out-String
  return [pscustomobject]@{ Code = $LASTEXITCODE; Out = $out }
}

# --- throwaway repo with two commits --------------------------------------
$tmp = Join-Path ([IO.Path]::GetTempPath()) ('sdprov-' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path (Join-Path $tmp 'ops') -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $tmp 'bin') -Force | Out-Null
Set-Content -LiteralPath (Join-Path $tmp 'ops\signaldeck-ctl.sh') -Value '#!/bin/sh' -Encoding ascii
Push-Location $tmp
git init -q . 2>$null | Out-Null
git -c user.email=t@t -c user.name=t commit -q --allow-empty -m one 2>$null | Out-Null
$old = (git rev-parse HEAD)
git -c user.email=t@t -c user.name=t commit -q --allow-empty -m two 2>$null | Out-Null
$head = (git rev-parse HEAD)
Pop-Location

$exe = Join-Path $tmp 'bin\signaldeckd.exe'
$log = Join-Path $tmp 'prov.log'
function Set-Stamp { param([string] $Body) Set-Content -LiteralPath $exe -Value $Body -Encoding ascii }

# 1. binary IS head -> OK, starts (CheckOnly exit 0), no stale wording
Set-Stamp ("go:buildinfo build`t-ldflags=`"-X pkg/lineage.ldflagsRev=$head`"")
$r = Run @{ Repo = $tmp; LogPath = $log }
Check 'binary at HEAD is OK' (($r.Code -eq 0) -and ($r.Out -match 'PROVENANCE: OK') -and ($r.Out -notmatch 'STALE')) $r.Out

# 2. binary one commit behind -> WARNING + still starts (the safe default)
Set-Stamp ("build`t-ldflags=`"-X pkg/lineage.ldflagsRev=$old`"")
$r = Run @{ Repo = $tmp; LogPath = $log }
Check 'stale binary warns and still starts' (($r.Code -eq 0) -and ($r.Out -match 'WARNING - STALE BINARY') -and ($r.Out -match 'behind\s+1 commit')) $r.Out

# 3. same binary, operator opted in -> refuses, starts nothing
$r = Run @{ Repo = $tmp; LogPath = $log; OnStale = 'Refuse' }
Check 'stale binary refuses under -OnStale Refuse' (($r.Code -eq 3) -and ($r.Out -match 'REFUSED - STALE BINARY')) $r.Out

# 4. the env override is honoured with no parameter
$env:SIGNALDECK_ON_STALE_BINARY = 'refuse'
$r = Run @{ Repo = $tmp; LogPath = $log }
Remove-Item Env:\SIGNALDECK_ON_STALE_BINARY
Check 'SIGNALDECK_ON_STALE_BINARY=refuse is honoured' (($r.Code -eq 3) -and ($r.Out -match 'REFUSED - STALE BINARY')) $r.Out

# 5. commit no longer in the repo -> hard refusal (mirrors main.go:110)
Set-Stamp 'build -ldflags="-X pkg/lineage.ldflagsRev=0000000000000000000000000000000000000000"'
$r = Run @{ Repo = $tmp; LogPath = $log }
Check 'absent commit is refused' (($r.Code -eq 3) -and ($r.Out -match 'does not have')) $r.Out

# 6. ...unless the development override is set (same var the daemon uses)
$env:SIGNALDECK_ALLOW_DIRTY_BUILD = '1'
$r = Run @{ Repo = $tmp; LogPath = $log }
Remove-Item Env:\SIGNALDECK_ALLOW_DIRTY_BUILD
Check 'SIGNALDECK_ALLOW_DIRTY_BUILD downgrades absent commit to a warning' (($r.Code -eq 0) -and ($r.Out -match 'WARNING')) $r.Out

# 7. no stamp at all -> hard refusal (mirrors main.go:86)
Set-Stamp 'no build info here'
$r = Run @{ Repo = $tmp; LogPath = $log }
Check 'unstamped binary is refused' (($r.Code -eq 3) -and ($r.Out -match 'unattributable')) $r.Out

# 8. dirty tree alone must NOT block a binary that is exactly HEAD
Set-Stamp ("build`t-ldflags=`"-X pkg/lineage.ldflagsRev=$head`"")
Set-Content -LiteralPath (Join-Path $tmp 'scratch.txt') -Value 'uncommitted' -Encoding ascii
$r = Run @{ Repo = $tmp; LogPath = $log }
Check 'a dirty tree does not block a current binary' (($r.Code -eq 0) -and ($r.Out -match 'PROVENANCE: OK')) $r.Out

# 9. missing binary -> refuse, and say what to run
Remove-Item -LiteralPath $exe -Force
$r = Run @{ Repo = $tmp; LogPath = $log }
Check 'missing binary is refused' (($r.Code -eq 3) -and ($r.Out -match 'no binary')) $r.Out

# 10. the operator message is persisted, not just printed
$logged = (Get-Content -LiteralPath $log -Raw)
Check 'operator message is appended to the log' ($logged -match 'STALE BINARY') $log

# --- the live repo, read-only ---------------------------------------------
$real = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$rlog = Join-Path $tmp 'real.log'
$r = Run @{ Repo = $real; LogPath = $rlog }
Check 'live repo: reports a verdict and starts nothing' (($r.Code -eq 0) -and ($r.Out -match 'PROVENANCE: (OK|WARNING)')) $r.Out
Write-Output ('      live verdict: ' + (($r.Out -split "`r?`n" | Where-Object { $_ -match 'PROVENANCE|behind|stamp|HEAD|NOTE' }) -join ' | '))

Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
Write-Output ''
if ($fails) { Write-Output ("{0} FAILURE(S)" -f $fails); exit 1 }
Write-Output 'all checks passed'
exit 0
