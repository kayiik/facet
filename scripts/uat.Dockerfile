# The end-to-end test of an installed facet; scripts/uat.py builds and runs
# it. A clean Ubuntu with one person's account, where the release's own
# install.sh installs facet in bundle mode, wired into OpenCode, with the
# default components. scripts/uat-journey.mjs then runs with no network.
FROM ubuntu:24.04

# What a desktop Linux has and a bare container lacks: CA certificates for the
# component downloads, and the libraries and fonts the headless browser draws
# with. No FFmpeg, Node or browser: facet installs its own. curl fetches
# OpenCode below.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl fonts-dejavu-core \
      libasound2t64 libatk-bridge2.0-0t64 libatk1.0-0t64 libcairo2 libcups2t64 libdbus-1-3 libgbm1 \
      libnspr4 libnss3 libpango-1.0-0 libxcomposite1 libxdamage1 libxfixes3 libxkbcommon0 libxrandr2 \
 && rm -rf /var/lib/apt/lists/*

# OpenCode, the agentic CLI facet is wired into, pinned with its SHA-256.
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
      amd64) asset=opencode-linux-x64.tar.gz; sum=7bb9e61ad6feacf2e89c10a6a2c1840e3de4220bb50ba1fc7110f31806076aec ;; \
      arm64) asset=opencode-linux-arm64.tar.gz; sum=e9841d43d3e1a8f36ed2691592d3b8b4fb097e606c8b559a277a1b845840e26b ;; \
      *) echo "no OpenCode build for $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/opencode.tar.gz "https://github.com/anomalyco/opencode/releases/download/v1.19.0/$asset"; \
    echo "$sum  /tmp/opencode.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin opencode; \
    rm /tmp/opencode.tar.gz

RUN useradd --create-home --uid 10001 facet
COPY release/ /release/
COPY uat/ /opt/uat/
USER facet
WORKDIR /home/facet
ENV PATH=/home/facet/.local/bin:$PATH \
    OPENCODE_DISABLE_AUTOUPDATE=true \
    OPENCODE_DISABLE_MODELS_FETCH=true

# The release's own installer: facet for this person, wired into OpenCode,
# with the default components. Its log is evidence.
RUN sh /release/install.sh --mode bundle --harness opencode --components defaults --distribution-dir /release \
      > /home/facet/uat-install.log 2>&1 \
 || { cat /home/facet/uat-install.log; exit 1; }
RUN mkdir /home/facet/uat-evidence

# The journey runs on the Node that facet installed.
CMD ["/home/facet/.local/lib/facet/dependencies/node/bin/node", "/opt/uat/uat-journey.mjs", "/home/facet/uat-evidence"]
