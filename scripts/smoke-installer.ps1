# Smoke test for installer/install.ps1 against two local releases built by
# scripts/build-release.py: install core, add the App, verify, refuse a plain
# install of a newer release, upgrade, refuse to remove a changed file, and
# uninstall without a trace. Changes the user PATH and the Start menu of the
# account it runs in; meant for CI runners.
#
#   pwsh -File scripts/smoke-installer.ps1 -Release DIR -Next DIR [-InstallDir DIR]
param(
    [Parameter(Mandatory = $true)][string]$Release,
    [Parameter(Mandatory = $true)][string]$Next,
    [string]$InstallDir = (Join-Path ([IO.Path]::GetTempPath()) ('facet-smoke-' + [Guid]::NewGuid().ToString('N')) | Join-Path -ChildPath 'Facet'),
    [switch]$KeepPath
)
$ErrorActionPreference = 'Stop'
$installer = Join-Path $PSScriptRoot '..\installer\install.ps1'
$version = (Get-Content -Raw (Join-Path $Release 'build-manifest.json') | ConvertFrom-Json).version
$nextVersion = (Get-Content -Raw (Join-Path $Next 'build-manifest.json') | ConvertFrom-Json).version
$bin = Join-Path $InstallDir 'bin'
$start = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Facet.lnk'
$pathOption = if ($KeepPath) { @('-NoPath') } else { @() }

function Install-Facet([string[]]$Arguments) {
    & pwsh -NoProfile -File $installer -InstallDir $InstallDir -Components none @Arguments
    if ($LASTEXITCODE -ne 0) { throw "install.ps1 $($Arguments -join ' ') exited $LASTEXITCODE" }
}

function Assert-Fails([string[]]$Arguments, [string]$Expect) {
    $output = & pwsh -NoProfile -File $installer -InstallDir $InstallDir -Components none @Arguments 2>&1 | Out-String
    if ($LASTEXITCODE -eq 0 -or $output -notmatch $Expect) { throw "install.ps1 $($Arguments -join ' ') should fail with '$Expect': $output" }
}

function Assert-Equal($Actual, $Expected, [string]$What) {
    if ($Actual -ne $Expected) { throw "${What}: got '$Actual', want '$Expected'" }
}

function User-Path { [Environment]::GetEnvironmentVariable('Path', 'User') }

$pathBefore = User-Path
Install-Facet (@('-Mode', 'core', '-DistributionDir', $Release) + $pathOption)
Assert-Equal (& (Join-Path $bin 'facet.exe') version) "facet v$version" 'facet version'
if (Test-Path -LiteralPath $start) { throw 'core mode made a Start menu entry' }
if (-not $KeepPath -and (User-Path) -notlike "*$bin*") { throw 'the user PATH does not hold the bin folder' }

Install-Facet (@('-Mode', 'app', '-NoLaunch', '-DistributionDir', $Release) + $pathOption)
Assert-Equal (& (Join-Path $bin 'facet-app.exe') version) "facet-app $version" 'facet-app version'
if (-not (Test-Path -LiteralPath $start)) { throw 'app mode made no Start menu entry' }
Install-Facet @('-Verify')

$plan = & pwsh -NoProfile -File $installer -InstallDir $InstallDir -Upgrade -DistributionDir $Next -DryRun | Out-String | ConvertFrom-Json
Assert-Equal $plan.version $nextVersion 'dry-run version'
Assert-Equal (@($plan.products) -join ',') 'core,app' 'dry-run products'

Assert-Fails @('-DistributionDir', $Next, '-NoLaunch') 'Run with -Upgrade'
Install-Facet @('-Upgrade', '-NoLaunch', '-DistributionDir', $Next)
Assert-Equal (& (Join-Path $bin 'facet.exe') version) "facet v$nextVersion" 'upgraded facet version'
Install-Facet @('-Verify')

$license = Join-Path $InstallDir 'LICENSE'
$saved = [IO.File]::ReadAllBytes($license)
Add-Content -LiteralPath $license -Value 'changed'
Assert-Fails @('-Uninstall') 'Preserving a modified or missing owned file'
[IO.File]::WriteAllBytes($license, $saved)

Install-Facet @('-Uninstall')
if (Test-Path -LiteralPath $InstallDir) { throw "uninstall left $InstallDir" }
if (Test-Path -LiteralPath $start) { throw 'uninstall left the Start menu entry' }
Assert-Equal (User-Path) $pathBefore 'the user PATH after uninstall'
Write-Host "install.ps1: core, App, verify, upgrade $version -> $nextVersion and uninstall passed"
