# Website bootstrap for: irm https://kayiik.github.io/facet/install.ps1 | iex
# The release's install.ps1 owns every installation decision; this only
# downloads the pinned installer package, verifies it, and starts it.
# The pinned version and SHA-256 below are the latest published installer.
# They change only when a release is published, never for local changes, and
# the allowed file list below is that package's layout.
& {
    $ErrorActionPreference = 'Stop'
    Set-StrictMode -Off
    $PSModuleAutoLoadingPreference = 'All'
    if ($PSVersionTable.PSEdition -ne 'Core') {
        $env:PSModulePath = (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/Modules') + ';' + $env:PSModulePath
    }
    Import-Module Microsoft.PowerShell.Utility
    $ProgressPreference = 'SilentlyContinue'
    if ($env:OS -ne 'Windows_NT') { throw 'Use the curl command on Linux/macOS.' }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }
    $version = '0.0.1'
    $expected = 'ee3ceddbd50847bac8b16ae65b4d8e3677deb76933baaa86091945567fc65f09'
    $url = "https://github.com/kayiik/facet/releases/download/v$version/facet-installer-$version.zip"
    $temp = Join-Path ([IO.Path]::GetTempPath()) ('facet-bootstrap-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $temp | Out-Null
    try {
        Write-Host "Downloading Facet $version installer..."
        $archive = Join-Path $temp 'installer.zip'
        for ($attempt=1; $attempt -le 3; $attempt++) {
            try { Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $archive -TimeoutSec 180; break }
            catch { if ($attempt -eq 3) { throw }; Write-Host "Download interrupted; retrying ($attempt/3)..."; Start-Sleep -Seconds ($attempt*2) }
        }
        if ((Get-Item -LiteralPath $archive).Length -gt 1MB -or (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne $expected) {
            throw 'Installer checksum mismatch; nothing executed.'
        }
        $zip = [IO.Compression.ZipFile]::OpenRead($archive)
        try {
            $names = @($zip.Entries | ForEach-Object FullName | Sort-Object)
            $allowed = @('install.ps1','install.sh','installer/manifest.tsv','installer/verify.html') | Sort-Object
            if ($names.Count -ne $allowed.Count -or (Compare-Object $names $allowed -CaseSensitive)) { throw 'Unexpected installer package layout.' }
        } finally { $zip.Dispose() }
        $package = Join-Path $temp 'package'
        Expand-Archive -LiteralPath $archive -DestinationPath $package
        $installer = Join-Path $package 'install.ps1'
        # irm | iex runs this text without a script-file policy check, but the
        # verified install.ps1 is a file: allow it for this process only.
        try { Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force -ErrorAction Stop }
        catch { Write-Warning "The execution policy for this session could not be relaxed: $($_.Exception.Message)" }
        # Environment settings become parameters, offered only when the
        # downloaded installer declares them (a pinned older package keeps
        # working while the bootstrap knows newer options). FACET_HOME needs
        # no mapping: the installer reads it from the environment itself.
        $tokens = $null; $errors = $null
        $ast = [Management.Automation.Language.Parser]::ParseFile($installer, [ref]$tokens, [ref]$errors)
        $declared = @()
        if ($ast.ParamBlock) { $declared = @($ast.ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath }) }
        $options = @{}
        foreach ($pair in @(@('FACET_ACTION','Action'),@('FACET_VERSION','Version'),@('FACET_COMPONENTS','Components'),@('FACET_WIRE','Wire'),@('FACET_SCOPE','Scope'),@('FACET_PROJECT','ProjectDir'))) {
            $value = [Environment]::GetEnvironmentVariable($pair[0])
            if ($value -and $pair[1] -in $declared) { $options[$pair[1]] = $value }
        }
        foreach ($pair in @(@('FACET_YES','NonInteractive'),@('FACET_NO_PATH','NoPath'),@('FACET_SKIP_VERIFY','SkipVerify'),@('FACET_PURGE','Purge'),@('FACET_PLAIN','Plain'))) {
            if ([Environment]::GetEnvironmentVariable($pair[0]) -eq '1' -and $pair[1] -in $declared) { $options[$pair[1]] = $true }
        }
        if ($PSVersionTable.PSVersion.Major -lt 7 -and [IO.File]::ReadAllText($installer) -match '(?m)^#requires -Version 7') {
            # Older published packages can require pwsh. Prefer an existing
            # compatible host; do not silently install another shell.
            $pwsh = Get-Command pwsh -ErrorAction SilentlyContinue
            if (-not $pwsh) { throw 'This published package requires PowerShell 7. Run this command in pwsh, or use the manual package.' }
            & $pwsh.Source -NoProfile -ExecutionPolicy Bypass -File $installer @options
            if ($LASTEXITCODE -ne 0) { throw "Installer exited with $LASTEXITCODE" }
        } else {
            # Invoke in this host so prompts stay interactive under irm | iex.
            & $installer @options
        }
    } finally {
        Remove-Item -LiteralPath $temp -Recurse -Force
    }
}
