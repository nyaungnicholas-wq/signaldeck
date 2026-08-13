# run-daemon-with-provenance.ps1 -- answer "is the binary on disk still the code
# in this repository?" before signaldeckd is started, and say so out loud.
#
# INSTALLED 2026-08-12. Drafted as SWARM 4a, 2026-08-06; the draft copy remains
# at round2-drafts/devops/ for history. ops/daemon-guard.ps1 calls this with
# -CheckOnly immediately before it starts the daemon. Until this file existed in
# ops/, that call site was guarded by a Test-Path that FAILED OPEN, so the
# stale-binary check never ran once between 2026-08-06 and 2026-08-12 - measured
# on install, bin/signaldeckd.exe was 19 commits behind HEAD.
#
# ASCII ONLY in this file. Task Scheduler invokes Windows PowerShell 5.1, which
# reads .ps1 as ANSI, so one UTF-8 em-dash in a comment makes the whole script a
# parse error. That is not hypothetical - see the same warning atop
# ops/daemon-guard.ps1.
#
# ---------------------------------------------------------------------------
# WHAT IS ACTUALLY MISSING
#
# ops/com.signaldeck.daemon.plist routes launchd through
# `signaldeck-ctl.sh launch`, whose build_from_head() rebuilds from `git archive
# HEAD` and refuses rather than exec an unattributable binary. The Windows task
# does not: its Exec action is
#   <Command>C:\...\signaldeck\bin\signaldeckd.exe</Command>
# (Export-ScheduledTask -TaskName "SignalDeck Daemon"), so restarting never
# rebuilds and a binary can outlive the commit it was built from indefinitely.
#
# The daemon is not defenceless - cmd/signaldeckd/main.go refuses to start an
# unattributable build (empty or "+dirty" stamp, main.go:86) and one whose
# commit has left the repository (main.go:110). What NOTHING checks is
# STALENESS: a clean binary stamped with a real, still-reachable commit that is
# simply old. bin/signaldeckd.exe is stamped 049941691d847455... and HEAD is
# 0b1f73b - 22 commits ahead. Every gate in the system passes and the daemon
# happily serves week-old code. That gap is what this script closes.
#
# ---------------------------------------------------------------------------
# WHY THE DEFAULT IS WARN-AND-RUN, NOT REFUSE
#
# A start-time gate chooses between two failures and they are not symmetric.
#
# Refusing turns "the collector runs last week's code" into "the collector does
# not run". SignalDeck's rows are a time series sampled from a market that
# closes; a session lost because the wrapper refused at 06:20 cannot be
# backfilled at 18:00. The refusal is also quiet: the Daemon task carries
# RestartOnFailure 999 x PT5M and ops/daemon-guard.ps1 fires on the same
# 5-minute cadence, so a hard refusal is a silent retry loop, not an alarm.
#
# Running stale is NOT the gamble the daemon's own gate refuses. main.go refuses
# builds that are UNATTRIBUTABLE, because tools/accuracy_registry.py permanently
# rejects their rows - that work is worthless the instant it is done. A
# stale-but-clean build is the opposite: its stamp is a real commit that is
# still in the repository, so every row it writes is attributable and gradable.
# It is old, not unusable.
#
# So: hard-refuse exactly what the daemon already hard-refuses (only sooner, and
# with a message an operator can read before the process exists), and refuse
# staleness only when an operator opts in with -OnStale Refuse.
#
# ---------------------------------------------------------------------------
# A DIRTY WORKING TREE IS REPORTED, NOT ENFORCED  (corrected premise)
#
# The brief asked this to refuse when "the tree is dirty". That is a BUILD-time
# gate and it already exists in the right place: build_from_head() in
# ops/signaldeck-ctl.sh:43-48 refuses to BUILD from an unclean tree. At START
# time the tree says nothing about the binary - the binary carries its own
# -ldflags commit stamp and is unaffected by uncommitted edits. Enforcing it
# here would take the collector down for the duration of every piece of
# in-progress work, including right now (the tree carries 13 modified tracked
# paths from earlier remediation rounds). Tree state is printed because it
# decides whether the suggested FIX will work, not whether the daemon may start.
#
# ---------------------------------------------------------------------------
# WHERE TO WIRE THIS  (read before repointing the Daemon task)
#
# PREFERRED: call it with -CheckOnly from ops/daemon-guard.ps1, immediately
# before `schtasks /Run /TN 'SignalDeck Daemon'`. The guard is the only thing
# that starts the daemon on this box, it already runs every 5 minutes, and it
# already knows how to stand down without starting. Nothing gets re-registered.
#
# NOT PREFERRED: making this script the Daemon task's Exec action. PowerShell
# has no exec(), so the daemon becomes a CHILD of this script, and
# ops/daemon-guard.ps1:79-91 records what that costs: sd_svc_stop is
# `schtasks /End` on "SignalDeck Daemon", /End only terminates the process Task
# Scheduler itself started, and it does not cascade to children - so every
# `signaldeck-ctl.sh stop` silently degrades into the caller's kill -9 fallback,
# skipping the worker drain and the WAL checkpoint. That regression was bought
# once already; do not buy it again to gain a check that -CheckOnly gives free.
# round2-drafts/devops/register-daemon-task.ps1 drafts the repoint anyway,
# because it was asked for, with the same warning on it.
#
# EXIT CODES: 0 = started (or -CheckOnly passed / warned). 3 = refused, nothing
# started. Anything else in wrapper mode is the daemon's own exit code.

