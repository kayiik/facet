# Facet development loop (Windows, PowerShell 7).
#
# Builds the facet in this checkout and lays it out like an installed runtime:
#
#   build/dev/bin/facet.exe
#   build/dev/dependencies/remotion-composer   (a junction to this checkout's composer)
#   build/dev/dependencies/hyperframes         (the pinned HyperFrames)
#
# so the dev build finds its composer and HyperFrames the way an installed one
# does, with no environment variables. Then it wires the dev build into your
# agentic CLIs. Run it again after changing Go code; composer changes need no
# rebuild, because the junction points at this checkout.
#
#   pwsh -File scripts/dev.ps1                    # build, prepare, wire claude and opencode
#   pwsh -File scripts/dev.ps1 -Wire all          # wire every CLI found
#   pwsh -File scripts/dev.ps1 -SkipComposer      # rebuild Go only
#   pwsh -File scripts/dev.ps1 -NoWire
param(
    [string]$Wire = "claude,opencode",
    [switch]$NoWire,
    [switch]$SkipComposer,
    [switch]$SkipHyperFrames
)
$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
$dev = Join-Path $repo "build/dev"
$bin = Join-Path $dev "bin"
$deps = Join-Path $dev "dependencies"
New-Item -ItemType Directory -Force $bin, $deps | Out-Null
$exe = Join-Path $bin "facet.exe"

Write-Host "facet: building $exe"
$env:GOTOOLCHAIN = "go1.26.6"
Push-Location $repo
try {
    go build -trimpath -o $exe ./cmd/facet
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally { Pop-Location }

$composer = Join-Path $repo "remotion-composer"
if (-not $SkipComposer) {
    Write-Host "facet: installing the composer's packages and browser"
    Push-Location $composer
    try {
        npm ci --no-audit --no-fund
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
        node node_modules/@remotion/cli/remotion-cli.js browser ensure
        if ($LASTEXITCODE -ne 0) { throw "remotion browser ensure failed" }
    } finally { Pop-Location }
}
$composerLink = Join-Path $deps "remotion-composer"
if (-not (Test-Path $composerLink)) {
    New-Item -ItemType Junction -Path $composerLink -Target $composer | Out-Null
}

$hyperDir = Join-Path $deps "hyperframes"
$hyperEntry = Join-Path $hyperDir "node_modules/hyperframes/bin/hyperframes.mjs"
if (-not $SkipHyperFrames -and -not (Test-Path $hyperEntry)) {
    $pin = (Get-Content (Join-Path $repo "installer/manifest.tsv") | Where-Object { $_ -match "^component\thyperframes\t" } | ForEach-Object { ($_ -split "`t")[2] } | Select-Object -First 1)
    if (-not $pin) { throw "no hyperframes pin in installer/manifest.tsv" }
    Write-Host "facet: installing HyperFrames $pin"
    New-Item -ItemType Directory -Force $hyperDir | Out-Null
    Push-Location $hyperDir
    try {
        if (-not (Test-Path "package.json")) { '{"private":true}' | Set-Content -Encoding utf8NoBOM package.json }
        npm install --no-audit --no-fund "hyperframes@$pin"
        if ($LASTEXITCODE -ne 0) { throw "npm install hyperframes failed" }
    } finally { Pop-Location }
}

# An explicit override would win over the layout above and hide it.
foreach ($name in "FACET_REMOTION_COMPOSER", "FACET_HYPERFRAMES") {
    if ([Environment]::GetEnvironmentVariable($name, "User") -or (Test-Path "Env:$name")) {
        Write-Host "facet: note: $name is set and overrides the dev layout; unset it to use build/dev/dependencies"
    }
}

if (-not $NoWire) {
    Write-Host "facet: wiring $Wire to the dev build"
    & $exe wire $Wire --exe $exe
    if ($LASTEXITCODE -ne 0) { throw "facet wire failed" }
}

& $exe version
Write-Host "facet: ready. Start a new CLI session so it loads the skill, agents and MCP server."
