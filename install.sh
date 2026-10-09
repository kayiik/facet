#!/usr/bin/env bash
# Facet installer for Linux and macOS.
#
# Installs one Facet runtime per user under ~/.facet/runtimes/<version>-<os>-<arch>,
# verifies it, activates it through the ~/.facet/current symbolic link and adds
# ~/.facet/current/bin to the user PATH. FACET_HOME, when set, replaces ~/.facet.
# Wiring Facet into agentic CLIs is a separate, explicit step (`facet wire`),
# offered at the end; wirings already recorded are refreshed to the active
# runtime after every install, update and rollback. Nothing here edits project
# files or CLI instruction files.
#
# A release ships a prebuilt facet for each platform. With --from-source (Facet
# 2.2.0 and later), facet is built here instead, from the release's source
# archive, by the official Go toolchain pinned with its SHA-256 in
# installer/manifest.tsv; Go and its build cache are removed afterwards.
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$SCRIPT_DIR/installer/manifest.tsv"
die() { printf '%s\n' "$*" >&2; exit 1; }
definition() {
    awk -F '\t' -v id="$1" -v col="$2" '$2==id {sub(/\r$/, "", $NF); print $col; n++} END {if(n!=1)exit 1}' "$MANIFEST"
}
[[ -f "$MANIFEST" ]] || die 'Run install.sh from the complete installer package.'
[[ $(definition layout 3) == 2 ]] || die 'Unsupported installer manifest schema.'

usage() {
    cat <<'EOF'
Usage: bash install.sh [options]

  --action install|update|rollback|uninstall   Default: install
  --version VERSION                            Release to install or roll back to
  --components remotion,piper,hyperframes|all|none   Default: remotion,piper
  --wire claude,codex,copilot,opencode,compa|all|none  Wire CLIs after installing
  --scope user|project                         Wiring scope (default: user)
  --project DIR                                Project to wire (implies --scope project)
  --archive ZIP --checksums FILE               Install from local release files
  --from-source                                Build facet here with the pinned Go toolchain
  --toolchain FILE                             The Go download to build with (implies --from-source)
  --yes                                        Noninteractive
  --no-path                                    Do not add ~/.facet/current/bin to PATH
  --skip-verify                                Skip media verification (reported as unverified)
  --purge                                      With uninstall: also delete installed runtimes
  --plain                                      Plain prompts
  --verbose                                    Show subprocess output
EOF
}

VERSION=${FACET_VERSION:-}; ACTION=${FACET_ACTION:-}; COMPONENTS=${FACET_COMPONENTS:-}
WIRE=${FACET_WIRE:-}; SCOPE=${FACET_SCOPE:-}; PROJECT=${FACET_PROJECT:-}
ARCHIVE=''; SUMS=''; TOOLCHAIN=''; FROM_SOURCE=${FACET_FROM_SOURCE:-0}
YES=${FACET_YES:-0}; NO_PATH=${FACET_NO_PATH:-0}; SKIP_VERIFY=${FACET_SKIP_VERIFY:-0}
PURGE=${FACET_PURGE:-0}; PLAIN=${FACET_PLAIN:-0}; DETAIL=${FACET_VERBOSE:-0}
while (($#)); do
    case "$1" in
        --action|--version|--components|--wire|--scope|--project|--archive|--checksums|--toolchain)
            (($# >= 2)) || die "Missing value for $1"
            case "$1" in
                --action) ACTION=$2;; --version) VERSION=$2;; --components) COMPONENTS=$2;;
                --wire) WIRE=$2;; --scope) SCOPE=$2;; --project) PROJECT=$2;;
                --archive) ARCHIVE=$2;; --checksums) SUMS=$2;; --toolchain) TOOLCHAIN=$2;;
            esac; shift 2;;
        --from-source) FROM_SOURCE=1; shift;;
        --yes) YES=1; shift;;
        --no-path) NO_PATH=1; shift;;
        --skip-verify) SKIP_VERIFY=1; shift;;
        --purge) PURGE=1; shift;;
        --plain) PLAIN=1; shift;;
        --verbose) DETAIL=1; shift;;
        --help|-h) usage; exit 0;;
        *) die "Unknown option: $1 (see --help)";;
    esac