[CmdletBinding()]
param(
  # Repo root. Auto-detected by walking up from this script.
  [string] $Repo,

  # What a stale-but-attributable binary should do. Safe default: Warn.
  # Env override: SIGNALDECK_ON_STALE_BINARY=refuse
  [ValidateSet('Warn', 'Refuse')]
  [string] $OnStale,

  # Run the checks, start nothing. This is the mode daemon-guard.ps1 wants.
  [switch] $CheckOnly,

  # Where the operator message is appended. Defaults to logs/daemon-provenance.log.
  [string] $LogPath
)

# Deliberately NOT 'Stop'. A provenance check must never become the reason the
# collector is down; every failure below is handled explicitly, and an
# unexpected one falls through to "start anyway". Continue also keeps PowerShell
# 7.4+ from turning a non-zero native exit into a terminating error.
$ErrorActionPreference = 'Continue'

# --- locate the repo -------------------------------------------------------
if (-not $Repo) {
  $probe = $PSScriptRoot
  while ($probe -and -not (Test-Path (Join-Path $probe 'ops\signaldeck-ctl.sh'))) {
    $probe = Split-Path $probe -Parent
  }
  $Repo = $probe
}
if (-not $Repo -or -not (Test-Path (Join-Path $Repo 'ops\signaldeck-ctl.sh'))) {
  Write-Output 'SIGNALDECK PROVENANCE: REFUSED - cannot locate the repo; pass -Repo. Nothing started.'
  exit 3
}

if (-not $OnStale) {
  $envStale = [Environment]::GetEnvironmentVariable('SIGNALDECK_ON_STALE_BINARY')
  if ($envStale -and $envStale.Trim().ToLower() -eq 'refuse') { $OnStale = 'Refuse' } else { $OnStale = 'Warn' }
}
if (-not $LogPath) { $LogPath = Join-Path $Repo 'logs\daemon-provenance.log' }

$exe = Join-Path $Repo 'bin\signaldeckd.exe'
$fix = 'ops/signaldeck-ctl.sh deploy   (rebuilds from git archive HEAD)'

