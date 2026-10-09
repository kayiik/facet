# Exercises the real irm | iex dispatch with mocked network and a fixture child.
# No agent settings, dependencies, or product installation are changed.
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$source = [IO.File]::ReadAllText((Join-Path $repo 'docs/install.ps1'))
$tokens=$null; $errors=$null
[void][Management.Automation.Language.Parser]::ParseInput($source,[ref]$tokens,[ref]$errors)
if ($errors.Count) { throw ($errors | Out-String) }
if ($source -notmatch "(?m)^\s*try \{ Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force") { throw 'The bootstrap does not allow the verified installer for this process.' }
if ($env:OS -ne 'Windows_NT') { 'PowerShell bootstrap parses; execution is Windows-only.'; return }
$pinned = 'ee3ceddbd50847bac8b16ae65b4d8e3677deb76933baaa86091945567fc65f09'
if (-not $source.Contains($pinned)) { throw 'The pinned installer digest changed; update this test with the release.' }
$root = Join-Path ([IO.Path]::GetTempPath()) ('facet-bootstrap-test-' + [guid]::NewGuid().ToString('N'))
$environment = @('FACET_BOOTSTRAP_TEST','FACET_BOOTSTRAP_FAIL','FACET_COMPONENTS','FACET_WIRE','FACET_SCOPE','FACET_NO_PATH','FACET_YES','FACET_ACTION','FACET_VERSION','FACET_PROJECT','FACET_SKIP_VERIFY','FACET_PURGE','FACET_PLAIN')
$saved = @{}
foreach ($name in $environment) { $saved[$name] = [Environment]::GetEnvironmentVariable($name) }
$savedPolicy = Get-ExecutionPolicy -Scope Process
New-Item -ItemType Directory -Path $root | Out-Null
try {
    foreach ($name in $environment) { [Environment]::SetEnvironmentVariable($name, $null) }
    $archive = Join-Path $root 'fixture.zip'
    Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::Open($archive,[IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($name in @('install.ps1','install.sh','installer/manifest.tsv','installer/verify.html')) {
            $entry = $zip.CreateEntry($name)
            $writer = [IO.StreamWriter]::new($entry.Open())
            if ($name -eq 'install.ps1') {
                # Declares some installer parameters; Scope is deliberately absent.
                $writer.Write(@'
param([string]$Components, [string[]]$Wire, [switch]$NoPath, [switch]$NonInteractive)
if ((Read-Host "Fixture prompt") -ne "answer") { throw "Prompt failed" }
$report = [ordered]@{ root = $PSScriptRoot; components = "$Components"; wire = "$($Wire -join ',')"; noPath = [bool]$NoPath; nonInteractive = [bool]$NonInteractive; policy = "$(Get-ExecutionPolicy -Scope Process)" }
[IO.File]::WriteAllText($env:FACET_BOOTSTRAP_TEST, ($report | ConvertTo-Json))
if ($env:FACET_BOOTSTRAP_FAIL -eq "1") { throw "Child failure" }
'@)
            } else { $writer.Write('fixture') }
            $writer.Dispose()
        }
    } finally { $zip.Dispose() }
    $hash = (Get-FileHash $archive).Hash.ToLowerInvariant()
    $script:bootstrapSource = $source.Replace($pinned,$hash)
    $script:archive = $archive
    $script:lastDownload = ''
    function Invoke-RestMethod { param($Uri); if ($Uri -ne 'https://kayiik.github.io/facet/install.ps1') { throw 'Unexpected bootstrap URL' }; $script:bootstrapSource }
    function Invoke-WebRequest { param($Uri,$OutFile,$TimeoutSec,[switch]$UseBasicParsing); if ($Uri -ne 'https://github.com/kayiik/facet/releases/download/v0.0.1/facet-installer-0.0.1.zip') { throw 'Unexpected release URL' }; $script:lastDownload=$OutFile; Copy-Item -LiteralPath $script:archive -Destination $OutFile }
    function Read-Host { param($Prompt); 'answer' }
    $env:FACET_BOOTSTRAP_TEST = Join-Path $root 'called'
    $env:FACET_COMPONENTS = 'none'
    $env:FACET_WIRE = 'claude,opencode'
    $env:FACET_NO_PATH = '1'
    $env:FACET_YES = '1'
    $env:FACET_SCOPE = 'project'
    irm https://kayiik.github.io/facet/install.ps1 | iex
    if (-not (Test-Path $env:FACET_BOOTSTRAP_TEST)) { throw 'Child never ran' }
    $report = [IO.File]::ReadAllText($env:FACET_BOOTSTRAP_TEST) | ConvertFrom-Json
    if ($report.components -ne 'none' -or $report.wire -ne 'claude,opencode' -or -not $report.noPath -or -not $report.nonInteractive) { throw "Environment options were not passed: $($report | ConvertTo-Json -Compress)" }
    if ($report.policy -ne 'Bypass') { throw "The installer ran under execution policy $($report.policy), not Bypass" }
    if (Test-Path (Split-Path -Parent $script:lastDownload)) { throw 'Bootstrap temporary directory leaked' }
    Remove-Item $env:FACET_BOOTSTRAP_TEST
    $script:bootstrapSource = $script:bootstrapSource.Replace($hash,('0'*64))
    try { irm https://kayiik.github.io/facet/install.ps1 | iex; throw 'Expected checksum failure' } catch { if ($_.Exception.Message -notmatch 'checksum mismatch') { throw } }
    if ((Test-Path $env:FACET_BOOTSTRAP_TEST) -or (Test-Path (Split-Path -Parent $script:lastDownload))) { throw 'Bad checksum executed child or leaked temporary files' }
    $script:bootstrapSource = $source.Replace($pinned,$hash)
    $env:FACET_BOOTSTRAP_FAIL = '1'
    try { irm https://kayiik.github.io/facet/install.ps1 | iex; throw 'Expected child failure' } catch { if ($_.Exception.Message -notmatch 'Child failure') { throw } }
    if (Test-Path (Split-Path -Parent $script:lastDownload)) { throw 'Child failure leaked temporary files' }
    'PASS: irm | iex, interactive host handoff, declared-only option passthrough, process-scope Bypass, checksum rejection, child failure, and cleanup.'
} finally {
    foreach ($name in $environment) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
    try { Set-ExecutionPolicy -Scope Process -ExecutionPolicy $savedPolicy -Force } catch { }
    Remove-Item -LiteralPath $root -Recurse -Force
}
