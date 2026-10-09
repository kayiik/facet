#!/usr/bin/env bash
# WSL-only validation adapter: native Linux payload, Windows FFmpeg version
# probes, media checks explicitly skipped. Not Linux render acceptance.
set -euo pipefail
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
ffmpeg=$(command -v ffmpeg.exe)
ffprobe=$(command -v ffprobe.exe)
printf '#!/usr/bin/env bash\nexec %q "$@"\n' "$ffmpeg" > "$root/ffmpeg"
printf '#!/usr/bin/env bash\nexec %q "$@"\n' "$ffprobe" > "$root/ffprobe"
chmod +x "$root/ffmpeg" "$root/ffprobe"
export PATH="$root:$PATH"
export FACET_INSTALL_SMOKE=1 FACET_INSTALL_SKIP_MEDIA=1
python3 scripts/test-prebuilt-install.py --os linux --arch amd64 --release-dir "$1" --updated-installer
