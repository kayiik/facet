#!/bin/sh
# Smoke test for installer/install.sh against two local releases built by
# scripts/build-release.py: install core, add the App, verify, refuse a plain
# install of a newer release, upgrade, refuse to remove a changed file, and
# uninstall without a trace. Runs with HOME set to a fresh temporary folder.
#
#   sh scripts/smoke-installer.sh RELEASE_DIR NEXT_DIR
set -eu
release=$1
next=$2
here=$(CDPATH= cd -P "$(dirname "$0")" && pwd -P)
installer=$here/../installer/install.sh
version=$(awk -F '\t' '$1=="version" {print $2}' "$release/manifest.tsv")
next_version=$(awk -F '\t' '$1=="version" {print $2}' "$next/manifest.tsv")
# The installer refuses linked paths, and macOS's temporary folder is under
# the /var link: use the physical path.
HOME=$(CDPATH= cd -P "$(mktemp -d "${TMPDIR:-/tmp}/facet-smoke-home.XXXXXXXX")" && pwd -P)
export HOME
root=$HOME/.local/lib/facet
case "$(uname -s)" in
    Darwin) start=$HOME/Applications/Facet.app/Contents/MacOS/Facet ;;
    *) start=$HOME/.local/share/applications/facet.desktop ;;
esac

fail() { printf 'smoke-installer: %s\n' "$*" >&2; exit 1; }
install_facet() { sh "$installer" --components none --no-launch "$@"; }
expect_failure() {
    want=$1; shift
    if output=$(sh "$installer" --components none --no-launch "$@" 2>&1); then
        fail "install.sh $* should have failed with '$want'"
    fi
    case "$output" in *"$want"*) ;; *) fail "install.sh $* failed differently: $output" ;; esac
}

install_facet --mode core --distribution-dir "$release"
[ "$("$HOME/.local/bin/facet" version)" = "facet v$version" ] || fail "facet version through ~/.local/bin"
[ ! -e "$start" ] || fail "core mode made a Start entry"

install_facet --mode app --distribution-dir "$release"
[ "$("$HOME/.local/bin/facet-app" version)" = "facet-app $version" ] || fail "facet-app version"
[ -x "$root/kernel/compa-kernel" ] || fail "no kernel"
[ -e "$start" ] || fail "app mode made no Start entry"
install_facet --verify

plan=$(sh "$installer" --upgrade --distribution-dir "$next" --dry-run)
case "$plan" in *"Dry-run: upgrade Facet $next_version (core, app)"*) ;; *) fail "dry run: $plan" ;; esac

expect_failure "Run with --upgrade" --distribution-dir "$next"
install_facet --upgrade --distribution-dir "$next"
[ "$("$HOME/.local/bin/facet" version)" = "facet v$next_version" ] || fail "upgraded facet version"
install_facet --verify

cp "$root/LICENSE" "$HOME/license.saved"
printf 'changed\n' >> "$root/LICENSE"
expect_failure "Preserving a modified or missing owned file" --uninstall
cp "$HOME/license.saved" "$root/LICENSE"
rm "$HOME/license.saved"

install_facet --uninstall
[ ! -e "$root" ] || fail "uninstall left $root"
[ ! -e "$HOME/.local/bin/facet" ] && [ ! -L "$HOME/.local/bin/facet" ] || fail "uninstall left the facet link"
[ ! -e "$start" ] || fail "uninstall left the Start entry"
rm -rf "$HOME"
printf 'install.sh: core, App, verify, upgrade %s -> %s and uninstall passed\n' "$version" "$next_version"