done
VERSION_EXPLICIT=0; [[ -z "$VERSION" ]] || VERSION_EXPLICIT=1
VERSION=${VERSION:-$(definition facet 3)}
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || die 'Invalid release version.'
ACTION_EXPLICIT=0; [[ -z "$ACTION" ]] || ACTION_EXPLICIT=1
ACTION=${ACTION:-install}
case "$ACTION" in install|update|rollback|uninstall) ;; *) die 'Choose --action install, update, rollback or uninstall.';; esac
[[ -z "$ARCHIVE" && -z "$SUMS" || -n "$ARCHIVE" && -n "$SUMS" ]] || die 'Supply both --archive and --checksums.'
[[ -z "$PROJECT" ]] || SCOPE=project
SCOPE=${SCOPE:-user}
case "$SCOPE" in user|project) ;; *) die 'Choose --scope user or project.';; esac
[[ "$SCOPE" != project || -n "$PROJECT" || $YES != 1 ]] || die '--scope project requires --project DIR.'
case $(uname -s) in Linux) OS=linux;; Darwin) OS=darwin;; *) die 'Use install.ps1 on Windows.';; esac
case $(uname -m) in x86_64|amd64) ARCH=amd64;; arm64|aarch64) ARCH=arm64;; *) die 'Unsupported architecture.';; esac
absolute() { case "$1" in /*) printf '%s' "$1";; *) printf '%s/%s' "$PWD" "$1";; esac; }
if [[ -n "$ARCHIVE" ]]; then ARCHIVE=$(absolute "$ARCHIVE"); SUMS=$(absolute "$SUMS"); fi
if [[ -n "$TOOLCHAIN" ]]; then TOOLCHAIN=$(absolute "$TOOLCHAIN"); FROM_SOURCE=1; fi
[[ "$FROM_SOURCE" == 0 || "$FROM_SOURCE" == 1 ]] || die 'FACET_FROM_SOURCE must be 0 or 1.'
if [[ -n "$PROJECT" ]]; then PROJECT=$(absolute "$PROJECT"); fi

# FACET_HOME moves Facet's home folder (runtimes, current, wiring record). It is
# made absolute once and handed to every facet this script runs, so they agree.
if [[ -n "${FACET_HOME:-}" ]]; then
    FACET_HOME_DIR=$(absolute "$FACET_HOME"); export FACET_HOME="$FACET_HOME_DIR"
else
    FACET_HOME_DIR="$HOME/.facet"
fi
# Facet 1.x kept its releases in ~/.facet whatever FACET_HOME says.
LEGACY_HOME="$HOME/.facet"
RUNTIMES="$FACET_HOME_DIR/runtimes"
CURRENT="$FACET_HOME_DIR/current"
STATE_FILE="$FACET_HOME_DIR/installer.json"
WIRING_FILE="$FACET_HOME_DIR/wiring.json"
PROFILE_START='# >>> facet path >>>'
PROFILE_END='# <<< facet path <<<'
SUPPORTED_CLIS='claude codex copilot opencode compa'

ask() {
    local answer
    printf '%s [%s]: ' "$1" "$2" >&2
    IFS= read -r answer || die 'Interactive input ended; use --yes with explicit options.'
    printf '%s' "${answer:-$2}"
}
RICH=0
if [[ $YES != 1 && $PLAIN != 1 && -z ${NO_COLOR:-} && -t 0 && -t 2 && ${TERM:-dumb} != dumb ]]; then RICH=1; fi
section() { if [[ $RICH == 1 ]]; then printf '\n\033[36;1m%s\033[0m\n' "$1"; else printf '\n%s\n' "$1"; fi; }
# Menu drawing goes to stderr; stdout carries only the selected value.
# Plain mode prints numbered labels and maps numeric answers to values.
choose() (
    title=$1; multiple=$2; defaults=$3; shift 3
    values=(); labels=(); checked=(); index=0
    while (($#)); do values+=("$1"); labels+=("$2"); shift 2; done
    if [[ $RICH != 1 ]]; then
        for ((i=0;i<${#values[@]};i++)); do printf '  %d. %s\n' "$((i+1))" "${labels[i]}" >&2; done
        answer=$(ask "$title" "$defaults")
        result=''
        IFS=',' read -r -a picks <<< "$answer"
        for pick in "${picks[@]:-}"; do
            pick=${pick// /}; [[ -n "$pick" ]] || continue
            if [[ "$pick" =~ ^[0-9]+$ ]]; then
                ((pick >= 1 && pick <= ${#values[@]})) || { printf 'No choice %s.\n' "$pick" >&2; exit 1; }
                pick=${values[pick-1]}
            fi
            result="${result:+$result,}$pick"
        done
        printf '%s' "${result:-none}"; exit 0
    fi
    for ((i=0;i<${#values[@]};i++)); do
        checked+=(0)
        if [[ ",$defaults," == *",${values[i]},"* ]]; then checked[i]=1; [[ $multiple == 1 ]] || index=$i; fi
    done
    old_tty=$(stty -g)
    trap 'stty "$old_tty"; printf "\033[?25h" >&2' EXIT
    trap 'exit 130' INT TERM
    stty -echo -icanon min 1 time 0
    printf '\n\033[36;1m? %s\033[0m\n\033[?25l' "$title" >&2
    first=1
    while :; do
        if [[ $first == 0 ]]; then printf '\033[%dA' "$((${#values[@]}+1))" >&2; fi
        first=0
        width=$(tput cols 2>/dev/null || printf 80); ((width>20)) || width=80
        for ((i=0;i<${#values[@]};i++)); do
            pointer=' '; mark='( )'; color='\033[0m'
            if [[ $multiple == 1 ]]; then mark='[ ]'; [[ ${checked[i]} == 0 ]] || mark='[x]'; fi
            if [[ $i == "$index" ]]; then pointer='>'; color='\033[32m'; [[ $multiple == 1 ]] || mark='(*)'; fi
            line="  $pointer $mark ${labels[i]}"
            printf '\033[2K%b%s\033[0m\n' "$color" "${line:0:$((width-2))}" >&2
        done
        if [[ $multiple == 1 ]]; then hint='Up/Down: move | Space: toggle | Enter: confirm | Esc: cancel'; else hint='Up/Down: move | Enter: confirm | Esc: cancel'; fi
        printf '\033[2K\033[90m%s\033[0m\n' "${hint:0:$((width-2))}" >&2
        IFS= read -rsn1 key || exit 1
        case "$key" in
            $'\033')
                # macOS ships Bash 3.2, whose read timeout must be an integer.
                suffix=''; IFS= read -rsn2 -t 1 suffix || true
                case "$suffix" in '[A') index=$(((index+${#values[@]}-1)%${#values[@]}));; '[B') index=$(((index+1)%${#values[@]}));; *) printf '\nInstallation cancelled.\n' >&2; exit 1;; esac;;
            ' ') if [[ $multiple == 1 ]]; then checked[index]=$((1-checked[index])); fi;;
            '')
                result=''
                for ((i=0;i<${#values[@]};i++)); do
                    if [[ $multiple == 1 && ${checked[i]} == 1 || $multiple == 0 && $i == "$index" ]]; then result="${result:+$result,}${values[i]}"; fi
                done
                result=${result:-none}
                printf '\033[%dA' "$((${#values[@]}+1))" >&2
                for ((i=0;i<=${#values[@]};i++)); do printf '\033[2K\n' >&2; done
                printf '\033[%dA\033[32m  Selected: %s\033[0m\n' "$((${#values[@]}+1))" "$result" >&2
                printf '%s' "$result"; exit 0;;
        esac
    done
)

# ------------------------------------------------------------------ helpers
if command -v shasum >/dev/null; then SHA_CMD=(shasum -a 256)
elif command -v sha256sum >/dev/null; then SHA_CMD=(sha256sum)
else die 'Install shasum or sha256sum and rerun.'; fi
hash_file() { "${SHA_CMD[@]}" "$1" | awk '{print $1}'; }
LOG_DIR=${FACET_LOG_DIR:-$FACET_HOME_DIR/logs}
LOG=''
TEMP=''; STAGE=''; NEW_RUNTIME=''; COMMITTED=0; ASIDE=''; ASIDE_TARGET=''
cleanup() {
    rc=$?
    [[ -z "$STAGE" ]] || rm -rf -- "$STAGE"
    if [[ $COMMITTED == 0 && -n "$NEW_RUNTIME" ]]; then rm -rf -- "$NEW_RUNTIME"; fi
    if [[ -n "$ASIDE" ]]; then
        # A replaced runtime is restored unless its verified replacement was activated.
        if [[ $COMMITTED == 0 && ! -e "$ASIDE_TARGET" ]]; then mv -- "$ASIDE" "$ASIDE_TARGET"; else rm -rf -- "$ASIDE"; fi
    fi
    [[ -z "$TEMP" ]] || rm -rf -- "$TEMP"
    if [[ $rc != 0 && -n "$LOG" ]]; then printf '\nSetup incomplete; the active runtime was not changed. Log: %s\n' "$LOG" >&2; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
start_log() {
    mkdir -p "$LOG_DIR"
    LOG="$LOG_DIR/install-$(date +%Y%m%d-%H%M%S)-$$.log"
    TEMP=$(mktemp -d)
}
redact() { sed -E 's#(https?://)[^/@[:space:]]+@#\1<redacted>@#g; s#([?&][^=[:space:]&]+)=[^&[:space:]]+#\1=<redacted>#g'; }
step() {
    local label=$1 rc; shift
    printf '  -> %s\n' "$label"
    set +e
    if [[ $DETAIL == 1 ]]; then (set -e; "$@") 2>&1 | redact | tee -a "$LOG"; rc=${PIPESTATUS[0]}; else (set -e; "$@") 2>&1 | redact >> "$LOG"; rc=${PIPESTATUS[0]}; fi
    set -e
    if [[ $rc != 0 ]]; then printf '  FAILED %s (see %s)\n' "$label" "$LOG" >&2; return "$rc"; fi
    printf '  OK %s\n' "$label"
}
fetch() { curl --fail --silent --show-error --location --retry 3 --retry-delay 2 --connect-timeout 20 --proto '=https' --tlsv1.2 --max-time 900 --output "$2" "$1"; }
verify_checksum() {
    local expected
    expected=$(awk -v name="$3" '{sub(/\r$/, "")} $2==name || $2=="*"name {print tolower($1);n++} END{if(n!=1)exit 1}' "$2") || die "Checksum entry missing or duplicated: $3"
    [[ "$expected" =~ ^[a-f0-9]{64}$ && $(hash_file "$1") == "$expected" ]] || die "Checksum mismatch: $3"
}
safe_name() {
    local name=$1 part
    while [[ "$name" == ./* ]]; do name=${name#./}; done
    [[ -n "$name" ]] || return 0
    [[ "$name" != /* && "$name" != *\\* && "$name" != *:* && "$name" != *$'\r'* && "$name" != *$'\t'* ]] || die "Unsafe archive path: $name"
    local parts
    IFS='/' read -r -a parts <<< "$name"
    for part in "${parts[@]}"; do [[ "$part" != .. && "$part" != . && -n "$part" && "$part" != *'.' && "$part" != *' ' ]] || die "Unsafe archive path: $name"; done
}
expand_zip() {
    local archive=$1 dest=$2 name
    zipinfo -1 "$archive" > "$TEMP/zip-names"
    while IFS= read -r name; do safe_name "${name%/}"; done < "$TEMP/zip-names"
    [[ $(sort -f "$TEMP/zip-names" | uniq -di | wc -l | tr -d ' ') == 0 ]] || die 'Duplicate archive entries.'
    zipinfo -l "$archive" > "$TEMP/zip-modes"
    awk 'length($1)==10 && $2 ~ /^[0-9]/ {if(substr($1,1,1)!="-" && substr($1,1,1)!="d")bad=1;total+=$4} END {if(bad || total>1073741824)exit 1}' "$TEMP/zip-modes" || die 'Archive links/special files or excessive size.'
    unzip -q "$archive" -d "$dest"
    [[ -z $(find "$dest" -type l -print) ]] || die 'Archive contains links.'
}
json_string() { printf '"%s"' "$(printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g')"; }
utc_now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# ------------------------------------------------------------ runtime state
runtime_dir() { printf '%s/%s' "$RUNTIMES" "$1"; }
read_state() { [[ -f "$STATE_FILE" ]] || return 0; awk -F '"' -v key="$1" '$2==key {print $4; exit}' "$STATE_FILE"; }
write_state() {
    mkdir -p "$FACET_HOME_DIR"
    printf '{\n  "schema": 1,\n  "current": %s,\n  "previous": %s,\n  "updated_at": %s\n}\n' \
        "$(json_string "$1")" "$(json_string "$2")" "$(json_string "$(utc_now)")" > "$STATE_FILE.tmp"
    mv -f "$STATE_FILE.tmp" "$STATE_FILE"
}
active_runtime() {
    [[ -L "$CURRENT" ]] || return 0
    basename -- "$(readlink "$CURRENT")"
}
runtime_record_ok() { [[ -f "$(runtime_dir "$1")/components.json" && -x "$(runtime_dir "$1")/bin/facet" ]]; }
recorded_components() { [[ -f "$1/components.json" ]] || return 0; tr -d '\n' < "$1/components.json" | sed -n 's/.*"components": *\[\([^]]*\)\].*/\1/p' | tr -d ' "' ; }
runtime_intact() {
    local dir=$1
    [[ -f "$dir/.facet-files.sha256" && -f "$dir/components.json" && ! -L "$dir/bin/facet" ]] || return 1
    (cd "$dir" && "${SHA_CMD[@]}" -c .facet-files.sha256) >/dev/null 2>&1
}
activate() {
    local target=$1 tmp
    mkdir -p "$FACET_HOME_DIR"
    [[ ! -e "$CURRENT" || -L "$CURRENT" ]] || die "$CURRENT exists and is not a link created by the Facet installer; it was left in place."
    tmp="$FACET_HOME_DIR/.current-$$"
    rm -f -- "$tmp"
    ln -s "$(runtime_dir "$target")" "$tmp"
    if mv -T "$tmp" "$CURRENT" 2>/dev/null; then :; else rm -f -- "$tmp"; ln -sfn "$(runtime_dir "$target")" "$CURRENT"; fi
}
v1_notice() {
    local d
    for d in "$LEGACY_HOME"/releases/1.*-*; do
        if [[ -d "$d" ]]; then
            printf '%s\n' 'Facet v1 project integrations are separate; remove them with the v1.1.0 installer --action uninstall in each project.'
            return 0
        fi
    done
}
prune_runtimes() {
    # One earlier runtime is kept for rollback; older ones are removed.
    local keep=$1 previous=$2 dir name
    for dir in "$RUNTIMES"/*; do
        [[ -d "$dir" && ! -L "$dir" ]] || continue
        name=$(basename -- "$dir")
        [[ "$name" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?-(linux|darwin)-(amd64|arm64)$ ]] || continue
        [[ "$name" != "$keep" && "$name" != "$previous" ]] || continue
        if rm -rf -- "$dir"; then printf '  OK Removed older runtime %s\n' "$name"; else printf '  Older runtime %s could not be removed and was kept.\n' "$name"; fi
    done
}

# --------------------------------------------------------------- PATH block
profile_files() {
    printf '%s\n' "$HOME/.profile"
    [[ ! -f "$HOME/.bashrc" && "${SHELL:-}" != */bash ]] || printf '%s\n' "$HOME/.bashrc"
    [[ ! -f "$HOME/.zshrc" && "${SHELL:-}" != */zsh ]] || printf '%s\n' "$HOME/.zshrc"
}
remove_profile_block() {
    local file=$1
    [[ -f "$file" ]] || return 0
    grep -qxF "$PROFILE_START" "$file" || return 0
    awk -v s="$PROFILE_START" -v e="$PROFILE_END" '$0==s {skip=1; next} $0==e && skip {skip=0; next} !skip {print}' "$file" > "$file.facet-tmp"
    cat "$file.facet-tmp" > "$file"; rm -f -- "$file.facet-tmp"
}
add_path() {
    local file changed=0 line
    # The default home is written as $HOME so the profile line stays portable;
    # a FACET_HOME is written as the absolute path it resolved to.
    line='export PATH="$HOME/.facet/current/bin:$PATH"'
    if [[ "$FACET_HOME_DIR" != "$HOME/.facet" ]]; then
        line="export PATH=\"$(printf '%s' "$CURRENT/bin" | sed 's/[\\"$`]/\\&/g'):\$PATH\""
    fi
    while IFS= read -r file; do
        [[ ! -L "$file" ]] || { printf '  Skipped linked profile %s\n' "$file"; continue; }
        if [[ -f "$file" ]] && grep -qxF "$PROFILE_START" "$file"; then continue; fi
        printf '\n%s\n%s\n%s\n' "$PROFILE_START" "$line" "$PROFILE_END" >> "$file"
        printf '  OK Added %s to PATH in %s\n' "$CURRENT/bin" "$file"; changed=1
    done < <(profile_files)
    [[ $changed == 0 ]] || printf '%s\n' '  Open a new terminal to use the facet command.'
}
remove_path() {
    local file
    for file in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
        [[ -L "$file" ]] || remove_profile_block "$file"
    done
}

# ----------------------------------------------------------------- wiring
run_wire() {
    local exe=$1; shift
    printf '  -> facet wire %s\n' "$*"
    "$exe" wire "$@"
}
wire_scope_args() {
    if [[ "$1" == project ]]; then printf '%s\n' --scope project --project "$2"; else printf '%s\n' --scope user; fi
}
recorded_wire_groups() {
    # Prints "scope<TAB>project" per recorded group from facet wire's registry.
    [[ -f "$WIRING_FILE" ]] || return 0
    if command -v python3 >/dev/null; then
        python3 - "$WIRING_FILE" <<'PY'
import json, sys
seen = set()
for w in json.load(open(sys.argv[1], encoding="utf-8")).get("wirings") or []:
    key = (w.get("scope") or "user", w.get("project") or "")
    if key not in seen:
        seen.add(key); print("%s\t%s" % key)
PY
    elif command -v node >/dev/null; then
        node -e 'const s=new Set();for(const w of (JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).wirings||[])){const k=(w.scope||"user")+"\t"+(w.project||"");if(!s.has(k)){s.add(k);console.log(k)}}' "$WIRING_FILE"
    else
        printf 'user\t\n'
    fi
}
refresh_wirings() {
    # facet wire registers current/bin/facet, so every wiring follows the
    # active runtime; only the skills it copied can be older. Refreshing them
    # keeps each CLI's Facet guidance at the active runtime's version. Files a
    # person changed are never overwritten: facet wire reports them.
    [[ -f "$WIRING_FILE" ]] || return 0
    section 'Refresh wirings'
    run_wire "$CURRENT/bin/facet" --refresh
}

# --------------------------------------------------------- system packages
install_linux_node() {
    local deps=$1 version arch stem base name nd entry
    version=$(definition node 3); arch=x64; [[ "$ARCH" != arm64 ]] || arch=arm64
    stem="node-v$version-linux-$arch"; name="$stem.tar.gz"; base="https://nodejs.org/dist/v$version"
    printf '  -> Download official Node.js %s into %s/node\n' "$version" "$deps"
    fetch "$base/$name" "$TEMP/$name"; fetch "$base/SHASUMS256.txt" "$TEMP/node-sums"
    verify_checksum "$TEMP/$name" "$TEMP/node-sums" "$name"
    [[ ! -e "$deps/node" ]] || rm -rf -- "$deps/node"
    tar -tzf "$TEMP/$name" > "$TEMP/node-names"
    while IFS= read -r entry; do safe_name "${entry%/}"; [[ "$entry" == "$stem/"* ]] || die 'Unexpected Node archive root.'; done < "$TEMP/node-names"
    # Skip npm/npx/corepack links and replace npm/npx with explicit launchers.
    tar -tvzf "$TEMP/$name" --exclude="$stem/bin/npm" --exclude="$stem/bin/npx" --exclude="$stem/bin/corepack" > "$TEMP/node-modes"
    awk 'substr($1,1,1)!="-" && substr($1,1,1)!="d" {bad=1} END {exit bad}' "$TEMP/node-modes" || die 'Unexpected links/special files in Node runtime.'
    nd="$TEMP/node-unpack"; mkdir -p "$nd"
    tar -xzf "$TEMP/$name" -C "$nd" --exclude="$stem/bin/npm" --exclude="$stem/bin/npx" --exclude="$stem/bin/corepack"
    for entry in npm npx; do
        printf '#!/usr/bin/env bash\nexec "$(dirname "$0")/node" "$(dirname "$0")/../lib/node_modules/npm/bin/%s-cli.js" "$@"\n' "$entry" > "$nd/$stem/bin/$entry"
        chmod +x "$nd/$stem/bin/$entry"
    done
    mv "$nd/$stem" "$deps/node"
    hash -r
}
system_package() {
    local id=$1 package packages
    if [[ "$OS" == darwin ]]; then
        package=$(definition "$id" 7); command -v brew >/dev/null || die "Install Homebrew or $id manually and rerun."
        printf '  -> brew install %s\n' "$package"; brew install "$package"
        if [[ "$id" == node || "$id" == python ]]; then export PATH="$(brew --prefix "$package")/bin:$PATH"; fi
        return 0
    fi
    package=$(definition "$id" 6); read -r -a packages <<< "$package"
    if ! command -v apt-get >/dev/null; then
        printf '  This system has no apt-get. Install these with your package manager, then rerun: %s\n' "${packages[*]}" >&2
        return 1
    fi
    if [[ "$id" == browser-libs ]] && ! apt-cache show libasound2t64 >/dev/null 2>&1; then package=${package//libasound2t64/libasound2}; read -r -a packages <<< "$package"; fi
    if [[ "$id" == browser-libs ]] && ! apt-cache show fonts-liberation >/dev/null 2>&1; then package=${package//fonts-liberation/fonts-liberation2}; read -r -a packages <<< "$package"; fi
    printf '  -> sudo apt-get install %s\n' "${packages[*]}"
    if [[ $EUID == 0 ]]; then
        apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${packages[@]}"
    else
        sudo apt-get update; sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${packages[@]}"
    fi
}
node_compatible() { command -v node >/dev/null && [[ $(node -p "process.versions.node.split('.')[0]") -ge $(definition node 4) ]]; }
# A headless browser needs shared libraries on Linux. Skip apt when the
# downloaded browser already resolves all of them.
browser_libraries_missing() {
    local composer=$1 shell
    shell=$(find "$composer/node_modules/.remotion" -type f -name 'chrome-headless-shell' 2>/dev/null | head -n 1)
    [[ -n "$shell" ]] || return 0
    command -v ldd >/dev/null || return 0
    ldd "$shell" 2>/dev/null | grep -q 'not found'
}

# ------------------------------------------------------- facet from source
# facet is compiled here from the release's source archive by the official Go
# toolchain that the manifest pins. The build reads only the archive (its
# modules are vendored), never the network, and sees no Go settings but its
# own. Go, its caches and the source are removed afterwards. Go builds are
# reproducible: every machine builds the same facet.
GO_VERSION=$(definition go 3 2>/dev/null || true)
GO_NAME="go$GO_VERSION.$OS-$ARCH.tar.gz"
go_checksum() {
    local pair
    for pair in $(definition go "$([[ "$OS" == linux ]] && printf 6 || printf 7)"); do
        if [[ "$pair" == "$ARCH:"* ]]; then printf '%s' "${pair#*:}"; return 0; fi
    done
    return 1
}
version_at_most() {
    local IFS=. a b i
    read -r -a a <<< "$1"; read -r -a b <<< "$2"
    for i in 0 1 2; do
        if ((10#${a[i]:-0} < 10#${b[i]:-0})); then return 0; fi
        if ((10#${a[i]:-0} > 10#${b[i]:-0})); then return 1; fi
    done
    return 0
}
fetch_toolchain() {
    fetch "$(definition go 4)$GO_NAME" "$1"
    [[ $(hash_file "$1") == "$(go_checksum)" ]] || die "Checksum mismatch: $GO_NAME"
}
unpack_toolchain() {
    mkdir -p "$2"
    # Go's own tests are not needed to build.
    tar -xzf "$1" -C "$2" --exclude=testdata --exclude=go/test
    [[ -x "$2/go/bin/go" ]] || die "$GO_NAME holds no go/bin/go."
}
unpack_source() {
    local archive=$1 tree=$2 needed required
    mkdir -p "$tree"
    expand_zip "$archive" "$tree"
    for required in LICENSE "notices/$OS-$ARCH.md" source/go.mod source/cmd/facet/main.go source/vendor/modules.txt; do
        [[ -f "$tree/$required" ]] || die "Release missing $required"
    done
    needed=$(awk '$1=="go" && $2 ~ /^[0-9]+\.[0-9]+(\.[0-9]+)?$/ {print $2; exit}' "$tree/source/go.mod")
    if [[ -n "$needed" ]] && ! version_at_most "$needed" "$GO_VERSION"; then
        die "Facet $VERSION needs Go $needed to build, and this installer brings Go $GO_VERSION; use the installer from the Facet $VERSION release."
    fi
}
build_facet() {
    local tree=$1 stage=$2 build=$3 var unset=()
    for var in $(compgen -e); do case "$var" in GO*|CGO_*) unset+=(-u "$var");; esac; done
    mkdir -p "$build/cache" "$build/path" "$build/tmp" "$build/home" "$stage/bin"
    # Go keeps telemetry counters in the user's configuration folder; with HOME
    # and XDG_CONFIG_HOME pointed here, they are removed with the build.
    (cd "$tree/source" && env ${unset[@]+"${unset[@]}"} GOTOOLCHAIN=local GOFLAGS=-mod=vendor GOPROXY=off GOWORK=off GOENV=off \
        GO111MODULE=on CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH" GOCACHE="$build/cache" GOPATH="$build/path" GOTMPDIR="$build/tmp" \
        HOME="$build/home" XDG_CONFIG_HOME="$build/home/.config" XDG_CACHE_HOME="$build/home/.cache" \
        "$build/go/bin/go" build -trimpath -buildvcs=false -ldflags "-s -w -X main.Version=$VERSION" -o "$stage/bin/facet" ./cmd/facet)
    cp "$tree/LICENSE" "$stage/LICENSE"
    cp "$tree/notices/$OS-$ARCH.md" "$stage/THIRD_PARTY_NOTICES.md"
    cp -R "$tree/dependencies" "$stage/dependencies"
}
remove_build() {
    chmod -R u+w "$1" 2>/dev/null || true
    rm -rf -- "$1"
    [[ -z "${2:-}" ]] || rm -f -- "$2"
}
source_build() {
    local stage=$1 build="$TEMP/build" tarball available
    go_checksum >/dev/null || die "The installer manifest pins no Go toolchain for $OS/$ARCH."
    command -v tar >/dev/null || die 'Install required utility: tar'
    available=$(df -Pk "$TEMP" | awk 'NR==2 {print $4}')
    if [[ "$available" =~ ^[0-9]+$ ]] && ((available < 512000)); then
        die "Building Facet needs about 500 MB of free disk space for Go and its build cache; $((available / 1024)) MB are free in $TEMP."
    fi
    # The source is checked before Go is fetched: a bad archive costs no download.
    step "Unpack Facet $VERSION source" unpack_source "$ARCHIVE" "$build/facet"
    if [[ -n "$TOOLCHAIN" ]]; then
        [[ -f "$TOOLCHAIN" ]] || die "Go toolchain not found: $TOOLCHAIN"
        [[ $(hash_file "$TOOLCHAIN") == "$(go_checksum)" ]] || die "$TOOLCHAIN is not the official $GO_NAME (SHA-256 mismatch)."
        printf '  OK Check Go %s\n' "$GO_VERSION"
        tarball=$TOOLCHAIN
    else
        tarball="$TEMP/$GO_NAME"
        step "Download Go $GO_VERSION" fetch_toolchain "$tarball"
    fi
    step "Unpack Go $GO_VERSION" unpack_toolchain "$tarball" "$build"
    step "Build Facet $VERSION (a minute or two)" build_facet "$build/facet" "$stage" "$build"
    if [[ -n "$TOOLCHAIN" ]]; then step 'Remove Go and the build cache' remove_build "$build"
    else step 'Remove Go and the build cache' remove_build "$build" "$tarball"; fi
}
# The release's checksum file says what it ships: a prebuilt archive for this
# platform and, from 2.2.0, Facet's source, which --from-source builds. With
# --archive, the archive's hash picks the entry.
select_archive() {
    local sums=$1 hash=$2 candidate expected listed='' candidates
    candidates=("facet-$VERSION-$OS-$ARCH.zip" "facet-$VERSION-source.zip")
    [[ $FROM_SOURCE != 1 ]] || candidates=("facet-$VERSION-source.zip")
    for candidate in "${candidates[@]}"; do
        expected=$(awk -v name="$candidate" '{sub(/\r$/, "")} $2==name || $2=="*"name {print tolower($1); n++} END {exit n>1}' "$sums") \
            || die "Checksum entry missing or duplicated: $candidate"
        [[ -n "$expected" ]] || continue
        [[ -n "$listed" ]] || listed=$candidate
        if [[ -z "$hash" || "$expected" == "$hash" ]]; then printf '%s' "$candidate"; return 0; fi
    done
    if [[ -z "$hash" ]]; then
        [[ $FROM_SOURCE != 1 ]] || die "Facet $VERSION has no source archive; --from-source needs Facet 2.2.0 or later."
        die "Facet $VERSION has no download for $OS/$ARCH."
    fi
    [[ -z "$listed" ]] || die "Checksum mismatch: $listed"
    die "Checksum entry missing or duplicated: ${candidates[0]}"
}

# ---------------------------------------------------------------- actions
do_uninstall() {
    section 'Uninstall'
    local exe='' name group scope project problems=0 runtime
    if [[ -x "$CURRENT/bin/facet" ]]; then exe="$CURRENT/bin/facet"; else
        for runtime in "$RUNTIMES"/*; do [[ -x "$runtime/bin/facet" ]] && exe="$runtime/bin/facet"; done
    fi
    while IFS=$'\t' read -r scope project; do
        [[ -n "$scope" ]] || continue
        if [[ -z "$exe" ]]; then printf '%s\n' '  Recorded CLI wirings remain, but no facet executable is available; reinstall Facet and run facet wire --remove all.' >&2; problems=1; break; fi
        local args=()
        while IFS= read -r a; do args+=("$a"); done < <(wire_scope_args "$scope" "$project")
        run_wire "$exe" --remove all "${args[@]}" || { printf '  facet wire --remove all failed for %s scope %s\n' "$scope" "$project" >&2; problems=1; }
    done < <(recorded_wire_groups)
    if [[ -L "$CURRENT" ]]; then rm -f -- "$CURRENT"; printf '  OK Removed %s\n' "$CURRENT"
    elif [[ -e "$CURRENT" ]]; then printf '  %s is not a link created by the Facet installer; it was left in place.\n' "$CURRENT" >&2; problems=1; fi
    if [[ $NO_PATH != 1 ]]; then remove_path; printf '%s\n' '  OK Removed the facet PATH block from shell profiles'; fi
    rm -f -- "$STATE_FILE"
    if [[ $PURGE == 1 ]]; then rm -rf -- "$RUNTIMES"; printf '  OK Deleted %s\n' "$RUNTIMES"
    else printf '  Runtimes kept in %s (use --purge to delete them).\n' "$RUNTIMES"; fi
    v1_notice
    [[ $problems == 0 ]] || exit 1
    printf '\nFacet is uninstalled.\n'
}

do_rollback() {
    section 'Roll back'
    local active target previous
    active=$(active_runtime)
    if [[ $VERSION_EXPLICIT == 1 ]]; then target="$VERSION-$OS-$ARCH"; else target=$(read_state previous); fi
    [[ -n "$target" && "$target" != "$active" ]] || die 'No previous Facet runtime is installed to roll back to.'
    runtime_record_ok "$target" || die "Runtime $target is not installed."
    [[ $("$(runtime_dir "$target")/bin/facet" version 2>/dev/null) == "facet v${target%-$OS-$ARCH}" ]] || die "Runtime $target does not run; reinstall it."
    activate "$target"
    write_state "$target" "$active"
    printf '  OK Active runtime: %s\n' "$target"
    refresh_wirings || die 'Facet was rolled back, but refreshing the recorded wirings failed; see the output above.'
}

do_install() {
    local runtime_name runtime deps archive_hash='' name base cache selected reuse=0 missing=() recorded item composer
    runtime_name="$VERSION-$OS-$ARCH"; runtime=$(runtime_dir "$runtime_name"); deps="$runtime/dependencies"
    if [[ -z "$COMPONENTS" && -d "$runtime" ]]; then COMPONENTS=$(recorded_components "$runtime"); fi
    COMPONENTS=${COMPONENTS:-remotion,piper}
    if [[ $YES != 1 ]]; then
        local options=()
        while IFS=$'\t' read -r kind id version value windows linux darwin size capability; do
            [[ "$kind" != component ]] || options+=("$id" "$id | $size | $capability")
        done < "$MANIFEST"
        COMPONENTS=$(choose 'Optional production tools (plain input: numbers or names, comma separated; none for core only)' 1 "$COMPONENTS" "${options[@]}")
    fi
    [[ "$COMPONENTS" != all ]] || COMPONENTS=$(awk -F '\t' '$1=="component" {printf "%s%s", sep, $2; sep=","}' "$MANIFEST")
    SELECTED=()
    local raw
    IFS=',' read -r -a raw <<< "$COMPONENTS"
    for item in "${raw[@]:-}"; do
        item=${item// /}; [[ -n "$item" ]] || continue
        if [[ "$item" != none ]]; then [[ $(definition "$item" 1 2>/dev/null) == component ]] || die "Unknown component: $item"; fi
        SELECTED+=("$item")
    done
    ((${#SELECTED[@]})) || SELECTED=(none)
    has() { local s; for s in "${SELECTED[@]}"; do [[ "$s" != "$1" ]] || return 0; done; return 1; }
    if has none && ((${#SELECTED[@]} != 1)); then die 'none cannot be combined with components.'; fi
    printf '\n  Action:      %s\n  Version:     %s\n  Components:  %s\n  Runtime:     %s\n' "$ACTION" "$VERSION" "${SELECTED[*]}" "$runtime"
    if [[ $FROM_SOURCE == 1 ]]; then
        printf '  Build:       on this computer by Go %s (%s)\n' "$GO_VERSION" "$(if [[ -n "$TOOLCHAIN" ]]; then printf 'from %s' "$TOOLCHAIN"; else definition go 8; fi)"
    fi
    [[ $YES == 1 ]] || [[ $(ask 'Continue?' y) =~ ^(y|yes)$ ]] || die 'Installation cancelled.'

    section 'Install and verify'
    local program
    for program in awk grep curl unzip zipinfo; do command -v "$program" >/dev/null || die "Install required utility: $program"; done
    start_log
    mkdir -p "$RUNTIMES"
    if [[ -z "$ARCHIVE" ]]; then
        base="https://github.com/$(definition facet 4)/releases/download/v$VERSION"
        cache="$FACET_HOME_DIR/cache"; mkdir -p "$cache"
        SUMS="$TEMP/SHA256SUMS.txt"
        step 'Check release download' fetch "$base/SHA256SUMS.txt" "$SUMS"
        name=$(select_archive "$SUMS" '') || exit 1
        ARCHIVE="$cache/$name"
        local expected
        expected=$(awk -v name="$name" '{sub(/\r$/,"")} $2==name{print $1}' "$SUMS")
        if [[ -f "$ARCHIVE" && $(hash_file "$ARCHIVE") == "$expected" ]]; then printf '  OK Reusing verified download\n'; else
            step 'Download Facet' fetch "$base/$name" "$ARCHIVE.partial"
            verify_checksum "$ARCHIVE.partial" "$SUMS" "$name"; mv "$ARCHIVE.partial" "$ARCHIVE"
        fi
    else
        name=$(select_archive "$SUMS" "$(hash_file "$ARCHIVE")") || exit 1
    fi
    verify_checksum "$ARCHIVE" "$SUMS" "$name"; archive_hash=$(hash_file "$ARCHIVE")

    if [[ -d "$runtime" ]]; then
        if runtime_intact "$runtime" && grep -q "\"archive_sha256\": \"$archive_hash\"" "$runtime/components.json"; then
            reuse=1; printf '  OK Reusing installed runtime %s\n' "$runtime_name"
        else
            printf '  Installed runtime %s is incomplete or modified; replacing it with a verified copy.\n' "$runtime_name"
        fi
    fi
    if [[ $reuse == 0 ]]; then
        # The new runtime is complete before the old one is touched, so a
        # failed build or unpack leaves the installed Facet as it was.
        STAGE=$(mktemp -d "$RUNTIMES/.facet-stage-XXXXXX")
        if [[ "$name" == "facet-$VERSION-source.zip" ]]; then source_build "$STAGE"; else expand_zip "$ARCHIVE" "$STAGE"; fi
        local required
        for required in bin/facet dependencies/remotion-composer/package-lock.json; do [[ -f "$STAGE/$required" ]] || die "Release missing $required"; done
        chmod +x "$STAGE/bin/facet"
        [[ $("$STAGE/bin/facet" version) == "facet v$VERSION" ]] || die 'Binary version mismatch.'
        (cd "$STAGE" && find bin dependencies -type f | LC_ALL=C sort | while IFS= read -r f; do "${SHA_CMD[@]}" "$f"; done) > "$STAGE/.facet-files.sha256"
        if [[ -d "$runtime" ]]; then
            ASIDE="$RUNTIMES/.facet-old-$runtime_name-$$"; ASIDE_TARGET=$runtime
            mv -- "$runtime" "$ASIDE"
        fi
        # Components are installed at the final path: Python virtual
        # environments and npm launchers record absolute paths.
        mv -- "$STAGE" "$runtime"; STAGE=''; NEW_RUNTIME=$runtime
        missing=("${SELECTED[@]}")
    else
        recorded=",$(recorded_components "$runtime"),"
        for item in "${SELECTED[@]}"; do [[ "$item" == none || "$recorded" == *",$item,"* ]] || missing+=("$item"); done
    fi
    mkdir -p "$deps"
    export PATH="$deps/node/bin:$deps/piper/bin:$PATH"
    needs() { local s; for s in "${missing[@]:-}"; do [[ "$s" != "$1" ]] || return 0; done; return 1; }

    if ! command -v ffmpeg >/dev/null || ! command -v ffprobe >/dev/null; then step 'Install media tools' system_package ffmpeg; fi
    step 'Check FFmpeg' ffmpeg -version; step 'Check FFprobe' ffprobe -version
    composer="$runtime/$(definition remotion 4)"
    if needs remotion || needs hyperframes; then
        if ! node_compatible; then
            if [[ "$OS" == linux ]]; then step 'Install private Node.js runtime' install_linux_node "$deps"; else step 'Install Node.js' system_package node; fi
        fi
        node_compatible || die "Node.js $(definition node 4)+ is required."
        command -v npm >/dev/null || die 'npm is missing; install it and rerun.'
    fi
    if needs remotion; then
        (cd "$composer" && step 'Install Remotion packages' npm ci --no-audit --no-fund && step 'Prepare Remotion browser' node node_modules/@remotion/cli/remotion-cli.js browser ensure)
        if [[ "$OS" == linux ]] && browser_libraries_missing "$composer"; then
            step 'Install browser system libraries' system_package browser-libs || printf '%s\n' '  Browser libraries are missing; rendering will fail until they are installed.' >&2
        fi
    fi
    local hf="$deps/hyperframes" hf_entry="$deps/hyperframes/node_modules/hyperframes/bin/hyperframes.mjs"
    if needs hyperframes; then
        mkdir -p "$hf"
        [[ -f "$hf/package.json" ]] || printf '{"private":true,"dependencies":{"hyperframes":"%s"}}\n' "$(definition hyperframes 3)" > "$hf/package.json"
        (cd "$hf" && step "Install HyperFrames $(definition hyperframes 3)" npm install --no-audit --no-fund)
        step 'Prepare HyperFrames browser' node "$hf_entry" browser ensure
    fi
    local python="$deps/piper/bin/python" voices="$deps/voices"
    if needs piper; then
        # Debian and Ubuntu ship venv without ensurepip until python3-venv is
        # installed, so a Python that imports venv can still fail to create
        # one, and leave a half-made environment without pip behind.
        if ! command -v python3 >/dev/null || ! python3 -c 'import sys, venv, ensurepip' >/dev/null 2>&1; then step 'Install Python for Piper' system_package python; fi
        if ! "$python" -m pip --version >/dev/null 2>&1; then
            rm -rf -- "$deps/piper"
            step 'Create Piper environment' python3 -m venv "$deps/piper"
        fi
        mkdir -p "$voices"
        step "Install Piper $(definition piper 3)" "$python" -m pip install --only-binary=:all: "piper-tts==$(definition piper 3)"
        step 'Download speech model' "$python" -m piper.download_voices --download-dir "$voices" "$(definition piper 4)"
    fi

    verify_media() (
        set -e
        local dir="$TEMP/verify" video
        mkdir -p "$dir"; video="$dir/test.mp4"
        ffmpeg -v error -f lavfi -i color=c=blue:s=320x180:r=24:d=1 -c:v libx264 -pix_fmt yuv420p -y "$video"
        if has remotion; then
            video="$dir/render.mp4"
            printf '{"width":320,"height":180,"fps":24,"duration_seconds":1,"output_path":%s,"cuts":[{"type":"text_card","text":"Facet setup","in_seconds":0,"out_seconds":1}]}\n' "$(json_string "$video")" > "$dir/render.json"
            # The runtime's own composer, beside its executable, is what
            # renders: a development override must not redirect the check.
            (cd "$dir" && env -u FACET_REMOTION_COMPOSER "$runtime/bin/facet" tools run video_compose --input "$dir/render.json")
        fi
        ffprobe -v error -show_streams "$video"; ffmpeg -v error -i "$video" -f null -
        if has piper; then
            printf 'Facet setup verification.\n' | "$python" -m piper --model "$voices/$(definition piper 4).onnx" --output_file "$dir/voice.wav"
            ffmpeg -v error -i "$dir/voice.wav" -f null -
        fi
        if has hyperframes; then
            mkdir -p "$dir/html"; cp "$SCRIPT_DIR/installer/verify.html" "$dir/html/index.html"
            (cd "$dir/html" && node "$hf_entry" render --output "$dir/html/render.mp4" --fps 24)
            ffmpeg -v error -i "$dir/html/render.mp4" -f null -
        fi
    )
    local verified=true
    if [[ $SKIP_VERIFY == 0 ]]; then step 'Verify selected local capabilities' verify_media
    else verified=false; printf '%s\n' '  Verification skipped: media readiness is unverified.'; fi

    local list='' s
    for s in "${SELECTED[@]}"; do [[ "$s" == none ]] || list="${list:+$list, }$(json_string "$s")"; done
    if [[ $reuse == 1 ]]; then
        recorded=$(recorded_components "$runtime")
        IFS=',' read -r -a raw <<< "$recorded"
        for s in "${raw[@]:-}"; do [[ -z "$s" || ", $list," == *"\"$s\""* ]] || list="${list:+$list, }$(json_string "$s")"; done
    fi
    printf '{\n  "schema": 1,\n  "version": %s,\n  "os": %s,\n  "arch": %s,\n  "components": [%s],\n  "verified": %s,\n  "archive_sha256": %s,\n  "installed_at": %s\n}\n' \
        "$(json_string "$VERSION")" "$(json_string "$OS")" "$(json_string "$ARCH")" "$list" "$verified" "$(json_string "$archive_hash")" "$(json_string "$(utc_now)")" > "$runtime/components.json"

    local active previous refresh_failed=0
    active=$(active_runtime)
    activate "$runtime_name"
    previous=$(read_state previous)
    if [[ -n "$active" && "$active" != "$runtime_name" ]]; then previous=$active; fi
    write_state "$runtime_name" "$previous"
    COMMITTED=1
    printf '  OK Active runtime: %s\n' "$runtime_name"
    prune_runtimes "$runtime_name" "$previous"
    if [[ $NO_PATH != 1 ]]; then add_path; else printf '  PATH unchanged (--no-path). Run the command as %s\n' "$CURRENT/bin/facet"; fi
    refresh_wirings || refresh_failed=1

    section 'Wire agentic CLIs'
    local exe="$CURRENT/bin/facet" detected='' cli
    for cli in $SUPPORTED_CLIS; do if command -v "$cli" >/dev/null; then detected="${detected:+$detected,}$cli"; fi; done
    if [[ -z "$WIRE" && $YES != 1 ]]; then
        local wire_options=()
        for cli in $SUPPORTED_CLIS; do
            if [[ ",$detected," == *",$cli,"* ]]; then wire_options+=("$cli" "$cli - detected"); else wire_options+=("$cli" "$cli - not detected"); fi
        done
        WIRE=$(choose 'Wire Facet into which CLIs? (none to skip)' 1 "${detected:-none}" "${wire_options[@]}")
        if [[ "$WIRE" != none ]]; then
            SCOPE=$(choose 'Wire for all your projects (user) or one project?' 0 "$SCOPE" user 'user - every project' project 'project - one directory')
            if [[ "$SCOPE" == project && -z "$PROJECT" ]]; then PROJECT=$(absolute "$(ask 'Project directory' .)"); fi
        fi
    fi
    if [[ -n "$WIRE" && "$WIRE" != none ]]; then
        local args=()
        while IFS= read -r a; do args+=("$a"); done < <(wire_scope_args "$SCOPE" "$PROJECT")
        run_wire "$exe" "$WIRE" "${args[@]}" || die 'facet wire failed; Facet is installed. Rerun facet wire after fixing the reported problem.'
    else
        printf '%s\n' '  Not wired. Later: facet wire <claude|codex|copilot|opencode|compa|all> [--scope user|project]'
    fi
    v1_notice
    [[ $refresh_failed == 0 ]] || die 'Facet is installed and active, but refreshing the recorded wirings failed; see the output above.'
    printf '\nFacet v%s is ready.\n  Check it: facet doctor\n' "$VERSION"
}

# ------------------------------------------------------------------- main
section 'FACET'
printf 'Video production tools for your agent.\nInstaller %s | %s/%s\n' "$VERSION" "$OS" "$ARCH"
if [[ $YES != 1 && $ACTION_EXPLICIT == 0 && -L "$CURRENT" ]]; then
    ACTION=$(choose 'Facet is installed. What should setup do?' 0 install install 'install - add components or repair' update 'update - install the selected version' rollback 'rollback - return to the previous runtime' uninstall 'uninstall - remove Facet')
fi
case "$ACTION" in
    uninstall) do_uninstall;;
    rollback) do_rollback;;
    install|update) do_install;;
esac
