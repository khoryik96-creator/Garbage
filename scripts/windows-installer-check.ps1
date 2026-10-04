param([Parameter(Mandatory=$true)][string]$Setup, [string]$PreviousSetup)
$ErrorActionPreference = 'Stop'
$data = Join-Path $env:LOCALAPPDATA 'GarbageTruck'
$app = Join-Path $env:LOCALAPPDATA 'Programs\Garbage Truck\Garbage Truck.exe'
$report = Join-Path $env:TEMP 'garbage-installer-history.json'
function Install-Checked([string]$Installer) {
    $result = Start-Process -FilePath $Installer -ArgumentList '/S' -PassThru -Wait
    if ($result.ExitCode -ne 0 -or !(Test-Path $app)) { throw 'Installer failed.' }
}
function Uninstall-Checked {
    $uninstaller = Join-Path (Split-Path $app) 'Uninstall.exe'
    $result = Start-Process $uninstaller -ArgumentList '/S' -PassThru -Wait
    if ($result.ExitCode -ne 0) { throw 'Uninstall failed.' }
    $deadline = (Get-Date).AddSeconds(15)
    while ((Test-Path $app) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 100 }
    if (Test-Path $app) { throw 'Uninstall left the application behind.' }
}
Install-Checked $(if ($PreviousSetup) { $PreviousSetup } else { $Setup })
python scripts/smoke-desktop.py --binary $app --data-dir $data --report $report
if ($LASTEXITCODE -ne 0) { throw 'Installed host failed launch/approval/undo/history checks.' }
$run = (Get-Content $report -Raw | ConvertFrom-Json).run_id
$database = Join-Path $data 'autocoder.db'
$hash = (Get-FileHash $database -Algorithm SHA256).Hash
$marker = Join-Path $data 'preserve-check.txt'
Set-Content $marker 'keep my saved workspace'
Install-Checked $Setup
if (!(Test-Path $marker) -or (Get-FileHash $database -Algorithm SHA256).Hash -ne $hash) { throw 'Upgrade changed saved data.' }
$reopen = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Garbage Truck\Open interface again.lnk'
if (!(Test-Path $reopen)) { throw 'The interface reopening shortcut is missing.' }
python scripts/smoke-desktop.py --binary $app --data-dir $data --expect-run $run
if ($LASTEXITCODE -ne 0) { throw 'Upgrade lost saved history.' }
$hash = (Get-FileHash $database -Algorithm SHA256).Hash
$signature = Get-AuthenticodeSignature -LiteralPath $app
Write-Host "Installed application signing status: $($signature.Status)"
Uninstall-Checked
if (!(Test-Path $marker) -or (Get-FileHash $database -Algorithm SHA256).Hash -ne $hash) { throw 'Uninstall changed saved data.' }
Install-Checked $Setup
if ((Get-FileHash $database -Algorithm SHA256).Hash -ne $hash) { throw 'Reinstall changed saved data.' }
python scripts/smoke-desktop.py --binary $app --data-dir $data --expect-run $run
if ($LASTEXITCODE -ne 0) { throw 'Reinstall lost saved history.' }
Uninstall-Checked
Write-Host 'Native Windows install, upgrade, uninstall, reinstall, and SQLite history checks passed.'
