# Smoke test for install.ps1 against releases built by scripts/build-release.py:
# install core, add the App, verify, refuse to remove a changed file, and
# uninstall without a trace. With -Next, a newer release, it also refuses a
# plain install of that release and upgrades to it. Changes the user PATH
# (unless -KeepPath) and the Start menu of the account it runs in; meant for CI
# runners. Runs in PowerShell 7 and Windows PowerShell 5.1; -Shell picks the
# one that runs the installer.
#
#   pwsh -File scripts/smoke-installer.ps1 -Release DIR [-Next DIR] [-Installer FILE]
#       [-Shell pwsh|powershell] [-InstallDir DIR] [-KeepPath]
param(
    [Parameter(Mandatory = $true)][string]$Release,
    [string]$Next = '',
    [string]$Installer = (Join-Path $PSScriptRoot '..\install.ps1'),
    [ValidateSet('pwsh', 'powershell')][string]$Shell = 'pwsh',
    [string]$InstallDir = (Join-Path ([IO.Path]::GetTempPath()) ('facet-smoke-' + [Guid]::NewGuid().ToString('N')) | Join-Path -ChildPath 'Facet'),
    [switch]$KeepPath
)
$ErrorActionPreference = 'Stop'
$Installer = (Resolve-Path -LiteralPath $Installer).Path
$version = (Get-Content -Raw (Join-Path $Release 'build-manifest.json') | ConvertFrom-Json).version
$bin = Join-Path $InstallDir 'bin'
$start = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Facet.lnk'
$pathOption = if ($KeepPath) { @('-NoPath') } else { @() }

function Invoke-Installer([string[]]$Arguments) {
    & $Shell -NoProfile -ExecutionPolicy Bypass -File $Installer -InstallDir $InstallDir @Arguments
}

function Install-Facet([string[]]$Arguments) {
    Invoke-Installer (@('-Components', 'none') + $Arguments)
    if ($LASTEXITCODE -ne 0) { throw "install.ps1 $($Arguments -join ' ') exited $LASTEXITCODE" }
}

function Assert-Fails([string[]]$Arguments, [string]$Expect) {
    # Windows PowerShell turns redirected stderr into errors, which Stop would throw.
    $ErrorActionPreference = 'Continue'
    $output = Invoke-Installer (@('-Components', 'none') + $Arguments) 2>&1 | Out-String
    if ($LASTEXITCODE -eq 0 -or $output -notmatch $Expect) { throw "install.ps1 $($Arguments -join ' ') should fail with '$Expect': $output" }
}

function Assert-Equal($Actual, $Expected, [string]$What) {
    if ($Actual -ne $Expected) { throw "${What}: got '$Actual', want '$Expected'" }
}

function Get-UserPath { [Environment]::GetEnvironmentVariable('Path', 'User') }

$pathBefore = Get-UserPath
Install-Facet (@('-Mode', 'core', '-DistributionDir', $Release) + $pathOption)
Assert-Equal (& (Join-Path $bin 'facet.exe') version) "facet v$version" 'facet version'
if (Test-Path -LiteralPath $start) { throw 'core mode made a Start menu entry' }
if (-not $KeepPath -and (Get-UserPath) -notlike "*$bin*") { throw 'the user PATH does not hold the bin folder' }

Install-Facet (@('-Mode', 'app', '-NoLaunch', '-DistributionDir', $Release) + $pathOption)
Assert-Equal (& (Join-Path $bin 'facet-app.exe') version) "facet-app $version" 'facet-app version'
if (-not (Test-Path -LiteralPath $start)) { throw 'app mode made no Start menu entry' }
Install-Facet @('-Verify')
$upgraded = ''

if ($Next) {
    $nextVersion = (Get-Content -Raw (Join-Path $Next 'build-manifest.json') | ConvertFrom-Json).version
    $plan = Invoke-Installer @('-Upgrade', '-DistributionDir', $Next, '-DryRun') | Out-String | ConvertFrom-Json
    Assert-Equal $plan.version $nextVersion 'dry-run version'
    Assert-Equal (@($plan.products) -join ',') 'core,app' 'dry-run products'

    Assert-Fails @('-DistributionDir', $Next, '-NoLaunch') 'Run with -Upgrade'
    Install-Facet @('-Upgrade', '-NoLaunch', '-DistributionDir', $Next)
    Assert-Equal (& (Join-Path $bin 'facet.exe') version) "facet v$nextVersion" 'upgraded facet version'
    Install-Facet @('-Verify')
    $upgraded = ", upgrade $version -> $nextVersion"
}

$license = Join-Path $InstallDir 'LICENSE'
$saved = [IO.File]::ReadAllBytes($license)
Add-Content -LiteralPath $license -Value 'changed'
Assert-Fails @('-Uninstall') 'Preserving a modified or missing owned file'
[IO.File]::WriteAllBytes($license, $saved)

Install-Facet @('-Uninstall')
if (Test-Path -LiteralPath $InstallDir) { throw "uninstall left $InstallDir" }
if (Test-Path -LiteralPath $start) { throw 'uninstall left the Start menu entry' }
Assert-Equal (Get-UserPath) $pathBefore 'the user PATH after uninstall'
Write-Host "install.ps1 in ${Shell}: core, App, verify$upgraded and uninstall passed"
