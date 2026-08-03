# Proves AppendLine keeps writing while another handle holds the file open.
#
# The regression: `Add-Content` raises a NON-terminating error under a lock, so
# `try { Add-Content } catch { }` caught nothing and 188 ledger lines were lost
# to the error stream while the loop reported success.
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
. "$PSScriptRoot\selfimprove-loop.ps1" -SelfTestOnly

$fails = 0
function Check([string]$what, [bool]$ok) {
  if ($ok) { Write-Host "  ok   $what" }
  else { Write-Host "  FAIL $what" -ForegroundColor Red; $script:fails++ }
}

$p = Join-Path $env:TEMP ("appendcheck-{0}.jsonl" -f [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType File -Path $p -Force | Out-Null

Check "writes to an unlocked file" (AppendLine $p '{"n":1}')

# Hold the file open the way a tail/reader does, then keep writing.
$reader = [IO.FileStream]::new($p, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
try {
  Check "writes while a reader holds the handle" (AppendLine $p '{"n":2}')
  Check "and again"                              (AppendLine $p '{"n":3}')
} finally { $reader.Dispose() }

$lines = @(Get-Content $p | Where-Object { $_ -match '\S' })
Check "every line landed (got $($lines.Count), want 3)" ($lines.Count -eq 3)
Check "content is intact" (($lines -join '') -match '"n":1.*"n":2.*"n":3')
Check "no BOM was written" ([IO.File]::ReadAllBytes($p)[0] -ne 0xEF)

Remove-Item $p -Force -ErrorAction SilentlyContinue
if ($fails) { Write-Host "$fails check(s) failed" -ForegroundColor Red; exit 1 }
Write-Host "all AppendLine checks passed"