function Say {
  param([string] $Text)
  Write-Output $Text
  # Logging must never be the reason the daemon does not start.
  try {
    $dir = Split-Path $LogPath -Parent
    if ($dir -and -not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
    Add-Content -LiteralPath $LogPath -Value ('{0} {1}' -f (Get-Date -Format 'yyyy-MM-ddTHH:mm:ss'), $Text)
  } catch { }
}

function Short {
  param([string] $Sha)
  if ($Sha -and $Sha.Length -ge 7) { return $Sha.Substring(0, 7) }
  return '(none)'
}

# --- read the binary's commit stamp ---------------------------------------
# No Go toolchain at service-start time: `go version -m` would answer this but
# would make starting the collector depend on the SDK still being installed and
# on PATH for the task's user. The stamp is a plain string in the build-info
# blob, so read it directly. Cross-checked against
# `go version -m bin/signaldeckd.exe`, which reports the same commit.
# GetEncoding(28591) is ISO-8859-1: byte-preserving, and unlike
# [Text.Encoding]::Latin1 it exists on .NET Framework, i.e. Windows PowerShell.
function Get-BinaryStamp {
  param([string] $Path)
  $txt = [Text.Encoding]::GetEncoding(28591).GetString([IO.File]::ReadAllBytes($Path))
  $revs = @([regex]::Matches($txt, '(?:ldflagsRev=|vcs\.revision=)([0-9a-f]{40})') |
    ForEach-Object { $_.Groups[1].Value } | Sort-Object -Unique)
  $rev = ''
  if ($revs.Count -eq 1) { $rev = $revs[0] }
  return [pscustomobject]@{
    Revision  = $rev                      # '' when absent or self-contradictory
    Ambiguous = ($revs.Count -gt 1)
    Modified  = ($txt -match 'vcs\.modified=true')
  }
}

# Same resolution order ops/install-windows-tasks.ps1 uses for bash: a scheduled
# task does not necessarily inherit an interactive PATH.
$git = @(
  (Join-Path $env:ProgramFiles 'Git\cmd\git.exe'),
  (Join-Path ${env:ProgramFiles(x86)} 'Git\cmd\git.exe')
) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $git) { $git = (Get-Command git -ErrorAction SilentlyContinue).Source }

function Invoke-Git {
  param([string[]] $GitArgs)
  return (& $git -C $Repo @GitArgs 2>$null)
}

$allowDirty = $null -ne [Environment]::GetEnvironmentVariable('SIGNALDECK_ALLOW_DIRTY_BUILD')

# --- 1. is there a binary at all ------------------------------------------
if (-not (Test-Path $exe)) {
  Say 'SIGNALDECK PROVENANCE: REFUSED - no binary'
  Say ('  binary   {0} does not exist' -f $exe)
  Say ('  fix      {0}' -f $fix)
  exit 3
}
$built = (Get-Item $exe).LastWriteTime

# --- 2. is it attributable ------------------------------------------------
# Mirrors cmd/signaldeckd/main.go:86 so the refusal is visible before the
# process exists, and honours the SAME override the daemon does rather than
# inventing a second one.
$stamp = $null
try { $stamp = Get-BinaryStamp $exe } catch { }
if (-not $stamp) {
  Say 'SIGNALDECK PROVENANCE: UNKNOWN - could not read the binary to check its stamp; starting'
} elseif ($stamp.Modified -or $stamp.Revision -eq '') {
  $why = 'carries no commit stamp'
  if ($stamp.Modified) { $why = 'was built from a modified tree (vcs.modified=true)' }
  elseif ($stamp.Ambiguous) { $why = 'carries more than one commit stamp' }
  if (-not $allowDirty) {
    Say 'SIGNALDECK PROVENANCE: REFUSED - unattributable binary'
    Say ('  binary   {0}  (built {1:yyyy-MM-dd HH:mm})' -f $exe, $built)
    Say ('  problem  it {0}; rows it writes can never be graded' -f $why)
    Say ('  fix      {0}' -f $fix)
    Say '  override SIGNALDECK_ALLOW_DIRTY_BUILD=1 (development only)'
    exit 3
  }
  Say ('SIGNALDECK PROVENANCE: WARNING - binary {0}; SIGNALDECK_ALLOW_DIRTY_BUILD is set, starting anyway' -f $why)
}

