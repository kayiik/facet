# Opt-in real source build of scripts/install-source.ps1. The build runs in a
# child process whose profile (HOME, USERPROFILE, APPDATA, LOCALAPPDATA) and Go
# build cache are throwaway; the existing Go module cache is only read. The
# user PATH is never changed (-NoPath). -WithComposer also installs the locked
# Remotion packages (network).
param([switch]$KeepArtifacts, [switch]$WithComposer)
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'Windows integration test requires Windows.' }
$repo = Split-Path -Parent $PSScriptRoot
$installer = Join-Path $PSScriptRoot 'install-source.ps1'
$version = (Get-Content -Raw -LiteralPath (Join-Path $repo 'package.json') | ConvertFrom-Json).version
$arch = if ("$env:PROCESSOR_ARCHITECTURE $env:PROCESSOR_ARCHITEW6432" -match 'ARM64') { 'arm64' } else { 'amd64' }
$root = Join-Path ([IO.Path]::GetTempPath()) ('facet-source-install-' + [guid]::NewGuid().ToString('N'))
$shell = (Get-Process -Id $PID).Path
function Get-StoredUserPath {
    # The stored value, unexpanded: [Environment]::GetEnvironmentVariable
    # expands a REG_EXPAND_SZ PATH such as %USERPROFILE%\AppData\... with this
    # process's variables, which this test redirects to a throwaway profile.
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
    if (-not $key) { return $null }
    try { return $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } finally { $key.Close() }
}
$userPath = Get-StoredUserPath
$moduleCache = "$(& go env GOMODCACHE)".Trim()
if ($LASTEXITCODE -ne 0 -or -not $moduleCache) { throw 'go env GOMODCACHE failed; is Go installed?' }
$names = @('HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'GOCACHE', 'GOMODCACHE', 'GOPATH', 'FACET_HOME', 'FACET_REMOTION_COMPOSER')
$saved = @{}
foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name) }
Write-Host "Isolated test root: $root"
try {
    $profileDir = Join-Path $root 'profile'
    foreach ($directory in @((Join-Path $profileDir 'AppData\Roaming'), (Join-Path $profileDir 'AppData\Local'))) { New-Item -ItemType Directory -Path $directory -Force | Out-Null }
    $env:HOME = $profileDir; $env:USERPROFILE = $profileDir
    $env:APPDATA = Join-Path $profileDir 'AppData\Roaming'; $env:LOCALAPPDATA = Join-Path $profileDir 'AppData\Local'
    $env:GOCACHE = Join-Path $root 'go-build'; $env:GOPATH = Join-Path $root 'go'; $env:GOMODCACHE = $moduleCache
    $env:FACET_HOME = $null; $env:FACET_REMOTION_COMPOSER = $null
    $components = if ($WithComposer) { 'remotion' } else { 'none' }
    $facetHome = Join-Path $profileDir '.facet'
    $runtime = Join-Path $facetHome "runtimes\$version-dev-windows-$arch"
    $facet = Join-Path $facetHome 'current\bin\facet.exe'
    foreach ($round in 1, 2) {
        # The second round replaces the active build of the same version.
        & $shell -NoProfile -ExecutionPolicy Bypass -File $installer -NoPath -Components $components
        if ($LASTEXITCODE -ne 0) { throw "install-source.ps1 failed in round $round." }
        $current = Get-Item -LiteralPath (Join-Path $facetHome 'current') -Force
        if ($current.LinkType -ne 'Junction' -or "$(@($current.Target)[0])".TrimEnd('\') -ne $runtime) { throw "~/.facet/current is not a junction to $runtime." }
        $reported = "$(& $facet version)".Trim()
        if ($LASTEXITCODE -ne 0 -or $reported -ne "facet v$version-dev") { throw "Unexpected version: $reported" }
        $record = Get-Content -Raw -LiteralPath (Join-Path $runtime 'components.json') | ConvertFrom-Json
        if ($record.version -ne "$version-dev" -or @($record.components).Count -ne [int][bool]$WithComposer) { throw 'components.json does not describe the build.' }
        foreach ($file in @('dependencies\remotion-composer\package-lock.json', 'dependencies\remotion-composer\composer-manifest.json', 'LICENSE', 'THIRD_PARTY_NOTICES.md')) {
            if (-not (Test-Path -LiteralPath (Join-Path $runtime $file) -PathType Leaf)) { throw "Runtime is missing $file" }
        }
        if ($WithComposer -and -not (Test-Path -LiteralPath (Join-Path $runtime 'dependencies\remotion-composer\node_modules\remotion\package.json'))) { throw 'Remotion packages are missing.' }
        foreach ($retired in @('bin', 'bundle')) { if (Test-Path -LiteralPath (Join-Path $facetHome $retired)) { throw "Retired ~/.facet/$retired layout created." } }
        if (@(Get-ChildItem -LiteralPath (Join-Path $facetHome 'runtimes') -Force).Count -ne 1) { throw 'Staging or replaced runtimes were left behind.' }
    }
    Push-Location $root
    try { & $facet doctor | Out-Null; if ($LASTEXITCODE -ne 0) { throw 'facet doctor failed.' } } finally { Pop-Location }
    if ((Get-StoredUserPath) -cne $userPath) { throw 'The user PATH changed.' }
    "PASS: source build into runtimes\$version-dev-windows-$arch, current junction, rebuild of the active runtime, doctor, PATH untouched."
} finally {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
    if (-not $KeepArtifacts) { Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue }
}
