<#
.SYNOPSIS
Prove that a running `next start` instance serves a COMPLETE build: the page,
every chunk the page references, and every font those chunks reference.

.DESCRIPTION
Measured 2026-09-13 on this machine, and the reason this file exists:

    :3000  GET /                                    -> 200 (37131 bytes)
    :3000  GET /login                               -> 200
    :3000  GET /_next/static/chunks/2s7rqns36j9nh.css -> 404
    :8323  GET /_next/static/chunks/2s7rqns36j9nh.css -> 200

Four of the fourteen assets the landing page references 404'd on :3000 while
EVERY ONE of them was present on disk in web/.next/static. The page rendered in
default browser typography with unstyled navigation, and ops/web-guard.ps1
reported "3000 ok" throughout, because /login answered 200.

THE MECHANISM. `next start` indexes .next/static ONCE, at boot. Both web
servers (ops/start-local-workspace.ps1, one per port) serve out of the SAME
mutable web/.next, and `next build` replaces that directory in place. Turbopack
names chunks by content hash, so a rebuild keeps the filename of every chunk
whose content did not change and mints a NEW filename for every chunk that did.
A server that predates the build still answers for the unchanged names and
404s the new ones -- which is why the failure is PARTIAL and why a status-code
probe of one route cannot see it. The four that failed were not older or newer
on disk than the ten that passed; they were the four whose names were new.

So the health question is not "does a route answer 200". It is "does this
instance serve every byte the page it just rendered asked for". That is what
this script measures, and it is deliberately driven off the page's OWN markup
rather than a hardcoded asset list, which would rot at the next build.

Exit 0 = complete. Exit 1 = an asset is missing or served wrong. Exit 2 = the
check could not run (the page itself did not answer), which is NOT the same
finding and must not be reported as one.

.PARAMETER Port
Loopback port of the `next start` instance to check.

.PARAMETER Path
Page to drive the check from. Defaults to "/", which is the public landing
page and pulls in the global stylesheet and the font faces.

.PARAMETER Quiet
Print only the verdict line.
#>
[CmdletBinding()]
param(
    [int]$Port = 8323,
    [string]$Path = '/',
    [int]$TimeoutSec = 20,
    [switch]$Quiet
)

$ErrorActionPreference = 'Stop'
$base = "http://127.0.0.1:$Port"

function Say([string]$m) { if (-not $Quiet) { Write-Output $m } }

# --- the page itself ---------------------------------------------------------
# A page that does not answer is an instance that is DOWN, not an instance
# serving a broken build. Exit 2 keeps those two findings apart: the caller
# restarts on one and pages a human on the other.
try {
    $page = Invoke-WebRequest -Uri "$base$Path" -UseBasicParsing -TimeoutSec $TimeoutSec
} catch {
    Write-Output ("web-assets: UNAVAILABLE {0}{1} did not answer ({2})" -f $base, $Path, $_.Exception.Message)
    exit 2
}
if ([int]$page.StatusCode -lt 200 -or [int]$page.StatusCode -ge 400) {
    Write-Output ("web-assets: UNAVAILABLE {0}{1} -> HTTP {2}" -f $base, $Path, [int]$page.StatusCode)
    exit 2
}

$html = $page.Content
if ([string]::IsNullOrWhiteSpace($html)) {
    Write-Output ("web-assets: UNAVAILABLE {0}{1} answered {2} with an empty body" -f $base, $Path, [int]$page.StatusCode)
    exit 2
}

# --- what the page asked for -------------------------------------------------
$refs = [System.Collections.Generic.List[string]]::new()
foreach ($m in [regex]::Matches($html, '/_next/static/[A-Za-z0-9_\-\./]+\.(?:css|js)')) {
    if (-not $refs.Contains($m.Value)) { $refs.Add($m.Value) | Out-Null }
}
if ($refs.Count -eq 0) {
    # No build assets at all on a Next page means the markup is not what we
    # think it is. Do not call that "complete".
    Write-Output ("web-assets: FAIL {0}{1} referenced no /_next/static assets at all" -f $base, $Path)
    exit 1
}

$expected = @{}   # url -> expected content-type fragment
foreach ($r in $refs) {
    $expected[$r] = if ($r.EndsWith('.css')) { 'text/css' } else { 'javascript' }
}

# --- fetch, and follow CSS to its fonts --------------------------------------
# Fonts are the half a chunk check misses: they are named inside the stylesheet
# as url(../media/<hash>.woff2), never in the HTML. A stylesheet that loads
# while its faces 404 still repaints the page in a fallback family.
$bad = [System.Collections.Generic.List[string]]::new()
$checked = 0
$fontRefs = [System.Collections.Generic.List[string]]::new()

foreach ($url in $refs) {
    $checked++
    try {
        $res = Invoke-WebRequest -Uri "$base$url" -UseBasicParsing -TimeoutSec $TimeoutSec
    } catch {
        $code = 0
        if ($_.Exception.Response) { $code = [int]$_.Exception.Response.StatusCode }
        $bad.Add(("{0} -> HTTP {1}" -f $url, $code)) | Out-Null
        continue
    }
    $ct = [string]$res.Headers['Content-Type']
    if ($ct -notlike ("*" + $expected[$url] + "*")) {
        # A 200 carrying text/plain is the shape of a dev-server or proxy error
        # page standing in for an asset. It is a failure even though it is 200.
        $bad.Add(("{0} -> 200 but Content-Type '{1}', expected {2}" -f $url, $ct, $expected[$url])) | Out-Null
        continue
    }
    if ($url.EndsWith('.css')) {
        foreach ($m in [regex]::Matches([string]$res.Content, 'url\(\.\./media/([A-Za-z0-9_\-\.]+)\)')) {
            $f = '/_next/static/media/' + $m.Groups[1].Value
            if (-not $fontRefs.Contains($f)) { $fontRefs.Add($f) | Out-Null }
        }
    }
}

foreach ($f in $fontRefs) {
    $checked++
    try {
        $null = Invoke-WebRequest -Uri "$base$f" -UseBasicParsing -TimeoutSec $TimeoutSec
    } catch {
        $code = 0
        if ($_.Exception.Response) { $code = [int]$_.Exception.Response.StatusCode }
        $bad.Add(("{0} -> HTTP {1} (font)" -f $f, $code)) | Out-Null
    }
}

# --- verdict -----------------------------------------------------------------
if ($bad.Count -gt 0) {
    Write-Output ("web-assets: FAIL port {0} {1} -- {2} of {3} assets unserved" -f $Port, $Path, $bad.Count, $checked)
    foreach ($b in $bad) { Write-Output ("  " + $b) }
    Write-Output "  The page answered 200 while these did not. A status-code probe cannot see this."
    Write-Output "  Usual cause: this instance predates the current web/.next build. Restart it."
    exit 1
}

Say ("web-assets: OK port {0} {1} -- {2} assets ({3} chunk, {4} font) all served" -f $Port, $Path, $checked, $refs.Count, $fontRefs.Count)
if ($Quiet) { Write-Output ("web-assets: OK port {0} ({1} assets)" -f $Port, $checked) }
exit 0
