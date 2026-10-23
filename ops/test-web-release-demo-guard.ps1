# Proves Find-DemoDataBuild (ops/web-release.ps1) refuses a demo-data web build:
# a build made with NEXT_PUBLIC_DEMO_DATA=1 shows synthetic
# values under a "DEMO DATA" banner and must never be promoted to the public site
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

$fails = 0
function Check([string]$what, [bool]$ok) {
  if ($ok) { Write-Host "  ok   $what" }
  else { Write-Host "  FAIL $what" -ForegroundColor Red; $script:fails++ }
}

$root = Join-Path $env:TEMP ("demoguard-{0}" -f [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $root -Force | Out-Null
. "$PSScriptRoot\web-release.ps1" -SelfTestOnly -LogPath (Join-Path $root 'release.log')

function NewBuild([string]$name, [hashtable]$files) {
  $buildDir = Join-Path $root $name
  New-Item -ItemType Directory -Path $buildDir -Force | Out-Null
  $utf8NoBom = New-Object System.Text.UTF8Encoding $false
  foreach ($relPath in $files.Keys) {
    $fullPath = Join-Path $buildDir $relPath
    $parentDir = Split-Path $fullPath -Parent
    if (-not (Test-Path -LiteralPath $parentDir -PathType Container)) {
      New-Item -ItemType Directory -Path $parentDir -Force | Out-Null
    }
    [System.IO.File]::WriteAllText($fullPath, $files[$relPath], $utf8NoBom)
  }
  return $buildDir
}

$banner = 'DEMO DATA - ' + $DemoBannerMarker + '. Real SignalDeck figures are withheld pending data permission.'

# Case 1: clean build
try {
  $dir = NewBuild 'clean' @{
    'static\chunks\a.js' = 'console.log("ok")'
    'server\app\index.html' = '<html><body>SignalDeck</body></html>'
    'required-server-files.json' = '{"config":{"env":{"SIGNALDECK_DEMO_BANNER":""}}}'
  }
  $r = Find-DemoDataBuild $dir
  Check 'clean build: no hits, 3 scanned' ($r.Hits.Count -eq 0 -and $r.Scanned -eq 3)
} catch { Check ("case 1 threw: " + $_.Exception.Message) $false }

# Case 2: banner in a client chunk
try {
  $dir = NewBuild 'client-banner' @{
    'static\chunks\a.js' = ('var t="' + $banner + '";')
    'server\app\index.html' = '<html></html>'
  }
  $r = Find-DemoDataBuild $dir
  Check 'banner in client chunk: 1 hit (a.js)' ($r.Hits.Count -eq 1 -and $r.Hits[0].EndsWith('a.js'))
} catch { Check ("case 2 threw: " + $_.Exception.Message) $false }

# Case 3: banner in prerendered html only
try {
  $dir = NewBuild 'prerendered-banner' @{
    'server\app\accuracy.html' = ('<div data-demo-data="1">' + $banner + '</div>')
    'static\chunks\b.js' = 'x'
  }
  $r = Find-DemoDataBuild $dir
  Check 'banner in prerendered html: 1 hit (accuracy.html)' ($r.Hits.Count -eq 1 -and $r.Hits[0].EndsWith('accuracy.html'))
} catch { Check ("case 3 threw: " + $_.Exception.Message) $false }

# Case 4: banner in an .rsc payload and in required-server-files.json
try {
  $dir = NewBuild 'rsc-and-config-banner' @{
    'server\app\rsc\x.rsc' = ('{"payload":"' + $banner + '"}')
    'required-server-files.json' = ('{"config":{"env":{"SIGNALDECK_DEMO_BANNER":"' + $banner + '"}}}')
    'static\chunks\c.js' = 'x'
  }
  $r = Find-DemoDataBuild $dir
  Check 'banner in .rsc and config: 2 hits' ($r.Hits.Count -eq 2)
} catch { Check ("case 4 threw: " + $_.Exception.Message) $false }

# Case 5: banner only inside the build cache
try {
  $dir = NewBuild 'cache-only' @{
    'cache\turbopack\x.sst' = $banner
    'static\chunks\c.js' = 'x'
  }
  $r = Find-DemoDataBuild $dir
  Check 'banner in cache only: 0 hits, 1 scanned' ($r.Hits.Count -eq 0 -and $r.Scanned -eq 1)
} catch { Check ("case 5 threw: " + $_.Exception.Message) $false }

# Case 6: banner inside a font file
try {
  $dir = NewBuild 'font-banner' @{
    'static\media\f.woff2' = $banner
    'static\chunks\d.js' = 'x'
  }
  $r = Find-DemoDataBuild $dir
  Check 'banner in font file: 0 hits, 1 scanned' ($r.Hits.Count -eq 0 -and $r.Scanned -eq 1)
} catch { Check ("case 6 threw: " + $_.Exception.Message) $false }

# Case 7: empty build directory
$threw = $false
try {
  $dir = NewBuild 'empty' @{}
  Find-DemoDataBuild $dir | Out-Null
  $threw = $false
} catch { $threw = $true }
Check 'an empty build throws instead of passing' $threw

# Case 8: a cache-only build
$threw = $false
try {
  $dir = NewBuild 'cache-only-build' @{
    'cache\x.js' = 'x'
  }
  Find-DemoDataBuild $dir | Out-Null
  $threw = $false
} catch { $threw = $true }
Check 'a cache-only build throws (nothing servable)' $threw

# Case 9: missing directory
$threw = $false
try {
  $dir = Join-Path $root 'does-not-exist'
  Find-DemoDataBuild $dir | Out-Null
  $threw = $false
} catch { $threw = $true }
Check 'missing directory throws' $threw

# Case 10: coupling: web/next.config.ts banner contains the guard marker
try {
  $cfg = [System.IO.File]::ReadAllText((Join-Path (Get-Location) 'web\next.config.ts'))
  Check 'web/next.config.ts banner contains the guard marker' ($cfg.Contains($DemoBannerMarker))
} catch { Check ("case 10 threw: " + $_.Exception.Message) $false }

# Case 11: wiring: web-release.ps1 contains the guard logic
try {
  $rel = [System.IO.File]::ReadAllText((Join-Path $PSScriptRoot 'web-release.ps1'))
  Check 'web-release.ps1 contains Find-DemoDataBuild $CandidateDir' ($rel.Contains('Find-DemoDataBuild $CandidateDir'))
  Check 'web-release.ps1 contains if ($demo.Hits.Count -gt 0)' ($rel.Contains('if ($demo.Hits.Count -gt 0)'))
  Check 'web-release.ps1 contains if ($env:NEXT_PUBLIC_DEMO_DATA)' ($rel.Contains('if ($env:NEXT_PUBLIC_DEMO_DATA)'))
} catch { Check ("case 11 threw: " + $_.Exception.Message) $false }

# Case 12: real demo build, only if it exists
try {
  $demoDir = Join-Path (Get-Location) 'web\.next-demo'
  if (Test-Path -LiteralPath $demoDir) {
    $r = Find-DemoDataBuild $demoDir
    Check ("the real demo build at web\.next-demo is caught ({0} of {1} files)" -f $r.Hits.Count, $r.Scanned) ($r.Hits.Count -gt 0)
  } else {
    Write-Host '  skip web\.next-demo not built (run ops\demo-run.ps1 -BuildOnly to include it)'
  }
} catch { Check ("case 12 threw: " + $_.Exception.Message) $false }

Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue

if ($fails -gt 0) { Write-Host ("FAILED: {0} check(s)" -f $fails) -ForegroundColor Red; exit 1 }
Write-Host 'DEMO GUARD SELFTEST OK'
exit 0