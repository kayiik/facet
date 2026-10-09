# Parser, preflight and helper checks for the Windows installer. Every
# installer run is a child process with a throwaway profile and never reaches
# a download, a package manager, the user PATH or the real profile.
$ErrorActionPreference = 'Stop'
$path = Join-Path (Split-Path -Parent $PSScriptRoot) 'install.ps1'
$tokens = $null; $errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($path, [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw ($errors | Out-String) }
if (@([IO.File]::ReadAllBytes($path) | Where-Object { $_ -gt 127 }).Count) { throw 'install.ps1 must stay ASCII: Windows PowerShell 5.1 reads a script without a BOM as ANSI.' }
$parameters = @($ast.ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath })
$expected = @('Action','Version','Components','Wire','Scope','ProjectDir','ArchivePath','ChecksumPath','ToolchainPath','NonInteractive','NoPath','SkipVerify','Purge','Plain')
if (Compare-Object $parameters $expected) { throw "install.ps1 parameters changed: $($parameters -join ', ')" }

if ($env:OS -ne 'Windows_NT') {
    try { & $path -NonInteractive; throw 'Expected the platform check.' } catch { if ($_.Exception.Message -notmatch 'Use install.sh') { throw } }
    'Script installer parser and platform check passed.'
    return
}

# Pure helpers, loaded from the script's own definitions.
foreach ($name in @('Resolve-UserPath','Split-List','Resolve-MenuAnswer','Test-PathListContains','Add-PathListEntry','Remove-PathListEntry','ConvertTo-JsonText')) {
    $definition = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name }, $true)
    if (-not $definition) { throw "install.ps1 no longer defines $name" }
    . ([scriptblock]::Create($definition.Extent.Text))
}
function Assert-Equal($Actual, $Expected, [string]$Label) {
    if ("$(@($Actual) -join '|')" -cne "$(@($Expected) -join '|')") { throw "${Label}: got '$(@($Actual) -join '|')', expected '$(@($Expected) -join '|')'" }
}
$entry = 'C:\Users\user\.facet\current\bin'
Assert-Equal (Add-PathListEntry '' $entry) $entry 'empty PATH'
Assert-Equal (Add-PathListEntry 'C:\a;C:\b' $entry) "$entry;C:\a;C:\b" 'prepend'
Assert-Equal (Add-PathListEntry 'C:\a;c:\users\user\.facet\current\bin\' $entry) 'C:\a;c:\users\user\.facet\current\bin\' 'present, other case and trailing slash'
$savedVariable = $env:FACET_TEST_PROFILE
try {
    $env:FACET_TEST_PROFILE = 'C:\Users\user'
    Assert-Equal (Add-PathListEntry '%FACET_TEST_PROFILE%\.facet\current\bin;C:\a' $entry) '%FACET_TEST_PROFILE%\.facet\current\bin;C:\a' 'present through an unexpanded variable'
    Assert-Equal (Remove-PathListEntry "%FACET_TEST_PROFILE%\bin;$entry;C:\a" $entry) '%FACET_TEST_PROFILE%\bin;C:\a' 'removal keeps unexpanded entries'
} finally { $env:FACET_TEST_PROFILE = $savedVariable }
Assert-Equal (Remove-PathListEntry "$entry;C:\a" $entry) 'C:\a' 'remove first'
Assert-Equal (Remove-PathListEntry "C:\a;$($entry.ToUpperInvariant())\" $entry) 'C:\a' 'remove other case'
Assert-Equal (Remove-PathListEntry 'C:\a;C:\b' $entry) 'C:\a;C:\b' 'remove absent'
Assert-Equal (Resolve-MenuAnswer '2' @('install','update','rollback')) 'update' 'numbered single answer'
Assert-Equal (Resolve-MenuAnswer '1, 3' @('remotion','piper','hyperframes')) @('remotion','hyperframes') 'numbered multiple answer'
Assert-Equal (Resolve-MenuAnswer 'Piper' @('remotion','piper')) 'piper' 'named answer'
try { Resolve-MenuAnswer '4' @('a','b','c'); throw 'unreachable' } catch { if ($_.Exception.Message -notmatch 'from 1 to 3') { throw } }
Assert-Equal (ConvertTo-JsonText "C:\a`"b`n") '"C:\\a\"b\u000a"' 'JSON escaping'
$location = Join-Path ([IO.Path]::GetTempPath()) ('facet-location-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $location | Out-Null
Push-Location $location
try {
    # PowerShell's location, not the process directory, anchors user paths.
    Assert-Equal (Resolve-UserPath 'archive.zip') (Join-Path $location 'archive.zip') 'relative user path'
} finally { Pop-Location; Remove-Item -LiteralPath $location -Recurse -Force }

# The Go build's command line and the choice between a release's source and
# its prebuilt archive.
foreach ($name in @('ConvertTo-ProcessArgument','Get-Sha256','Find-Checksum','Select-ReleaseArchive')) {
    $definition = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name }, $true)
    if (-not $definition) { throw "install.ps1 no longer defines $name" }
    . ([scriptblock]::Create($definition.Extent.Text))
}
Assert-Equal (ConvertTo-ProcessArgument './cmd/facet') './cmd/facet' 'plain argument'
Assert-Equal (ConvertTo-ProcessArgument '-s -w -X main.Version=2.2.0') '"-s -w -X main.Version=2.2.0"' 'argument with spaces'
Assert-Equal (ConvertTo-ProcessArgument 'C:\Users\test user\facet.exe') '"C:\Users\test user\facet.exe"' 'path with a space'
Assert-Equal (ConvertTo-ProcessArgument 'C:\a b\') '"C:\a b\\"' 'trailing backslash'
Assert-Equal (ConvertTo-ProcessArgument 'say "hi"') '"say \"hi\""' 'embedded quotes'
Assert-Equal (ConvertTo-ProcessArgument '') '""' 'empty argument'
$targetVersion = '2.2.0'; $sourceArchiveName = 'facet-2.2.0-source.zip'; $platformArchiveName = 'facet-2.2.0-windows-amd64.zip'
$sums = Join-Path ([IO.Path]::GetTempPath()) ('facet-sums-' + [guid]::NewGuid().ToString('N') + '.txt')
$sourceHash = 'a' * 64; $platformHash = 'b' * 64
try {
    [IO.File]::WriteAllText($sums, "$sourceHash  $sourceArchiveName`n$platformHash  $platformArchiveName`n$('c' * 64)  facet-installer-2.2.0.zip`n")
    Assert-Equal (Find-Checksum $sums $sourceArchiveName) $sourceHash 'listed checksum'
    Assert-Equal (Find-Checksum $sums 'facet-2.2.0-linux-amd64.zip') '' 'unlisted checksum'
    Assert-Equal (Select-ReleaseArchive $sums '') $sourceArchiveName 'a release with source is built'
    Assert-Equal (Select-ReleaseArchive $sums $platformHash) $platformArchiveName 'a supplied prebuilt archive is unpacked'
    try { Select-ReleaseArchive $sums ('d' * 64); throw 'unreachable' } catch { if ($_.Exception.Message -notmatch 'Checksum mismatch: facet-2\.2\.0-source\.zip') { throw } }
    [IO.File]::WriteAllText($sums, "$platformHash  $platformArchiveName`n")
    Assert-Equal (Select-ReleaseArchive $sums '') $platformArchiveName 'a release before 2.2.0 ships a prebuilt archive'
    [IO.File]::WriteAllText($sums, "$sourceHash  facet-2.1.2-source.zip`n")
    try { Select-ReleaseArchive $sums ''; throw 'unreachable' } catch { if ($_.Exception.Message -notmatch 'has no Windows download') { throw } }
    try { Select-ReleaseArchive $sums $sourceHash; throw 'unreachable' } catch { if ($_.Exception.Message -notmatch 'Checksum entry missing or duplicated') { throw } }
    [IO.File]::WriteAllText($sums, "$sourceHash  $sourceArchiveName`n$sourceHash  $sourceArchiveName`n")
    try { Select-ReleaseArchive $sums ''; throw 'unreachable' } catch { if ($_.Exception.Message -notmatch 'Checksum entry missing or duplicated') { throw } }
} finally { Remove-Item -LiteralPath $sums -Force -ErrorAction SilentlyContinue }

# Extraction and the runtime file list under a short-name (8.3) path, as a
# hosted runner's TEMP is: GetFullPath and Get-ChildItem report the long form,
# and the containment check and the relative names must agree with it.
foreach ($name in @('Get-LongPath','Expand-SafeZip','Get-RelativeFiles')) {
    $definition = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name }, $true)
    if (-not $definition) { throw "install.ps1 no longer defines $name" }
    . ([scriptblock]::Create($definition.Extent.Text))
}
Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
$longRoot = Join-Path ([IO.Path]::GetTempPath()) ('facet-short-name-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $longRoot | Out-Null
try {
    $shortRoot = (New-Object -ComObject Scripting.FileSystemObject).GetFolder($longRoot).ShortPath
    $fixture = Join-Path $longRoot 'fixture.zip'
    $zip = [IO.Compression.ZipFile]::Open($fixture, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($member in @('bin/facet.exe','dependencies/remotion-composer/package.json')) {
            $writer = [IO.StreamWriter]::new($zip.CreateEntry($member).Open()); $writer.Write('fixture'); $writer.Dispose()
        }
    } finally { $zip.Dispose() }
    $destination = Join-Path $shortRoot 'stage'
    New-Item -ItemType Directory -Path $destination | Out-Null
    Expand-SafeZip $fixture $destination
    Assert-Equal (Get-RelativeFiles $destination) @('bin/facet.exe','dependencies/remotion-composer/package.json') "files extracted under $destination"
} finally { Remove-Item -LiteralPath $longRoot -Recurse -Force }

# The user PATH edit, driven through a stand-in for HKCU\Environment: the real
# registry is never opened by this test.
$definition = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Update-PathValue' }, $true)
if (-not $definition) { throw 'install.ps1 no longer defines Update-PathValue' }
. ([scriptblock]::Create($definition.Extent.Text))
$pathEntry = $entry
function New-EnvironmentKey($Value, $Kind) {
    $key = [pscustomobject]@{ Value = $Value; Kind = $Kind; Name = 'Path'; Writes = 0; Deleted = $false }
    $key | Add-Member ScriptMethod GetValueNames { if ($null -ne $this.Value) { @($this.Name) } else { @() } }
    $key | Add-Member ScriptMethod GetValue { param($Name, $Default, $Options)
        if ($Options -ne [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) { throw 'PATH must be read unexpanded.' }
        $this.Value }
    $key | Add-Member ScriptMethod GetValueKind { param($Name) $this.Kind }
    $key | Add-Member ScriptMethod SetValue { param($Name, $Value, $Kind) $this.Name = $Name; $this.Value = $Value; $this.Kind = $Kind; $this.Writes++ }
    $key | Add-Member ScriptMethod DeleteValue { param($Name, $Throw) $this.Value = $null; $this.Deleted = $true }
    return $key
}
$expand = [Microsoft.Win32.RegistryValueKind]::ExpandString
$plain = [Microsoft.Win32.RegistryValueKind]::String
$key = New-EnvironmentKey '%USERPROFILE%\bin;C:\tools' $expand
if (-not (Update-PathValue $key) -or $key.Value -cne "$entry;%USERPROFILE%\bin;C:\tools" -or $key.Kind -ne $expand) { throw "PATH add lost unexpanded entries: $($key.Value) ($($key.Kind))" }
if (Update-PathValue $key) { throw 'PATH add was not idempotent.' }
if ($key.Writes -ne 1) { throw 'An unchanged PATH was rewritten.' }
if (-not (Update-PathValue $key -Remove) -or $key.Value -cne '%USERPROFILE%\bin;C:\tools' -or $key.Kind -ne $expand) { throw "PATH removal changed other entries: $($key.Value)" }
$key = New-EnvironmentKey 'C:\tools' $plain
[void](Update-PathValue $key)
if ($key.Kind -ne $plain) { throw 'A REG_SZ user PATH changed kind.' }
$key = New-EnvironmentKey $null $null
if (-not (Update-PathValue $key) -or $key.Value -cne $entry -or $key.Kind -ne $expand) { throw 'A missing user PATH was not created as REG_EXPAND_SZ.' }
if (-not (Update-PathValue $key -Remove) -or -not $key.Deleted) { throw 'Removing the only entry did not remove the value.' }

$root = Join-Path ([IO.Path]::GetTempPath()) ('facet-preflight-' + [guid]::NewGuid().ToString('N'))
function Invoke-Installer([string]$Shell, [string[]]$Arguments) {
    # Windows PowerShell turns a child's stderr into terminating errors under
    # Stop; the exit code and text are what is checked here.
    $ErrorActionPreference = 'Continue'
    $text = (& $Shell -NoProfile -ExecutionPolicy Bypass -File $path @Arguments 2>&1 | ForEach-Object { "$_" }) -join "`n"
    return @{ Code = $LASTEXITCODE; Output = $text }
}
$names = @('HOME','USERPROFILE','APPDATA','LOCALAPPDATA','FACET_ACTION','FACET_COMPONENTS','FACET_WIRE','FACET_SCOPE','FACET_PROJECT','FACET_YES','FACET_PURGE','FACET_VERSION','FACET_LOG_DIR')
$saved = @{}
foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name) }
function Get-StoredUserPath {
    # The stored value, unexpanded: [Environment]::GetEnvironmentVariable
    # expands a REG_EXPAND_SZ PATH such as %USERPROFILE%\AppData\... with this
    # process's variables, which this test redirects to a throwaway profile.
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
    if (-not $key) { return $null }
    try { return $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) } finally { $key.Close() }
}
$userPath = Get-StoredUserPath
try {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $null) }
    $profileDir = Join-Path $root 'profile'
    foreach ($directory in @($profileDir, (Join-Path $profileDir 'AppData\Roaming'), (Join-Path $profileDir 'AppData\Local'))) { New-Item -ItemType Directory -Path $directory -Force | Out-Null }
    $env:HOME = $profileDir; $env:USERPROFILE = $profileDir
    $env:APPDATA = Join-Path $profileDir 'AppData\Roaming'; $env:LOCALAPPDATA = Join-Path $profileDir 'AppData\Local'
    # A missing archive is a safety net: a regression that got past the
    # preflight fails on it instead of downloading anything.
    $missing = @('-NonInteractive','-NoPath','-SkipVerify','-ArchivePath',(Join-Path $root 'missing.zip'),'-ChecksumPath',(Join-Path $root 'missing.txt'))
    $cases = @(
        @{ Arguments = @('-NonInteractive','-NoPath','-ArchivePath',(Join-Path $root 'only.zip')); Message = 'Supply both -ArchivePath and -ChecksumPath' },
        @{ Arguments = $missing + @('-Components','bogus'); Message = "Unknown component 'bogus'" },
        @{ Arguments = $missing + @('-Components','remotion,none'); Message = 'none cannot be combined' },
        @{ Arguments = $missing + @('-Wire','emacs'); Message = "Unknown CLI 'emacs'" },
        @{ Arguments = $missing + @('-Version','2.0'); Message = 'Invalid release version' },
        @{ Arguments = $missing + @('-Purge'); Message = '-Purge applies only to uninstall' },
        @{ Arguments = $missing + @('-Wire','claude','-Scope','user','-ProjectDir',$root); Message = '-ProjectDir applies only to -Scope project' },
        @{ Arguments = $missing + @('-Wire','opencode','-ProjectDir',(Join-Path $root 'absent')); Message = 'Project directory not found' },
        @{ Arguments = @('-NonInteractive','-Action','uninstall','-Components','none'); Message = '-Components applies to install and update' },
        @{ Arguments = @('-NonInteractive','-Action','rollback','-ToolchainPath',(Join-Path $root 'go.zip')); Message = '-ToolchainPath applies to install and update' }
    )
    $hosts = @('pwsh.exe', 'powershell.exe' | Where-Object { Get-Command $_ -ErrorAction SilentlyContinue })
    foreach ($shell in $hosts) {
        foreach ($case in $cases) {
            $result = Invoke-Installer $shell $case.Arguments
            if ($result.Code -eq 0 -or $result.Output -notmatch [regex]::Escape($case.Message)) { throw "$shell $($case.Arguments -join ' '): expected '$($case.Message)', exit $($result.Code)`n$($result.Output)" }
        }
        if (Test-Path -LiteralPath (Join-Path $profileDir '.facet')) { throw "$shell wrote into the profile during a rejected preflight." }
        # Nothing installed: rollback has no target and stops.
        $result = Invoke-Installer $shell @('-NonInteractive','-Action','rollback')
        if ($result.Code -eq 0 -or $result.Output -notmatch 'No previous Facet runtime') { throw "$shell rollback without runtimes: exit $($result.Code)`n$($result.Output)" }
        if (Test-Path -LiteralPath (Join-Path $profileDir '.facet/current')) { throw 'A failed rollback created ~/.facet/current.' }
        Remove-Item -LiteralPath (Join-Path $profileDir '.facet') -Recurse -Force -ErrorAction SilentlyContinue
    }
    if ((Get-StoredUserPath) -cne $userPath) { throw 'The user PATH changed.' }
    $global:LASTEXITCODE = 0
    "Script installer parser, parameters, helpers and preflight passed ($($hosts -join ', '))."
} finally {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
