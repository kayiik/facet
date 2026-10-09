#!/usr/bin/env bash
# Facet development loop (Linux, macOS).
#
# Builds the facet in this checkout and lays it out like an installed runtime:
#
#   build/dev/bin/facet
#   build/dev/dependencies/remotion-composer   (a link to this checkout's composer)
#   build/dev/dependencies/hyperframes         (the pinned HyperFrames)
#
# so the dev build finds its composer and HyperFrames the way an installed one
# does, with no environment variables. Then it wires the dev build into your
# agentic CLIs. Run it again after changing Go code; composer changes need no
# rebuild, because the link points at this checkout.
#
#   scripts/dev.sh                 # build, prepare, wire claude and opencode
#   WIRE=all scripts/dev.sh        # wire every CLI found
#   SKIP_COMPOSER=1 scripts/dev.sh # rebuild Go only
#   NO_WIRE=1 scripts/dev.sh
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"
dev="$repo/build/dev"
deps="$dev/dependencies"
mkdir -p "$dev/bin" "$deps"
exe="$dev/bin/facet"

echo "facet: building $exe"
(cd "$repo" && GOTOOLCHAIN=go1.26.6 go build -trimpath -o "$exe" ./cmd/facet)

composer="$repo/remotion-composer"
if [ -z "${SKIP_COMPOSER:-}" ]; then
  echo "facet: installing the composer's packages and browser"
  (cd "$composer" && npm ci --no-audit --no-fund && node node_modules/@remotion/cli/remotion-cli.js browser ensure)
fi
[ -e "$deps/remotion-composer" ] || ln -s "$composer" "$deps/remotion-composer"

hyper_dir="$deps/hyperframes"
hyper_entry="$hyper_dir/node_modules/hyperframes/bin/hyperframes.mjs"
if [ -z "${SKIP_HYPERFRAMES:-}" ] && [ ! -f "$hyper_entry" ]; then
  pin="$(awk -F'\t' '$1=="component" && $2=="hyperframes" {print $3; exit}' "$repo/installer/manifest.tsv")"
  echo "facet: installing HyperFrames $pin"
  mkdir -p "$hyper_dir"
  [ -f "$hyper_dir/package.json" ] || echo '{"private":true}' > "$hyper_dir/package.json"
  (cd "$hyper_dir" && npm install --no-audit --no-fund "hyperframes@$pin")
fi

# An explicit override would win over the layout above and hide it.
for name in FACET_REMOTION_COMPOSER FACET_HYPERFRAMES; do
  if [ -n "${!name:-}" ]; then
    echo "facet: note: $name is set and overrides the dev layout; unset it to use build/dev/dependencies"
  fi
done

if [ -z "${NO_WIRE:-}" ]; then
  echo "facet: wiring ${WIRE:-claude,opencode} to the dev build"
  "$exe" wire "${WIRE:-claude,opencode}" --exe "$exe"
fi

"$exe" version
echo "facet: ready. Start a new CLI session so it loads the skill, agents and MCP server."
