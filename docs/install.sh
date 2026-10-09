#!/bin/sh
# Website bootstrap. Installation policy remains in the release's install.sh.
# The function is parsed in full before executing, so a piped script cannot be
# mistaken for prompt input. Interactive input is explicitly taken from the TTY.
facet_bootstrap() (
    set -eu
    # Pinned to the last published installer; the pin moves only when a
    # release is published, with that installer's checksum. The expected file
    # list below is the pinned package's layout.
    version=0.0.1
    expected=ee3ceddbd50847bac8b16ae65b4d8e3677deb76933baaa86091945567fc65f09
    url="https://github.com/kayiik/facet/releases/download/v$version/facet-installer-$version.zip"
    case "$(uname -s)" in Linux|Darwin) ;; *) printf '%s\n' 'Use the PowerShell command on Windows.' >&2; exit 1;; esac
    noninteractive=0
    for argument in "$@"; do [ "$argument" != --yes ] || noninteractive=1; done
    [ "${FACET_YES:-0}" != 1 ] || noninteractive=1
    if [ "$noninteractive" = 0 ]; then
        if ! ( : </dev/tty ) 2>/dev/null; then
            printf '%s\n' 'Interactive setup needs a terminal. For automation, pass --yes (with --components and --wire as needed), or set FACET_YES=1.' >&2
            exit 1
        fi
        exec 3</dev/tty
    fi
    missing=''
    for command in bash curl unzip zipinfo; do command -v "$command" >/dev/null || missing="$missing $command"; done
    if ! command -v shasum >/dev/null && ! command -v sha256sum >/dev/null; then missing="$missing checksum-utility"; fi
    if [ -n "$missing" ]; then
        printf 'Missing download utilities:%s\n' "$missing"
        if [ "$noninteractive" = 0 ]; then
            printf 'Install required download utilities using the system package manager? [Y/n]: '
            read -r answer <&3
            case "$answer" in ''|y|Y|yes) ;; *) exit 1;; esac
        fi
        if command -v apt-get >/dev/null; then
            if [ "$(id -u)" = 0 ]; then apt-get update && apt-get install -y --no-install-recommends bash curl unzip perl ca-certificates;
            else sudo apt-get update && sudo apt-get install -y --no-install-recommends bash curl unzip perl ca-certificates; fi
        else
            printf '%s\n' 'Install bash, curl, unzip and a SHA-256 utility with your system package manager, then rerun.' >&2; exit 1
        fi
    fi
    temp=$(mktemp -d "${TMPDIR:-/tmp}/facet-bootstrap.XXXXXX")
    trap 'rm -rf -- "$temp"' EXIT
    printf 'Downloading Facet %s installer…\n' "$version"
    curl --fail --silent --show-error --location --retry 3 --retry-delay 2 --connect-timeout 20 --proto '=https' --tlsv1.2 --max-time 180 --max-filesize 1048576 "$url" -o "$temp/installer.zip"
    if command -v shasum >/dev/null; then actual=$(shasum -a 256 "$temp/installer.zip"); else actual=$(sha256sum "$temp/installer.zip"); fi
    [ "${actual%% *}" = "$expected" ] || { printf '%s\n' 'Installer checksum mismatch; nothing executed.' >&2; exit 1; }
    # Accept only the small, known installer package, never arbitrary ZIP paths.
    printf '%s\n' install.ps1 install.sh installer/manifest.tsv installer/verify.html | LC_ALL=C sort > "$temp/expected"
    zipinfo -1 "$temp/installer.zip" | LC_ALL=C sort > "$temp/actual"
    cmp -s "$temp/expected" "$temp/actual" || { printf '%s\n' 'Unexpected installer package layout.' >&2; exit 1; }
    mkdir "$temp/package"
    unzip -q "$temp/installer.zip" -d "$temp/package"
    # install.sh reads FACET_* variables itself, so options set in the
    # environment reach it even when this bootstrap arrives through a pipe.
    if [ "$noninteractive" = 1 ]; then
        bash "$temp/package/install.sh" "$@" </dev/null
    else
        bash "$temp/package/install.sh" "$@" <&3
    fi
)
facet_bootstrap "$@"
