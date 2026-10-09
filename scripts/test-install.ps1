# Parser and preflight regression tests for scripts/install-source.ps1: no
# builds, downloads, PATH changes or profile writes. Every run of the script
# is a child process with a throwaway profile and a PATH holding only shims.
$ErrorActionPreference = 'Stop'
$installer = Join-Path $PSScriptRoot 'install-source.ps1'
$tokens = $null
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($installer, [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw ($errors | Out-String) }
$content = [IO.File]::ReadAllText($installer)
if ($content -match '\$env:GOTOOLCHAIN\s*=|GOTOOLCHAIN\s*=\s*.?local|bundle --target app|facet-ui|CreateShortcut') { throw 'install-source.ps1 must not pin GOTOOLCHAIN, build an app bundle, or create shortcuts.' }
try {
    & ([scriptblock]::Create($content)) -NoPath
    throw 'Piped invocation unexpectedly succeeded.'
} catch {
    if ($_.Exception.Message -notmatch 'complete source checkout') { throw }
}
if ($env:OS -ne 'Windows_NT') { 'PASS: parser and piped invocation (execution is Windows-only).'; return }

$root = Join-Path ([IO.Path]::GetTempPath()) ('facet-source-preflight-' + [guid]::NewGuid().ToString('N'))
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
$names = @('HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'PATH')
$saved = @{}
foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name) }
function Invoke-Source([string]$Script, [string[]]$Arguments) {
    $ErrorActionPreference = 'Continue'
    $text = (& $shell -NoProfile -ExecutionPolicy Bypass -File $Script @Arguments 2>&1 | ForEach-Object { "$_" }) -join "`n"
    return @{ Code = $LASTEXITCODE; Output = $text }
}
try {
    $profileDir = Join-Path $root 'profile'
    $shims = Join-Path $root 'shims'
    foreach ($directory in @($profileDir, (Join-Path $profileDir 'AppData\Roaming'), (Join-Path $profileDir 'AppData\Local'), $shims)) { New-Item -ItemType Directory -Path $directory -Force | Out-Null }
    $env:HOME = $profileDir; $env:USERPROFILE = $profileDir
    $env:APPDATA = Join-Path $profileDir 'AppData\Roaming'; $env:LOCALAPPDATA = Join-Path $profileDir 'AppData\Local'
    $env:PATH = "$shims;$env:SystemRoot\System32"
    function Set-Shim([string]$Name, [string]$Body) { [IO.File]::WriteAllText((Join-Path $shims "$Name.cmd"), "@echo off`r`n$Body`r`n") }

    $copy = Join-Path $root 'incomplete/scripts/install-source.ps1'
    New-Item -ItemType Directory -Path (Split-Path -Parent $copy) -Force | Out-Null
    Copy-Item -LiteralPath $installer -Destination $copy
    $result = Invoke-Source $copy @('-NoPath')
    if ($result.Code -eq 0 -or $result.Output -notmatch 'complete source checkout') { throw "Incomplete checkout: $($result.Output)" }

    $result = Invoke-Source $installer @('-NoPath')
    if ($result.Code -eq 0 -or $result.Output -notmatch 'Missing prerequisite: go') { throw "Missing Go: $($result.Output)" }

    $required = @((Get-Content -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'go.mod')) | Where-Object { $_ -match '^go\s+(\S+)' } | ForEach-Object { $Matches[1] })[0]
    Set-Shim 'go' 'echo go1.21.0'
    $result = Invoke-Source $installer @('-NoPath')
    if ($result.Code -eq 0 -or $result.Output -notmatch [regex]::Escape("Go $required or newer is required by go.mod")) { throw "Old Go: $($result.Output)" }

    Set-Shim 'go' 'echo go99.0.0'
    $result = Invoke-Source $installer @('-NoPath')
    if ($result.Code -eq 0 -or $result.Output -notmatch 'Missing prerequisite: node') { throw "Missing Node: $($result.Output)" }

    Set-Shim 'node' 'echo v16.20.0'
    $result = Invoke-Source $installer @('-NoPath')
    if ($result.Code -eq 0 -or $result.Output -notmatch 'Node\.js \d+\+ is required') { throw "Old Node: $($result.Output)" }

    if (Test-Path -LiteralPath (Join-Path $profileDir '.facet')) { throw 'Preflight wrote into the profile.' }
    if ((Get-StoredUserPath) -cne $userPath) { throw 'The user PATH changed.' }
    'PASS: parser, piped invocation, incomplete checkout, go.mod Go requirement, Node requirement, and no profile or PATH writes.'
} finally {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
