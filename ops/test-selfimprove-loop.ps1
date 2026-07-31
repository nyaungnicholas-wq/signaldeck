# The loop's whole value rests on Run() reporting exit codes honestly. `exit 3`
# inside a PowerShell background job leaves State=Completed, so the obvious
# implementation marks every gate green and the loop "improves" a repo it never
# tested. This pins that behaviour.
#
#   pwsh -File ops/test-selfimprove-loop.ps1

. "$PSScriptRoot\selfimprove-loop.ps1" -SelfTestOnly

$ErrorActionPreference = 'Stop'   # a check that ERRORS must not be a check that vanishes

$fail = 0
$ran  = 0
function Check($label, $actual, $expect) {
  $script:ran += 1
  $script:fail += [int]($actual -ne $expect)
  "{0,-34} got={1,-6} want={2,-6} {3}" -f $label, $actual, $expect, $(if ($actual -eq $expect) { 'PASS' } else { 'FAIL' })
}

# If Run() fails to load, every Check below throws while evaluating its argument
# and never increments the counter -- the run then ends with "0 failures" and
# reports success having tested nothing. The expected count is the guard.
$EXPECTED_CHECKS = 11

Check 'native non-zero exit is RED'    (Run t 'cmd /c "exit 3"').Ok             $false
Check 'native zero exit is GREEN'      (Run t 'cmd /c "exit 0"').Ok             $true
Check 'thrown exception is RED'        (Run t 'throw "boom"').Ok                $false
Check 'real go build is GREEN'         (Run t 'cd daemon; go build ./...').Ok   $true
Check 'bogus go package is RED'        (Run t 'cd daemon; go build ./nope/...').Ok $false
Check 'timeout is RED'                 (Run t 'Start-Sleep 30' 2).Ok            $false
Check 'sentinel stripped from output'  ((Run t 'cmd /c "exit 3"').Output -match '__EXIT__') $false

# Every backlog item must carry a runnable verification, or the loop cannot tell
# done from claimed-done.
$text = Get-Content "$PSScriptRoot\IMPROVE_BACKLOG.md" -Raw
$open = [regex]::Matches($text, '(?ms)^## \[ \] (.+?)$(.*?)(?=^## |\z)')
$withVerify = @($open | Where-Object { $_.Groups[2].Value -match '(?m)^verify: `(.+)`\s*$' }).Count
Check 'every open backlog item verifiable' $withVerify $open.Count

# -UntilGoal stops the loop when gates are green AND NextBacklogItem is null. If
# the parser silently returned null while work remained, the loop would declare
# GOAL-MET and quit having done nothing. Work remains, so this must not be null.
Check 'backlog still has work to hand out' ($null -ne (NextBacklogItem)) $true

# The bug this exists to catch: a gate body calling `exit` terminates the job
# before Run() prints its sentinel, so Run() sees no verdict and reports RED.
# The whole battery read red on its first live run and the loop would have spent
# a day "fixing" code that was already correct. Stand-in commands did not catch
# it, so exercise the REAL bodies.
Check 'no gate body calls exit' (@(GateSpecs | Where-Object { $_.c -match '(^|;|\s)exit\s' }).Count) 0

# go-build is the cheapest real gate and is known green right now. If the gate
# plumbing breaks again, this goes red without waiting for a full sweep.
$goBuild = GateSpecs | Where-Object { $_.n -eq 'go-build' }
Check 'real go-build gate body is GREEN' (Run 'go-build' $goBuild.c).Ok $true

if ($ran -ne $EXPECTED_CHECKS) {
  "`nONLY $ran OF $EXPECTED_CHECKS CHECKS RAN -- the rest errored out, so this run proves nothing"
  exit 1
}
if ($fail) { "`n$fail OF $ran CHECKS FAILED"; exit 1 }
"`nALL $ran CHECKS PASSED"
exit 0
