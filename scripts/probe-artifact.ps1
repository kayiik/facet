# Fail closed; use the same decoder and codec assertions on every platform.
param(
    [string]$FilePath = "renders/final.mp4"
)

$ErrorActionPreference = 'Stop'
try {
    & node (Join-Path $PSScriptRoot 'probe-artifact.mjs') $FilePath
    exit $LASTEXITCODE
} catch {
    Write-Error 'Artifact verifier could not execute; Node, ffprobe and ffmpeg are required.'
    exit 1
}
