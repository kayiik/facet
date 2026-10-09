#!/bin/sh
# Report Remotion composer sources in an installed Facet runtime that no
# longer match this repository.
#
# A runtime's composer is a COPY made when the release archive was packaged,
# and it is what the installed `facet` renders with. Installed composers have
# drifted months behind the repository before (a stale padding rule, a
# composer that ignored duration_seconds), so compare the bytes. The guidance
# (skills, packs, agents) is compiled into the facet binary itself; a CLI's
# copies of it are checked by `facet wire --status`.
#
# Exits non-zero when anything is missing, stale or left over, so this can gate
# a release rather than being something a person has to remember to look at.
#
# Usage: scripts/check-composer-current.sh [COMPOSER_DIR]
# An explicit argument wins, and naming a directory that does not exist is an
# error. Otherwise the active runtime's composer:
# <Facet home>/current/dependencies/remotion-composer, where the Facet home is
# $FACET_HOME or ~/.facet; only that default may be absent (nothing installed).
set -eu

if [ -n "${1:-}" ]; then
  if [ ! -d "$1" ]; then
    echo "FATAL: no composer at $1 (named explicitly)" >&2
    exit 1
  fi
  composer=$(cd "$1" && pwd)
else
  composer=${FACET_HOME:-$HOME/.facet}/current/dependencies/remotion-composer
  if [ ! -d "$composer" ]; then
    echo "no installed composer at $composer; nothing to compare"
    exit 0
  fi
fi
cd "$(dirname "$0")/../remotion-composer"
echo "comparing against: $composer"

# Everything a release ships of the composer: its source and manifests, never
# its installed node_modules.
list() (
  cd "$1"
  [ ! -d src ] || find src -type f ! -path '*/node_modules/*'
  for file in package.json package-lock.json tsconfig.json composer-manifest.json; do
    [ ! -f "$file" ] || echo "$file"
  done
)

repo_files=$(mktemp)
installed_files=$(mktemp)
trap 'rm -f "$repo_files" "$installed_files"' EXIT
list . | LC_ALL=C sort > "$repo_files"
list "$composer" | LC_ALL=C sort > "$installed_files"

status=0
while IFS= read -r rel; do
  if [ ! -f "$composer/$rel" ]; then
    echo "MISSING in the installed composer: $rel"
    status=1
  elif ! cmp -s "$rel" "$composer/$rel"; then
    echo "STALE in the installed composer: $rel"
    status=1
  fi
done < "$repo_files"
extra=$(LC_ALL=C comm -13 "$repo_files" "$installed_files")
if [ -n "$extra" ]; then
  printf '%s\n' "$extra" | sed 's/^/EXTRA in the installed composer: /'
  status=1
fi

if [ "$status" -eq 0 ]; then
  echo "installed composer matches the repository"
else
  echo
  echo "the installed facet renders with its own copy, not this repository."
  echo "refresh: package a release archive (scripts/package-release.py) and reinstall it with"
  echo "  install.sh --archive <zip> --checksums <file>, or install the matching published release."
fi
exit "$status"
