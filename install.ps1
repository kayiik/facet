#requires -Version 5.1
<#
.SYNOPSIS
Install, update, roll back, or uninstall Facet for the current user.
.DESCRIPTION
Facet installs once per user. Each runtime lives in
~/.facet/runtimes/<version>-windows-<arch>; the active one is the directory
junction ~/.facet/current, and ~/.facet/current/bin is added to the user PATH.
FACET_HOME, when set, replaces ~/.facet. Optional components (remotion, piper,
hyperframes) are installed into the runtime and verified before it is
activated. The previous runtime is kept for rollback, and recorded CLI wirings
are refreshed to the active runtime after every install, update and rollback.

From Facet 2.2.0, facet.exe is built on this computer from the release's
source archive, by the official Go toolchain pinned (with its SHA-256) in
installer/manifest.tsv. Go is downloaded, used and removed again, with its
build cache; -ToolchainPath supplies the Go zip for offline installs. Earlier
releases ship a prebuilt facet.exe, which is installed as before.

Wiring Facet into an agentic CLI is a separate decision made with
`facet wire`. This script runs it only when asked: interactively, or with
-Wire in noninteractive mode.

Run it from the installer package (install.ps1 beside installer/manifest.tsv).
#>
[CmdletBinding()]
param(
    [ValidateSet('install','update','rollback','uninstall')][string]$Action,
    [string]$Version,
    [string[]]$Components,
    [string[]]$Wire,
    [ValidateSet('user','project')][string]$Scope,
    [string]$ProjectDir,
    [string]$ArchivePath,
    [string]$ChecksumPath,
    [string]$ToolchainPath,
    [switch]$NonInteractive,
    [switch]$NoPath,
    [switch]$SkipVerify,
    [switch]$Purge,
    [switch]$Plain
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$PSNativeCommandUseErrorActionPreference = $false
if ($env:OS -ne 'Windows_NT') { throw 'Use install.sh on Linux/macOS.' }
if (-not $PSCommandPath) { throw 'Run install.ps1 from the downloaded installer package, not a pipe.' }
$PSModuleAutoLoadingPreference = 'All'
if ($PSVersionTable.PSEdition -ne 'Core' -and $env:SystemRoot) {
    # Windows PowerShell started from PowerShell 7 inherits a module path whose
    # 7.x modules it cannot load; put its own modules first.
    $env:PSModulePath = (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/Modules') + ';' + $env:PSModulePath
}
Import-Module Microsoft.PowerShell.Utility, Microsoft.PowerShell.Management
Set-StrictMode -Version Latest
Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

$utf8NoBom = New-Object Text.UTF8Encoding($false)
$supportedClis = @('claude','codex','copilot','opencode','compa')

# ----------------------------------------------------------------- helpers

function Resolve-UserPath([string]$Path) {
    # Relative to PowerShell's location. [IO.Path]::GetFullPath would resolve
    # against the process directory, which `cd` in PowerShell does not change.
    return $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
}
function Get-LongPath([string]$Path) {
    # An absolute path with its 8.3 short names expanded (RUNNER~1 becomes
    # runneradmin), as Get-ChildItem and GetFullPath report the existing part
    # of a path. Paths compared by prefix must agree on this form.
    return [IO.Path]::GetFullPath($Path)
}
function Split-List([string]$Text) {
    return @("$Text".Split([char[]]@(',',';',' ',"`t"), [StringSplitOptions]::RemoveEmptyEntries) | ForEach-Object { $_.Trim().ToLowerInvariant() } | Where-Object { $_ })
}
function Resolve-MenuAnswer([string]$Answer, [string[]]$Values) {
    # Plain menus accept numbers or names. Numbers are mapped to names here,
    # before any value reaches a validated variable or parameter.
    $result = @()
    foreach ($item in @(Split-List $Answer)) {
        if ($item -match '^\d+$') {
            $index = [int]$item - 1
            if ($index -lt 0 -or $index -ge $Values.Count) { throw "Choose a number from 1 to $($Values.Count), not $item." }
            $item = $Values[$index]
        }
        $result += $item
    }
    return @($result)
}
function Test-PathListContains([string]$List, [string]$Entry) {
    $want = $Entry.Trim().TrimEnd('\','/')
    foreach ($item in "$List".Split(';')) {
        $have = [Environment]::ExpandEnvironmentVariables($item.Trim()).TrimEnd('\','/')
        if ($have -and [string]::Equals($have, $want, [StringComparison]::OrdinalIgnoreCase)) { return $true }
    }
    return $false
}
function Add-PathListEntry([string]$List, [string]$Entry) {
    if (Test-PathListContains $List $Entry) { return "$List" }
    $rest = "$List".Trim().TrimStart(';')
    if (-not $rest) { return $Entry }
    return "$Entry;$rest"
}
function Remove-PathListEntry([string]$List, [string]$Entry) {
    $want = $Entry.Trim().TrimEnd('\','/')
    $kept = @("$List".Split(';') | Where-Object {
        $have = [Environment]::ExpandEnvironmentVariables($_.Trim()).TrimEnd('\','/')
        -not ($have -and [string]::Equals($have, $want, [StringComparison]::OrdinalIgnoreCase))
    })
    return ($kept -join ';')
}
function ConvertTo-JsonText([string]$Text) {
    $builder = New-Object Text.StringBuilder
    [void]$builder.Append('"')
    foreach ($char in $Text.ToCharArray()) {
        switch ($char) {
            '"' { [void]$builder.Append('\"') }
            '\' { [void]$builder.Append('\\') }
            default {
                if ([int]$char -lt 32) { [void]$builder.Append(('\u{0:x4}' -f [int]$char)) } else { [void]$builder.Append($char) }
            }
        }
    }
    [void]$builder.Append('"')
    return $builder.ToString()
}
function Write-TextFile([string]$Path, [string]$Text) {
    # UTF-8 without a byte-order mark on Windows PowerShell 5.1 as well, written
    # beside the destination and moved into place.
    $temporary = "$Path.tmp-" + [guid]::NewGuid().ToString('N')
    try {
        [IO.File]::WriteAllText($temporary, $Text, $utf8NoBom)
        # [NullString]::Value: PowerShell would pass $null as an empty string.
        if (Test-Path -LiteralPath $Path) { [IO.File]::Replace($temporary, $Path, [NullString]::Value) } else { [IO.File]::Move($temporary, $Path) }
    } catch {
        Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
        throw
    }
}
function Get-Sha256([string]$Path) { return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }
function Get-ItemOrNull([string]$Path) { return Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue }
function Test-Link([string]$Path) {
    # Only junctions and symbolic links count as links. Cloud-file placeholders
    # (OneDrive) are reparse points too and are ordinary directories here.
    $item = Get-ItemOrNull $Path
    return [bool]($item -and $item.LinkType -in @('Junction','SymbolicLink'))
}
function Get-LinkTarget([string]$Path) {
    $item = Get-ItemOrNull $Path
    if (-not $item -or $item.LinkType -notin @('Junction','SymbolicLink')) { return '' }
    $target = "$(@($item.Target)[0])"
    if ($target.StartsWith('\\?\') -or $target.StartsWith('\??\')) { $target = $target.Substring(4) }
    if ($target -and -not [IO.Path]::IsPathRooted($target)) { $target = Join-Path (Split-Path -Parent $item.FullName) $target }
    return $target
}
function Remove-Link([string]$Path) {
    $item = Get-ItemOrNull $Path
    if (-not $item) { return }
    if ($item.LinkType -notin @('Junction','SymbolicLink')) { throw "Not a link: $Path" }
    # Removes the link itself, never its target.
    if ($item.PSIsContainer) { [IO.Directory]::Delete($item.FullName, $false) } else { [IO.File]::Delete($item.FullName) }
}
function Remove-Tree([string]$Path) {
    # Deletes a directory tree without following links: a junction or symbolic
    # link inside it is removed as a link. Windows PowerShell's recursive
    # Remove-Item can follow junctions, and .NET's recursive delete fails on
    # them, so the walk is explicit.
    $root = Get-ItemOrNull $Path
    if (-not $root) { return }
    if ($root.LinkType -in @('Junction','SymbolicLink') -or -not $root.PSIsContainer) {
        if ($root.PSIsContainer) { [IO.Directory]::Delete($root.FullName, $false) }
        else { if ($root.Attributes -band [IO.FileAttributes]::ReadOnly) { $root.Attributes = 'Normal' }; [IO.File]::Delete($root.FullName) }
        return
    }
    $directories = New-Object Collections.Generic.List[string]
    $pending = New-Object Collections.Generic.Stack[string]
    $pending.Push($root.FullName)
    while ($pending.Count) {
        $directory = $pending.Pop()
        $directories.Add($directory)
        foreach ($entry in ([IO.DirectoryInfo]$directory).EnumerateFileSystemInfos()) {
            $isLink = [bool]($entry.Attributes -band [IO.FileAttributes]::ReparsePoint)
            if ($entry -is [IO.DirectoryInfo]) {
                if ($isLink -and $entry.LinkType -in @('Junction','SymbolicLink')) { [IO.Directory]::Delete($entry.FullName, $false) }
                else { $pending.Push($entry.FullName) }
            } else {
                if ($entry.Attributes -band [IO.FileAttributes]::ReadOnly) { $entry.Attributes = [IO.FileAttributes]::Normal }
                [IO.File]::Delete($entry.FullName)
            }
        }
    }
    for ($i = $directories.Count - 1; $i -ge 0; $i--) {
        for ($attempt = 1; ; $attempt++) {
            try { [IO.Directory]::Delete($directories[$i], $false); break }
            catch { if ($attempt -ge 5) { throw }; Start-Sleep -Milliseconds (200 * $attempt) }
        }
    }
}
function Move-Directory([string]$Source, [string]$Destination) {
    # Freshly written executables can be held briefly by malware scanners.
    for ($attempt = 1; ; $attempt++) {
        try { [IO.Directory]::Move($Source, $Destination); return }
        catch { if ($attempt -ge 10) { throw }; Start-Sleep -Milliseconds 300 }
    }
}

# ---------------------------------------------------------------- manifest

$manifestPath = Join-Path $PSScriptRoot 'installer/manifest.tsv'
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw 'Run install.ps1 from the complete installer package; installer/manifest.tsv is missing.' }
$manifest = @(Import-Csv -LiteralPath $manifestPath -Delimiter "`t")
function Definition([string]$Id) {
    $rows = @($manifest | Where-Object id -EQ $Id)
    if ($rows.Count -ne 1) { throw "Invalid manifest entry: $Id" }
    return $rows[0]
}
# Layout 2 is the user-wide manifest; layout 1 carried extra per-project rows
# that this installer ignores. Only release, runtime and component rows are read.
if ((Definition layout).version -notin @('1','2')) { throw 'Unsupported installer manifest schema.' }
$componentRows = @($manifest | Where-Object kind -EQ 'component')
$componentIds = @($componentRows | ForEach-Object id)

# ---------------------------------------------------------------- settings

$selectedAction = ''
if ($PSBoundParameters.ContainsKey('Action')) { $selectedAction = $Action }
elseif ($env:FACET_ACTION) { $selectedAction = $env:FACET_ACTION.Trim().ToLowerInvariant() }
if ($selectedAction -and $selectedAction -notin @('install','update','rollback','uninstall')) { throw "Unknown action '$selectedAction'; use install, update, rollback, or uninstall." }
$requestedVersion = ''
if ($Version) { $requestedVersion = $Version.Trim() } elseif ($env:FACET_VERSION) { $requestedVersion = $env:FACET_VERSION.Trim() }
if ($requestedVersion -and $requestedVersion -notmatch '^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$') { throw 'Invalid release version.' }
$componentText = $null
if ($PSBoundParameters.ContainsKey('Components')) { $componentText = @($Components) -join ',' } elseif ($env:FACET_COMPONENTS) { $componentText = $env:FACET_COMPONENTS }
$wireText = ''
if ($PSBoundParameters.ContainsKey('Wire')) { $wireText = @($Wire) -join ',' } elseif ($env:FACET_WIRE) { $wireText = $env:FACET_WIRE }
$wireScope = ''
if ($PSBoundParameters.ContainsKey('Scope')) { $wireScope = $Scope } elseif ($env:FACET_SCOPE) { $wireScope = $env:FACET_SCOPE.Trim().ToLowerInvariant() }
$wireProject = ''
if ($PSBoundParameters.ContainsKey('ProjectDir')) { $wireProject = $ProjectDir } elseif ($env:FACET_PROJECT) { $wireProject = $env:FACET_PROJECT }
$batch = [bool]$NonInteractive -or $env:FACET_YES -eq '1'
$skipPath = [bool]$NoPath -or $env:FACET_NO_PATH -eq '1'
$skipVerification = [bool]$SkipVerify -or $env:FACET_SKIP_VERIFY -eq '1'
$purgeRuntimes = [bool]$Purge -or $env:FACET_PURGE -eq '1'
$detail = $VerbosePreference -ne 'SilentlyContinue'
$script:rich = -not $Plain -and $env:FACET_PLAIN -ne '1' -and -not $env:NO_COLOR -and -not $batch
try { $script:rich = $script:rich -and -not [Console]::IsInputRedirected -and -not [Console]::IsOutputRedirected } catch { $script:rich = $false }

if ([bool]$ArchivePath -ne [bool]$ChecksumPath) { throw 'Supply both -ArchivePath and -ChecksumPath.' }
if ($ArchivePath) { $ArchivePath = Resolve-UserPath $ArchivePath; $ChecksumPath = Resolve-UserPath $ChecksumPath }
if ($ToolchainPath) { $ToolchainPath = Resolve-UserPath $ToolchainPath }
if ($wireScope -and $wireScope -notin @('user','project')) { throw 'Scope must be user or project.' }
if ($wireProject) {
    if (-not $wireScope) { $wireScope = 'project' }
    elseif ($wireScope -ne 'project') { throw '-ProjectDir applies only to -Scope project.' }
    $wireProject = Resolve-UserPath $wireProject
}
if (-not $wireScope) { $wireScope = 'user' }
$wireTargets = @()
if ($wireText) {
    $items = @(Split-List $wireText)
    if ('none' -in $items) { if ($items.Count -ne 1) { throw 'none cannot be combined with CLI names.' }; $items = @() }
    foreach ($item in $items) { if ($item -notin ($supportedClis + 'all')) { throw "Unknown CLI '$item'; choose claude, codex, copilot, opencode, compa, or all." } }
    if ('all' -in $items) { $items = @('all') }
    $wireTargets = @($items | Select-Object -Unique)
}
if ($wireTargets.Count -and $wireScope -eq 'project') {
    # facet wire would use its own working directory, which is not
    # PowerShell's location; name the project explicitly.
    if (-not $wireProject) { $wireProject = (Get-Location).ProviderPath }
    if (-not (Test-Path -LiteralPath $wireProject -PathType Container)) { throw "Project directory not found: $wireProject" }
}
if ($selectedAction -in @('rollback','uninstall')) {
    foreach ($name in @('Components','ArchivePath','ChecksumPath','ToolchainPath','SkipVerify')) {
        if ($PSBoundParameters.ContainsKey($name)) { throw "-$name applies to install and update, not $selectedAction." }
    }
}
if ($selectedAction -eq 'uninstall' -and $wireTargets.Count) { throw '-Wire cannot be combined with uninstall; uninstall removes recorded wirings.' }
if ($purgeRuntimes -and $selectedAction -and $selectedAction -ne 'uninstall') { throw '-Purge applies only to uninstall.' }

$arch = ''
$osArchitecture = "$env:PROCESSOR_ARCHITECTURE"
try { $osArchitecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { }
switch -Regex ($osArchitecture) {
    '^(?i:x64|amd64)$' { $arch = 'amd64' }
    '^(?i:arm64)$' { $arch = 'arm64' }
    default { throw "Unsupported architecture: $osArchitecture" }
}
$releaseVersion = (Definition facet).version
$targetVersion = if ($requestedVersion) { $requestedVersion } else { $releaseVersion }
$runtimePattern = '^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?-windows-(?:amd64|arm64)$'
# A release from 2.2.0 on ships Facet's source, which this script builds;
# earlier releases ship a prebuilt Windows archive.
$sourceArchiveName = "facet-$targetVersion-source.zip"
$platformArchiveName = "facet-$targetVersion-windows-$arch.zip"

$userHome = if ($HOME) { $HOME } else { $env:USERPROFILE }
if (-not $userHome -or -not [IO.Path]::IsPathRooted($userHome)) { throw 'The home directory is unknown; set USERPROFILE.' }
$userHome = Get-LongPath $userHome
# FACET_HOME moves Facet's home folder (runtimes, current, wiring record). It is
# resolved once and handed to every facet this script runs, so they agree.
$facetHome = Join-Path $userHome '.facet'
if ("$env:FACET_HOME".Trim()) {
    $facetHome = Get-LongPath (Resolve-UserPath "$env:FACET_HOME".Trim())
    $env:FACET_HOME = $facetHome
}
# Facet 1.x kept its releases in ~/.facet whatever FACET_HOME says.
$legacyHome = Join-Path $userHome '.facet'
$runtimesDir = Join-Path $facetHome 'runtimes'
$currentLink = Join-Path $facetHome 'current'
$statePath = Join-Path $facetHome 'installer.json'
$pathEntry = Join-Path $currentLink 'bin'
$currentExe = Join-Path $pathEntry 'facet.exe'
if (($facetHome + $ArchivePath + $ChecksumPath + $ToolchainPath + $wireProject) -match '[\x00-\x1f]') { throw 'Control characters are unsupported in paths.' }

# -------------------------------------------------------------- state model

function Get-RuntimeVersion([string]$Name) { return ($Name -replace '-windows-(amd64|arm64)$','') }
function Read-JsonFile([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $null }
    try { return ([IO.File]::ReadAllText($Path) | ConvertFrom-Json) } catch { return $null }
}
function Get-JsonValue($Object, [string]$Name) {
    if ($null -eq $Object) { return $null }
    $property = $Object.PSObject.Properties[$Name]
    if ($property) { return $property.Value }
    return $null
}
function Get-ActiveRuntimeName {
    $target = Get-LinkTarget $currentLink
    if (-not $target) { return '' }
    $leaf = Split-Path -Leaf $target.TrimEnd('\','/')
    $parent = Split-Path -Parent $target.TrimEnd('\','/')
    if (-not [string]::Equals($parent.TrimEnd('\','/'), $runtimesDir.TrimEnd('\','/'), [StringComparison]::OrdinalIgnoreCase)) { return '' }
    if ($leaf -notmatch $runtimePattern) { return '' }
    return $leaf
}
function Read-RuntimeRecord([string]$Name) {
    $directory = Join-Path $runtimesDir $Name
    $item = Get-ItemOrNull $directory
    if (-not $item -or -not $item.PSIsContainer -or (Test-Link $directory)) { return $null }
    $record = Read-JsonFile (Join-Path $directory 'components.json')
    if (-not $record -or (Get-JsonValue $record 'schema') -ne 1 -or (Get-JsonValue $record 'version') -ne (Get-RuntimeVersion $Name)) { return $null }
    if (-not (Test-Path -LiteralPath (Join-Path $directory 'bin/facet.exe') -PathType Leaf)) { return $null }
    return $record
}
function Get-RecordComponents($Record) { return @(@(Get-JsonValue $Record 'components') | Where-Object { $_ -in $componentIds }) }
function Read-InstallerState {
    $state = Read-JsonFile $statePath
    $result = @{ current = ''; previous = '' }
    foreach ($key in @('current','previous')) {
        $value = "$(Get-JsonValue $state $key)"
        if ($value -match $runtimePattern) { $result[$key] = $value }
    }
    return $result
}
function Write-InstallerState([string]$Current, [string]$Previous) {
    New-Item -ItemType Directory -Path $facetHome -Force | Out-Null
    $text = "{`n  `"schema`": 1,`n  `"current`": $(ConvertTo-JsonText $Current),`n  `"previous`": $(ConvertTo-JsonText $Previous),`n  `"updated_at`": $(ConvertTo-JsonText ([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')))`n}`n"
    Write-TextFile $statePath $text
}
function Get-InstalledRuntimeNames {
    if (-not (Test-Path -LiteralPath $runtimesDir -PathType Container)) { return @() }
    return @(Get-ChildItem -LiteralPath $runtimesDir -Directory -Force | Where-Object { $_.Name -match $runtimePattern } | ForEach-Object Name)
}
function Get-PreviousRuntimeName([string]$Active) {
    $state = Read-InstallerState
    if ($state.previous -and $state.previous -ne $Active -and (Read-RuntimeRecord $state.previous)) { return $state.previous }
    # The state file can be missing after an interrupted run: fall back to the
    # most recently installed complete runtime.
    $candidates = @(Get-InstalledRuntimeNames | Where-Object { $_ -ne $Active } | ForEach-Object {
        $record = Read-RuntimeRecord $_
        if ($record) { [pscustomobject]@{ Name = $_; At = "$(Get-JsonValue $record 'installed_at')" } }
    } | Sort-Object At -Descending)
    if ($candidates.Count) { return $candidates[0].Name }
    return ''
}
function Test-V1Install {
    $releases = Join-Path $legacyHome 'releases'
    if (-not (Test-Path -LiteralPath $releases -PathType Container)) { return $false }
    return [bool]@(Get-ChildItem -LiteralPath $releases -Directory -Force -ErrorAction SilentlyContinue | Where-Object Name -Like '1.*').Count
}
function Write-V1Note {
    if (Test-V1Install) {
        Write-Host ''
        Write-Host 'Facet v1 project integrations are separate; remove them with the v1.1.0 installer --action uninstall (-Action uninstall on Windows) in each project.' -ForegroundColor Yellow
        Write-Host "  The v1 runtimes in $(Join-Path $legacyHome 'releases') are left untouched."
    }
}

# --------------------------------------------------------------- interface

function Section([string]$Label) { Write-Host "`n$Label" -ForegroundColor Cyan }
function Ask([string]$Prompt, [string]$Default) {
    $answer = Read-Host "$Prompt [$Default]"
    if ($null -eq $answer) { throw 'Interactive input ended; rerun with -NonInteractive and explicit options.' }
    if (-not $answer.Trim()) { return $Default }
    return $answer.Trim()
}
function Confirm-Choice([string]$Prompt, [bool]$Default) {
    $suffix = if ($Default) { 'Y/n' } else { 'y/N' }
    $answer = (Ask $Prompt $suffix).ToLowerInvariant()
    if ($answer -eq $suffix.ToLowerInvariant()) { return $Default }
    if ($answer -in @('y','yes')) { return $true }
    if ($answer -in @('n','no')) { return $false }
    throw "Answer yes or no, not '$answer'."
}
function Select-Option([string]$Title, [string[]]$Labels, [string[]]$Values, [string[]]$Defaults, [switch]$Multiple) {
    $Defaults = @($Defaults | Where-Object { $_ })
    if (-not $script:rich) {
        Write-Host "`n$Title"
        for ($i = 0; $i -lt $Labels.Count; $i++) { Write-Host "  $($i + 1). $($Labels[$i])" }
        $default = if ($Defaults.Count) { $Defaults -join ',' } else { 'none' }
        $hint = if ($Multiple) { 'Numbers or names, comma-separated; none for no selection' } else { 'Number or name' }
        $answer = Ask $hint $default
        $chosen = @(Resolve-MenuAnswer $answer $Values)
        if ($chosen.Count -eq 1 -and $chosen[0] -eq 'none' -and $Multiple) { return @() }
        if (-not $Multiple -and $chosen.Count -ne 1) { throw 'Choose exactly one option.' }
        foreach ($value in $chosen) { if ($value -notin $Values) { throw "Unknown choice '$value'; choose from: $($Values -join ', ')." } }
        if (@($chosen | Select-Object -Unique).Count -ne $chosen.Count) { throw 'Duplicate choice.' }
        return @($chosen)
    }
    $index = 0
    $checked = @{}
    for ($i = 0; $i -lt $Values.Count; $i++) {
        if ($Values[$i] -in @($Defaults)) { $checked[$i] = $true; if (-not $Multiple) { $index = $i } }
    }
    Write-Host "`n? $Title" -ForegroundColor Cyan
    $top = [Console]::CursorTop
    $oldCursor = [Console]::CursorVisible
    try {
        [Console]::CursorVisible = $false
        while ($true) {
            [Console]::SetCursorPosition(0, $top)
            $width = [Math]::Max(20, [Console]::WindowWidth - 2)
            for ($i = 0; $i -lt $Labels.Count; $i++) {
                $pointer = if ($i -eq $index) { '>' } else { ' ' }
                $mark = if ($Multiple) { if ($checked.ContainsKey($i)) { '[x]' } else { '[ ]' } } else { if ($i -eq $index) { '(*)' } else { '( )' } }
                $line = "  $pointer $mark $($Labels[$i])"
                if ($line.Length -gt $width) { $line = $line.Substring(0, $width - 3) + '...' }
                Write-Host $line.PadRight($width) -ForegroundColor $(if ($i -eq $index) { 'Green' } else { 'Gray' })
            }
            $hintLine = if ($Multiple) { '  Up/Down: move | Space: toggle | Enter: confirm | Esc: cancel' } else { '  Up/Down: move | Enter: confirm | Esc: cancel' }
            Write-Host $hintLine.Substring(0, [Math]::Min($hintLine.Length, $width)).PadRight($width) -ForegroundColor DarkGray
            $key = [Console]::ReadKey($true)
            switch ($key.Key) {
                'UpArrow' { $index = ($index + $Labels.Count - 1) % $Labels.Count }
                'DownArrow' { $index = ($index + 1) % $Labels.Count }
                'Spacebar' { if ($Multiple) { if ($checked.ContainsKey($index)) { $checked.Remove($index) } else { $checked[$index] = $true } } }
                'Escape' { throw 'Installation cancelled.' }
                'Enter' {
                    $result = @()
                    for ($i = 0; $i -lt $Values.Count; $i++) {
                        if (($Multiple -and $checked.ContainsKey($i)) -or (-not $Multiple -and $i -eq $index)) { $result += $Values[$i] }
                    }
                    [Console]::SetCursorPosition(0, $top)
                    for ($i = 0; $i -le $Labels.Count; $i++) { Write-Host (' ' * $width) }
                    [Console]::SetCursorPosition(0, $top)
                    $shown = if ($result.Count) { $result -join ', ' } else { 'none' }
                    Write-Host "  Selected: $shown" -ForegroundColor Green
                    return @($result)
                }
            }
        }
    } finally { [Console]::CursorVisible = $oldCursor }
}

# ----------------------------------------------------------------- logging

$script:logFile = ''
function Open-Log {
    $logRoot = if ($env:FACET_LOG_DIR) { Resolve-UserPath $env:FACET_LOG_DIR } else { Join-Path $facetHome 'logs' }
    New-Item -ItemType Directory -Path $logRoot -Force | Out-Null
    $script:logFile = Join-Path $logRoot ('install-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8) + '.log')
    [IO.File]::WriteAllText($script:logFile, "Facet installer $releaseVersion, action $selectedAction, $(Get-Date -Format o)`n", $utf8NoBom)
}
function Redact([string]$Text) {
    return ($Text -replace '(https?://)[^/\s@]+@','$1<redacted>@' -replace '([?&][^=\s&]+)=([^&\s]+)','$1=<redacted>')
}
function Write-Log([string]$Text) {
    if ($script:logFile) { [IO.File]::AppendAllText($script:logFile, (Redact $Text) + "`n", $utf8NoBom) }
}
function Step([string]$StepName, [scriptblock]$StepScript) {
    # The script block reads its caller's variables through dynamic scoping,
    # so these parameter names must not collide with any caller's names.
    Write-Host "  -> $StepName"
    Write-Log "== $StepName"
    try {
        & $StepScript 2>&1 | ForEach-Object {
            $line = Redact "$_"
            Write-Log $line
            if ($detail) { Write-Host "     $line" }
        }
        Write-Host "  OK $StepName"
    } catch {
        Write-Log $_.Exception.Message
        throw "$StepName failed: $($_.Exception.Message) Details: $($script:logFile)"
    }
}
function Resolve-Program([string]$Name) {
    $found = @(Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue)
    if (-not $found.Count) { throw "Required command not found: $Name" }
    return $found[0].Source
}
function Run([string]$Program, [string[]]$Arguments) {
    $resolved = if ([IO.Path]::IsPathRooted($Program)) { $Program } else { Resolve-Program $Program }
    # Windows PowerShell represents native stderr as error records. Warnings
    # from npm or pip must not turn a successful exit into a failure.
    $ErrorActionPreference = 'Continue'
    & $resolved @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Command failed ($LASTEXITCODE): $(Split-Path -Leaf $Program) $($Arguments -join ' ')" }
}
function Invoke-Probe([string]$Program, [string[]]$Arguments) {
    $ErrorActionPreference = 'Continue'
    try { $output = @(& $Program @Arguments 2>$null) } catch { return @{ Code = -1; Text = '' } }
    $text = if ($output.Count) { "$($output[-1])".Trim() } else { '' }
    return @{ Code = $LASTEXITCODE; Text = $text }
}
function Download([string]$Url, [string]$Destination, [int]$TimeoutSec = 300) {
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        try { Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Destination -TimeoutSec $TimeoutSec; return }
        catch {
            if ($attempt -eq 3) { throw }
            Write-Host "     Download interrupted; retrying ($attempt/3)..."
            Start-Sleep -Seconds ($attempt * 2)
        }
    }
}
function Find-Checksum([string]$Sums, [string]$Name) {
    # The SHA-256 a checksum file records for Name, or '' when it lists none.
    $found = @([IO.File]::ReadAllLines($Sums) | ForEach-Object {
        if ($_ -match '^([a-fA-F0-9]{64})\s+\*?(.+?)\s*$' -and $Matches[2] -ceq $Name) { $Matches[1].ToLowerInvariant() }
    })
    if ($found.Count -gt 1) { throw "Checksum entry missing or duplicated: $Name" }
    if ($found.Count) { return $found[0] }
    return ''
}
function Assert-Checksum([string]$Archive, [string]$Sums, [string]$Name) {
    $expected = Find-Checksum $Sums $Name
    if (-not $expected) { throw "Checksum entry missing or duplicated: $Name" }
    if ((Get-Sha256 $Archive) -ne $expected) { throw "Checksum mismatch: $Name" }
}
function Select-ReleaseArchive([string]$Sums, [string]$Hash) {
    # The checksum file says what a release ships: its source, built here, or
    # (before 2.2.0) a prebuilt Windows archive. With -ArchivePath, Hash names
    # the archive supplied.
    $names = @($sourceArchiveName, $platformArchiveName)
    foreach ($name in $names) {
        $expected = Find-Checksum $Sums $name
        if ($expected -and (-not $Hash -or $expected -eq $Hash)) { return $name }
    }
    if (-not $Hash) { throw "Facet $targetVersion has no Windows download: the release lists neither $sourceArchiveName nor $platformArchiveName." }
    foreach ($name in $names) { if (Find-Checksum $Sums $name) { throw "Checksum mismatch: $name" } }
    throw "Checksum entry missing or duplicated: $platformArchiveName"
}
function Expand-SafeZip([string]$Archive, [string]$Destination) {
    # Every entry's full path is compared with the destination's, both in
    # their long form: a destination under a short-name TEMP (RUNNER~1, as on
    # hosted CI runners) would otherwise never contain its own entries.
    $root = (Get-LongPath $Destination).TrimEnd('\') + '\'
    $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
    try {
        $seen = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        $total = [long]0
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\','/')
            while ($name.StartsWith('./')) { $name = $name.Substring(2) }
            if (-not $name) { continue }
            if ($name.StartsWith('/') -or $name.Contains(':') -or $name -match '(^|/)\.\.(/|$)|[\x00-\x1f]') { throw "Unsafe archive path: $name" }
            foreach ($part in $name.TrimEnd('/').Split('/')) {
                if (-not $part -or $part -eq '.' -or $part.EndsWith('.') -or $part.EndsWith(' ') -or $part -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)') { throw "Unsafe archive path: $name" }
            }
            if (-not $seen.Add($name.TrimEnd('/'))) { throw "Duplicate archive entry: $name" }
            $type = ($entry.ExternalAttributes -shr 16) -band 0xF000
            if ($type -notin @(0, 0x8000, 0x4000)) { throw 'Archive links and special files are unsupported.' }
            $total += $entry.Length
            if ($total -gt 1GB) { throw 'Archive exceeds the extraction limit.' }
            $destinationPath = [IO.Path]::GetFullPath((Join-Path $Destination $name))
            if (-not $destinationPath.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) { throw 'Archive leaves its destination.' }
        }
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\','/')
            while ($name.StartsWith('./')) { $name = $name.Substring(2) }
            if (-not $name) { continue }
            $destinationPath = Join-Path $Destination $name
            if ($name.EndsWith('/')) { New-Item -ItemType Directory -Path $destinationPath -Force | Out-Null }
            else {
                New-Item -ItemType Directory -Path (Split-Path -Parent $destinationPath) -Force | Out-Null
                [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $destinationPath, $false)
            }
        }
    } finally { $zip.Dispose() }
}
function Get-RelativeFiles([string]$Root) {
    # Get-ChildItem reports long paths, so the prefix is taken in that form.
    $prefix = (Get-LongPath $Root).TrimEnd('\') + '\'
    return @(Get-ChildItem -LiteralPath $Root -Recurse -File -Force | ForEach-Object { $_.FullName.Substring($prefix.Length).Replace('\','/') } | Sort-Object)
}
function Test-RuntimeFiles([string]$Directory) {
    # The archive's files, recorded at extraction, must be unchanged.
    $list = Join-Path $Directory '.facet-files.sha256'
    if (-not (Test-Path -LiteralPath $list -PathType Leaf)) { return $false }
    $lines = @([IO.File]::ReadAllLines($list) | Where-Object { $_ })
    if (-not @($lines | Where-Object { $_ -match '  bin/facet\.exe$' }).Count) { return $false }
    foreach ($line in $lines) {
        if ($line -notmatch '^([a-f0-9]{64})  (.+)$') { return $false }
        $hash = $Matches[1]; $relative = $Matches[2]
        if ($relative -match '(^|/)\.\.(/|$)|:|\\' -or $relative.StartsWith('/')) { return $false }
        $path = Join-Path $Directory $relative
        $item = Get-ItemOrNull $path
        if (-not $item -or $item.PSIsContainer -or $item.LinkType) { return $false }
        if ((Get-Sha256 $path) -ne $hash) { return $false }
    }
    return $true
}

# ------------------------------------------------------- facet from source

function ConvertTo-ProcessArgument([string]$Text) {
    # One argument of a Windows command line, quoted as the C runtime parses it.
    if ($Text -and $Text -notmatch '[\s"]') { return $Text }
    return '"' + (($Text -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"'
}
function Invoke-Go([string]$GoExe, [string]$Directory, [hashtable]$Settings, [string[]]$Arguments) {
    # Runs the pinned Go in a child process that sees these Go settings and
    # no others: none of the user's GO* or CGO_* variables reach it.
    $info = New-Object Diagnostics.ProcessStartInfo
    $info.FileName = $GoExe
    $info.WorkingDirectory = $Directory
    $info.Arguments = @($Arguments | ForEach-Object { ConvertTo-ProcessArgument $_ }) -join ' '
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    foreach ($name in @($info.EnvironmentVariables.Keys)) { if ($name -match '^(?i:GO|CGO_)') { $info.EnvironmentVariables.Remove($name) } }
    foreach ($name in $Settings.Keys) { $info.EnvironmentVariables[$name] = $Settings[$name] }
    $process = [Diagnostics.Process]::Start($info)
    $standardOutput = $process.StandardOutput.ReadToEndAsync()
    $standardError = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    $text = @(($standardOutput.Result + "`n" + $standardError) -split "`r?`n" | Where-Object { $_.Trim() })
    $text
    if ($process.ExitCode -ne 0) { throw "go $($Arguments[0]) exited $($process.ExitCode): $(@($text | Select-Object -Last 3) -join ' | ')" }
}
function Complete-Stage([string]$Stage) {
    # A runtime holds what a release archive does, its facet reports the
    # version being installed, and every file is recorded for repair checks.
    foreach ($required in @('bin/facet.exe','dependencies/remotion-composer/package.json','dependencies/remotion-composer/package-lock.json','dependencies/remotion-composer/composer-manifest.json')) {
        if (-not (Test-Path -LiteralPath (Join-Path $Stage $required) -PathType Leaf)) { throw "Release archive is missing $required." }
    }
    $reported = Invoke-Probe (Join-Path $Stage 'bin/facet.exe') @('version')
    if ($reported.Code -ne 0 -or $reported.Text -ne "facet v$targetVersion") { throw "Binary version mismatch: expected facet v$targetVersion, got '$($reported.Text)'." }
    $hashes = @(Get-RelativeFiles $Stage | ForEach-Object { "$(Get-Sha256 (Join-Path $Stage $_))  $_" })
    [IO.File]::WriteAllText((Join-Path $Stage '.facet-files.sha256'), (($hashes -join "`n") + "`n"), $utf8NoBom)
}
function Build-Facet([string]$Archive, [string]$Stage) {
    # facet.exe is compiled here from the release's source by the official Go
    # toolchain that the manifest pins, so no prebuilt executable is
    # downloaded. The build reads only the archive (its modules are vendored)
    # and never the network. Go, its caches and the source are removed
    # afterwards. Go builds are reproducible: every computer builds the same
    # facet.exe.
    $go = Definition go
    $goVersion = "$($go.version)"
    $goName = "go$goVersion.windows-$arch.zip"
    $goSha = ''
    foreach ($pair in "$($go.windows)".Split(' ')) { if ($pair -match "^$($arch):([0-9a-f]{64})$") { $goSha = $Matches[1] } }
    if ($goVersion -notmatch '^\d+\.\d+\.\d+$' -or -not $goSha) { throw "The installer manifest pins no Go toolchain for windows/$arch." }
    $tar = Join-Path $env:SystemRoot 'System32\tar.exe'
    if (-not (Test-Path -LiteralPath $tar -PathType Leaf)) { throw 'Building Facet needs tar.exe, which Windows includes from Windows 10 version 1803.' }
    $buildRoot = Join-Path $script:temp 'build'
    $tree = Join-Path $buildRoot 'facet'
    $notice = "notices/windows-$arch.md"
    $goZip = if ($ToolchainPath) { $ToolchainPath } else { Join-Path $script:temp $goName }
    $free = $null
    try { $free = (New-Object IO.DriveInfo ([IO.Path]::GetPathRoot($script:temp))).AvailableFreeSpace } catch { }
    if ($null -ne $free -and $free -lt 500MB) { throw "Building Facet needs about 500 MB of free disk space for Go and its build cache; $([int]($free / 1MB)) MB are free on $([IO.Path]::GetPathRoot($script:temp))." }
    # The source is checked before Go is fetched: a bad archive costs no download.
    Step "Unpack Facet $targetVersion source" {
        New-Item -ItemType Directory -Path $tree | Out-Null
        Expand-SafeZip $Archive $tree
        foreach ($required in @('LICENSE', $notice, 'source/go.mod', 'source/cmd/facet/main.go', 'source/vendor/modules.txt')) {
            if (-not (Test-Path -LiteralPath (Join-Path $tree $required) -PathType Leaf)) { throw "Release archive is missing $required." }
        }
        $needed = @([IO.File]::ReadAllLines((Join-Path $tree 'source/go.mod')) | ForEach-Object { if ($_ -match '^go\s+(\d+\.\d+(?:\.\d+)?)\s*$') { $Matches[1] } })
        if ($needed.Count -and [version]($needed[0]) -gt [version]$goVersion) {
            throw "Facet $targetVersion needs Go $($needed[0]) to build, and this installer brings Go $goVersion; use the installer from the Facet $targetVersion release."
        }
    }
    if ($ToolchainPath) {
        Step "Check Go $goVersion" {
            if (-not (Test-Path -LiteralPath $goZip -PathType Leaf)) { throw "Go toolchain not found: $goZip" }
            if ((Get-Sha256 $goZip) -ne $goSha) { throw "$goZip is not the official $goName (SHA-256 mismatch)." }
        }
    } else {
        Step "Download Go $goVersion" {
            Download "$($go.value)$goName" $goZip 1800
            if ((Get-Sha256 $goZip) -ne $goSha) { throw "Checksum mismatch: $goName" }
        }
    }
    Step "Unpack Go $goVersion" {
        # Go's own tests are not needed to build, and their deep paths can
        # exceed the Windows path limit.
        Run $tar @('-xf', $goZip, '-C', $buildRoot, '--exclude', 'testdata', '--exclude', 'go/test')
        if (-not (Test-Path -LiteralPath (Join-Path $buildRoot 'go/bin/go.exe') -PathType Leaf)) { throw "$goName holds no go/bin/go.exe." }
    }
    Step "Build Facet $targetVersion (a minute or two)" {
        $settings = @{
            GOTOOLCHAIN = 'local'; GOFLAGS = '-mod=vendor'; GOPROXY = 'off'; GOWORK = 'off'; GOENV = 'off'; GO111MODULE = 'on'
            CGO_ENABLED = '0'; GOOS = 'windows'; GOARCH = $arch
            GOCACHE = (Join-Path $buildRoot 'cache'); GOPATH = (Join-Path $buildRoot 'path'); GOTMPDIR = (Join-Path $buildRoot 'tmp')
            # Go keeps telemetry counters in the user's configuration folder;
            # pointed here, they are removed with the build.
            APPDATA = (Join-Path $buildRoot 'config'); LOCALAPPDATA = (Join-Path $buildRoot 'local')
        }
        foreach ($folder in @('cache','path','tmp','config','local')) { New-Item -ItemType Directory -Path (Join-Path $buildRoot $folder) | Out-Null }
        New-Item -ItemType Directory -Path (Join-Path $Stage 'bin') -Force | Out-Null
        Invoke-Go (Join-Path $buildRoot 'go/bin/go.exe') (Join-Path $tree 'source') $settings @(
            'build', '-trimpath', '-buildvcs=false', '-ldflags', "-s -w -X main.Version=$targetVersion", '-o', (Join-Path $Stage 'bin/facet.exe'), './cmd/facet')
        Copy-Item -LiteralPath (Join-Path $tree 'LICENSE') -Destination (Join-Path $Stage 'LICENSE')
        Copy-Item -LiteralPath (Join-Path $tree $notice) -Destination (Join-Path $Stage 'THIRD_PARTY_NOTICES.md')
        Copy-Item -LiteralPath (Join-Path $tree 'dependencies') -Destination $Stage -Recurse
        Complete-Stage $Stage
    }
    Step 'Remove Go and the build cache' {
        Remove-Tree $buildRoot
        if (-not $ToolchainPath) { Remove-Item -LiteralPath $goZip -Force }
    }
}

# ------------------------------------------------------- system dependencies

function Update-ProcessPath {
    # Pick up PATH changes made by package managers during this run.
    $machine = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $user = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = (@($env:Path, $user, $machine) | Where-Object { $_ }) -join ';'
}
function Install-WithWinget([string]$Id, [string[]]$Extra) {
    $package = (Definition $Id).windows
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        throw "winget is unavailable. Install $Id ($package) with its vendor installer, then rerun the installer."
    }
    Write-Host "     Installing $Id with winget ($package)."
    Step "Install $Id" { Run 'winget' (@('install','--id',$package,'--exact','--source','winget','--accept-package-agreements','--accept-source-agreements','--disable-interactivity') + $Extra) }
    Update-ProcessPath
}
function Get-NodeMajor([string]$Node) {
    $probe = Invoke-Probe $Node @('-p','process.versions.node.split(".")[0]')
    if ($probe.Code -ne 0 -or $probe.Text -notmatch '^\d+$') { return 0 }
    return [int]$probe.Text
}
function Resolve-Node([string]$Dependencies) {
    $minimum = [int](Definition node).value
    $private = Join-Path $Dependencies 'node/node.exe'
    if ((Test-Path -LiteralPath $private -PathType Leaf) -and (Get-NodeMajor $private) -ge $minimum) { return $private }
    foreach ($candidate in @(Get-Command node.exe -CommandType Application -ErrorAction SilentlyContinue)) {
        if ((Get-NodeMajor $candidate.Source) -ge $minimum) { return $candidate.Source }
    }
    return ''
}
function Install-PrivateNode([string]$Dependencies) {
    # A user-scope Node.js zip in the runtime instead of a machine-wide MSI.
    $nodeVersion = (Definition node).version
    $nodeArch = if ($arch -eq 'arm64') { 'arm64' } else { 'x64' }
    $stem = "node-v$nodeVersion-win-$nodeArch"
    $base = "https://nodejs.org/dist/v$nodeVersion"
    $downloadDir = Join-Path $script:temp 'node'
    New-Item -ItemType Directory -Path $downloadDir -Force | Out-Null
    Step "Download Node.js $nodeVersion (private, about 30 MB)" {
        Download "$base/SHASUMS256.txt" (Join-Path $downloadDir 'SHASUMS256.txt')
        Download "$base/$stem.zip" (Join-Path $downloadDir "$stem.zip")
        Assert-Checksum (Join-Path $downloadDir "$stem.zip") (Join-Path $downloadDir 'SHASUMS256.txt') "$stem.zip"
    }
    $unpacked = Join-Path $downloadDir 'unpacked'
    New-Item -ItemType Directory -Path $unpacked | Out-Null
    Step 'Unpack Node.js' { Expand-SafeZip (Join-Path $downloadDir "$stem.zip") $unpacked }
    $root = Join-Path $unpacked $stem
    if (-not (Test-Path -LiteralPath (Join-Path $root 'node.exe') -PathType Leaf)) { throw 'Unexpected Node.js archive layout.' }
    Move-Directory $root (Join-Path $Dependencies 'node')
    return (Join-Path $Dependencies 'node/node.exe')
}
function Get-NpmCommand([string]$Node) {
    $cli = Join-Path (Split-Path -Parent $Node) 'node_modules/npm/bin/npm-cli.js'
    if (Test-Path -LiteralPath $cli -PathType Leaf) { return @($Node, $cli) }
    $npm = @(Get-Command npm.cmd -CommandType Application -ErrorAction SilentlyContinue)
    if ($npm.Count) { return @($npm[0].Source) }
    throw 'npm is missing next to Node.js; reinstall Node.js with npm and rerun.'
}
function Invoke-Npm([string]$Node, [string[]]$Arguments) {
    $npm = @(Get-NpmCommand $Node)
    if ($npm.Count -eq 2) { Run $npm[0] (@($npm[1]) + $Arguments) } else { Run $npm[0] $Arguments }
}
function Find-Python {
    # Get-Command is not enough: the Microsoft Store alias python.exe exists
    # without a working interpreter. Each candidate must actually run.
    $candidates = New-Object Collections.Generic.List[object]
    foreach ($launcher in @(Get-Command py.exe -CommandType Application -ErrorAction SilentlyContinue)) { $candidates.Add(@($launcher.Source, '-3')) }
    foreach ($name in @('python.exe','python3.exe')) {
        foreach ($command in @(Get-Command $name -CommandType Application -ErrorAction SilentlyContinue)) { $candidates.Add(@($command.Source)) }
    }
    if ($env:LOCALAPPDATA) {
        $local = Join-Path $env:LOCALAPPDATA 'Programs/Python'
        if (Test-Path -LiteralPath $local) {
            foreach ($directory in @(Get-ChildItem -LiteralPath $local -Directory -Filter 'Python3*' -ErrorAction SilentlyContinue | Sort-Object Name -Descending)) {
                $exe = Join-Path $directory.FullName 'python.exe'
                if (Test-Path -LiteralPath $exe -PathType Leaf) { $candidates.Add(@($exe)) }
            }
        }
    }
    foreach ($candidate in $candidates) {
        $arguments = @($candidate | Select-Object -Skip 1) + @('-c', 'import sys; assert sys.version_info >= (3, 9); print(sys.executable)')
        $probe = Invoke-Probe $candidate[0] $arguments
        if ($probe.Code -eq 0 -and $probe.Text -and (Test-Path -LiteralPath $probe.Text -PathType Leaf)) { return $probe.Text }
    }
    return ''
}

# --------------------------------------------------------------- activation

function Set-CurrentRuntime([string]$Name) {
    $target = Join-Path $runtimesDir $Name
    $existing = Get-ItemOrNull $currentLink
    if ($existing -and -not (Test-Link $currentLink)) { throw "$currentLink is not a link created by the Facet installer; move it aside and rerun." }
    $previousTarget = Get-LinkTarget $currentLink
    $temporary = Join-Path $facetHome ('.current-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Junction -Path $temporary -Value $target | Out-Null
    try {
        # A junction cannot replace another in one rename: remove the old link
        # and rename the prepared one into place immediately.
        if ($existing) { Remove-Link $currentLink }
        Move-Directory $temporary $currentLink
    } catch {
        if (-not (Get-ItemOrNull $currentLink) -and $previousTarget) { New-Item -ItemType Junction -Path $currentLink -Value $previousTarget | Out-Null }
        if (Get-ItemOrNull $temporary) { Remove-Link $temporary }
        throw
    }
}
function Send-EnvironmentChange {
    # Tell Explorer that the user environment changed, so new terminals see PATH.
    try {
        if (-not ('FacetInstaller.NativeMethods' -as [type])) {
            Add-Type -Namespace FacetInstaller -Name NativeMethods -MemberDefinition '[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)] public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);'
        }
        $result = [UIntPtr]::Zero
        [void][FacetInstaller.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
    } catch { Write-Log "Environment change broadcast failed: $($_.Exception.Message)" }
}
function Update-PathValue($Key, [switch]$Remove) {
    # $Key is HKCU\Environment (or a stand-in with the same methods in tests).
    # The value is edited unexpanded and keeps its kind, so REG_EXPAND_SZ
    # entries such as %USERPROFILE%\bin survive; the .NET user-environment
    # API would expand them.
    $valueName = @($Key.GetValueNames() | Where-Object { $_ -ieq 'Path' } | Select-Object -First 1)
    $raw = ''
    $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
    if ($valueName.Count) {
        $raw = [string]$Key.GetValue($valueName[0], '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if ($Key.GetValueKind($valueName[0]) -eq [Microsoft.Win32.RegistryValueKind]::String) { $kind = [Microsoft.Win32.RegistryValueKind]::String }
    }
    $updated = if ($Remove) { Remove-PathListEntry $raw $pathEntry } else { Add-PathListEntry $raw $pathEntry }
    if ($updated -ceq $raw) { return $false }
    $name = if ($valueName.Count) { $valueName[0] } else { 'Path' }
    if ($updated) { [void]$Key.SetValue($name, $updated, $kind) } else { [void]$Key.DeleteValue($name, $false) }
    return $true
}
function Update-UserPath([switch]$Remove) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    try { $changed = Update-PathValue $key -Remove:$Remove } finally { $key.Close() }
    if ($changed) { Send-EnvironmentChange }
    return $changed
}

# ------------------------------------------------------------------- wiring

function Get-Wirings {
    # Every wiring recorded in facet wire's own registry, wiring.json in Facet's home folder.
    $registry = Read-JsonFile (Join-Path $facetHome 'wiring.json')
    $result = @()
    foreach ($wiring in @(Get-JsonValue $registry 'wirings')) {
        if ($null -eq $wiring) { continue }
        $cli = "$(Get-JsonValue $wiring 'cli')"
        $recordedScope = "$(Get-JsonValue $wiring 'scope')"
        $project = "$(Get-JsonValue $wiring 'project')"
        if ($cli -notin $supportedClis -or $recordedScope -notin @('user','project')) { continue }
        if ($recordedScope -eq 'user') { $project = '' } elseif (-not $project) { continue }
        $result += [pscustomobject]@{ Cli = $cli; Scope = $recordedScope; Project = $project; Version = "$(Get-JsonValue $wiring 'facet_version')" }
    }
    return @($result)
}
function Group-Wirings($Wirings) {
    # One facet wire call per (scope, project), naming every CLI recorded there.
    $groups = @()
    foreach ($wiring in @($Wirings)) {
        # A function's empty result passed as an argument arrives as $null.
        if ($null -eq $wiring) { continue }
        $group = @($groups | Where-Object { $_.Scope -eq $wiring.Scope -and [string]::Equals($_.Project, $wiring.Project, [StringComparison]::OrdinalIgnoreCase) })
        if ($group.Count) { if ($wiring.Cli -notin $group[0].Clis) { $group[0].Clis = @($group[0].Clis) + $wiring.Cli } }
        else { $groups += [pscustomobject]@{ Scope = $wiring.Scope; Project = $wiring.Project; Clis = @($wiring.Cli) } }
    }
    return @($groups)
}
function Format-Wiring($Wiring) {
    if ($Wiring.Scope -eq 'project') { return "$($Wiring.Cli) (project $($Wiring.Project))" }
    return "$($Wiring.Cli) (user)"
}
function Get-WireArguments([string]$WireScope, [string]$Project) {
    if ($WireScope -eq 'project') { return @('--scope','project','--project',$Project) }
    return @('--scope','user')
}
function Invoke-FacetWire([string]$Executable, [string[]]$Arguments) {
    Write-Host "  -> facet wire $($Arguments -join ' ')"
    Write-Log "== facet wire $($Arguments -join ' ')"
    $ErrorActionPreference = 'Continue'
    $output = @(& $Executable wire @Arguments 2>&1)
    $code = $LASTEXITCODE
    foreach ($line in $output) { Write-Log "$line"; Write-Host "     $line" }
    return $code
}
function Get-DetectedClis {
    return @($supportedClis | Where-Object { Get-Command $_ -ErrorAction SilentlyContinue })
}

# ---------------------------------------------------------------- the runs

function Invoke-Uninstall {
    Section 'Uninstall'
    $activeName = Get-ActiveRuntimeName
    $executable = ''
    if (Test-Path -LiteralPath $currentExe -PathType Leaf) { $executable = $currentExe }
    else {
        foreach ($name in @(Get-InstalledRuntimeNames | Sort-Object -Descending)) {
            $candidate = Join-Path $runtimesDir "$name/bin/facet.exe"
            if (Test-Path -LiteralPath $candidate -PathType Leaf) { $executable = $candidate; break }
        }
    }
    $problems = @()
    $groups = @(Group-Wirings (Get-Wirings))
    if ($groups.Count -and -not $executable) {
        $problems += 'Recorded CLI wirings remain, but no facet executable is available to remove them; reinstall Facet and run facet wire --remove all.'
    }
    foreach ($group in $groups) {
        if (-not $executable) { break }
        # facet wire removes only what it recorded and left unmodified.
        $code = Invoke-FacetWire $executable (@('--remove','all') + (Get-WireArguments $group.Scope $group.Project))
        if ($code -ne 0) { $problems += "facet wire --remove all exited $code for $(if ($group.Scope -eq 'project') { "project $($group.Project)" } else { 'user scope' }); see the output above." }
    }
    if (Get-ItemOrNull $currentLink) {
        if (-not (Test-Link $currentLink)) { throw "$currentLink is not a link created by the Facet installer; it was left in place." }
        Remove-Link $currentLink
        Write-Host "  OK Removed $currentLink"
    }
    if (-not $skipPath) {
        try { if (Update-UserPath -Remove) { Write-Host "  OK Removed $pathEntry from your user PATH" } }
        catch { $problems += "Could not update the user PATH: $($_.Exception.Message)" }
        $env:Path = Remove-PathListEntry $env:Path $pathEntry
    }
    if (Test-Path -LiteralPath $statePath) { Remove-Item -LiteralPath $statePath -Force }
    if ($purgeRuntimes) {
        if (Test-Path -LiteralPath $runtimesDir) { Remove-Tree $runtimesDir; Write-Host "  OK Deleted $runtimesDir" }
        $cache = Join-Path $facetHome 'cache'
        if (Test-Path -LiteralPath $cache) {
            foreach ($file in @(Get-ChildItem -LiteralPath $cache -File -Force | Where-Object { $_.Name -match '^facet-\d+\..*-(windows-(amd64|arm64)|source)\.zip(\.partial)?$' -and $_.Name -notmatch '^facet-1\.' })) { Remove-Item -LiteralPath $file.FullName -Force }
            if (-not @(Get-ChildItem -LiteralPath $cache -Force).Count) { Remove-Item -LiteralPath $cache -Force }
        }
    } else {
        $kept = @(Get-InstalledRuntimeNames)
        if ($kept.Count) { Write-Host "  Runtimes kept in $runtimesDir ($($kept -join ', ')); rerun with -Purge to delete them." }
    }
    Write-V1Note
    foreach ($problem in $problems) { Write-Warning $problem }
    if ($activeName) { Write-Host "`nFacet v$(Get-RuntimeVersion $activeName) was uninstalled." -ForegroundColor Green }
    else { Write-Host "`nNo active Facet runtime was found; leftovers were cleaned up." -ForegroundColor Green }
    if ($script:logFile) { Write-Host "Log: $($script:logFile)" }
}

function Invoke-Rollback {
    Section 'Roll back'
    $activeName = Get-ActiveRuntimeName
    $target = if ($requestedVersion) { "$requestedVersion-windows-$arch" } else { Get-PreviousRuntimeName $activeName }
    if (-not $target) { throw 'No previous Facet runtime is installed to roll back to.' }
    if ($target -eq $activeName) { Write-Host "Facet v$(Get-RuntimeVersion $target) is already active."; return }
    if (-not (Read-RuntimeRecord $target)) { throw "Runtime $target is not installed or incomplete in $runtimesDir." }
    $reported = Invoke-Probe (Join-Path $runtimesDir "$target/bin/facet.exe") @('version')
    if ($reported.Code -ne 0 -or $reported.Text -ne "facet v$(Get-RuntimeVersion $target)") { throw "Runtime $target does not run (reported '$($reported.Text)'); reinstall it." }
    Set-CurrentRuntime $target
    Write-InstallerState $target $activeName
    Write-Host "  OK $currentLink -> $(Join-Path $runtimesDir $target)"
    Complete-Activation $target @() $false $false
}

function Complete-Activation([string]$Name, [string[]]$Verified, [bool]$Unverified, [bool]$OfferWiring) {
    # PATH and optional wiring, after the runtime is active.
    $pathNote = ''
    if ($skipPath) { $pathNote = "unchanged (-NoPath); run Facet as & '$currentExe'" }
    else {
        try {
            $changed = Update-UserPath
            $pathNote = if ($changed) { "added $pathEntry to your user PATH; open a new terminal to use facet" } else { "$pathEntry is already on your user PATH" }
        } catch { $pathNote = "could not update your user PATH ($($_.Exception.Message)); add $pathEntry yourself" }
        if (-not (Test-PathListContains $env:Path $pathEntry)) { $env:Path = "$pathEntry;$env:Path" }
    }
    $wireFailed = $false
    $activeVersion = Get-RuntimeVersion $Name
    # facet wire registers current/bin/facet.exe, so every wiring follows the
    # active runtime; only the skills it copied can be older. They are
    # refreshed after every install, update and rollback, so each CLI's Facet
    # guidance matches the active runtime. Files a person changed are never
    # overwritten: facet wire reports them and leaves them alone.
    $stale = @(Get-Wirings | Where-Object { $_.Version -ne $activeVersion })
    if ($stale.Count) {
        Section 'Refresh wirings'
        $code = Invoke-FacetWire $currentExe @('--refresh')
        if ($code -ne 0) { $wireFailed = $true }
        $stale = @(Get-Wirings | Where-Object { $_.Version -ne $activeVersion })
    }
    $requested = @($wireTargets)
    $requestedScope = $wireScope
    $requestedProject = $wireProject
    if (-not $batch -and $OfferWiring -and -not $requested.Count) {
        # Wiring is the user's decision: nothing is preselected.
        $detected = @(Get-DetectedClis)
        if ($detected.Count) {
            $labels = @($detected | ForEach-Object { "$_ (detected)" })
            $requested = @(Select-Option 'Wire Facet into which CLIs now? (none skips; run facet wire later)' $labels $detected @() -Multiple)
            if ($requested.Count) {
                $requestedScope = @(Select-Option 'Where should these CLIs find Facet?' @('user - every project for your account','project - one project directory') @('user','project') @('user'))[0]
                if ($requestedScope -eq 'project') {
                    $requestedProject = Resolve-UserPath (Ask 'Project directory' (Get-Location).ProviderPath)
                    if (-not (Test-Path -LiteralPath $requestedProject -PathType Container)) { throw "Project directory not found: $requestedProject" }
                }
            }
        } else {
            Write-Host "`nNo supported CLI was detected on PATH (claude, codex, copilot, opencode, compa). Wire one later with: facet wire <cli>"
        }
    }
    if ($requested.Count) {
        Section 'Wire your CLIs'
        $code = Invoke-FacetWire $currentExe (@($requested -join ',') + (Get-WireArguments $requestedScope $requestedProject))
        if ($code -ne 0) { $wireFailed = $true }
    }
    $recorded = @(Get-Wirings)
    Write-Host ''
    Write-Host "Facet v$activeVersion is active." -ForegroundColor Green
    Write-Host "  Runtime:  $(Join-Path $runtimesDir $Name)"
    Write-Host "  Command:  $currentExe"
    Write-Host "  PATH:     $pathNote"
    if ($Unverified) { Write-Host '  Checks:   skipped; media readiness is unverified (-SkipVerify)' -ForegroundColor Yellow }
    elseif (@($Verified).Count) { Write-Host "  Checks:   $($Verified -join ', ')" }
    if ($recorded.Count) { Write-Host "  Wired:    $(@($recorded | ForEach-Object { Format-Wiring $_ }) -join ', ')" }
    if ($stale.Count) {
        Write-Host "  Note:     $($stale.Count) wiring(s) still carry skills from another Facet version; see facet wire --status, then rerun facet wire --refresh."
    }
    if (-not $recorded.Count) { Write-Host '  Next:     facet wire <claude|codex|copilot|opencode|compa> to use Facet from your CLI; facet doctor to check dependencies' }
    Write-V1Note
    if ($script:logFile) { Write-Host "Log: $($script:logFile)" }
    if ($wireFailed) { throw 'Facet is installed and active, but facet wire failed; see the output above.' }
}

function Invoke-Install([string[]]$Selected) {
    $runtimeName = "$targetVersion-windows-$arch"
    $runtimeDir = Join-Path $runtimesDir $runtimeName
    $activeName = Get-ActiveRuntimeName
    $archiveName = ''
    $record = Read-RuntimeRecord $runtimeName
    $intact = [bool]($record -and (Test-RuntimeFiles $runtimeDir))
    $archiveHash = ''
    if ($ArchivePath) {
        $archiveHash = Get-Sha256 $ArchivePath
        $archiveName = Select-ReleaseArchive $ChecksumPath $archiveHash
    }
    $reuse = $intact -and (-not $archiveHash -or "$(Get-JsonValue $record 'archive_sha256')" -eq $archiveHash)
    $installed = if ($reuse) { @(Get-RecordComponents $record) } else { @() }
    # A rebuilt runtime gets back the components its predecessor had.
    $carried = if (-not $reuse -and $record) { @(Get-RecordComponents $record | Where-Object { (Definition $_).windows -in @('all',$arch) }) } else { @() }
    $missing = @($Selected | Where-Object { $_ -notin $installed })
    if ($selectedAction -eq 'update' -and $reuse -and $activeName -eq $runtimeName -and -not $missing.Count) {
        Write-Host "Facet v$targetVersion is already installed and active; nothing to update."
        Complete-Activation $runtimeName @() $false $true
        return
    }
    $built = $false
    $movedAside = ''
    $created = New-Object Collections.Generic.List[string]
    $activated = $false
    try {
        Section '[2/3] Install and verify'
        New-Item -ItemType Directory -Path $runtimesDir -Force | Out-Null
        if (-not $reuse) {
            if (-not $ArchivePath) {
                $base = "https://github.com/$((Definition facet).value)/releases/download/v$targetVersion"
                $cache = Join-Path $facetHome 'cache'
                New-Item -ItemType Directory -Path $cache -Force | Out-Null
                $script:downloadSums = Join-Path $script:temp 'SHA256SUMS.txt'
                Step 'Check release download' { Download "$base/SHA256SUMS.txt" $script:downloadSums }
                $archiveName = Select-ReleaseArchive $script:downloadSums ''
                $cached = Join-Path $cache $archiveName
                $valid = $false
                if (Test-Path -LiteralPath $cached) { try { Assert-Checksum $cached $script:downloadSums $archiveName; $valid = $true } catch { } }
                if ($valid) { Write-Host '  OK Reusing verified download' }
                else {
                    Step "Download Facet $targetVersion" {
                        Download "$base/$archiveName" "$cached.partial"
                        Assert-Checksum "$cached.partial" $script:downloadSums $archiveName
                        Move-Item -LiteralPath "$cached.partial" -Destination $cached -Force
                    }
                }
                $script:sourceArchive = $cached
                $archiveHash = Get-Sha256 $cached
            } else { $script:sourceArchive = $ArchivePath }
            # The new runtime is complete before the old one is touched, so a
            # failed build or unpack leaves the installed Facet as it was.
            $stage = Join-Path $runtimesDir ('.stage-' + [guid]::NewGuid().ToString('N'))
            $script:stage = $stage
            if ($archiveName -eq $sourceArchiveName) { Build-Facet $script:sourceArchive $stage }
            else {
                if ($ToolchainPath) { Write-Host "  Note: Facet $targetVersion ships a prebuilt facet.exe; -ToolchainPath is not needed." }
                Step 'Unpack Facet' {
                    New-Item -ItemType Directory -Path $stage | Out-Null
                    Expand-SafeZip $script:sourceArchive $stage
                    Complete-Stage $stage
                }
            }
            if (Get-ItemOrNull $runtimeDir) {
                if (Test-Link $runtimeDir) { Remove-Link $runtimeDir }
                elseif ($activeName -eq $runtimeName) {
                    # Rebuild the active runtime beside its predecessor, which is
                    # restored if anything fails.
                    $movedAside = Join-Path $runtimesDir ('.old-' + $runtimeName + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
                    try { Move-Directory $runtimeDir $movedAside }
                    catch { throw "Runtime $runtimeName is in use; close agent CLIs that run Facet and rerun. ($($_.Exception.Message))" }
                } else { Remove-Tree $runtimeDir }
            }
            Move-Directory $stage $runtimeDir
            $script:stage = ''
            $built = $true
            $missing = @(@($Selected) + @($carried) | Select-Object -Unique)
        } else {
            Write-Host "  OK Reusing installed runtime $runtimeName"
        }
        $deps = Join-Path $runtimeDir 'dependencies'
        New-Item -ItemType Directory -Path $deps -Force | Out-Null
        $composer = Join-Path $deps 'remotion-composer'
        $hfDir = Join-Path $deps 'hyperframes'
        $hfEntry = Join-Path $hfDir 'node_modules/hyperframes/bin/hyperframes.mjs'
        $piperDir = Join-Path $deps 'piper'
        $voicesDir = Join-Path $deps 'voices'
        $voice = (Definition piper).value
        $all = @(@($installed) + @($missing) | Select-Object -Unique)
        $node = ''
        if ('remotion' -in $all -or 'hyperframes' -in $all) {
            $node = Resolve-Node $deps
            if (-not $node) {
                $privateNode = Join-Path $deps 'node'
                if (Get-ItemOrNull $privateNode) { Remove-Tree $privateNode }
                $created.Add($privateNode)
                $node = Install-PrivateNode $deps
            }
            $env:Path = (Split-Path -Parent $node) + ';' + $env:Path
            Write-Log "Node.js: $node"
        }
        if ('remotion' -in $missing) {
            $created.Add((Join-Path $composer 'node_modules'))
            Step 'Install Remotion packages' { Invoke-Npm $node @('ci','--prefix',$composer,'--no-audit','--no-fund') }
            $browser = "$env:REMOTION_BROWSER_EXECUTABLE"
            if ($browser -and (Test-Path -LiteralPath $browser -PathType Leaf)) { Write-Host "  OK Using the browser from REMOTION_BROWSER_EXECUTABLE: $browser" }
            else {
                Push-Location $composer
                try { Step 'Prepare Remotion browser' { Run $node @((Join-Path $composer 'node_modules/@remotion/cli/remotion-cli.js'),'browser','ensure') } } finally { Pop-Location }
            }
        }
        if ('hyperframes' -in $missing) {
            if (Get-ItemOrNull $hfDir) { Remove-Tree $hfDir }
            $created.Add($hfDir)
            New-Item -ItemType Directory -Path $hfDir | Out-Null
            $pin = (Definition hyperframes).version
            Write-TextFile (Join-Path $hfDir 'package.json') "{`n  `"private`": true,`n  `"dependencies`": {`n    `"hyperframes`": $(ConvertTo-JsonText $pin)`n  }`n}`n"
            Step "Install HyperFrames $pin" { Invoke-Npm $node @('install','--prefix',$hfDir,'--no-audit','--no-fund') }
            Step 'Prepare HyperFrames browser' { Run $node @($hfEntry,'browser','ensure') }
        }
        if ('piper' -in $missing) {
            foreach ($path in @($piperDir, $voicesDir)) { if (Get-ItemOrNull $path) { Remove-Tree $path }; $created.Add($path) }
            $python = Find-Python
            if (-not $python) {
                Install-WithWinget python @('--scope','user')
                $python = Find-Python
            }
            if (-not $python) { throw 'Python 3.9 or newer is required for Piper but was not found after installation. Install Python, then rerun.' }
            Write-Log "Python: $python"
            $piperVersion = (Definition piper).version
            Step 'Prepare private Python environment' { Run $python @('-m','venv',$piperDir) }
            $venvPython = Join-Path $piperDir 'Scripts/python.exe'
            Step "Install Piper $piperVersion" { Run $venvPython @('-m','pip','install','--disable-pip-version-check','--no-input','--only-binary=:all:',"piper-tts==$piperVersion") }
            New-Item -ItemType Directory -Path $voicesDir -Force | Out-Null
            Step "Download voice $voice" { Run $venvPython @('-m','piper.download_voices','--download-dir',$voicesDir,$voice) }
            foreach ($required in @((Join-Path $piperDir 'Scripts/piper.exe'), (Join-Path $voicesDir "$voice.onnx"), (Join-Path $voicesDir "$voice.onnx.json"))) {
                if (-not (Test-Path -LiteralPath $required -PathType Leaf)) { throw "Piper installation is missing $required." }
            }
        }
        $verified = @()
        if (-not $skipVerification) {
            if (-not (Get-Command ffmpeg -ErrorAction SilentlyContinue) -or -not (Get-Command ffprobe -ErrorAction SilentlyContinue)) {
                Install-WithWinget ffmpeg @()
            }
            $verify = Join-Path $script:temp 'verify'
            New-Item -ItemType Directory -Path $verify -Force | Out-Null
            $runtimeExe = Join-Path $runtimeDir 'bin/facet.exe'
            Step 'Verify FFmpeg and FFprobe' {
                Run ffmpeg @('-version')
                Run ffprobe @('-version')
                $sample = Join-Path $verify 'sample.mp4'
                Run ffmpeg @('-v','error','-y','-f','lavfi','-i','color=c=blue:s=320x180:r=24:d=1','-c:v','libx264','-pix_fmt','yuv420p',$sample)
                Run ffprobe @('-v','error','-show_streams',$sample)
            }
            $verified += 'FFmpeg'
            if ('remotion' -in $all) {
                Step 'Verify a Remotion render with facet tools run video_compose' {
                    $renderDir = Join-Path $verify 'remotion'
                    New-Item -ItemType Directory -Path $renderDir | Out-Null
                    $video = Join-Path $renderDir 'render.mp4'
                    $request = "{`"width`": 320, `"height`": 180, `"fps`": 24, `"duration_seconds`": 1, `"output_path`": $(ConvertTo-JsonText $video), `"cuts`": [{`"type`": `"text_card`", `"text`": `"Facet setup`", `"in_seconds`": 0, `"out_seconds`": 1}]}"
                    [IO.File]::WriteAllText((Join-Path $renderDir 'render.json'), $request, $utf8NoBom)
                    # The runtime's own composer, found beside its executable, is
                    # what renders: a development override must not redirect the check.
                    $savedComposer = $env:FACET_REMOTION_COMPOSER
                    $env:FACET_REMOTION_COMPOSER = $null
                    Push-Location $renderDir
                    try { Run $runtimeExe @('tools','run','video_compose','--input',(Join-Path $renderDir 'render.json')) }
                    finally { Pop-Location; $env:FACET_REMOTION_COMPOSER = $savedComposer }
                    if (-not (Test-Path -LiteralPath $video -PathType Leaf)) { throw 'The Remotion render produced no video.' }
                    Run ffprobe @('-v','error','-show_streams',$video)
                    Run ffmpeg @('-v','error','-i',$video,'-f','null','-')
                }
                $verified += 'Remotion render'
            }
            if ('piper' -in $all) {
                Step 'Verify Piper speech' {
                    $wav = Join-Path $verify 'voice.wav'
                    $ErrorActionPreference = 'Continue'
                    'Facet setup verification.' | & (Join-Path $piperDir 'Scripts/piper.exe') --model (Join-Path $voicesDir "$voice.onnx") --output_file $wav
                    if ($LASTEXITCODE -ne 0) { throw "Piper exited $LASTEXITCODE." }
                    Run ffmpeg @('-v','error','-i',$wav,'-f','null','-')
                }
                $verified += 'Piper speech'
            }
            if ('hyperframes' -in $all) {
                Step 'Verify a HyperFrames render' {
                    $html = Join-Path $verify 'hyperframes'
                    New-Item -ItemType Directory -Path $html | Out-Null
                    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'installer/verify.html') -Destination (Join-Path $html 'index.html')
                    Push-Location $html
                    try { Run $node @($hfEntry,'render','--output',(Join-Path $html 'render.mp4'),'--fps','24') } finally { Pop-Location }
                    Run ffmpeg @('-v','error','-i',(Join-Path $html 'render.mp4'),'-f','null','-')
                }
                $verified += 'HyperFrames render'
            }
        } else {
            Write-Host '  Verification skipped (-SkipVerify): FFmpeg and the selected components were not checked.' -ForegroundColor Yellow
        }
        $sha = if ($archiveHash) { $archiveHash } else { "$(Get-JsonValue $record 'archive_sha256')" }
        $componentList = (@($componentIds | Where-Object { $_ -in $all } | ForEach-Object { ConvertTo-JsonText $_ }) -join ', ')
        $verifiedFlag = if ($skipVerification) { 'false' } else { 'true' }
        $text = "{`n  `"schema`": 1,`n  `"version`": $(ConvertTo-JsonText $targetVersion),`n  `"os`": `"windows`",`n  `"arch`": $(ConvertTo-JsonText $arch),`n  `"components`": [$componentList],`n  `"verified`": $verifiedFlag,`n  `"archive_sha256`": $(ConvertTo-JsonText $sha),`n  `"installed_at`": $(ConvertTo-JsonText ([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')))`n}`n"
        Write-TextFile (Join-Path $runtimeDir 'components.json') $text
        Section '[3/3] Activate'
        $previous = if ($activeName -and $activeName -ne $runtimeName) { $activeName } else { (Read-InstallerState).previous }
        if ($activeName -ne $runtimeName -or -not (Test-Link $currentLink)) { Set-CurrentRuntime $runtimeName }
        $activated = $true
        Write-InstallerState $runtimeName $previous
        Write-Host "  OK $currentLink -> $runtimeDir"
        if ($movedAside) { try { Remove-Tree $movedAside } catch { Write-Log "Could not delete ${movedAside}: $($_.Exception.Message)" }; $movedAside = '' }
        foreach ($name in @(Get-InstalledRuntimeNames)) {
            # One earlier runtime is kept for rollback; older ones are removed.
            if ($name -in @($runtimeName, $previous)) { continue }
            try { Remove-Tree (Join-Path $runtimesDir $name); Write-Host "  OK Removed older runtime $name" }
            catch { Write-Host "     Older runtime $name is in use and was kept: $($_.Exception.Message)" }
        }
        $verifiedNames = @($verified)
        Complete-Activation $runtimeName $verifiedNames $skipVerification $true
    } catch {
        if (-not $activated) {
            if ($built -and (Get-ItemOrNull $runtimeDir)) { try { Remove-Tree $runtimeDir } catch { Write-Log "Cleanup failed: $($_.Exception.Message)" } }
            if (-not $built) { foreach ($path in $created) { try { Remove-Tree $path } catch { Write-Log "Cleanup failed: $($_.Exception.Message)" } } }
            if ($movedAside -and -not (Get-ItemOrNull $runtimeDir)) { Move-Directory $movedAside $runtimeDir }
            Write-Host "`nSetup incomplete; the active runtime was not changed." -ForegroundColor Yellow
            if ($script:logFile) { Write-Host "Log: $($script:logFile)" }
        }
        throw
    }
}

# --------------------------------------------------------------------- main

Write-Host "`n  FACET" -ForegroundColor Cyan
Write-Host '  Video production for your agentic CLI.' -ForegroundColor DarkGray
Write-Host "  Installer $releaseVersion | Windows $arch"

$activeName = Get-ActiveRuntimeName
if ((Get-ItemOrNull $currentLink) -and -not (Test-Link $currentLink)) { throw "$currentLink is not a link created by the Facet installer; move it aside and rerun." }
$activeRecord = if ($activeName) { Read-RuntimeRecord $activeName } else { $null }
$previousName = Get-PreviousRuntimeName $activeName
if ($activeName) { Write-Host "  Active: facet v$(Get-RuntimeVersion $activeName) ($activeName)" }

$script:chooseShown = $false
if (-not $selectedAction) {
    if ($batch -or -not $activeName) { $selectedAction = 'install' }
    else {
        Section '[1/3] Choose'
        $script:chooseShown = $true
        $labels = @("install - install or repair facet v$targetVersion and its components", "update - switch to facet v$targetVersion, keeping your components")
        $values = @('install','update')
        if ($previousName) { $labels += "rollback - return to facet v$(Get-RuntimeVersion $previousName)"; $values += 'rollback' }
        $labels += 'uninstall - unwire your CLIs and remove Facet from your PATH'; $values += 'uninstall'
        $default = if ((Get-RuntimeVersion $activeName) -ne $targetVersion) { 'update' } else { 'install' }
        $selectedAction = @(Select-Option 'What should the installer do?' $labels $values @($default))[0]
    }
}
if ($purgeRuntimes -and $selectedAction -ne 'uninstall') { throw '-Purge applies only to uninstall.' }

$selected = @()
if ($selectedAction -in @('install','update')) {
    $available = @($componentRows | Where-Object { $_.windows -in @('all',$arch) })
    if ($null -ne $componentText) {
        $items = @(Resolve-MenuAnswer $componentText $componentIds)
        if ('all' -in $items) { if ($items.Count -ne 1) { throw 'all cannot be combined with component names.' }; $items = @($available | ForEach-Object id) }
        if ('none' -in $items) { if ($items.Count -ne 1) { throw 'none cannot be combined with components.' }; $items = @() }
        if (@($items | Select-Object -Unique).Count -ne $items.Count) { throw 'Duplicate component.' }
        foreach ($id in $items) {
            if ($id -notin $componentIds) { throw "Unknown component '$id'; choose remotion, piper, hyperframes, all, or none." }
            if ((Definition $id).windows -notin @('all',$arch)) { throw "Component unavailable on windows/${arch}: $id" }
        }
        $selected = @($items)
    } elseif ($activeRecord) {
        $selected = @(Get-RecordComponents $activeRecord | Where-Object { (Definition $_).windows -in @('all',$arch) })
    } else {
        $selected = @('remotion','piper' | Where-Object { (Definition $_).windows -in @('all',$arch) })
    }
    if (-not $batch -and $null -eq $componentText) {
        if (-not $script:chooseShown) { Section '[1/3] Choose'; $script:chooseShown = $true }
        Write-Host 'Core: the facet toolset and guidance, with FFmpeg/FFprobe. Edge TTS is built in (keyless network speech).'
        $labels = @($available | ForEach-Object { "$($_.id) | $($_.size) | $($_.capability)" })
        $selected = @(Select-Option 'Optional components (none keeps the core only)' $labels @($available | ForEach-Object id) $selected -Multiple)
    }
    $kept = @()
    if ($activeRecord -and (Get-RuntimeVersion $activeName) -eq $targetVersion) { $kept = @(Get-RecordComponents $activeRecord | Where-Object { $_ -notin $selected }) }
    Write-Host ''
    Write-Host "  Action:      $selectedAction facet v$targetVersion (windows/$arch)"
    Write-Host "  Runtime:     $(Join-Path $runtimesDir "$targetVersion-windows-$arch")"
    # This installer's own release, like every release from 2.2.0, is built here.
    $buildsHere = $targetVersion -eq $releaseVersion
    if ($ArchivePath) { try { $buildsHere = [bool](Find-Checksum $ChecksumPath $sourceArchiveName) } catch { $buildsHere = $false } }
    if ($buildsHere) { Write-Host "  Build:       on this computer by Go $((Definition go).version) ($(if ($ToolchainPath) { "from $ToolchainPath" } else { (Definition go).size }))" }
    Write-Host "  Components:  $(if ($selected.Count) { $selected -join ', ' } else { 'none (core only)' })"
    if ($kept.Count) { Write-Host "  Kept:        $($kept -join ', ') (already installed)" }
    Write-Host "  PATH:        $(if ($skipPath) { 'unchanged' } else { "$pathEntry on your user PATH" })"
    Write-Host "  Checks:      $(if ($skipVerification) { 'skipped (-SkipVerify)' } else { 'FFmpeg and each selected component, before activation' })"
    if ($wireTargets.Count) { Write-Host "  Wire:        $($wireTargets -join ', ') ($wireScope scope$(if ($wireProject) { ": $wireProject" }))" }
    if ($activeName) { Write-Host '  Rollback:    the active runtime stays installed' }
} elseif ($selectedAction -eq 'rollback') {
    $rollbackTarget = if ($requestedVersion) { "$requestedVersion-windows-$arch" } else { $previousName }
    Write-Host ''
    Write-Host "  Action:      roll back to $(if ($rollbackTarget) { "facet v$(Get-RuntimeVersion $rollbackTarget)" } else { 'a previous runtime (none installed)' })"
} else {
    if (-not $batch -and -not $purgeRuntimes -and @(Get-InstalledRuntimeNames).Count) {
        $purgeRuntimes = Confirm-Choice "Also delete the installed runtimes in $runtimesDir?" $false
    }
    Write-Host ''
    Write-Host ("  Action:      uninstall: remove recorded CLI wirings and $currentLink" + $(if (-not $skipPath) { ', and its user PATH entry' } else { '' }))
    Write-Host "  Runtimes:    $(if ($purgeRuntimes) { "deleted ($runtimesDir)" } else { "kept in $runtimesDir" })"
}
if (-not $batch) {
    $defaultYes = $selectedAction -ne 'uninstall'
    if (-not (Confirm-Choice 'Continue?' $defaultYes)) { throw 'Installation cancelled.' }
}

$lockDir = Join-Path $facetHome '.installer-lock'
$script:temp = ''
$script:stage = ''
$locked = $false
$savedPath = $env:Path
try {
    New-Item -ItemType Directory -Path $facetHome -Force | Out-Null
    for ($attempt = 1; -not $locked; $attempt++) {
        try { New-Item -ItemType Directory -Path $lockDir -ErrorAction Stop | Out-Null; $locked = $true }
        catch {
            $owner = "$(Get-Content -LiteralPath (Join-Path $lockDir 'pid') -ErrorAction SilentlyContinue)".Trim()
            $alive = $owner -match '^\d+$' -and [bool](Get-Process -Id ([int]$owner) -ErrorAction SilentlyContinue)
            if ($alive -or $attempt -ge 3) { throw "Another Facet installer is running (lock: $lockDir). Wait for it, or delete the lock if no installer is running." }
            Remove-Item -LiteralPath $lockDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    [IO.File]::WriteAllText((Join-Path $lockDir 'pid'), "$PID", $utf8NoBom)
    Open-Log
    $script:temp = Join-Path (Get-LongPath ([IO.Path]::GetTempPath())) ('facet-install-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $script:temp | Out-Null
    foreach ($leftover in @(Get-ChildItem -LiteralPath $facetHome -Force -Filter '.current-*' -ErrorAction SilentlyContinue)) { try { Remove-Link $leftover.FullName } catch { } }
    if (Test-Path -LiteralPath $runtimesDir) {
        foreach ($leftover in @(Get-ChildItem -LiteralPath $runtimesDir -Directory -Force | Where-Object { $_.Name -like '.stage-*' -or $_.Name -like '.old-*' })) {
            try {
                $restore = ''
                if ($leftover.Name -match '^\.old-(.+)-[0-9a-f]{8}$') { $restore = $Matches[1] }
                if ($restore -match $runtimePattern -and -not (Read-RuntimeRecord $restore) -and (Test-Path -LiteralPath (Join-Path $leftover.FullName 'components.json') -PathType Leaf)) {
                    # An interrupted rebuild: put the complete predecessor back.
                    $incomplete = Join-Path $runtimesDir $restore
                    if (Get-ItemOrNull $incomplete) { Remove-Tree $incomplete }
                    Move-Directory $leftover.FullName $incomplete
                    Write-Log "Restored $restore after an interrupted rebuild."
                } else { Remove-Tree $leftover.FullName }
            } catch { Write-Log "Could not clean up $($leftover.FullName): $($_.Exception.Message)" }
        }
    }
    switch ($selectedAction) {
        'uninstall' { Invoke-Uninstall }
        'rollback' { Invoke-Rollback }
        default { Invoke-Install $selected }
    }
} finally {
    if ($selectedAction -in @('install','update')) {
        # Keep this session's PATH entry for the active runtime; drop the
        # temporary dependency paths that were only needed during setup.
        $env:Path = $savedPath
        if (-not $skipPath -and (Test-Path -LiteralPath $currentExe) -and -not (Test-PathListContains $env:Path $pathEntry)) { $env:Path = "$pathEntry;$env:Path" }
    }
    if ($script:stage -and (Test-Path -LiteralPath $script:stage)) { try { Remove-Tree $script:stage } catch { } }
    if ($script:temp -and (Test-Path -LiteralPath $script:temp)) { try { Remove-Tree $script:temp } catch { } }
    if ($locked) { Remove-Item -LiteralPath $lockDir -Recurse -Force -ErrorAction SilentlyContinue }
}
