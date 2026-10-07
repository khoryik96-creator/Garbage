param([Parameter(Mandatory=$true)][string]$Target)
$ErrorActionPreference = 'Stop'
$imported = $null
$alreadyPresent = @(Get-ChildItem Cert:\CurrentUser\My | ForEach-Object { $_.Thumbprint })
try {
    $thumbprint = $env:GARBAGE_SIGNING_CERT_THUMBPRINT
    if ($env:GARBAGE_SIGNING_PFX_PATH) {
        if (!$env:GARBAGE_SIGNING_PFX_PASSWORD) { throw 'The signing certificate password is not configured.' }
        $password = ConvertTo-SecureString $env:GARBAGE_SIGNING_PFX_PASSWORD -AsPlainText -Force
        $imported = Import-PfxCertificate -FilePath $env:GARBAGE_SIGNING_PFX_PATH -CertStoreLocation Cert:\CurrentUser\My -Password $password
        $thumbprint = $imported.Thumbprint
    }
    if (!$thumbprint) { throw 'Configure a Windows signing certificate before requesting a signed package.' }
    $tool = $env:GARBAGE_SIGNTOOL_PATH
    if (!$tool) {
        $tool = (Get-ChildItem 'C:\Program Files (x86)\Windows Kits\10\bin\*\x64\signtool.exe' | Sort-Object FullName -Descending | Select-Object -First 1).FullName
    }
    if (!$tool) { throw 'Windows SDK signtool.exe was not found.' }
    $timestamp = $env:GARBAGE_SIGNING_TIMESTAMP_URL
    if (!$timestamp) { $timestamp = 'https://timestamp.digicert.com' }
    # Select the imported certificate by thumbprint; no password in process arguments.
    & $tool sign /sha1 $thumbprint /s My /fd SHA256 /tr $timestamp /td SHA256 $Target
    if ($LASTEXITCODE -ne 0) { throw 'Authenticode signing failed.' }
    $signature = Get-AuthenticodeSignature -LiteralPath $Target
    if ($signature.Status -ne 'Valid') { throw "Windows did not verify the signature: $($signature.Status)" }
    Write-Host 'Authenticode signature verified by Windows.'
} finally {
    if ($imported -and $alreadyPresent -notcontains $imported.Thumbprint) { Remove-Item "Cert:\CurrentUser\My\$($imported.Thumbprint)" -ErrorAction SilentlyContinue }
}
