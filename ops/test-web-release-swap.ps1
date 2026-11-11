# Proves Switch-WebBuild (ops/web-release.ps1) cannot leave web\.next missing.
#
# The regression: the promote moved the live build aside and then moved the
# candidate in. When the second move threw, nothing caught it: the task stayed
# stopped and the site stayed down. Runs against scratch directories only.
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

$fails = 0
function Check([string]$what, [bool]$ok) {
  if ($ok) { Write-Host "  ok   $what" }
  else { Write-Host "  FAIL $what" -ForegroundColor Red; $script:fails++ }
}

$root = Join-Path $env:TEMP ("webswap-{0}" -f [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $root -Force | Out-Null
. "$PSScriptRoot\web-release.ps1" -SelfTestOnly -LogPath (Join-Path $root 'release.log')

function NewBuild([string]$dir, [string]$id) {
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  Set-Content -LiteralPath (Join-Path $dir 'BUILD_ID') -Value $id -Encoding ASCII
}
function BuildId([string]$dir) {
  return (Get-Content -LiteralPath (Join-Path $dir 'BUILD_ID') -TotalCount 1).Trim()
}

# Case 1: a clean swap promotes the candidate and keeps the old build.
$c1 = Join-Path $root 'clean'
NewBuild (Join-Path $c1 '.next') 'old'
NewBuild (Join-Path $c1 '.next-release-abc') 'new'
$r1 = Switch-WebBuild -LiveDist (Join-Path $c1 '.next') -CandidateDir (Join-Path $c1 '.next-release-abc') -Kept (Join-Path $c1 '.next-prev-1')
Check 'clean swap reports ok' ($r1.Ok -eq $true)
Check 'clean swap serves the new build' ((BuildId (Join-Path $c1 '.next')) -eq 'new')
Check 'clean swap kept the old build' ((BuildId (Join-Path $c1 '.next-prev-1')) -eq 'old')

# Case 2: the candidate move throws AFTER the live build was moved aside. A file
# held open inside the candidate is what does this on the real host.
$c2 = Join-Path $root 'locked'
NewBuild (Join-Path $c2 '.next') 'old'
NewBuild (Join-Path $c2 '.next-release-abc') 'new'
$held = [System.IO.File]::Open((Join-Path $c2 '.next-release-abc\BUILD_ID'), 'Open', 'Read', 'None')
try {
  $r2 = Switch-WebBuild -LiveDist (Join-Path $c2 '.next') -CandidateDir (Join-Path $c2 '.next-release-abc') -Kept (Join-Path $c2 '.next-prev-1')
} finally {
  $held.Close()
}
Check 'failed swap reports not ok' ($r2.Ok -eq $false)
Check 'failed swap names an error' ($r2.Error.Length -gt 0)
Check 'failed swap says it restored' ($r2.Restored -eq $true)
Check 'failed swap leaves the OLD build live' ((Test-Path -LiteralPath (Join-Path $c2 '.next\BUILD_ID')) -and ((BuildId (Join-Path $c2 '.next')) -eq 'old'))
Check 'failed swap did not strand the old build aside' (-not (Test-Path -LiteralPath (Join-Path $c2 '.next-prev-1')))

# Case 3: no live build at all still promotes.
$c3 = Join-Path $root 'first'
NewBuild (Join-Path $c3 '.next-release-abc') 'new'
$r3 = Switch-WebBuild -LiveDist (Join-Path $c3 '.next') -CandidateDir (Join-Path $c3 '.next-release-abc') -Kept (Join-Path $c3 '.next-prev-1')
Check 'first-ever swap is ok and moved nothing aside' (($r3.Ok -eq $true) -and ($r3.MovedAside -eq $false))

Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
if ($fails -gt 0) { Write-Host "SWAP TEST FAILED ($fails)"; exit 1 }
Write-Host 'SWAP TEST OK'
