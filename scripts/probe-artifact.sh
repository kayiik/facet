#!/usr/bin/env bash
# Fail closed: Node, ffprobe and ffmpeg are mandatory, including full decoding.
set -euo pipefail

exec node "$(dirname "${BASH_SOURCE[0]}")/probe-artifact.mjs" "${1:-renders/final.mp4}"
