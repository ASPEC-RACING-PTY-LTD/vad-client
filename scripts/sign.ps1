<#
.SYNOPSIS
  Authenticode-signs files with signtool (MASTER_PLAN 7.14, O4).

.DESCRIPTION
  Reads the code-signing certificate from environment variables:
    WINDOWS_CERT_PFX       base64 of the .pfx file (required to sign)
    WINDOWS_CERT_PASSWORD  .pfx password (optional)
  When WINDOWS_CERT_PFX is unset or empty the script prints a notice and exits
  0 without signing, so release builds stay unsigned until a certificate is
  supplied. Signatures use SHA-256 and an RFC 3161 timestamp; each file is
  verified afterwards. Secrets are never written to the output.

.EXAMPLE
  ./scripts/sign.ps1 desktop/build/bin/VaultDock.exe
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0, ValueFromRemainingArguments = $true)]
    [string[]] $Path,
    [string[]] $TimestampUrl = @('http://timestamp.digicert.com', 'http://timestamp.sectigo.com'),
    [string] $Description = 'VaultDock'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Write-Notice([string] $msg) {
    if ($env:GITHUB_ACTIONS -eq 'true') { Write-Host "::notice::$msg" } else { Write-Host $msg }
}

if ([string]::IsNullOrWhiteSpace($env:WINDOWS_CERT_PFX)) {
    Write-Notice 'No signing certificate configured (WINDOWS_CERT_PFX is empty); skipping Authenticode signing.'
    exit 0
}

$files = foreach ($p in $Path) {
    if (-not (Test-Path -LiteralPath $p -PathType Leaf)) { throw "File to sign not found: $p" }
    (Resolve-Path -LiteralPath $p).Path
}

function Find-SignTool {
    $cmd = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $kits = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
    $found = Get-ChildItem -Path $kits -Filter signtool.exe -Recurse -ErrorAction SilentlyContinue |
        Where-Object { $_.DirectoryName -match '\\x64$' } |
        Sort-Object { $_.Directory.Parent.Name } -Descending |
        Select-Object -First 1
    if ($found) { return $found.FullName }
    throw 'signtool.exe not found (install the Windows SDK).'
}

$signtool = Find-SignTool
$tempDir = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
$pfx = Join-Path $tempDir ("sign-{0}.pfx" -f [guid]::NewGuid().ToString('N'))

try {
    try {
        [IO.File]::WriteAllBytes($pfx, [Convert]::FromBase64String($env:WINDOWS_CERT_PFX.Trim()))
    } catch {
        throw 'WINDOWS_CERT_PFX is not valid base64.'
    }

    $auth = @('/f', $pfx)
    if (-not [string]::IsNullOrEmpty($env:WINDOWS_CERT_PASSWORD)) { $auth += @('/p', $env:WINDOWS_CERT_PASSWORD) }

    foreach ($file in $files) {
        $signed = $false
        foreach ($ts in $TimestampUrl) {
            Write-Host "Signing $file (timestamp $ts)"
            # signtool never prints the password; its own messages are kept for
            # diagnosis. Do not add /debug or echo the argument list.
            & $signtool sign /fd sha256 /td sha256 /tr $ts /d $Description @auth $file
            if ($LASTEXITCODE -eq 0) { $signed = $true; break }
            Write-Warning "signtool failed with exit code $LASTEXITCODE; retrying with the next timestamp server."
            Start-Sleep -Seconds 5
        }
        if (-not $signed) { throw "Could not sign $file." }

        & $signtool verify /pa /q $file
        if ($LASTEXITCODE -ne 0) { throw "Signature verification failed for $file." }
        Write-Host "Signed and verified $file"
    }
} finally {
    if (Test-Path -LiteralPath $pfx) { Remove-Item -LiteralPath $pfx -Force }
}
exit 0
