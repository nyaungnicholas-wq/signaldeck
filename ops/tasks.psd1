@{
    GeneratedUtc = '2026-09-19T23:35:33Z'
    Prefix = 'SignalDeck'
    Tasks = @(
        @{
            TaskName = 'SignalDeck Accuracy'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/accuracy-registry.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/accuracy.out.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskDailyTrigger')
        }
        @{
            TaskName = 'SignalDeck Anchor-Publish'
            Execute = '"C:\Program Files\Git\bin\bash.exe"'
            Arguments = '"/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/anchor-publish.sh"'
            WorkingDirectory = ''
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskDailyTrigger')
        }
        @{
            TaskName = 'SignalDeck Bias'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/nightly-bias.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/bias.out.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskDailyTrigger')
        }
        @{
            TaskName = 'SignalDeck Check-Grader-Health'
            Execute = 'powershell.exe'
            Arguments = '-NoProfile -ExecutionPolicy Bypass -Command "& ''C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\check-grader-health.ps1'' *>> ''C:\Users\Nicholas_N\Desktop\claude code\signaldeck\logs\check-grader-health.log''"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskDailyTrigger')
        }
        @{
            TaskName = 'SignalDeck Check-Task-Health'
            Execute = 'powershell.exe'
            Arguments = '-NoProfile -ExecutionPolicy Bypass -Command "& ''C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\check-task-health.ps1'' *>> ''C:\Users\Nicholas_N\Desktop\claude code\signaldeck\logs\check-task-health.log''"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskDailyTrigger')
        }
        @{
            TaskName = 'SignalDeck Cleanup'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/signaldeck-cleanup.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/cleanup.sched.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskDailyTrigger')
        }
        @{
            TaskName = 'SignalDeck Daemon'
            Execute = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck\bin\signaldeckd.exe'
            Arguments = ''
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck\daemon'
            ExecutionTimeLimit = 'PT0S'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @()
        }
        @{
            TaskName = 'SignalDeck Daemon Keepalive'
            Execute = 'powershell'
            Arguments = '-NoProfile -ExecutionPolicy Bypass -File "C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\daemon-guard.ps1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT10M'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskLogonTrigger','MSFT_TaskTimeTrigger')
        }
        @{
            TaskName = 'SignalDeck Daily-Refresh'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/signaldeck-refresh.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/refresh.sched.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Highest'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger')
        }
        @{
            TaskName = 'SignalDeck Eighty Loop'
            Execute = 'powershell'
            Arguments = '-NoProfile -ExecutionPolicy Bypass -Command "& ''C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\eighty-loop.ps1'' -Hours 24 -MaxCycles 500 *>> ''C:\Users\Nicholas_N\Desktop\claude code\signaldeck\logs\eighty-console.log''"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT0S'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $false
            Triggers = @('MSFT_TaskLogonTrigger','MSFT_TaskTimeTrigger')
        }
        @{
            TaskName = 'SignalDeck Local Workspace'
            Execute = 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe'
            Arguments = '-NoProfile -WindowStyle Hidden -File "C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\start-local-workspace.ps1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT0S'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskTimeTrigger')
        }
        @{
            TaskName = 'SignalDeck Market-Close'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/market-close.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/schedule.out.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Highest'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger')
        }
        @{
            TaskName = 'SignalDeck Market-Open'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/market-open-guard.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/schedule.out.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger','MSFT_TaskWeeklyTrigger')
        }
        @{
            TaskName = 'SignalDeck Research-Liveness'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/research-liveness.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/research-liveness.out.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskDailyTrigger')
        }
        @{
            TaskName = 'SignalDeck Restore'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/restore-rehearsal-notify.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/restore.out.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskWeeklyTrigger')
        }
        @{
            TaskName = 'SignalDeck Revalidation'
            Execute = '"C:\Program Files\Git\bin\bash.exe"'
            Arguments = '"/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/revalidate-structural.sh"'
            WorkingDirectory = ''
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskTrigger')
        }
        @{
            TaskName = 'SignalDeck Structural-Liveness'
            Execute = 'C:\Program Files\Git\bin\bash.exe'
            Arguments = '-lc "''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/ops/structural-liveness.sh'' >> ''/c/Users/Nicholas_N/Desktop/claude code/signaldeck/logs/structural-liveness.out.log'' 2>&1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT6H'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskDailyTrigger')
        }
        @{
            TaskName = 'SignalDeck Tunnel'
            Execute = 'C:\Users\Nicholas_N\AppData\Local\Microsoft\WinGet\Links\ngrok.exe'
            Arguments = 'http 8322 --domain=spearfish-dwindle-module.ngrok-free.dev --log="C:\Users\Nicholas_N\Desktop\claude code\signaldeck\logs\tunnel.log" --log-level=warn'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT0S'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @()
        }
        @{
            TaskName = 'SignalDeck Tunnel Keepalive'
            Execute = 'powershell.exe'
            Arguments = '-NoProfile -ExecutionPolicy Bypass -File "C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\tunnel-guard.ps1"'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck'
            ExecutionTimeLimit = 'PT10M'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskLogonTrigger','MSFT_TaskTimeTrigger')
        }
        @{
            TaskName = 'SignalDeck Web'
            Execute = 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe'
            Arguments = '-NoProfile -WindowStyle Hidden -File "C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\start-local-workspace.ps1" -Port 8323'
            WorkingDirectory = 'C:\Users\Nicholas_N\Desktop\claude code\signaldeck\web'
            ExecutionTimeLimit = 'PT0S'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskBootTrigger')
        }
        @{
            TaskName = 'SignalDeck Web Keepalive'
            Execute = 'powershell.exe'
            Arguments = '-NoProfile -ExecutionPolicy Bypass -File "C:\Users\Nicholas_N\Desktop\claude code\signaldeck\ops\web-guard.ps1"'
            WorkingDirectory = ''
            ExecutionTimeLimit = 'PT3M'
            LogonType = 'S4U'
            RunLevel = 'Limited'
            UserId = 'Nicholas_N'
            Enabled = $true
            Triggers = @('MSFT_TaskTimeTrigger')
        }
    )
}