# --- 3. how far behind HEAD is it -----------------------------------------
# When git cannot be consulted the question is unanswerable, and an unanswerable
# question must not be read as a refusal - the same doctrine as
# lineage.BuildReachable (cmd/signaldeckd/main.go:100-109).
$head = ''
$behind = -1
$dirtyPaths = 0
if (-not $git) {
  Say 'SIGNALDECK PROVENANCE: UNKNOWN - no git.exe found, cannot compare the binary to HEAD; starting'
} elseif ($stamp -and $stamp.Revision -ne '') {
  $head = (Invoke-Git @('rev-parse', 'HEAD'))
  # Empty output means rev-list refused the range, i.e. the commit is gone.
  $countRaw = (Invoke-Git @('rev-list', '--count', ('{0}..HEAD' -f $stamp.Revision)))
  if (-not $head) {
    Say 'SIGNALDECK PROVENANCE: UNKNOWN - git could not resolve HEAD here; starting'
  } elseif (-not $countRaw) {
    # main.go:110 refuses this too. Say it here, where the fix is readable.
    if (-not $allowDirty) {
      Say 'SIGNALDECK PROVENANCE: REFUSED - the binary names a commit this repository does not have'
      Say ('  binary   {0}  (built {1:yyyy-MM-dd HH:mm})' -f $exe, $built)
      Say ('  stamp    {0}' -f $stamp.Revision)
      Say '  problem  history was rewritten under this build; its rows are ungradable'
      Say ('  fix      {0}' -f $fix)
      exit 3
    }
    Say 'SIGNALDECK PROVENANCE: WARNING - the stamped commit is absent from the repository; SIGNALDECK_ALLOW_DIRTY_BUILD is set, starting anyway'
  } else {
    $behind = [int]$countRaw
    $dirtyPaths = @(Invoke-Git @('status', '--porcelain')).Count
  }
}

# --- 4. the staleness verdict ---------------------------------------------
if ($behind -gt 0) {
  $refusing = ($OnStale -eq 'Refuse')
  $verdict = 'WARNING'
  if ($refusing) { $verdict = 'REFUSED' }
  Say ('SIGNALDECK PROVENANCE: {0} - STALE BINARY' -f $verdict)
  Say ('  binary   {0}' -f $exe)
  Say ('  stamp    {0}  (built {1:yyyy-MM-dd HH:mm})' -f (Short $stamp.Revision), $built)
  Say ('  HEAD     {0}' -f (Short $head))
  Say ('  behind   {0} commit(s) - this build cannot contain anything committed since' -f $behind)
  Say ('  fix      {0}' -f $fix)
  if ($dirtyPaths -gt 0) {
    Say ('  NOTE     the tree has {0} uncommitted path(s), so that deploy will REFUSE until they are committed or stashed' -f $dirtyPaths)
  }
  if ($refusing) {
    Say '  action   NOT STARTING (-OnStale Refuse / SIGNALDECK_ON_STALE_BINARY=refuse)'
    Say '           this leaves the collector DOWN until the binary is rebuilt'
    exit 3
  }
  Say '  action   STARTING ANYWAY (default). Set SIGNALDECK_ON_STALE_BINARY=refuse to make this fatal.'
} elseif ($behind -eq 0) {
  Say ('SIGNALDECK PROVENANCE: OK - binary is HEAD ({0})' -f (Short $stamp.Revision))
}

if ($CheckOnly) { exit 0 }

# --- 5. start -------------------------------------------------------------
# Foreground on purpose: this process must remain the one Task Scheduler is
# tracking. See the wiring note in the header - in wrapper mode the daemon is
# still a child of this script, and `schtasks /End` will not reach it.
& $exe
exit $LASTEXITCODE
