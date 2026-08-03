# Proves Run() surfaces a failing command's stderr.
#
# The regression this pins: `Receive-Job 2>&1` redirects Receive-Job's OWN error
# stream, not the native stderr the job's child process wrote. Thirty eighty-loop
# cycles were killed and journalled with an empty diagnostic block while Python
# was printing the exact SyntaxError line and caret to a stream nobody read.
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
. "$PSScriptRoot\selfimprove-loop.ps1" -SelfTestOnly

$fails = 0
function Check([string]$what, [bool]$ok) {
  if ($ok) { Write-Host "  ok   $what" }
  else { Write-Host "  FAIL $what" -ForegroundColor Red; $script:fails++ }
}

$tmp = Join-Path $env:TEMP ("runcheck-{0}.py" -f [guid]::NewGuid().ToString('N').Substring(0, 8))

# 1. A syntax error must reach the caller, not vanish.
Set-Content -Path $tmp -Value "call = 'UP' if up : 'DOWN'" -Encoding UTF8
$r = Run "syntaxerr" "python `"$tmp`"" 120
Check "failing script reports Ok=false"      (-not $r.Ok)
Check "stderr text is captured"              ($r.Output -match 'SyntaxError')
Check "the offending source line is present" ($r.Output -match 'call =')

# 2. A traceback from a script that starts and then dies must survive too.
Set-Content -Path $tmp -Value "raise ValueError('boom-42')" -Encoding UTF8
$r = Run "traceback" "python `"$tmp`"" 120
Check "runtime failure reports Ok=false" (-not $r.Ok)
Check "traceback text is captured"       ($r.Output -match 'boom-42')

# 3. Success still reports Ok and keeps stdout — the fix must not invert the verdict.
Set-Content -Path $tmp -Value "print('ISSUED=7')" -Encoding UTF8
$r = Run "success" "python `"$tmp`"" 120
Check "passing script reports Ok=true" ($r.Ok)
Check "stdout is captured"             ($r.Output -match 'ISSUED=7')

# 4. A non-zero exit with no output is still a failure (the sentinel path).
$r = Run "exitcode" "cmd /c exit 3" 120
Check "non-zero exit reports Ok=false" (-not $r.Ok)

Remove-Item $tmp -Force -ErrorAction SilentlyContinue
if ($fails) { Write-Host "$fails check(s) failed" -ForegroundColor Red; exit 1 }
Write-Host "all Run() stderr checks passed"
