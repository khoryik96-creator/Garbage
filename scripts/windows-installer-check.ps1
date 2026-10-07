param([Parameter(Mandatory=$true)][string]$Setup, [string]$PreviousSetup, [string]$Version = "0.5.1")
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
# Exercise real NSIS extraction-abort and process-interruption paths natively.
# Fault switches are compile-time defines used only by these test installers.
$installDirectory = Split-Path $app
$previousApp = Join-Path $installDirectory 'Garbage Truck.previous.exe'
$workingHash = (Get-FileHash $app -Algorithm SHA256).Hash
$testDirectory = Join-Path $env:TEMP ('garbage-installer-faults-' + [Guid]::NewGuid())
New-Item -ItemType Directory $testDirectory | Out-Null
$fixtureExe = Join-Path $testDirectory 'installed-fixture.exe'
Copy-Item $app $fixtureExe
try {
    foreach ($fault in @('TEST_DENY_EXTRACTION', 'TEST_INTERRUPT_AFTER_BACKUP', 'TEST_INTERRUPT_AFTER_REPLACE')) {
        $faultSetup = Join-Path $testDirectory "$fault.exe"
        $root = Split-Path $PSScriptRoot
        & makensis /V2 "/DVERSION=$Version" "/DSOURCE_EXE=$fixtureExe" "/DICON_FILE=$root\packaging\windows\app.ico" "/DOUTPUT=$faultSetup" "/D$fault" "$root\packaging\windows\installer.nsi"
        if ($LASTEXITCODE -ne 0) { throw "Fault installer did not compile: $fault" }
        $result = Start-Process $faultSetup -ArgumentList '/S' -PassThru -Wait
        if ($result.ExitCode -eq 0) { throw "Fault was not injected: $fault" }
        if (!(Test-Path $app) -or (Get-FileHash $app -Algorithm SHA256).Hash -ne $workingHash) { throw "Interrupted update lost the working executable: $fault" }
        if ((Get-FileHash $database -Algorithm SHA256).Hash -ne $hash) { throw "Failed upgrade changed saved data: $fault" }
        Install-Checked $Setup
        if (Test-Path $previousApp) { throw "Retry left a recovery executable: $fault" }
        if (Test-Path (Join-Path $installDirectory '.garbage-update')) { throw "Retry left staged installer files: $fault" }
        if ((Get-FileHash $database -Algorithm SHA256).Hash -ne $hash) { throw "Retry changed saved data: $fault" }
        Write-Host "Native installer fault and recovery passed: $fault"
    }
    # Simulate legacy interrupted installers, including a missing launch path.
    Copy-Item $app $previousApp -Force
    Install-Checked $Setup
    Copy-Item $app $previousApp -Force
    Remove-Item $app
    Install-Checked $Setup
    if ((Get-FileHash $app -Algorithm SHA256).Hash -ne $workingHash -or (Get-FileHash $database -Algorithm SHA256).Hash -ne $hash) { throw 'Legacy recovery changed the app or saved data.' }
} finally {
    Remove-Item $testDirectory -Recurse -Force -ErrorAction SilentlyContinue
}
$reopen = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Garbage Truck\Open interface again.lnk'
if (!(Test-Path $reopen)) { throw 'The interface reopening shortcut is missing.' }
$shell = New-Object -ComObject WScript.Shell
foreach ($link in @($reopen, (Join-Path (Split-Path $reopen) 'Garbage Truck.lnk'), (Join-Path ([Environment]::GetFolderPath('Desktop')) 'Garbage Truck.lnk'))) {
    $shortcut = $shell.CreateShortcut($link)
    if ($shortcut.TargetPath -ne $app -or $shortcut.WorkingDirectory -ne $installDirectory) { throw "Shortcut targets a missing or temporary directory: $link" }
}
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
