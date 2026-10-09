#requires -Version 5.1
<#
.SYNOPSIS
Build Facet from this checkout and make it the active runtime for this user.
.DESCRIPTION
For contributors. Builds ./cmd/facet into
~/.facet/runtimes/<version>-dev-windows-<arch> (the version comes from
package.json), places the Remotion composer sources beside it as the release
does, installs the composer's locked packages unless -Components none, links
~/.facet/current to the new runtime and adds ~/.facet/current/bin to the user
PATH unless -NoPath. The previously active runtime is kept, so the release
installer's rollback action can return to it.

The required Go version is go.mod's go directive. Go's own toolchain
selection (GOTOOLCHAIN) is left alone, so it may download that toolchain.
Run with -File from a complete, trusted checkout; nothing else is downloaded
except Go modules and npm packages.
#>
[CmdletBinding()]
param(
    [ValidateSet('remotion','none')][string]$Components = 'remotion',
    [switch]$NoPath
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$sourceHelp = 'Facet requires a complete source checkout; this script does not download source. Run: git clone https://github.com/kayiik/facet.git ; then: pwsh -File /absolute/path/to/facet/scripts/install-source.ps1'
# Invoke-Expression and piped invocations have no script path. Never infer the
# source from the working directory.
if (-not $PSCommandPath) { throw $sourceHelp }
$repo = Split-Path -Parent $PSScriptRoot
foreach ($file in @('go.mod', 'package.json', 'cmd/facet/main.go', 'capability.go', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'remotion-composer/composer-manifest.json', 'remotion-composer/package.json', 'remotion-composer/package-lock.json', 'remotion-composer/tsconfig.json')) {
    if (-not (Test-Path -LiteralPath (Join-Path $repo $file) -PathType Leaf)) { throw $sourceHelp }
}
if ($env:OS -ne 'Windows_NT') { throw 'This installer supports Windows. Use scripts/install-source.sh on Linux/macOS.' }
Set-StrictMode -Version Latest
$utf8NoBom = New-Object Text.UTF8Encoding($false)

function Test-Link([string]$Path) {
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    return [bool]($item -and $item.LinkType -in @('Junction','SymbolicLink'))
}
function Get-LinkTarget([string]$Path) {
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    if (-not $item -or $item.LinkType -notin @('Junction','SymbolicLink')) { return '' }
    $target = "$(@($item.Target)[0])"
    if ($target.StartsWith('\\?\') -or $target.StartsWith('\??\')) { $target = $target.Substring(4) }
    return $target
}
function Remove-Link([string]$Path) {
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    if (-not $item) { return }
    if ($item.LinkType -notin @('Junction','SymbolicLink')) { throw "Not a link: $Path" }
    if ($item.PSIsContainer) { [IO.Directory]::Delete($item.FullName, $false) } else { [IO.File]::Delete($item.FullName) }
}
function Remove-Tree([string]$Path) {
    # Never follows a junction or symbolic link inside the tree.
    $root = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    if (-not $root) { return }
    if ($root.LinkType -in @('Junction','SymbolicLink') -or -not $root.PSIsContainer) {
        if ($root.PSIsContainer) { [IO.Directory]::Delete($root.FullName, $false) } else { [IO.File]::Delete($root.FullName) }
        return
    }
    $directories = New-Object Collections.Generic.List[string]
    $pending = New-Object Collections.Generic.Stack[string]
    $pending.Push($root.FullName)
    while ($pending.Count) {
        $directory = $pending.Pop()
        $directories.Add($directory)
        foreach ($entry in ([IO.DirectoryInfo]$directory).EnumerateFileSystemInfos()) {
            if ($entry -is [IO.DirectoryInfo]) {
                if ($entry.LinkType -in @('Junction','SymbolicLink')) { [IO.Directory]::Delete($entry.FullName, $false) } else { $pending.Push($entry.FullName) }
            } else {
                if ($entry.Attributes -band [IO.FileAttributes]::ReadOnly) { $entry.Attributes = [IO.FileAttributes]::Normal }
                [IO.File]::Delete($entry.FullName)
            }
        }
    }
    for ($i = $directories.Count - 1; $i -ge 0; $i--) { [IO.Directory]::Delete($directories[$i], $false) }
}
function Move-Directory([string]$Source, [string]$Destination) {
    for ($attempt = 1; ; $attempt++) {
        try { [IO.Directory]::Move($Source, $Destination); return }
        catch { if ($attempt -ge 10) { throw }; Start-Sleep -Milliseconds 300 }
    }
}
function ConvertTo-JsonText([string]$Text) {
    return '"' + $Text.Replace('\', '\\').Replace('"', '\"') + '"'
}
function Write-TextFile([string]$Path, [string]$Text) {
    $temporary = "$Path.tmp-" + [guid]::NewGuid().ToString('N')
    [IO.File]::WriteAllText($temporary, $Text, $utf8NoBom)
    if (Test-Path -LiteralPath $Path) { [IO.File]::Replace($temporary, $Path, [NullString]::Value) } else { [IO.File]::Move($temporary, $Path) }
}
function Test-PathListContains([string]$List, [string]$Entry) {
    $want = $Entry.Trim().TrimEnd('\','/')
    foreach ($item in "$List".Split(';')) {
        $have = [Environment]::ExpandEnvironmentVariables($item.Trim()).TrimEnd('\','/')
        if ($have -and [string]::Equals($have, $want, [StringComparison]::OrdinalIgnoreCase)) { return $true }
    }
    return $false
}
function Invoke-Native([string]$Program, [string[]]$Arguments) {
    $ErrorActionPreference = 'Continue'
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program $($Arguments -join ' ') failed with exit code $LASTEXITCODE." }
}
function Get-VersionParts([string]$Text) {
    if ($Text -notmatch '(\d+)\.(\d+)(?:\.(\d+))?') { return $null }
    $patch = if ($Matches[3]) { [int]$Matches[3] } else { 0 }
    return @([int]$Matches[1], [int]$Matches[2], $patch)
}
function Test-VersionAtLeast($Have, $Want) {
    for ($i = 0; $i -lt 3; $i++) {
        if ($Have[$i] -gt $Want[$i]) { return $true }
        if ($Have[$i] -lt $Want[$i]) { return $false }
    }
    return $true
}

$version = (Get-Content -Raw -LiteralPath (Join-Path $repo 'package.json') | ConvertFrom-Json).version
if ("$version" -notmatch '^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$') { throw "package.json has an invalid version: $version" }
$devVersion = "$version-dev"
$arch = if ("$env:PROCESSOR_ARCHITECTURE $env:PROCESSOR_ARCHITEW6432" -match 'ARM64') { 'arm64' } else { 'amd64' }
$userHome = if ($HOME) { $HOME } else { $env:USERPROFILE }
$facetHome = if ("$env:FACET_HOME".Trim()) { $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath("$env:FACET_HOME".Trim()) } else { Join-Path $userHome '.facet' }
$runtimesDir = Join-Path $facetHome 'runtimes'
$runtimeName = "$devVersion-windows-$arch"
$runtimeDir = Join-Path $runtimesDir $runtimeName
$currentLink = Join-Path $facetHome 'current'
$pathEntry = Join-Path $currentLink 'bin'

Write-Host "Checking prerequisites for a Facet $devVersion source build..."
$goModule = Get-Content -LiteralPath (Join-Path $repo 'go.mod')
$directive = @($goModule | Where-Object { $_ -match '^go\s+(\d+\.\d+(?:\.\d+)?)\s*$' } | ForEach-Object { $Matches[1] })
if ($directive.Count -ne 1) { throw 'go.mod has no single go directive.' }
$requiredGo = $directive[0]
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "Missing prerequisite: go. Install Go $requiredGo or newer (go.mod)." }
Push-Location $repo
try { $ErrorActionPreference = 'Continue'; $goVersion = "$(& go env GOVERSION 2>$null)".Trim(); $goExit = $LASTEXITCODE; $ErrorActionPreference = 'Stop' } finally { Pop-Location }
$haveGo = Get-VersionParts $goVersion
if ($goExit -ne 0 -or -not $haveGo -or -not (Test-VersionAtLeast $haveGo (Get-VersionParts $requiredGo))) {
    throw "Go $requiredGo or newer is required by go.mod; found '$goVersion'. Install it, or allow Go to fetch it (GOTOOLCHAIN=auto)."
}
$node = ''
if ($Components -eq 'remotion') {
    $minimumNode = 22
    $manifest = Join-Path $repo 'installer/manifest.tsv'
    if (Test-Path -LiteralPath $manifest) {
        $row = @(Import-Csv -LiteralPath $manifest -Delimiter "`t" | Where-Object { $_.kind -eq 'runtime' -and $_.id -eq 'node' })
        if ($row.Count -eq 1 -and $row[0].value -match '^\d+$') { $minimumNode = [int]$row[0].value }
    }
    $nodeCommand = Get-Command node -ErrorAction SilentlyContinue
    if (-not $nodeCommand) { throw "Missing prerequisite: node. Install Node.js $minimumNode+ with npm for the Remotion composer, or rerun with -Components none." }
    $node = $nodeCommand.Source
    $ErrorActionPreference = 'Continue'
    $nodeVersion = "$(& $node --version 2>$null)".Trim()
    $ErrorActionPreference = 'Stop'
    if ($nodeVersion -notmatch '^v(\d+)\.' -or [int]$Matches[1] -lt $minimumNode) { throw "Node.js $minimumNode+ is required for the Remotion composer; found '$nodeVersion'. Rerun with -Components none to skip it." }
}

$lock = Join-Path $facetHome '.installer-lock'
$stage = ''
$movedAside = ''
$locked = $false
$savedCgo = $env:CGO_ENABLED
try {
    New-Item -ItemType Directory -Path $runtimesDir -Force | Out-Null
    try { New-Item -ItemType Directory -Path $lock -ErrorAction Stop | Out-Null; $locked = $true }
    catch { throw "Another Facet installer is running (lock: $lock). Wait for it, or delete the lock if no installer is running." }
    [IO.File]::WriteAllText((Join-Path $lock 'pid'), "$PID", $utf8NoBom)
    $existing = Get-Item -LiteralPath $currentLink -Force -ErrorAction SilentlyContinue
    if ($existing -and -not (Test-Link $currentLink)) { throw "$currentLink is not a link created by the Facet installer; move it aside and rerun." }
    $stage = Join-Path $runtimesDir ('.stage-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path (Join-Path $stage 'bin') -Force | Out-Null
    Write-Host "Building facet $devVersion..."
    $env:CGO_ENABLED = '0'
    $binary = Join-Path $stage 'bin/facet.exe'
    Push-Location $repo
    try { Invoke-Native 'go' @('build', '-trimpath', '-ldflags', "-X main.Version=$devVersion", '-o', $binary, './cmd/facet') } finally { Pop-Location }
    $reported = "$(& $binary version)".Trim()
    if ($LASTEXITCODE -ne 0 -or $reported -ne "facet v$devVersion") { throw "The built binary reports '$reported', not facet v$devVersion." }
    # The same composer files a release ships: the allowlisted sources and
    # their package metadata, never node_modules or local outputs.
    $composerManifest = Get-Content -Raw -LiteralPath (Join-Path $repo 'remotion-composer/composer-manifest.json') | ConvertFrom-Json
    $composerFiles = @('package.json', 'package-lock.json', 'tsconfig.json', 'composer-manifest.json') + @($composerManifest.allowedSourcePaths)
    foreach ($name in $composerFiles) {
        if ($name -match '(^|[\\/])\.\.([\\/]|$)|:' -or $name.StartsWith('/')) { throw "Unsafe composer path: $name" }
        $source = Join-Path $repo "remotion-composer/$name"
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw "Composer file missing: remotion-composer/$name" }
        $target = Join-Path $stage "dependencies/remotion-composer/$name"
        New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
        Copy-Item -LiteralPath $source -Destination $target
    }
    foreach ($name in @('LICENSE', 'THIRD_PARTY_NOTICES.md')) { Copy-Item -LiteralPath (Join-Path $repo $name) -Destination (Join-Path $stage $name) }
    New-Item -ItemType Directory -Path (Join-Path $stage 'dependencies') -Force | Out-Null
    $installed = @()
    if ($Components -eq 'remotion') {
        Write-Host 'Installing the locked Remotion composer packages (network access may be required)...'
        $npmCli = Join-Path (Split-Path -Parent $node) 'node_modules/npm/bin/npm-cli.js'
        $composer = Join-Path $stage 'dependencies/remotion-composer'
        if (Test-Path -LiteralPath $npmCli -PathType Leaf) { Invoke-Native $node @($npmCli, 'ci', '--prefix', $composer, '--no-audit', '--no-fund') }
        else { Invoke-Native 'npm.cmd' @('ci', '--prefix', $composer, '--no-audit', '--no-fund') }
        $installed += 'remotion'
    }
    $componentList = (@($installed | ForEach-Object { ConvertTo-JsonText $_ }) -join ', ')
    $now = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    Write-TextFile (Join-Path $stage 'components.json') "{`n  `"schema`": 1,`n  `"version`": $(ConvertTo-JsonText $devVersion),`n  `"os`": `"windows`",`n  `"arch`": $(ConvertTo-JsonText $arch),`n  `"components`": [$componentList],`n  `"verified`": false,`n  `"source`": `"checkout`",`n  `"installed_at`": $(ConvertTo-JsonText $now)`n}`n"

    # Replace an earlier build of the same version; restore it on failure.
    if (Get-Item -LiteralPath $runtimeDir -Force -ErrorAction SilentlyContinue) {
        if (Test-Link $runtimeDir) { Remove-Link $runtimeDir }
        else {
            $movedAside = Join-Path $runtimesDir ('.old-' + $runtimeName + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
            try { Move-Directory $runtimeDir $movedAside } catch { throw "Runtime $runtimeName is in use; close agent CLIs that run Facet and rerun. ($($_.Exception.Message))" }
        }
    }
    Move-Directory $stage $runtimeDir
    $stage = ''
    $previousTarget = Get-LinkTarget $currentLink
    $previousName = if ($previousTarget) { Split-Path -Leaf $previousTarget.TrimEnd('\') } else { '' }
    if ($previousName -ne $runtimeName -or -not $existing) {
        $temporary = Join-Path $facetHome ('.current-' + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Junction -Path $temporary -Value $runtimeDir | Out-Null
        try {
            if ($existing) { Remove-Link $currentLink }
            Move-Directory $temporary $currentLink
        } catch {
            if (-not (Get-Item -LiteralPath $currentLink -Force -ErrorAction SilentlyContinue) -and $previousTarget) { New-Item -ItemType Junction -Path $currentLink -Value $previousTarget | Out-Null }
            if (Get-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue) { Remove-Link $temporary }
            throw
        }
    }
    $statePath = Join-Path $facetHome 'installer.json'
    $previousState = ''
    if ($previousName -and $previousName -ne $runtimeName) { $previousState = $previousName }
    elseif (Test-Path -LiteralPath $statePath) { try { $previousState = "$(([IO.File]::ReadAllText($statePath) | ConvertFrom-Json).previous)" } catch { $previousState = '' } }
    Write-TextFile $statePath "{`n  `"schema`": 1,`n  `"current`": $(ConvertTo-JsonText $runtimeName),`n  `"previous`": $(ConvertTo-JsonText $previousState),`n  `"updated_at`": $(ConvertTo-JsonText $now)`n}`n"
    if ($movedAside) { Remove-Tree $movedAside; $movedAside = '' }

    $pathNote = "unchanged (-NoPath); run & '$(Join-Path $pathEntry 'facet.exe')'"
    if (-not $NoPath) {
        # HKCU\Environment\Path is edited directly so REG_EXPAND_SZ entries
        # keep their unexpanded %VARIABLES%.
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        try {
            $valueName = @($key.GetValueNames() | Where-Object { $_ -ieq 'Path' } | Select-Object -First 1)
            $raw = ''
            $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
            if ($valueName.Count) {
                $raw = [string]$key.GetValue($valueName[0], '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
                if ($key.GetValueKind($valueName[0]) -eq [Microsoft.Win32.RegistryValueKind]::String) { $kind = [Microsoft.Win32.RegistryValueKind]::String }
            }
            if (Test-PathListContains $raw $pathEntry) { $pathNote = "$pathEntry is already on your user PATH" }
            else {
                $name = if ($valueName.Count) { $valueName[0] } else { 'Path' }
                $rest = $raw.Trim().TrimStart(';')
                $key.SetValue($name, $(if ($rest) { "$pathEntry;$rest" } else { $pathEntry }), $kind)
                $pathNote = "added $pathEntry to your user PATH; open a new terminal to use facet"
                try {
                    if (-not ('FacetInstaller.NativeMethods' -as [type])) {
                        Add-Type -Namespace FacetInstaller -Name NativeMethods -MemberDefinition '[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)] public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);'
                    }
                    $result = [UIntPtr]::Zero
                    [void][FacetInstaller.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
                } catch { }
            }
        } finally { $key.Close() }
        if (-not (Test-PathListContains $env:Path $pathEntry)) { $env:Path = "$pathEntry;$env:Path" }
    }
    Write-Host ''
    Write-Host "Facet $devVersion (source build) is active." -ForegroundColor Green
    Write-Host "  Runtime:  $runtimeDir"
    Write-Host "  Command:  $(Join-Path $pathEntry 'facet.exe')"
    Write-Host "  PATH:     $pathNote"
    if ($installed.Count) { Write-Host '  Remotion: packages installed; fetch its headless browser with: node node_modules/@remotion/cli/remotion-cli.js browser ensure (in dependencies\remotion-composer)' }
    if ($previousState) { Write-Host "  Previous: $previousState (kept; the release installer's -Action rollback returns to it)" }
    Write-Host '  No render was verified. Run facet doctor, then facet wire <cli> to use it from your agentic CLI.'
} catch {
    if ($movedAside -and -not (Get-Item -LiteralPath $runtimeDir -Force -ErrorAction SilentlyContinue)) { Move-Directory $movedAside $runtimeDir }
    throw
} finally {
    $env:CGO_ENABLED = $savedCgo
    if ($stage -and (Test-Path -LiteralPath $stage)) { Remove-Tree $stage }
    if ($locked) { Remove-Item -LiteralPath $lock -Recurse -Force -ErrorAction SilentlyContinue }
}
