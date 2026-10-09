# Local UAT image for the Facet 2.0 CLI journey.
# The image build packages a release archive from this source and installs it
# with the product installer; scripts/uat-journey.mjs then drives the installed
# `facet` in a sealed network.
FROM golang:1.26.6-bookworm AS go
FROM node:24.14.0-bookworm
ARG NPM_REGISTRY=https://registry.npmjs.org
ARG FACET_VERSION=0.0.1
COPY --from=go /usr/local/go /usr/local/go
ENV npm_config_registry=${NPM_REGISTRY}
# ~/.facet/current/bin is what the installer puts on PATH; --no-path below leaves
# shell profiles untouched, so the image provides the same PATH directly.
ENV PATH="/home/facet/.facet/current/bin:/usr/local/go/bin:${PATH}" \
    GOPATH=/opt/go \
    GOCACHE=/opt/go/cache \
    FACET_UAT_VERSION=${FACET_VERSION} \
    REMOTION_BROWSER_EXECUTABLE=/usr/bin/chromium
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg chromium fonts-dejavu-core ca-certificates git bash curl unzip zip python3 \
    && rm -rf /var/lib/apt/lists/*
# The agentic CLI the journey wires Facet into.
RUN npm install --global opencode-ai@1.18.29 --registry="${NPM_REGISTRY}"
RUN useradd --create-home --uid 10001 facet
COPY scripts/uat-*.mjs scripts/probe-artifact.* /opt/uat/
COPY . /opt/facet-source
WORKDIR /opt/facet-source
# The release archive, built exactly as a release builds it: the single `facet`
# binary (-X main.Version=${FACET_VERSION}, from package.json), the composer
# sources under dependencies/remotion-composer, LICENSE and the generated
# third-party notices.
RUN set -eu; \
    arch="$(go env GOARCH)"; \
    python3 scripts/package-release.py --os linux --arch "$arch" --out /tmp/facet-release; \
    test -f "/tmp/facet-release/facet-${FACET_VERSION}-linux-${arch}.zip"; \
    chmod -R a+rX /tmp/facet-release
USER facet
WORKDIR /home/facet
# The product installer, noninteractive, with the Remotion component and no PATH
# or profile edits. Its log is journey evidence.
RUN arch="$(dpkg --print-architecture)"; \
    bash /opt/facet-source/install.sh --yes --components remotion --no-path \
      --archive "/tmp/facet-release/facet-${FACET_VERSION}-linux-${arch}.zip" \
      --checksums "/tmp/facet-release/checksums-linux-${arch}.txt" \
      > /home/facet/uat-install-build.log 2>&1 \
    || { cat /home/facet/uat-install-build.log; cat /home/facet/.facet/logs/*.log 2>/dev/null; exit 1; }
RUN mkdir -p /home/facet/uat-evidence/journey
ENTRYPOINT ["node", "/opt/uat/uat-entrypoint.mjs"]
