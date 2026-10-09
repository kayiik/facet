#!/usr/bin/env bash
# Contributor source build: bash /path/to/facet/scripts/install-source.sh [--no-path]
# Builds Facet from this checkout into the same user-wide layout the release
# installer uses (~/.facet/runtimes/<version>-dev-<os>-<arch>) and activates it
# through ~/.facet/current. Wiring into CLIs stays an explicit `facet wire` step.
if [ -z "${BASH_VERSION:-}" ]; then
    printf '%s\n' 'Run this source installer with bash, not sh.' >&2
    exit 1
fi
set -euo pipefail

case "$(uname -s)" in
    Linux) OS=linux ;;
    Darwin) OS=darwin ;;
    *) printf '%s\n' 'This installer supports Linux/macOS. Use scripts/install-source.ps1 on Windows.' >&2; exit 1 ;;
esac
case "$(uname -m)" in x86_64|amd64) ARCH=amd64 ;; arm64|aarch64) ARCH=arm64 ;; *) printf '%s\n' 'Unsupported architecture.' >&2; exit 1 ;; esac
NO_PATH=${FACET_NO_PATH:-0}
for argument in "$@"; do
    case "$argument" in
        --no-path) NO_PATH=1 ;;
        --help|-h) printf '%s\n' 'Usage: bash scripts/install-source.sh [--no-path]'; exit 0 ;;
        *) printf 'Unknown option: %s\n' "$argument" >&2; exit 1 ;;
    esac
done

source_help() {
    printf '%s\n' \
        'Facet requires a complete source checkout; this script does not download source code.' \
        'Run: git clone https://github.com/kayiik/facet.git' \
        'Then: bash /absolute/path/to/facet/scripts/install-source.sh' >&2
    exit 1
}
[ -n "${BASH_SOURCE[0]:-}" ] || source_help
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
for file in go.mod package.json cmd/facet/main.go remotion-composer/package-lock.json remotion-composer/composer-manifest.json; do
    [ -f "${SCRIPT_DIR}/${file}" ] || source_help
done

for program in go node npm ffmpeg ffprobe; do
    command -v "$program" >/dev/null 2>&1 || {
        printf 'Missing prerequisite: %s. Install the Go version named in go.mod, Node.js with npm, and FFmpeg/FFprobe first.\n' "$program" >&2
        exit 1
    }
done
# The go directive in go.mod is the required toolchain; Go may download it.
REQUIRED_GO=$(awk '$1=="go" {print $2; exit}' "$SCRIPT_DIR/go.mod")
printf 'go.mod requires Go %s (found %s; Go downloads the required toolchain when allowed).\n' "$REQUIRED_GO" "$(go env GOVERSION)"

VERSION=$(node -p "require(process.argv[1]).version" "$SCRIPT_DIR/package.json")
NAME="$VERSION-dev-$OS-$ARCH"
FACET_HOME_DIR="${FACET_HOME:-${HOME:?HOME must be set}/.facet}"
case "$FACET_HOME_DIR" in /*) ;; *) FACET_HOME_DIR="$PWD/$FACET_HOME_DIR" ;; esac
RUNTIMES="$FACET_HOME_DIR/runtimes"
RUNTIME="$RUNTIMES/$NAME"
STAGE=$(mktemp -d "${TMPDIR:-/tmp}/facet-source.XXXXXX")
trap 'rm -rf -- "$STAGE"' EXIT

printf 'Building Facet %s from source...\n' "$VERSION"
(cd "$SCRIPT_DIR" && go build -trimpath -ldflags "-X main.Version=$VERSION" -o "$STAGE/bin/facet" ./cmd/facet)

printf '%s\n' 'Copying the Remotion composer sources...'
mkdir -p "$STAGE/dependencies/remotion-composer"
(
    cd "$SCRIPT_DIR/remotion-composer"
    node -e 'const m=require("./composer-manifest.json"); for (const p of [...m.allowedSourcePaths, "package.json", "package-lock.json", "tsconfig.json", "composer-manifest.json"]) console.log(p)' |
        while IFS= read -r path; do
            mkdir -p "$STAGE/dependencies/remotion-composer/$(dirname -- "$path")"
            cp -- "$path" "$STAGE/dependencies/remotion-composer/$path"
        done
)

printf '%s\n' 'Installing locked Remotion dependencies with npm ci (network access may be required)...'
rm -rf -- "$RUNTIME"
mkdir -p "$RUNTIMES"
mv -- "$STAGE" "$RUNTIME"
STAGE=$(mktemp -d "${TMPDIR:-/tmp}/facet-source.XXXXXX")
npm ci --prefix "$RUNTIME/dependencies/remotion-composer" --no-audit --no-fund
printf '{\n  "schema": 1,\n  "version": "%s",\n  "os": "%s",\n  "arch": "%s",\n  "components": ["remotion"],\n  "verified": false,\n  "archive_sha256": "",\n  "installed_at": "%s"\n}\n' \
    "$VERSION" "$OS" "$ARCH" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$RUNTIME/components.json"

PREVIOUS=''
[ ! -L "$FACET_HOME_DIR/current" ] || PREVIOUS=$(basename -- "$(readlink "$FACET_HOME_DIR/current")")
ln -sfn "$RUNTIME" "$FACET_HOME_DIR/current"
[ "$PREVIOUS" != "$NAME" ] || PREVIOUS=''
printf '{\n  "schema": 1,\n  "current": "%s",\n  "previous": "%s",\n  "updated_at": "%s"\n}\n' "$NAME" "$PREVIOUS" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$FACET_HOME_DIR/installer.json"

if [ "$NO_PATH" != 1 ]; then
    PROFILE="$HOME/.profile"
    if ! grep -qxF '# >>> facet path >>>' "$PROFILE" 2>/dev/null; then
        printf '\n# >>> facet path >>>\nexport PATH="$HOME/.facet/current/bin:$PATH"\n# <<< facet path <<<\n' >> "$PROFILE"
        printf 'Added ~/.facet/current/bin to PATH in %s; open a new terminal.\n' "$PROFILE"
    fi
fi
printf '\nActive runtime: %s\n' "$RUNTIME"
printf '%s\n' 'Next: facet doctor' '      facet wire <claude|codex|copilot|opencode|all> [--scope user|project]'
