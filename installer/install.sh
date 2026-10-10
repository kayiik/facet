#!/bin/sh
# Standalone POSIX entry: definitions are consumed before anything reads stdin.
# Installs, updates, verifies and removes Facet: the facet command and the Facet App.
# One installer and one receipt for both products; no Python, Go or Node.

fb_fail() {
    printf 'facet install: %s\n' "$*" >&2
    exit 1
}

fb_path() (
    fb_p=$1
    [ -n "$fb_p" ] || fb_fail "An empty path is not allowed"
    printf '%s\n' "$fb_p" | awk 'NR != 1 || /[[:cntrl:]]/ {bad=1} END {exit bad}' ||
        fb_fail "Paths must not contain control characters"
    case "$fb_p" in
        *\\*) fb_fail "Backslashes are not supported in Unix paths" ;;
        /*) ;;
        *) fb_p=$fb_cwd/$fb_p ;;
    esac
    printf '%s\n' "$fb_p" | awk -F / '{
        n=0
        for(i=1;i<=NF;i++) {
            if($i=="" || $i==".") continue
            if($i=="..") {if(n>0) n--; continue}
            parts[++n]=$i
        }
        result=""
        for(i=1;i<=n;i++) result=result "/" parts[i]
        if(result=="") print "/"
        else print result
    }'
)

fb_no_links() (
    fb_n=$1
    while [ "$fb_n" != / ]; do
        [ ! -L "$fb_n" ] || fb_fail "Refusing linked path: $fb_n"
        [ "$fb_n" = "$1" ] || [ ! -e "$fb_n" ] || [ -d "$fb_n" ] ||
            fb_fail "A path ancestor is not a directory: $fb_n"
        fb_n=${fb_n%/*}
        [ -n "$fb_n" ] || fb_n=/
    done
)

fb_under() {
    [ "$2" != / ] || return 0
    case "$1/" in "$2/"*) return 0 ;; esac
    return 1
}

fb_state_disjoint() (
    fb_sd_logical=$(fb_path "$1")
    fb_sd_parent=$fb_sd_logical
    fb_sd_suffix=
    while [ ! -d "$fb_sd_parent" ]; do
        [ ! -L "$fb_sd_parent" ] || fb_fail "Cannot resolve protected state path: $fb_sd_parent"
        fb_sd_suffix=/${fb_sd_parent##*/}$fb_sd_suffix
        fb_sd_parent=${fb_sd_parent%/*}
        [ -n "$fb_sd_parent" ] || fb_sd_parent=/
    done
    fb_sd_physical=$(CDPATH= cd -P "$fb_sd_parent" && pwd -P) ||
        fb_fail "Cannot resolve protected state directory: $fb_sd_parent"
    fb_sd_physical=$(fb_path "$fb_sd_physical$fb_sd_suffix")
    fb_sd_install=$fb_root
    for fb_sd_state in "$fb_sd_logical" "$fb_sd_physical"; do
        if [ "$fb_system" = darwin ]; then
            # Darwin installations must also be safe on case-insensitive volumes.
            fb_sd_install=$(printf '%s\n' "$fb_sd_install" | awk '{print tolower($0)}')
            fb_sd_state=$(printf '%s\n' "$fb_sd_state" | awk '{print tolower($0)}')
        fi
        if fb_under "$fb_sd_install" "$fb_sd_state" || fb_under "$fb_sd_state" "$fb_sd_install"; then
            fb_fail "Installation/data-state overlap with $2: $fb_sd_logical"
        fi
    done
)

fb_protect_state() (
    case "$fb_system" in
        darwin) fb_ps_app="$fb_home/Library/Application Support/Facet App" ;;
        linux) fb_ps_app=${XDG_DATA_HOME:-$fb_home/.local/share}/facet-app ;;
    esac
    fb_state_disjoint "${FACET_APP_HOME:-$fb_ps_app}" "App data"
    fb_state_disjoint "${FACET_HOME:-$fb_home/.facet}" "Facet's state"
    if [ -n "${COMPA_HOME:-}" ]; then
        fb_state_disjoint "$COMPA_HOME" COMPA_HOME
    fi
)

fb_hash() (
    if [ "$fb_hasher" = sha256sum ]; then
        fb_h=$(sha256sum "$1") || fb_fail "Cannot hash $1"
    else
        fb_h=$(shasum -a 256 "$1") || fb_fail "Cannot hash $1"
    fi
    printf '%s\n' "${fb_h%% *}"
)

fb_limit() (
    fb_l=$(wc -c < "$1") || fb_fail "Cannot measure $1"
    [ "$fb_l" -le "$2" ] || fb_fail "Size limit exceeded: $1 ($2 bytes maximum)"
)

fb_copy() (
    fb_no_links "$1"
    [ -f "$1" ] || fb_fail "Expected a regular input file: $1"
    head -c "$(($3 + 1))" < "$1" > "$2" || fb_fail "Cannot snapshot $1"
    fb_limit "$2" "$3"
)

# Fetch release asset $1 into $fb_work/distribution/$1.
fb_fetch() (
    fb_fa_name=$1 fb_fa_limit=$2
    if [ -n "$fb_distribution" ]; then
        [ -e "$fb_distribution/$fb_fa_name" ] || [ -L "$fb_distribution/$fb_fa_name" ] ||
            fb_fail "--distribution-dir needs $fb_fa_name"
        fb_copy "$fb_distribution/$fb_fa_name" "$fb_work/distribution/$fb_fa_name" "$fb_fa_limit"
    else
        # -q must be first: no ~/.curlrc, netrc, token lookup, or auth fallback.
        (
            set +e
            curl -q -fsSL --proto '=https' --proto-redir '=https' \
                --connect-timeout 15 --max-time 600 --max-filesize "$fb_fa_limit" "$fb_url/$fb_fa_name"
            printf '%s\n' "$?" > "$fb_work/download.status"
        ) | head -c "$((fb_fa_limit + 1))" > "$fb_work/distribution/$fb_fa_name"
        fb_limit "$fb_work/distribution/$fb_fa_name" "$fb_fa_limit"
        [ -f "$fb_work/download.status" ] &&
            [ "$(cat "$fb_work/download.status")" = 0 ] ||
            fb_fail "Download failed: $fb_url/$fb_fa_name (no fallback was attempted)"
    fi
)

fb_checksums() {
    awk '
        {
            hash=substr($0,1,64); name=substr($0,67)
            if (length(hash)!=64 || hash ~ /[^0-9a-fA-F]/ ||
                substr($0,65,2)!="  " || name !~ /^[A-Za-z0-9][A-Za-z0-9._+-]*$/ ||
                seen[tolower(name)]++) bad=1
            names[name]=1
        }
        END {
            if (!names["manifest.tsv"] || !names["build-manifest.json"] ||
                !names["install.sh"] || !names["install.ps1"] || NR>4096) bad=1
            exit bad
        }
    ' "$fb_work/distribution/SHA256SUMS" || fb_fail "Invalid or duplicate SHA256SUMS checksum inventory"
}

fb_checksum() (
    fb_c=$(awk -v name="$1" 'substr($0,67)==name {print tolower(substr($0,1,64))}' \
        "$fb_work/distribution/SHA256SUMS")
    [ -n "$fb_c" ] || fb_fail "Missing checksum for $1"
    [ "$(fb_hash "$fb_work/distribution/$1")" = "$fb_c" ] || fb_fail "Checksum mismatch: $1"
)

fb_manifest() {
    awk -F '\t' '
        function reject() {bad=1; exit 1}
        function path(s, a,n,i) {
            if (s !~ /^[A-Za-z0-9][A-Za-z0-9._+\/-]*$/) return 0
            n=split(s,a,"/")
            for(i=1;i<=n;i++) if(a[i]=="" || a[i]=="." || a[i]=="..") return 0
            return 1
        }
        function product(p) {return (p=="core" || p=="app")}
        function target(t) {return (t ~ /^(linux|darwin|windows)\/(amd64|arm64)$/)}
        function layout(p,s) {
            if(p=="core") return (s=="bin/facet" || s=="bin/facet.exe" || s=="LICENSE" || s=="THIRD_PARTY_NOTICES.md")
            return (s=="bin/facet-app" || s=="bin/facet-app.exe" || s ~ /^kernel\/(compa-kernel|compa-kernel\.exe|LICENSE|NOTICE)$/ ||
                s=="app/LICENSE" || s=="app/THIRD_PARTY_NOTICES.md")
        }
        NR==1 {if($0!="format\tfacet-release-v1") reject()}
        $1=="format" {if(NF!=2 || $2!="facet-release-v1" || format++) reject(); next}
        $1=="version" {
            if(NF!=2 || version!="" || $2 !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/) reject()
            version=$2; next
        }
        $1=="commit" {
            if(NF!=2 || commit++ || (length($2)!=40 && length($2)!=64) || $2 ~ /[^0-9a-fA-F]/) reject()
            next
        }
        $1=="archive" {
            key=$2 SUBSEP $3
            if(NF!=4 || !product($2) || !target($3) || archives[key]!="" ||
                $4 !~ /^[A-Za-z0-9][A-Za-z0-9._+-]*$/ || archiveNames[tolower($4)]++) reject()
            archives[key]=$4; products[key]=$2; targets[key]=$3; next
        }
        $1=="file" {
            key=$2 SUBSEP $3; full=key SUBSEP $4
            if(NF!=5 || !product($2) || !target($3) || !path($4) || !layout($2,$4) ||
                length($5)!=64 || $5 ~ /[^0-9a-fA-F]/ || seen[key SUBSEP tolower($4)]++) reject()
            files[full]=1; fileKeys[full]=key; fileNames[full]=$4
            if(++counts[key]>4096) reject()
            next
        }
        {reject()}
        END {
            if(bad) exit 1
            if(format!=1 || commit!=1 || version=="") exit 1
            for(key in archives) {
                t=targets[key]; p=products[key]; gsub(/\//,"_",t)
                suffix=".tar.gz"; extension=""
                if(targets[key] ~ /^windows\//) {suffix=".zip"; extension=".exe"}
                if(archives[key]!="facet-" p "_" version "_" t suffix || !counts[key]) exit 1
                if(p=="core" && (counts[key]!=3 || !files[key SUBSEP "bin/facet" extension] ||
                    !files[key SUBSEP "LICENSE"] || !files[key SUBSEP "THIRD_PARTY_NOTICES.md"])) exit 1
                if(p=="app" && (counts[key]!=6 || !files[key SUBSEP "bin/facet-app" extension] ||
                    !files[key SUBSEP "kernel/compa-kernel" extension] || !files[key SUBSEP "kernel/LICENSE"] ||
                    !files[key SUBSEP "kernel/NOTICE"] || !files[key SUBSEP "app/LICENSE"] ||
                    !files[key SUBSEP "app/THIRD_PARTY_NOTICES.md"])) exit 1
            }
            for(full in files) if(!(fileKeys[full] in archives)) exit 1
        }
    ' "$fb_work/distribution/manifest.tsv" || fb_fail "Invalid release manifest.tsv or archive/file allowlist"
}

fb_select() (
    fb_s=$(awk -F '\t' -v p="$1" -v t="$2" '$1=="archive" && $2==p && $3==t {print $4}' \
        "$fb_work/distribution/manifest.tsv")
    [ -n "$fb_s" ] || fb_fail "No exact native $1 archive declared for $2"
    awk -F '\t' -v p="$1" -v t="$2" \
        '$1=="file" && $2==p && $3==t {print $4 "\t" tolower($5)}' \
        "$fb_work/distribution/manifest.tsv" | sort > "$fb_work/$1.files"
    printf '%s\n' "$fb_s"
)

fb_extract_tar() (
    fb_xt_product=$1
    fb_xt_archive=$fb_work/distribution/$2
    fb_xt_tar=$fb_work/$1.tar
    fb_xt_dest=$3
    (
        set +e
        gzip -dc "$fb_xt_archive"
        printf '%s\n' "$?" > "$fb_work/gzip.status"
    ) | head -c "$((fb_archive_limit + 1))" > "$fb_xt_tar"
    fb_limit "$fb_xt_tar" "$fb_archive_limit"
    [ -f "$fb_work/gzip.status" ] && [ "$(cat "$fb_work/gzip.status")" = 0 ] ||
        fb_fail "Invalid compressed archive: $2"
    # Preserve raw names while listing; ignore zero padding so concatenated
    # archives cannot hide extra members. Extraction only ever writes stdout.
    tar -tP --ignore-zeros -f "$fb_xt_tar" > "$fb_work/tar.names" || fb_fail "Invalid archive inventory: $2"
    tar -tvP --ignore-zeros -f "$fb_xt_tar" > "$fb_work/tar.types" || fb_fail "Invalid archive types: $2"
    awk 'substr($0,1,1)!="-" {bad=1} END {exit (bad || NR==0 || NR>4096)}' \
        "$fb_work/tar.types" || fb_fail "Archive must contain only ordinary files, not links/devices/directories"
    [ "$(wc -l < "$fb_work/tar.types")" -eq "$(wc -l < "$fb_work/tar.names")" ] ||
        fb_fail "Ambiguous archive member names"
    sort "$fb_work/tar.names" > "$fb_work/tar.sorted"
    cut -f 1 "$fb_work/$fb_xt_product.files" | sort > "$fb_work/tar.expected"
    cmp -s "$fb_work/tar.sorted" "$fb_work/tar.expected" ||
        fb_fail "Archive members differ from the exact canonical file allowlist (unsafe, extra or duplicate member)"
    mkdir "$fb_xt_dest" || fb_fail "Cannot create private extraction directory"
    fb_xt_total=0
    while IFS="$fb_tab" read -r fb_xt_name fb_xt_hash; do
        fb_xt_file=$fb_xt_dest/$fb_xt_name
        mkdir -p "${fb_xt_file%/*}" || fb_fail "Cannot create private member directory"
        (
            set +e
            tar -xOP --ignore-zeros -f "$fb_xt_tar" "$fb_xt_name"
            printf '%s\n' "$?" > "$fb_work/tar.status"
        ) | head -c "$((fb_archive_limit - fb_xt_total + 1))" > "$fb_xt_file"
        fb_xt_size=$(wc -c < "$fb_xt_file")
        fb_xt_total=$((fb_xt_total + fb_xt_size))
        [ "$fb_xt_total" -le "$fb_archive_limit" ] || fb_fail "Expanded archive size limit exceeded"
        [ -f "$fb_work/tar.status" ] && [ "$(cat "$fb_work/tar.status")" = 0 ] ||
            fb_fail "Archive member extraction failed: $fb_xt_name"
        [ "$(fb_hash "$fb_xt_file")" = "$fb_xt_hash" ] ||
            fb_fail "File checksum mismatch: $fb_xt_name"
        chmod 644 "$fb_xt_file" || fb_fail "Cannot set private member permissions"
    done < "$fb_work/$fb_xt_product.files"
)

fb_check_file() (
    fb_no_links "$1"
    [ -f "$1" ] || fb_fail "Preserving a modified or missing owned file: $1"
    [ "$(fb_hash "$1")" = "$2" ] || fb_fail "Preserving a modified or missing owned file: $1"
)

fb_same_file() {
    [ ! -L "$1" ] && [ -f "$1" ] && [ "$(fb_hash "$1")" = "$2" ]
}

fb_check_link() (
    fb_no_links "${1%/*}"
    [ -L "$1" ] && [ "$(readlink "$1")" = "$2" ] || fb_fail "Modified or missing owned launch link: $1"
)

# Read the receipt into $fb_work/owned.{meta,products,places,files,links}.
fb_read_receipt() {
    for fb_rr_part in meta products places files links; do : > "$fb_work/owned.$fb_rr_part"; done
    fb_owned=none fb_old_receipt=
    if [ ! -e "$fb_receipt" ] && [ ! -L "$fb_receipt" ]; then return; fi
    fb_copy "$fb_receipt" "$fb_work/receipt.old" "$fb_metadata_limit"
    awk -F '\t' -v root="$fb_root" '
        function bad() {failed=1; exit 1}
        function path(s, a,n,i) {
            if (s !~ /^[A-Za-z0-9][A-Za-z0-9._+\/-]*$/) return 0
            n=split(s,a,"/")
            for(i=1;i<=n;i++) if(a[i]=="" || a[i]=="." || a[i]=="..") return 0
            return 1
        }
        $1 ~ /^(format|version|commit|repository|target|root)$/ {
            if(NF!=2 || ($1 in meta)) bad()
            meta[$1]=$2; next
        }
        $1=="product" {if(NF!=2 || $2 !~ /^(core|app)$/ || products[$2]++) bad(); next}
        $1=="place" {
            if(NF!=7 || $2 !~ /^[1-9][0-9]*$/ || length($2)>6 || ($2 in kinds) || $7 !~ /^\// || dirs[$7]++ ||
                $6 !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/ || $3!="start" || $4!="-" || $5!="-") bad()
            kinds[$2]=$3; next
        }
        $1=="file" {
            if(NF!=4 || ($2!="root" && !($2 in kinds)) || !path($3) ||
                length($4)!=64 || $4 ~ /[^0-9a-f]/ || seen[$2 SUBSEP tolower($3)]++) bad()
            count[$2]++; bases[++entries]=$2; names[entries]=tolower($3); next
        }
        $1=="link" {
            name=$2; sub(/^.*\//,"",name)
            if(NF!=3 || $2 !~ /^\// || (name!="facet" && name!="facet-app") || links[$2]++ || $3!=root "/bin/" name) bad()
            next
        }
        {bad()}
        END {
            if(failed) exit 1
            if(meta["format"]!="facet-install-v1" ||
                meta["version"] !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/ ||
                meta["commit"] !~ /^[0-9a-f]+$/ || (length(meta["commit"])!=40 && length(meta["commit"])!=64) ||
                meta["target"] !~ /^(linux|darwin)\/(amd64|arm64)$/ || meta["root"]!=root ||
                !products["core"] || !count["root"]) exit 1
            for(id in kinds) if(!count[id]) exit 1
            for(i=1;i<=entries;i++) {
                parent=names[i]
                while(sub(/\/[^\/]+$/,"",parent)) if((bases[i] SUBSEP parent) in seen) exit 1
            }
        }
    ' "$fb_work/receipt.old" || fb_fail "Invalid or relocated installation receipt; refusing its ownership"
    fb_old_receipt=$(fb_hash "$fb_work/receipt.old")
    fb_owned=current
    awk -F '\t' '$1 ~ /^(version|commit|repository|target)$/ {print}' "$fb_work/receipt.old" > "$fb_work/owned.meta"
    awk -F '\t' '$1=="product"{print $2}' "$fb_work/receipt.old" > "$fb_work/owned.products"
    awk -F '\t' '$1=="place"{print $2 "\t" $3 "\t" $4 "\t" $5 "\t" $6 "\t" $7}' "$fb_work/receipt.old" > "$fb_work/owned.places"
    awk -F '\t' '$1=="file"{print $2 "\t" $3 "\t" $4}' "$fb_work/receipt.old" > "$fb_work/owned.files"
    awk -F '\t' '$1=="link"{print $2 "\t" $3}' "$fb_work/receipt.old" > "$fb_work/owned.links"
    while IFS="$fb_tab" read -r fb_rr_id fb_rr_kind fb_rr_harness fb_rr_scope fb_rr_version fb_rr_dir; do
        [ "$(fb_path "$fb_rr_dir")" = "$fb_rr_dir" ] || fb_fail "Invalid receipt place folder: $fb_rr_dir"
    done < "$fb_work/owned.places"
}

fb_owned_meta() {
    awk -F '\t' -v key="$1" '$1==key {print $2}' "$fb_work/owned.meta"
}

fb_has_line() {
    awk -v value="$2" '$0==value {found=1} END {exit (!found)}' "$1"
}

fb_start_dir() (
    case "$fb_system" in
        darwin) fb_path "$fb_home/Applications" ;;
        *) fb_path "${XDG_DATA_HOME:-$fb_home/.local/share}/applications" ;;
    esac
)

# Write the Start entry for this platform into $1 and list its files, TAB mode, in $2.
fb_write_start() (
    mkdir -p "$1" || fb_fail "Cannot stage the Start entry"
    case "$fb_system" in
        darwin)
            mkdir -p "$1/Facet.app/Contents/MacOS" || fb_fail "Cannot stage the Start entry"
            fb_ws_quoted=$(printf '%s\n' "$fb_root/bin/facet-app" | awk -v q="'" '{
                out = ""
                for (i = 1; i <= length($0); i++) {
                    c = substr($0, i, 1)
                    if (c == q) out = out q "\\" q q
                    else out = out c
                }
                print out
            }')
            {
                printf '#!/bin/sh\n'
                printf "exec '%s' \"\$@\"\n" "$fb_ws_quoted"
            } > "$1/Facet.app/Contents/MacOS/Facet"
            {
                printf '<?xml version="1.0" encoding="UTF-8"?>\n'
                printf '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n'
                printf '<plist version="1.0">\n<dict>\n'
                printf '  <key>CFBundleExecutable</key><string>Facet</string>\n'
                printf '  <key>CFBundleIdentifier</key><string>com.kayiik.facet</string>\n'
                printf '  <key>CFBundleName</key><string>Facet</string>\n'
                printf '  <key>CFBundlePackageType</key><string>APPL</string>\n'
                printf '  <key>CFBundleShortVersionString</key><string>%s</string>\n' "$fb_version"
                printf '</dict>\n</plist>\n'
            } > "$1/Facet.app/Contents/Info.plist"
            printf 'Facet.app/Contents/Info.plist\t644\nFacet.app/Contents/MacOS/Facet\t755\n' > "$2"
            ;;
        *)
            fb_ws_exec=$(printf '%s\n' "$fb_root/bin/facet-app" | awk '{
                out = ""
                for (i = 1; i <= length($0); i++) {
                    c = substr($0, i, 1)
                    if (c == "\"" || c == "`" || c == "$") out = out "\\" c
                    else if (c == "%") out = out "%%"
                    else out = out c
                }
                print out
            }')
            {
                printf '[Desktop Entry]\nType=Application\nName=Facet\n'
                printf 'Comment=Make videos with an AI producer\n'
                printf 'Exec="%s"\nTerminal=true\nCategories=AudioVideo;Video;\n' "$fb_ws_exec"
            } > "$1/facet.desktop"
            printf 'facet.desktop\t644\n' > "$2"
            ;;
    esac
)

fb_mkdir() (
    fb_md=$1
    fb_no_links "$fb_md"
    : > "$fb_work/directories.pending"
    while [ ! -d "$fb_md" ]; do
        [ ! -e "$fb_md" ] || fb_fail "Directory path is occupied: $fb_md"
        printf '%s\n' "$fb_md" >> "$fb_work/directories.pending"
        fb_md=${fb_md%/*}
        [ -n "$fb_md" ] || fb_md=/
    done
    sort "$fb_work/directories.pending" > "$fb_work/directories.sorted"
    while IFS= read -r fb_md; do
        mkdir "$fb_md" || fb_fail "Cannot create installation directory: $fb_md"
        printf '%s\n' "$fb_md" >> "$fb_work/directories.created"
    done < "$fb_work/directories.sorted"
)

fb_apply_file() {
    fb_af_dest=$1 fb_af_source=$2 fb_af_old=$3 fb_af_new=$4 fb_af_mode=$5
    fb_no_links "$fb_af_dest"
    if [ "$fb_af_old" = - ]; then
        [ ! -e "$fb_af_dest" ] && [ ! -L "$fb_af_dest" ] ||
            fb_fail "Refusing to overwrite a file Facet does not own: $fb_af_dest"
    else
        fb_check_file "$fb_af_dest" "$fb_af_old"
    fi
    fb_sequence=$((fb_sequence + 1))
    fb_af_backup=$fb_work/backups/$fb_sequence
    if [ "$fb_af_old" != - ]; then
        cp -p "$fb_af_dest" "$fb_af_backup" || fb_fail "Cannot back up $fb_af_dest"
        fb_check_file "$fb_af_backup" "$fb_af_old"
    fi
    printf 'file\t%s\t%s\t%s\t%s\n' "$fb_af_dest" "$fb_af_old" "$fb_af_new" "$fb_af_backup" \
        > "$fb_work/journal/$fb_sequence"
    printf '%s\n' "$fb_sequence" >> "$fb_work/order"
    if [ "$fb_af_new" = - ]; then
        rm "$fb_af_dest" || fb_fail "Cannot remove owned file: $fb_af_dest"
    else
        fb_mkdir "${fb_af_dest%/*}"
        fb_af_temp=$(mktemp "${fb_af_dest%/*}/.facet-install.XXXXXXXX") || fb_fail "Cannot stage atomic publication"
        printf '%s\n' "$fb_af_temp" >> "$fb_work/temporaries"
        cp "$fb_af_source" "$fb_af_temp" && chmod "$fb_af_mode" "$fb_af_temp" ||
            fb_fail "Cannot prepare publication: $fb_af_dest"
        fb_check_file "$fb_af_temp" "$fb_af_new"
        if [ "$fb_af_old" = - ]; then
            ln "$fb_af_temp" "$fb_af_dest" || fb_fail "Atomic no-clobber publication failed: $fb_af_dest"
            rm "$fb_af_temp" || fb_fail "Cannot remove publication temporary"
        else
            fb_check_file "$fb_af_dest" "$fb_af_old"
            mv -f "$fb_af_temp" "$fb_af_dest" || fb_fail "Atomic publication failure: $fb_af_dest"
        fi
        fb_check_file "$fb_af_dest" "$fb_af_new"
    fi
}

fb_apply_link() {
    fb_al_path=$1 fb_al_old=$2 fb_al_new=$3
    fb_no_links "${fb_al_path%/*}"
    if [ "$fb_al_old" = - ]; then
        [ ! -e "$fb_al_path" ] && [ ! -L "$fb_al_path" ] || fb_fail "PATH entry appeared: $fb_al_path"
    else
        fb_check_link "$fb_al_path" "$fb_al_old"
    fi
    fb_sequence=$((fb_sequence + 1))
    printf 'link\t%s\t%s\t%s\t-\n' "$fb_al_path" "$fb_al_old" "$fb_al_new" > "$fb_work/journal/$fb_sequence"
    printf '%s\n' "$fb_sequence" >> "$fb_work/order"
    if [ "$fb_al_new" = - ]; then
        rm "$fb_al_path" || fb_fail "Cannot remove owned launch link: $fb_al_path"
    else
        fb_mkdir "${fb_al_path%/*}"
        ln -s "$fb_al_new" "$fb_al_path" || fb_fail "Cannot create owned launch link: $fb_al_path"
        fb_check_link "$fb_al_path" "$fb_al_new"
    fi
}

fb_rollback() (
    fb_rb_failed=0
    for fb_rb_id in $(sort -rn "$fb_work/order"); do
        IFS="$fb_tab" read -r fb_rb_type fb_rb_path fb_rb_before fb_rb_after fb_rb_backup < "$fb_work/journal/$fb_rb_id"
        if ! fb_no_links "${fb_rb_path%/*}"; then fb_rb_failed=1; continue; fi
        fb_rb_current=-
        if [ "$fb_rb_type" = link ]; then
            if [ -L "$fb_rb_path" ]; then
                fb_rb_current=$(readlink "$fb_rb_path") || { fb_rb_failed=1; continue; }
            elif [ -e "$fb_rb_path" ]; then
                fb_rb_current=unexpected
            fi
        elif [ -L "$fb_rb_path" ]; then
            fb_rb_current=unexpected
        elif [ -f "$fb_rb_path" ]; then
            fb_rb_current=$(fb_hash "$fb_rb_path") || { fb_rb_failed=1; continue; }
        elif [ -e "$fb_rb_path" ]; then
            fb_rb_current=unexpected
        fi
        [ "$fb_rb_current" != "$fb_rb_before" ] || continue
        if [ "$fb_rb_current" != "$fb_rb_after" ]; then
            printf 'facet install: refusing rollback over a changed path: %s\n' "$fb_rb_path" >&2
            fb_rb_failed=1
            continue
        fi
        if [ "$fb_rb_before" = - ]; then
            rm "$fb_rb_path" || fb_rb_failed=1
        elif [ "$fb_rb_type" = link ]; then
            ln -s "$fb_rb_before" "$fb_rb_path" || fb_rb_failed=1
        else
            if ! fb_check_file "$fb_rb_backup" "$fb_rb_before"; then fb_rb_failed=1; continue; fi
            mkdir -p "${fb_rb_path%/*}" || { fb_rb_failed=1; continue; }
            fb_rb_temp=$(mktemp "${fb_rb_path%/*}/.facet-install.XXXXXXXX") ||
                { fb_rb_failed=1; continue; }
            printf '%s\n' "$fb_rb_temp" >> "$fb_work/temporaries"
            cp -p "$fb_rb_backup" "$fb_rb_temp" && mv -f "$fb_rb_temp" "$fb_rb_path" ||
                fb_rb_failed=1
        fi
    done
    return "$fb_rb_failed"
)

fb_cleanup() {
    fb_cl_status=$1
    set +e
    if [ -n "$fb_work" ]; then
        if [ "$fb_committed" = no ] && [ -s "$fb_work/order" ]; then
            if ! fb_rollback; then
                printf 'facet install: recovery required; backups/journal retained at %s; lock retained at %s\n' \
                    "$fb_work" "$fb_lock" >&2
                return 1
            fi
        fi
        while IFS= read -r fb_cl_temp; do
            if [ -e "$fb_cl_temp" ] || [ -L "$fb_cl_temp" ]; then
                rm "$fb_cl_temp" || fb_cl_status=1
            fi
        done < "$fb_work/temporaries"
        if [ "$fb_locked" = yes ]; then
            rmdir "$fb_lock" || fb_cl_status=1
            fb_locked=no
        fi
        if [ "$fb_committed" = no ]; then
            sort -r "$fb_work/directories.created" > "$fb_work/directories.cleanup"
            while IFS= read -r fb_cl_dir; do
                if [ -d "$fb_cl_dir" ] && [ -z "$(ls -A "$fb_cl_dir")" ]; then
                    rmdir "$fb_cl_dir" || fb_cl_status=1
                fi
            done < "$fb_work/directories.cleanup"
        fi
        # Only this private mktemp tree, never an installation/home/state tree.
        find "$fb_work" -depth ! -type d -exec rm -f {} + &&
            find "$fb_work" -depth -type d -exec rmdir {} + || fb_cl_status=1
        fb_work=
    fi
    return "$fb_cl_status"
}

# Remove folders a removal left empty, up to but not including $2.
fb_prune() (
    fb_pr_dir=${1%/*}
    while [ "$fb_pr_dir" != "$2" ] && fb_under "$fb_pr_dir" "$2"; do
        [ -d "$fb_pr_dir" ] && [ ! -L "$fb_pr_dir" ] && [ -z "$(ls -A "$fb_pr_dir")" ] || break
        rmdir "$fb_pr_dir" || break
        fb_pr_dir=${fb_pr_dir%/*}
    done
)

# Plan lines: file TAB path TAB old TAB new TAB source TAB mode TAB base, or
# link TAB path TAB old TAB new; "-" means absent.
fb_plan_file() {
    printf 'file\t%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" "$6" >> "$fb_work/plan"
}

fb_note() {
    printf '%s\n' "$*" >> "$fb_work/notes"
}

fb_mode_of() {
    case "$1" in bin/facet|bin/facet-app|kernel/compa-kernel|Facet.app/Contents/MacOS/Facet) printf '755\n' ;; *) printf '644\n' ;; esac
}

# Plan removals for one owned place; edited files are kept and noted.
fb_plan_place_removal() {
    fb_pr_id=$1 fb_pr_dir=$2
    awk -F '\t' -v id="$fb_pr_id" '$1==id {print $2 "\t" $3}' "$fb_work/owned.files" > "$fb_work/place.files"
    while IFS="$fb_tab" read -r fb_pr_rel fb_pr_hash; do
        if fb_same_file "$fb_pr_dir/$fb_pr_rel" "$fb_pr_hash"; then
            fb_plan_file "$fb_pr_dir/$fb_pr_rel" "$fb_pr_hash" - - 644 "$fb_pr_dir"
        elif [ -e "$fb_pr_dir/$fb_pr_rel" ] || [ -L "$fb_pr_dir/$fb_pr_rel" ]; then
            fb_note "Kept $fb_pr_dir/$fb_pr_rel: it was changed since installation"
        fi
    done < "$fb_work/place.files"
}

fb_place_unchanged() {
    awk -F '\t' -v id="$1" '$1==id {print $2 "\t" $3}' "$fb_work/owned.files" > "$fb_work/place.check"
    while IFS="$fb_tab" read -r fb_pu_rel fb_pu_hash; do
        fb_same_file "$2/$fb_pu_rel" "$fb_pu_hash" || return 1
    done < "$fb_work/place.check"
    return 0
}

fb_write_receipt() {
    {
        printf 'format\tfacet-install-v1\nversion\t%s\ncommit\t%s\nrepository\t%s\ntarget\t%s\nroot\t%s\n' \
            "$fb_version" "$fb_commit" "$fb_repository" "$fb_target" "$fb_root"
        for fb_wr_product in core app; do
            if fb_has_line "$fb_work/new.products" "$fb_wr_product"; then printf 'product\t%s\n' "$fb_wr_product"; fi
        done
        awk '{print "place\t" $0}' "$fb_work/new.places"
        awk -F '\t' '$1=="root" {print "file\t" $0}' "$fb_work/new.files" | sort
        awk -F '\t' '$1!="root" {print "file\t" $0}' "$fb_work/new.files"
        awk '{print "link\t" $0}' "$fb_work/new.links"
    } > "$fb_work/receipt.new"
    fb_limit "$fb_work/receipt.new" "$fb_metadata_limit"
}

# Run the installed facet; a failure is reported by the caller.
fb_facet() {
    "$fb_root/bin/facet" "$@"
}

facet_install_main() (
    set -efu
    umask 077
    LC_ALL=C
    export LC_ALL
    unset CDPATH TAR_OPTIONS GZIP UNZIP ZIPINFO
    fb_tab=$(printf '\t')
    fb_metadata_limit=1048576
    fb_archive_limit=268435456
    fb_mode= fb_requested_version=latest fb_repository=kayiik/facet fb_components=ask
    fb_distribution= fb_root_arg= fb_project= fb_harness_args=
    fb_operation=install fb_path_links=yes fb_launch=yes fb_open=yes fb_dry_run=no
    fb_cwd=$(pwd -P) || fb_fail "Cannot resolve the current directory"
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --mode|--version|--install-dir|--project|--harness|--distribution-dir|--repository|--components)
                [ "$#" -ge 2 ] && [ -n "$2" ] || fb_fail "Missing value for $1"
                case "$2" in -*) fb_fail "Missing value for $1 (received option $2)" ;; esac
                case "$1" in
                    --mode) fb_mode=$2 ;;
                    --version) fb_requested_version=$2 ;;
                    --install-dir) fb_root_arg=$2 ;;
                    --project) fb_project=$2 ;;
                    --harness) fb_harness_args="$fb_harness_args,$2" ;;
                    --distribution-dir) fb_distribution=$2 ;;
                    --repository) fb_repository=$2 ;;
                    --components) fb_components=$2 ;;
                esac
                shift 2 ;;
            --no-path) fb_path_links=no; shift ;;
            --no-launch) fb_launch=no; shift ;;
            --no-open) fb_open=no; shift ;;
            --dry-run) fb_dry_run=yes; shift ;;
            --upgrade|--verify|--uninstall)
                [ "$fb_operation" = install ] || fb_fail "Choose only one of --upgrade, --verify, --uninstall"
                fb_operation=${1#--}; shift ;;
            --help|-h)
                printf '%s\n' \
                    'Usage: sh install.sh [--mode app|core|bundle] [--harness NAME] [--project DIR] [--version latest|VERSION]' \
                    '  --mode app (default)    facet and the Facet App, with a Start entry' \
                    '  --mode core             facet only' \
                    '  --mode bundle           facet, wired into each --harness claude|codex|copilot|opencode|compa' \
                    '  --project DIR           Wire the CLIs for this project instead of for you' \
                    '  --components ask|defaults|all|none   What facet setup installs at the end (default: ask)' \
                    '  --install-dir DIR       Default: $HOME/.local/lib/facet' \
                    '  --distribution-dir DIR  Use a local copy of a release; never download' \
                    '  --repository OWNER/NAME Default: kayiik/facet; no ambient authentication' \
                    '  --no-path --no-launch --no-open --dry-run' \
                    '  --upgrade | --verify | --uninstall   (--uninstall --harness NAME removes only those wirings)' \
                    'Needs only native tools: curl, tar, gzip and sha256sum or shasum. No Python, Go or Node.' \
                    'Install paths must not overlap the App data, FACET_HOME or an explicit COMPA_HOME.' \
                    'Checksums provide integrity, not cryptographic authenticity. Trust the download channel.'
                exit 0 ;;
            *) fb_fail "Unknown option: $1" ;;
        esac
    done
    case "$fb_mode" in ''|app|core|bundle) ;; *) fb_fail "Unsupported mode: $fb_mode (use app, core or bundle)" ;; esac
    case "$fb_components" in ask|defaults|all|none) ;; *) fb_fail "Unsupported --components $fb_components (use ask, defaults, all or none)" ;; esac
    fb_harnesses=$(printf '%s\n' "$fb_harness_args" | awk -F ',' '{
        for (i = 1; i <= NF; i++) { gsub(/^ +| +$/, "", $i); if ($i != "" && !seen[$i]++) out = out (out == "" ? "" : ",") $i }
        print out
    }')
    for fb_name in $(printf '%s\n' "$fb_harnesses" | tr ',' ' '); do
        case "$fb_name" in claude|codex|copilot|opencode|compa) ;; *) fb_fail "Unknown harness '$fb_name'; use claude, codex, copilot, opencode or compa" ;; esac
    done
    case "$fb_operation" in
        install|upgrade)
            [ "$fb_mode" != bundle ] || [ -n "$fb_harnesses" ] || fb_fail "--mode bundle needs --harness claude, codex, copilot, opencode or compa"
            [ -z "$fb_harnesses" ] || [ "$fb_mode" = bundle ] || fb_fail "--harness goes with --mode bundle" ;;
        verify)
            [ -z "$fb_harnesses" ] && [ -z "$fb_project" ] ||
                fb_fail "--verify checks the whole installation; leave out --harness and --project" ;;
    esac
    [ -z "$fb_project" ] || [ -n "$fb_harnesses" ] || fb_fail "--project goes with --harness"
    printf '%s\n' "$fb_repository" | awk '
        NR!=1 || $0 !~ /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/ {bad=1}
        END {exit bad}
    ' || fb_fail "Repository must be an explicit owner/name, not a URL or path"
    if [ "$fb_requested_version" != latest ]; then
        fb_requested_version=${fb_requested_version#v}
        printf '%s\n' "$fb_requested_version" | awk '
            NR!=1 || $0 !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/ {bad=1}
            END {exit bad}
        ' || fb_fail "Invalid release version"
    fi
    [ -n "${HOME:-}" ] || fb_fail "HOME is required for a per-user installation"
    fb_home=$(fb_path "$HOME")
    fb_root=$(fb_path "${fb_root_arg:-$fb_home/.local/lib/facet}")
    fb_bin=$fb_home/.local/bin
    fb_receipt=$fb_root/install-receipt.tsv
    fb_lock=$fb_root.facet-install.lock
    [ "$fb_root" != / ] && [ "$fb_root" != "$fb_home" ] || fb_fail "Install into a dedicated directory, not / or HOME"
    fb_no_links "$fb_home"
    fb_no_links "$fb_root"
    [ -d "$fb_home" ] || fb_fail "HOME must be an existing directory"
    [ ! -e "$fb_root" ] || [ -d "$fb_root" ] || fb_fail "Install directory is not a directory"
    fb_wire_scope=
    if [ -n "$fb_project" ]; then
        fb_project=$(fb_path "$fb_project")
        fb_no_links "$fb_project"
        [ -d "$fb_project" ] || fb_fail "--project must be an existing folder"
        if fb_under "$fb_root" "$fb_project" || fb_under "$fb_project" "$fb_root"; then
            fb_fail "The installation folder and the project must not overlap"
        fi
    fi
    if [ -n "$fb_distribution" ]; then
        fb_distribution=$(fb_path "$fb_distribution")
        fb_no_links "$fb_distribution"
        [ -d "$fb_distribution" ] || fb_fail "--distribution-dir must be an existing directory"
        if fb_under "$fb_root" "$fb_distribution" || fb_under "$fb_distribution" "$fb_root"; then
            fb_fail "Installation and distribution directories must not overlap"
        fi
    fi
    [ ! -e "$fb_lock" ] && [ ! -L "$fb_lock" ] ||
        fb_fail "An installation operation or stale lock exists: $fb_lock"
    fb_system=$(uname -s) || fb_fail "Cannot determine native system"
    fb_machine=$(uname -m) || fb_fail "Cannot determine native architecture"
    case "$fb_system" in Linux) fb_system=linux ;; Darwin) fb_system=darwin ;;
        *) fb_fail "Unsupported native system: $fb_system (use install.ps1 on Windows)" ;; esac
    case "$fb_machine" in x86_64|amd64) fb_arch=amd64 ;; arm64|aarch64) fb_arch=arm64 ;;
        *) fb_fail "Unsupported native architecture: $fb_machine" ;; esac
    fb_target=$fb_system/$fb_arch
    for fb_command in awk sort cut cmp head wc cat mkdir mktemp chmod cp mv ln rm rmdir find readlink ls tr; do
        command -v "$fb_command" >/dev/null 2>&1 || fb_fail "Required native tool missing: $fb_command"
    done
    if command -v sha256sum >/dev/null 2>&1; then fb_hasher=sha256sum
    elif command -v shasum >/dev/null 2>&1; then fb_hasher=shasum
    else fb_fail "SHA-256 verification requires sha256sum or shasum; nothing was installed"; fi
    fb_protect_state
    fb_temp_parent=$(CDPATH= cd -P "${TMPDIR:-/tmp}" && pwd -P) ||
        fb_fail "Temporary directory must already exist"
    fb_temp_parent=$(fb_path "$fb_temp_parent")
    fb_no_links "$fb_temp_parent"
    if fb_under "$fb_temp_parent" "$fb_root" || fb_under "$fb_temp_parent" "$fb_bin" ||
        { [ -n "$fb_project" ] && fb_under "$fb_temp_parent" "$fb_project"; } ||
        { [ -n "$fb_distribution" ] && fb_under "$fb_temp_parent" "$fb_distribution"; }; then
        fb_fail "Staging requires an external temporary directory; set TMPDIR outside project, release and installation paths"
    fi
    fb_work=$(mktemp -d "$fb_temp_parent/facet-install.XXXXXXXX") ||
        fb_fail "Cannot create safe external staging directory"
    fb_locked=no fb_committed=no fb_sequence=0
    : > "$fb_work/order"
    : > "$fb_work/temporaries"
    : > "$fb_work/directories.created"
    trap 'fb_exit=$?; trap - 0 HUP INT TERM; fb_cleanup "$fb_exit"; exit $?' 0
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    mkdir "$fb_work/distribution" "$fb_work/backups" "$fb_work/journal"
    for fb_part in plan notes new.products new.places new.files new.links; do
        : > "$fb_work/$fb_part"
    done
    fb_read_receipt
    [ "$fb_operation" = install ] || [ "$fb_owned" != none ] ||
        fb_fail "No Facet installation in $fb_root; nothing to $fb_operation"
    fb_version=$(fb_owned_meta version)

    if [ "$fb_operation" = verify ]; then
        : > "$fb_work/problems"
        while IFS="$fb_tab" read -r fb_base fb_rel fb_hash; do
            if [ "$fb_base" = root ]; then fb_dir=$fb_root
            else fb_dir=$(awk -F '\t' -v id="$fb_base" '$1==id {print $6}' "$fb_work/owned.places"); fi
            fb_same_file "$fb_dir/$fb_rel" "$fb_hash" || printf '%s\n' "$fb_dir/$fb_rel" >> "$fb_work/problems"
        done < "$fb_work/owned.files"
        while IFS="$fb_tab" read -r fb_link fb_link_target; do
            [ -L "$fb_link" ] && [ "$(readlink "$fb_link")" = "$fb_link_target" ] ||
                printf '%s\n' "$fb_link" >> "$fb_work/problems"
        done < "$fb_work/owned.links"
        if [ -s "$fb_work/problems" ]; then
            printf 'facet install: These Facet files were changed or removed since installation:\n' >&2
            awk '{print "  " $0}' "$fb_work/problems" >&2
            exit 1
        fi
        printf 'Verified Facet %s (%s) in %s: every owned file and launch link matches the receipt\n' \
            "$fb_version" "$(awk 'BEGIN {ORS=""} {print (NR>1?", ":"") $0}' "$fb_work/owned.products")" "$fb_root"
        fb_committed=yes
        exit 0
    fi

    if [ "$fb_operation" = uninstall ]; then
        if [ -n "$fb_harnesses" ]; then
            if [ "$fb_dry_run" = yes ]; then
                printf 'Dry-run: facet wire --remove %s%s\n' "$fb_harnesses" "${fb_project:+ --project $fb_project}"
                fb_committed=yes
                exit 0
            fi
            if [ -n "$fb_project" ]; then
                fb_facet wire --remove "$fb_harnesses" --project "$fb_project" || fb_fail "facet wire --remove $fb_harnesses failed"
            else
                fb_facet wire --remove "$fb_harnesses" || fb_fail "facet wire --remove $fb_harnesses failed"
            fi
            printf 'Removed Facet from %s\n' "$fb_harnesses"
            fb_committed=yes
            exit 0
        fi
        cp "$fb_work/owned.products" "$fb_work/new.products"
        cp "$fb_work/owned.links" "$fb_work/new.links"
        fb_commit=$(fb_owned_meta commit)
        fb_target=$(fb_owned_meta target)
        while IFS="$fb_tab" read -r fb_base fb_rel fb_hash; do
            [ "$fb_base" = root ] || continue
            fb_check_file "$fb_root/$fb_rel" "$fb_hash"
            fb_plan_file "$fb_root/$fb_rel" "$fb_hash" - - 644 "$fb_root"
        done < "$fb_work/owned.files"
        while IFS="$fb_tab" read -r fb_id fb_kind fb_name fb_place_scope fb_place_version fb_dir; do
            fb_plan_place_removal "$fb_id" "$fb_dir"
        done < "$fb_work/owned.places"
        while IFS="$fb_tab" read -r fb_link fb_link_target; do
            if [ -L "$fb_link" ] && [ "$(readlink "$fb_link")" = "$fb_link_target" ]; then
                printf 'link\t%s\t%s\t-\n' "$fb_link" "$fb_link_target" >> "$fb_work/plan"
            else
                fb_note "Kept $fb_link: it no longer points at this Facet"
            fi
        done < "$fb_work/owned.links"
        fb_plan_file "$fb_receipt" "$fb_old_receipt" - - 644 "$fb_root"
    else
        for fb_command in tar gzip; do
            command -v "$fb_command" >/dev/null 2>&1 || fb_fail "Required native tool missing: $fb_command"
        done
        if [ -z "$fb_distribution" ]; then
            command -v curl >/dev/null 2>&1 || fb_fail "Downloading requires curl; nothing was installed"
        fi
        fb_url=https://github.com/$fb_repository/releases/latest/download
        [ "$fb_requested_version" = latest ] ||
            fb_url=https://github.com/$fb_repository/releases/download/v$fb_requested_version
        fb_fetch SHA256SUMS "$fb_metadata_limit"
        fb_checksums
        for fb_metadata in build-manifest.json manifest.tsv; do
            fb_fetch "$fb_metadata" "$fb_metadata_limit"
            fb_checksum "$fb_metadata"
        done
        fb_manifest
        fb_owned_version=$fb_version
        fb_version=$(awk -F '\t' '$1=="version"{print $2}' "$fb_work/distribution/manifest.tsv")
        fb_commit=$(awk -F '\t' '$1=="commit"{print $2}' "$fb_work/distribution/manifest.tsv")
        [ "$fb_requested_version" = latest ] || [ "$fb_requested_version" = "$fb_version" ] ||
            fb_fail "Requested version differs from the verified release manifest"
        if [ "$fb_operation" = install ] && [ "$fb_owned" = current ] && [ "$fb_owned_version" != "$fb_version" ]; then
            fb_fail "Facet $fb_owned_version is installed. Run with --upgrade to move it to $fb_version, or add --version $fb_owned_version to add to it."
        fi
        case "$fb_mode" in
            core|bundle) fb_requested="core" ;;
            app) fb_requested="core app" ;;
            *) if [ "$fb_operation" = install ]; then fb_requested="core app"; else fb_requested=; fi ;;
        esac
        for fb_product in core app; do
            if fb_has_line "$fb_work/owned.products" "$fb_product" ||
                case " $fb_requested " in *" $fb_product "*) true ;; *) false ;; esac; then
                printf '%s\n' "$fb_product" >> "$fb_work/new.products"
            fi
        done
        : > "$fb_work/desired.files"
        while IFS= read -r fb_product; do
            fb_archive=$(fb_select "$fb_product" "$fb_target")
            fb_fetch "$fb_archive" "$fb_archive_limit"
            fb_checksum "$fb_archive"
            case "$fb_archive" in
                *.tar.gz) fb_extract_tar "$fb_product" "$fb_archive" "$fb_work/payload-$fb_product" ;;
                *) fb_fail "Unexpected archive for $fb_target: $fb_archive" ;;
            esac
            while IFS="$fb_tab" read -r fb_rel fb_hash; do
                printf '%s\t%s\t%s\t%s\n' "$fb_rel" "$fb_hash" "$fb_work/payload-$fb_product/$fb_rel" "$(fb_mode_of "$fb_rel")" \
                    >> "$fb_work/desired.files"
            done < "$fb_work/$fb_product.files"
        done < "$fb_work/new.products"
        awk -F '\t' '{name=tolower($1); if (seen[name]++) bad=1; n=split($1,parts,"/"); prefix=""
            for (i=1;i<n;i++) {prefix=prefix (i>1?"/":"") tolower(parts[i]); dirs[prefix]=1}}
            END {for (name in seen) if (name in dirs) bad=1; exit bad}' "$fb_work/desired.files" ||
            fb_fail "Products collide on an installation path"
        while IFS="$fb_tab" read -r fb_rel fb_hash fb_source fb_permissions; do
            fb_old=$(awk -F '\t' -v n="$fb_rel" '$1=="root" && $2==n {print $3}' "$fb_work/owned.files")
            printf 'root\t%s\t%s\n' "$fb_rel" "$fb_hash" >> "$fb_work/new.files"
            if [ -n "$fb_old" ] && [ "$fb_old" = "$fb_hash" ]; then
                fb_check_file "$fb_root/$fb_rel" "$fb_old"
                continue
            fi
            fb_plan_file "$fb_root/$fb_rel" "${fb_old:--}" "$fb_hash" "$fb_source" "$fb_permissions" "$fb_root"
        done < "$fb_work/desired.files"
        while IFS="$fb_tab" read -r fb_base fb_rel fb_hash; do
            [ "$fb_base" = root ] || continue
            awk -F '\t' -v n="$fb_rel" '$1==n {found=1} END {exit (!found)}' "$fb_work/desired.files" ||
                fb_plan_file "$fb_root/$fb_rel" "$fb_hash" - - 644 "$fb_root"
        done < "$fb_work/owned.files"
        fb_next_id=$(awk -F '\t' 'BEGIN {max=0} {if ($1+0 > max) max=$1+0} END {print max+1}' "$fb_work/owned.places")
        if fb_has_line "$fb_work/new.products" app; then
            fb_start=$(fb_start_dir)
            fb_no_links "$fb_start"
            fb_write_start "$fb_work/start" "$fb_work/start.files"
            fb_start_id=$(awk -F '\t' '$2=="start" {print $1}' "$fb_work/owned.places")
            if [ -n "$fb_start_id" ] && ! fb_place_unchanged "$fb_start_id" "$fb_start" &&
                awk -F '\t' -v id="$fb_start_id" '$1==id {print $2}' "$fb_work/owned.files" |
                { while IFS= read -r fb_rel; do [ -e "$fb_start/$fb_rel" ] && exit 0; done; exit 1; }; then
                fb_note "Kept the Start entry in $fb_start: it was changed since installation"
                awk -F '\t' -v id="$fb_start_id" '$1==id' "$fb_work/owned.places" >> "$fb_work/new.places"
                awk -F '\t' -v id="$fb_start_id" '$1==id' "$fb_work/owned.files" >> "$fb_work/new.files"
            else
                [ -n "$fb_start_id" ] || { fb_start_id=$fb_next_id; fb_next_id=$((fb_next_id + 1)); }
                printf '%s\tstart\t-\t-\t%s\t%s\n' "$fb_start_id" "$fb_version" "$fb_start" >> "$fb_work/new.places"
                while IFS="$fb_tab" read -r fb_rel fb_permissions; do
                    fb_hash=$(fb_hash "$fb_work/start/$fb_rel")
                    fb_old=$(awk -F '\t' -v id="$fb_start_id" -v n="$fb_rel" '$1==id && $2==n {print $3}' "$fb_work/owned.files")
                    if [ -n "$fb_old" ] && ! fb_same_file "$fb_start/$fb_rel" "$fb_old"; then fb_old=; fi
                    printf '%s\t%s\t%s\n' "$fb_start_id" "$fb_rel" "$fb_hash" >> "$fb_work/new.files"
                    [ "$fb_old" != "$fb_hash" ] || continue
                    fb_plan_file "$fb_start/$fb_rel" "${fb_old:--}" "$fb_hash" "$fb_work/start/$fb_rel" "$fb_permissions" "$fb_start"
                done < "$fb_work/start.files"
            fi
        fi
        cp "$fb_work/owned.links" "$fb_work/new.links"
        if [ "$fb_path_links" = yes ]; then
            for fb_name in facet facet-app; do
                [ "$fb_name" = facet ] || fb_has_line "$fb_work/new.products" app || continue
                ! awk -F '\t' -v p="$fb_bin/$fb_name" '$1==p {found=1} END {exit (!found)}' "$fb_work/owned.links" || continue
                [ ! -e "$fb_bin/$fb_name" ] && [ ! -L "$fb_bin/$fb_name" ] ||
                    fb_fail "Refusing unowned PATH entry: $fb_bin/$fb_name (use --no-path)"
                printf 'link\t%s\t-\t%s\n' "$fb_bin/$fb_name" "$fb_root/bin/$fb_name" >> "$fb_work/plan"
                printf '%s\t%s\n' "$fb_bin/$fb_name" "$fb_root/bin/$fb_name" >> "$fb_work/new.links"
            done
        fi
        fb_write_receipt
        fb_plan_file "$fb_receipt" "${fb_old_receipt:--}" "$(fb_hash "$fb_work/receipt.new")" "$fb_work/receipt.new" 644 "$fb_root"
    fi

    # Every planned change is checked before anything is written.
    while IFS="$fb_tab" read -r fb_type fb_path fb_old fb_new fb_source fb_permissions fb_base; do
        if [ "$fb_type" = file ]; then
            fb_no_links "$fb_path"
            if [ "$fb_old" = - ]; then
                [ ! -e "$fb_path" ] && [ ! -L "$fb_path" ] ||
                    fb_fail "Refusing to overwrite a file Facet does not own: $fb_path"
            else
                fb_check_file "$fb_path" "$fb_old"
            fi
        else
            fb_no_links "${fb_path%/*}"
            [ ! -e "${fb_path%/*}" ] || [ -d "${fb_path%/*}" ] || fb_fail "PATH link directory is occupied: ${fb_path%/*}"
        fi
    done < "$fb_work/plan"

    if [ "$fb_dry_run" = yes ]; then
        printf 'Dry-run: %s Facet %s (%s) in %s\n' "$fb_operation" "$fb_version" \
            "$(awk 'BEGIN {ORS=""} {print (NR>1?", ":"") $0}' "$fb_work/new.products")" "$fb_root"
        awk -F '\t' '{
            action = ($3=="-") ? "create" : (($4=="-") ? "remove" : "replace")
            print "  " action " " $2
        }' "$fb_work/plan"
        if [ "$fb_operation" = uninstall ]; then
            printf 'Afterwards: facet wire --remove all, facet remove all\n'
        else
            [ "$fb_operation" != upgrade ] || printf 'Afterwards: facet wire --refresh\n'
            [ -z "$fb_harnesses" ] || printf 'Afterwards: facet wire %s\n' "$fb_harnesses"
            [ "$fb_components" = none ] || printf 'Afterwards: facet setup (%s)\n' "$fb_components"
        fi
        cat "$fb_work/notes"
        printf 'Nothing was written, extracted into place, probed or launched; no PATH link or profile changed.\n'
        printf 'Checksums establish integrity, not cryptographic authenticity.\n'
        fb_committed=yes
        exit 0
    fi
    if [ "$fb_operation" = uninstall ]; then
        # The wirings and components are facet's own to remove, before facet goes.
        if [ -x "$fb_root/bin/facet" ]; then
            fb_facet wire --remove all </dev/null || fb_note "facet wire --remove all did not finish; check facet wire --status"
            fb_facet remove all </dev/null || fb_note "Some components were left in $fb_root/dependencies"
        fi
    else
        chmod 755 "$fb_work/payload-core/bin/facet"
        fb_probe=$("$fb_work/payload-core/bin/facet" version </dev/null 2>"$fb_work/probe.stderr") ||
            fb_fail "Verified core version probe failed"
        [ "$fb_probe" = "facet v$fb_version" ] && [ ! -s "$fb_work/probe.stderr" ] ||
            fb_fail "Core version probe does not match the verified manifest"
        if fb_has_line "$fb_work/new.products" app; then
            chmod 755 "$fb_work/payload-app/bin/facet-app"
            fb_probe=$("$fb_work/payload-app/bin/facet-app" version </dev/null 2>"$fb_work/probe.stderr") ||
                fb_fail "Verified app version probe failed"
            [ "$fb_probe" = "facet-app $fb_version" ] && [ ! -s "$fb_work/probe.stderr" ] ||
                fb_fail "App version probe does not match the verified manifest"
        fi
    fi
    fb_mkdir "${fb_root%/*}"
    mkdir "$fb_lock" || fb_fail "Cannot acquire installation lock: $fb_lock"
    fb_locked=yes
    # The receipt goes last, so a failure anywhere before it leaves the old receipt describing the old files.
    awk -F '\t' -v receipt="$fb_receipt" '$2!=receipt' "$fb_work/plan" > "$fb_work/plan.ordered"
    awk -F '\t' -v receipt="$fb_receipt" '$2==receipt' "$fb_work/plan" >> "$fb_work/plan.ordered"
    while IFS="$fb_tab" read -r fb_type fb_path fb_old fb_new fb_source fb_permissions fb_base; do
        if [ "$fb_type" = file ]; then
            fb_apply_file "$fb_path" "$fb_source" "$fb_old" "$fb_new" "$fb_permissions"
        else
            fb_apply_link "$fb_path" "$fb_old" "$fb_new"
        fi
    done < "$fb_work/plan.ordered"
    fb_committed=yes
    while IFS="$fb_tab" read -r fb_type fb_path fb_old fb_new fb_source fb_permissions fb_base; do
        [ "$fb_type" = file ] && [ "$fb_new" = - ] || continue
        fb_prune "$fb_path" "$fb_base"
    done < "$fb_work/plan"
    if [ "$fb_operation" = uninstall ]; then
        for fb_dir in "$fb_root/dependencies" "$fb_root"; do
            if [ -d "$fb_dir" ] && [ -z "$(ls -A "$fb_dir")" ]; then rmdir "$fb_dir" || true; fi
        done
        printf 'Removed Facet'\''s wirings, its components, its unchanged files and launch links. Facet'\''s state (~/.facet), the App'\''s data and files Facet does not own were kept.\n'
        cat "$fb_work/notes"
        exit 0
    fi
    printf 'Installed Facet %s (%s) in %s\n' "$fb_version" \
        "$(awk 'BEGIN {ORS=""} {print (NR>1?", ":"") $0}' "$fb_work/new.products")" "$fb_root"
    cat "$fb_work/notes"
    printf 'Checksums establish integrity, not cryptographic authenticity; trust the release channel.\n'
    printf 'facet: %s/bin/facet\n' "$fb_root"
    if [ -s "$fb_work/new.links" ]; then
        printf 'Owned launch links: %s (no PATH or shell profile was changed)\n' "$fb_bin"
    fi
    if [ "$fb_operation" = upgrade ]; then
        fb_facet wire --refresh </dev/null ||
            printf 'facet install: facet wire --refresh did not finish; run it again, or check facet wire --status\n' >&2
    fi
    if [ -n "$fb_harnesses" ]; then
        if [ -n "$fb_project" ]; then
            fb_facet wire "$fb_harnesses" --project "$fb_project" </dev/null || fb_fail "facet wire $fb_harnesses failed"
        else
            fb_facet wire "$fb_harnesses" </dev/null || fb_fail "facet wire $fb_harnesses failed"
        fi
    fi
    fb_setup_failed=no
    case "$fb_components" in
        none) ;;
        defaults) fb_facet setup --yes </dev/null || fb_setup_failed=yes ;;
        all) fb_facet setup --yes --all </dev/null || fb_setup_failed=yes ;;
        *)
            # curl | sh reads the script from stdin; the person answers on the terminal.
            if (: </dev/tty) 2>/dev/null; then
                fb_facet setup </dev/tty || fb_setup_failed=yes
            else
                fb_facet setup --yes </dev/null || fb_setup_failed=yes
            fi ;;
    esac
    [ "$fb_setup_failed" = no ] ||
        printf 'facet install: not every component was installed; facet components shows what is missing, and facet install adds it.\n' >&2
    if fb_has_line "$fb_work/new.products" app && [ "$fb_launch" = yes ] &&
        { [ -z "$fb_mode" ] || [ "$fb_mode" = app ]; }; then
        fb_cleanup 0 || exit 1
        trap - 0 HUP INT TERM
        if [ "$fb_open" = no ]; then
            exec "$fb_root/bin/facet-app" --no-browser </dev/null
        else
            exec "$fb_root/bin/facet-app" </dev/null
        fi
    fi
)

facet_install_main "$@"
