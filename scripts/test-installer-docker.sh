#!/usr/bin/env bash
# Run inside an ephemeral Ubuntu container with /source mounted read-only.
# Installs a release archive for an unprivileged user with every optional
# component, verifies real renders, then runs the lifecycle suite.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --no-install-recommends ca-certificates curl unzip perl sudo python3
useradd --create-home --shell /bin/bash tester
printf 'tester ALL=(ALL) NOPASSWD: ALL\n' > /etc/sudoers.d/facet-test
mkdir /work
chown tester:tester /work
version=$(awk -F '\t' '$2=="facet" {print $3}' /source/installer/manifest.tsv)
sudo -H -u tester env FACET_DOCKER_VERSION="$version" FACET_DOCKER_RELEASE_DIR="${FACET_DOCKER_RELEASE_DIR:-/source/build/script-installer-linux}" bash -c '
    set -euo pipefail
    cd /work
    release_dir=${FACET_DOCKER_RELEASE_DIR:-/source/build/script-installer-linux}
    version=$FACET_DOCKER_VERSION
    unzip -q "$release_dir/facet-installer-$version.zip" -d installer
    archive="$release_dir/facet-$version-linux-amd64.zip"
    sums="$release_dir/checksums-linux-amd64.txt"
    bash installer/install.sh --yes --no-path --archive "$archive" --checksums "$sums" \
        --components remotion,piper,hyperframes
    "$HOME/.facet/current/bin/facet" version
    "$HOME/.facet/current/bin/facet" doctor
    # A repeat must reuse the verified runtime without reinstalling packages.
    bash installer/install.sh --yes --no-path --archive "$archive" --checksums "$sums" --components none | grep -i reusing
    export PATH="$HOME/.facet/current/dependencies/node/bin:$PATH"
    FACET_INSTALL_SMOKE=1 python3 /source/scripts/test-prebuilt-install.py --os linux --arch amd64 \
        --release-dir "$release_dir"
'
