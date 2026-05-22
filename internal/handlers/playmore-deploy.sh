#!/usr/bin/env bash
set -euo pipefail

VERSION="1.0.0"
CONFIG_FILE=".playmore"
GLOBAL_CONFIG="${HOME}/.config/playmore/config"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

die()     { echo -e "${RED}Error:${NC} $*" >&2; exit 1; }
info()    { echo -e "${CYAN}→${NC} $*"; }
success() { echo -e "${GREEN}✓${NC} $*"; }
warn()    { echo -e "${YELLOW}⚠${NC} $*"; }

usage() {
    cat <<'USAGE'
playmore-deploy — CLI tool for deploying games to PlayMore

Usage:
  playmore-deploy init    [--server URL] [--key KEY]     Configure server and API key
  playmore-deploy push    [--file PATH] [--title T] ...  Upload or re-upload game
  playmore-deploy update  [--title T] [--desc D] ...     Update game metadata
  playmore-deploy devlog  --title T [--content C]        Post a devlog entry
  playmore-deploy status                                 Show current config and game info

Options for push:
  --file PATH       Game file (.html, .zip) or directory to zip
  --title TITLE     Game title (required for first push)
  --genre GENRE     Game genre (required for first push)
  --desc TEXT       Game description
  --tags TAGS       Comma-separated tags
  --cover PATH      Cover image file
  --webgpu          Mark as WebGPU game

Options for update:
  --title TITLE     Update title
  --desc TEXT       Update description
  --genre GENRE     Update genre
  --price PRICE     Update price
  --tags TAGS       Update tags
  --video URL       YouTube embed URL

Options for devlog:
  --title TITLE     Devlog title (required)
  --content TEXT    Devlog content (or reads from stdin)
  --file PATH       Read content from file

Config is saved to .playmore in the current directory.

Install:
  curl -fsSL https://YOUR_SERVER/deploy.sh -o playmore-deploy && chmod +x playmore-deploy
USAGE
    exit 0
}

load_config() {
    SERVER="" API_KEY="" GAME_ID="" UPLOAD_ID=""
    local file=""
    if [[ -f "$CONFIG_FILE" ]]; then file="$CONFIG_FILE"
    elif [[ -f "$GLOBAL_CONFIG" ]]; then file="$GLOBAL_CONFIG"
    fi
    [[ -n "$file" ]] || return
    while IFS='=' read -r key value; do
        value="${value#\'}" ; value="${value%\'}"
        case "$key" in
            SERVER)    SERVER="$value" ;;
            API_KEY)   API_KEY="$value" ;;
            GAME_ID)   GAME_ID="$value" ;;
            UPLOAD_ID) UPLOAD_ID="$value" ;;
        esac
    done < "$file"
}

save_config() {
    cat > "$CONFIG_FILE" <<EOF
SERVER='$SERVER'
API_KEY='$API_KEY'
GAME_ID='$GAME_ID'
UPLOAD_ID='${UPLOAD_ID:-}'
EOF
    success "Config saved to $CONFIG_FILE"
}

api_call() {
    local method="$1" path="$2"
    shift 2
    local url="${SERVER}${path}"
    local response
    response=$(curl -s -w "\n%{http_code}" -X "$method" \
        -H "Authorization: Bearer $API_KEY" \
        "$@" "$url") || die "Connection failed. Is the server running?"

    local http_code body
    http_code=$(echo "$response" | tail -1)
    body=$(echo "$response" | sed '$d')

    if [[ "$http_code" -ge 400 ]]; then
        local error
        error=$(echo "$body" | grep -o '"error":"[^"]*"' | head -1 | cut -d'"' -f4)
        die "${error:-HTTP $http_code}"
    fi
    echo "$body"
}

json_val() {
    echo "$1" | grep -o "\"$2\":\"[^\"]*\"" | head -1 | cut -d'"' -f4
}

json_val_raw() {
    echo "$1" | grep -o "\"$2\":[^,}]*" | head -1 | sed "s/\"$2\"://"
}

file_size() {
    # GNU vs BSD stat
    stat -c%s "$1" 2>/dev/null || stat -f%z "$1"
}

file_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

cmd_init() {
    local server="" key=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --server) server="$2"; shift 2 ;;
            --key)    key="$2"; shift 2 ;;
            *)        die "Unknown option: $1" ;;
        esac
    done

    if [[ -z "$server" ]]; then
        read -rp "PlayMore server URL: " server
    fi
    server="${server%/}" # strip trailing slash

    if [[ -z "$key" ]]; then
        read -rp "API key (pm_k_...): " key
    fi

    [[ "$server" == http* ]] || die "Server URL must start with http:// or https://"
    [[ "$key" == pm_k_* ]] || die "API key must start with pm_k_"

    SERVER="$server"
    API_KEY="$key"
    GAME_ID=""

    info "Verifying API key..."
    local result
    result=$(api_call GET "/api/auth/me") || exit 1
    local username
    username=$(json_val "$result" "username")
    success "Authenticated as $username"

    save_config
}

cmd_push() {
    load_config
    [[ -n "$SERVER" ]] || die "Not configured. Run: playmore-deploy init"
    [[ -n "$API_KEY" ]] || die "No API key. Run: playmore-deploy init"

    local file="" title="" genre="" desc="" tags="" cover="" webgpu="false"
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --file)   file="$2"; shift 2 ;;
            --title)  title="$2"; shift 2 ;;
            --genre)  genre="$2"; shift 2 ;;
            --desc)   desc="$2"; shift 2 ;;
            --tags)   tags="$2"; shift 2 ;;
            --cover)  cover="$2"; shift 2 ;;
            --webgpu) webgpu="true"; shift ;;
            *)        die "Unknown option: $1" ;;
        esac
    done

    # Auto-detect file if not specified
    if [[ -z "$file" ]]; then
        if [[ -f "index.html" ]]; then
            file="index.html"
        elif compgen -G "*.zip" > /dev/null; then
            file=$(ls -t *.zip | head -1)
        elif compgen -G "*.html" > /dev/null; then
            file=$(ls -t *.html | head -1)
        else
            die "No game file found. Use --file or place index.html/zip in current directory."
        fi
        info "Auto-detected: $file"
    fi

    # If file is a directory, zip it
    if [[ -d "$file" ]]; then
        command -v zip >/dev/null || die "'zip' is required to compress directories. Install it or provide a .zip file."
        local zipfile="playmore_upload_$(date +%s).zip"
        info "Zipping directory $file..."
        (cd "$file" && zip -r "../$zipfile" . -x '.*' '__MACOSX/*') || die "Failed to zip"
        file="$zipfile"
    fi

    [[ -f "$file" ]] || die "File not found: $file"

    local size sha
    size=$(file_size "$file")
    local THRESHOLD=$((64*1024*1024))

    if [[ -n "$GAME_ID" ]]; then
        if [[ $size -gt $THRESHOLD ]]; then
            info "Re-uploading via chunked pipeline ($size bytes)..."
            sha=$(file_sha256 "$file")
            TARGET_GAME_ID="$GAME_ID" \
                chunked_push "$file" "$size" "$sha" "reupload"
        else
            # Existing single-shot reupload path (unchanged)
            info "Re-uploading files to game $GAME_ID..."
            local result
            result=$(api_call POST "/api/games/$GAME_ID/reupload" \
                -F "game_file=@$file") || exit 1
            success "Game files updated!"
        fi
    else
        [[ -n "$title" ]] || read -rp "Game title: " title
        [[ -n "$genre" ]] || read -rp "Genre (action/adventure/rpg/strategy/puzzle/racing/horror/experimental): " genre
        [[ -n "$title" ]] || die "Title is required"
        [[ -n "$genre" ]] || die "Genre is required"

        if [[ $size -gt $THRESHOLD ]]; then
            info "Uploading new game via chunked pipeline: $title ($size bytes)..."
            sha=$(file_sha256 "$file")
            TITLE="$title" GENRE="$genre" DESC="$desc" TAGS="$tags" IS_WEBGPU="$webgpu" \
                chunked_push "$file" "$size" "$sha" "new_game"
            # Cover image (if specified) — out-of-band after finalize
            if [[ -n "$cover" && -f "$cover" ]]; then
                info "Uploading cover image..."
                local cover_res cover_url
                cover_res=$(api_call POST "/api/upload/image" -F "image=@$cover") || warn "cover upload failed"
                cover_url=$(json_val "$cover_res" "url")
                [[ -n "$cover_url" ]] && api_call PUT "/api/games/$GAME_ID" \
                    -H "Content-Type: application/json" \
                    -d "{\"cover_url\":\"$cover_url\"}" > /dev/null
            fi
        else
            # Existing single-shot new-game path (unchanged)
            info "Uploading new game: $title..."
            local curl_args=(-F "game_file=@$file" -F "title=$title" -F "genre=$genre" -F "is_webgpu=$webgpu")
            [[ -n "$desc" ]] && curl_args+=(-F "description=$desc")
            [[ -n "$tags" ]] && curl_args+=(-F "tags=$tags")
            [[ -n "$cover" && -f "$cover" ]] && curl_args+=(-F "cover=@$cover")

            local result
            result=$(api_call POST "/api/games" "${curl_args[@]}") || exit 1

            local new_id
            new_id=$(json_val "$result" "id")
            if [[ -n "$new_id" ]]; then
                GAME_ID="$new_id"
                save_config
                success "Game uploaded! ID: $GAME_ID"
            else
                success "Game uploaded!"
            fi
        fi
    fi

    echo ""
    echo "  View: ${SERVER}/#game/${GAME_ID:-unknown}"
}

# chunked_push uploads $file via /api/uploads/init|chunks|finalize when the file
# is larger than the single-shot threshold (called from cmd_push).
#
# Args: $1=file  $2=size  $3=sha256  $4=kind (new_game|reupload)
#       For kind=new_game: env vars TITLE, GENRE, DESC, TAGS, IS_WEBGPU
#       For kind=reupload: env var TARGET_GAME_ID
chunked_push() {
    local file="$1" size="$2" sha="$3" kind="$4"
    local upload_id="" chunk_size=""

    # If we have a stashed UPLOAD_ID from a prior run, try to resume against it.
    local resume_id="${UPLOAD_ID:-}"
    if [[ -n "$resume_id" ]]; then
        local status
        status=$(api_call GET "/api/uploads/$resume_id" 2>/dev/null) || resume_id=""
        if [[ -n "$resume_id" ]]; then
            local declared_size
            declared_size=$(json_val_raw "$status" "size" | tr -d ' ')
            if [[ "$declared_size" == "$size" ]]; then
                upload_id="$resume_id"
                chunk_size=$((8*1024*1024))
                info "Resuming previous upload $upload_id"
            else
                warn "Stashed UPLOAD_ID doesn't match this file's size — starting fresh"
                resume_id=""
            fi
        fi
    fi

    if [[ -z "$upload_id" ]]; then
        local init_body
        if [[ "$kind" == "new_game" ]]; then
            init_body=$(printf '{"filename":"%s","size":%s,"kind":"new_game","metadata":{"title":"%s","genre":"%s","description":"%s","tags":[%s],"is_webgpu":%s}}' \
                "$(basename "$file")" "$size" "${TITLE//\"/\\\"}" "${GENRE//\"/\\\"}" "${DESC//\"/\\\"}" \
                "$(printf '"%s",' ${TAGS//,/ } | sed 's/,$//')" "${IS_WEBGPU:-false}")
        else
            init_body=$(printf '{"filename":"%s","size":%s,"kind":"reupload","game_id":"%s"}' \
                "$(basename "$file")" "$size" "$TARGET_GAME_ID")
        fi
        local init
        init=$(api_call POST "/api/uploads/init" -H "Content-Type: application/json" -d "$init_body") || die "init failed"
        upload_id=$(json_val "$init" "upload_id")
        chunk_size=$(json_val_raw "$init" "chunk_size" | tr -d ' ')
        UPLOAD_ID="$upload_id"
        save_config
    fi

    # Determine missing offsets if resuming
    local status received_to=0
    if [[ -n "$resume_id" ]]; then
        status=$(api_call GET "/api/uploads/$upload_id")
        received_to=$(echo "$status" | grep -o '"received_ranges":\[\[0,[0-9]*\]\]' | grep -o '[0-9]*' | tail -1)
        # Note: this simple parser assumes the (common) single contiguous-from-zero range case.
        # For complex gap recovery, the SPA path is more robust; CLI sequential uploads almost
        # always have contiguous-from-zero received_ranges.
        [[ -z "$received_to" ]] && received_to=0
    fi

    local offset=$received_to
    local total_chunks=$(( (size + chunk_size - 1) / chunk_size ))
    local current_chunk=$(( received_to / chunk_size ))
    while [[ $offset -lt $size ]]; do
        local remaining=$((size - offset))
        local this_chunk=$chunk_size
        [[ $remaining -lt $chunk_size ]] && this_chunk=$remaining

        local skip=$((offset / chunk_size))
        current_chunk=$((current_chunk + 1))
        printf "  [%2d/%2d] uploading chunk @ offset %d (%d bytes)\n" \
            "$current_chunk" "$total_chunks" "$offset" "$this_chunk"

        local attempt
        local ok=false
        for attempt in 0 1 2 3; do
            if [[ $attempt -gt 0 ]]; then
                sleep $((1 << (attempt - 1)))
                warn "  retry $attempt"
            fi
            if [[ $remaining -ge $chunk_size ]]; then
                if dd if="$file" bs="$chunk_size" skip="$skip" count=1 status=none 2>/dev/null \
                    | curl --fail -s -X PUT --data-binary @- \
                        -H "Authorization: Bearer $API_KEY" \
                        -H "Content-Type: application/octet-stream" \
                        "${SERVER}/api/uploads/${upload_id}/chunks?offset=${offset}" > /dev/null
                then
                    ok=true; break
                fi
            else
                # Last (partial) chunk — bs=1 is slow but only for the trailing remainder
                if dd if="$file" bs=1 skip="$offset" count="$this_chunk" status=none 2>/dev/null \
                    | curl --fail -s -X PUT --data-binary @- \
                        -H "Authorization: Bearer $API_KEY" \
                        -H "Content-Type: application/octet-stream" \
                        "${SERVER}/api/uploads/${upload_id}/chunks?offset=${offset}" > /dev/null
                then
                    ok=true; break
                fi
            fi
        done
        $ok || die "chunk upload failed after 4 attempts — re-run 'playmore-deploy push' to resume"

        offset=$((offset + this_chunk))
    done

    info "Finalizing..."
    local fin
    fin=$(api_call POST "/api/uploads/$upload_id/finalize" \
            -H "Content-Type: application/json" \
            -d "{\"sha256\":\"$sha\"}") || die "finalize failed"

    # Clear stashed UPLOAD_ID on success
    UPLOAD_ID=""
    save_config

    if [[ "$kind" == "new_game" ]]; then
        local new_id
        new_id=$(json_val "$fin" "game_id")
        if [[ -n "$new_id" ]]; then
            GAME_ID="$new_id"
            save_config
            success "Game uploaded! ID: $GAME_ID"
        fi
    else
        success "Game files updated!"
    fi
}

cmd_update() {
    load_config
    [[ -n "$SERVER" ]] || die "Not configured. Run: playmore-deploy init"
    [[ -n "$GAME_ID" ]] || die "No game ID. Run 'playmore-deploy push' first."

    local title="" desc="" genre="" price="" tags="" video=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --title) title="$2"; shift 2 ;;
            --desc)  desc="$2"; shift 2 ;;
            --genre) genre="$2"; shift 2 ;;
            --price) price="$2"; shift 2 ;;
            --tags)  tags="$2"; shift 2 ;;
            --video) video="$2"; shift 2 ;;
            *)       die "Unknown option: $1" ;;
        esac
    done

    # Build JSON safely
    local json
    if command -v jq >/dev/null; then
        json=$(jq -nc \
            --arg title "$title" --arg desc "$desc" --arg genre "$genre" \
            --arg price "$price" --arg tags "$tags" --arg video "$video" \
            '{} |
            (if $title != "" then . + {title: $title} else . end) |
            (if $desc != "" then . + {description: $desc} else . end) |
            (if $genre != "" then . + {genre: $genre} else . end) |
            (if $price != "" then . + {price: ($price | tonumber)} else . end) |
            (if $tags != "" then . + {tags: ($tags | split(",") | map(gsub("^\\s+|\\s+$";"")))} else . end) |
            (if $video != "" then . + {videos: [$video]} else . end)')
    else
        json="{"
        local first=true
        _esc() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
        _add() { [[ -z "$2" ]] && return; $first || json+=","; first=false; json+="\"$1\":\"$(_esc "$2")\""; }
        _add "title" "$title"; _add "description" "$desc"; _add "genre" "$genre"
        if [[ -n "$price" ]]; then $first || json+=","; first=false; json+="\"price\":$price"; fi
        if [[ -n "$tags" ]]; then
            $first || json+=","; first=false
            local tags_json; tags_json=$(_esc "$tags" | sed 's/,/","/g')
            json+="\"tags\":[\"$tags_json\"]"
        fi
        if [[ -n "$video" ]]; then $first || json+=","; first=false; json+="\"videos\":[\"$(_esc "$video")\"]"; fi
        json+="}"
    fi

    if [[ "$json" == "{}" ]]; then
        die "No fields to update. Use --title, --desc, --genre, --price, --tags, or --video."
    fi

    info "Updating game $GAME_ID..."
    api_call PUT "/api/games/$GAME_ID" \
        -H "Content-Type: application/json" \
        -d "$json" > /dev/null || exit 1
    success "Game updated!"
}

cmd_devlog() {
    load_config
    [[ -n "$SERVER" ]] || die "Not configured. Run: playmore-deploy init"
    [[ -n "$GAME_ID" ]] || die "No game ID. Run 'playmore-deploy push' first."

    local title="" content="" content_file=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --title)   title="$2"; shift 2 ;;
            --content) content="$2"; shift 2 ;;
            --file)    content_file="$2"; shift 2 ;;
            *)         die "Unknown option: $1" ;;
        esac
    done

    [[ -n "$title" ]] || read -rp "Devlog title: " title
    [[ -n "$title" ]] || die "Title is required"

    if [[ -n "$content_file" ]]; then
        [[ -f "$content_file" ]] || die "File not found: $content_file"
        content=$(cat "$content_file")
    fi

    if [[ -z "$content" ]]; then
        info "Enter devlog content (Ctrl+D to finish):"
        content=$(cat)
    fi

    info "Posting devlog..."
    local json
    if command -v jq >/dev/null; then
        json=$(jq -nc --arg title "$title" --arg content "$content" '{title: $title, content: $content}')
    else
        local esc_title esc_content
        esc_title=$(printf '%s' "$title" | sed 's/\\/\\\\/g; s/"/\\"/g')
        esc_content=$(printf '%s' "$content" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\t/\\t/g' | tr '\n' '\\' | sed 's/\\/\\n/g')
        json="{\"title\":\"$esc_title\",\"content\":\"$esc_content\"}"
    fi
    api_call POST "/api/games/$GAME_ID/devlogs" \
        -H "Content-Type: application/json" \
        -d "$json" > /dev/null || exit 1
    success "Devlog posted: $title"
}

cmd_status() {
    load_config
    echo -e "${CYAN}PlayMore Deploy v${VERSION}${NC}"
    echo ""
    if [[ -z "$SERVER" ]]; then
        warn "Not configured. Run: playmore-deploy init"
        return
    fi
    echo "  Server:  $SERVER"
    echo "  Key:     ${API_KEY:0:13}..."

    local result
    result=$(api_call GET "/api/auth/me" 2>/dev/null) || { warn "Auth failed — key may be revoked."; return; }
    local username
    username=$(json_val "$result" "username")
    echo "  User:    $username"

    if [[ -n "$GAME_ID" ]]; then
        local game
        game=$(api_call GET "/api/games/$GAME_ID" 2>/dev/null) || { echo "  Game:    $GAME_ID (not found)"; return; }
        local game_title
        game_title=$(json_val "$game" "title")
        echo "  Game:    $game_title ($GAME_ID)"
        echo "  URL:     ${SERVER}/#game/${GAME_ID}"
    else
        echo "  Game:    (none — run 'push' to upload)"
    fi
}

# --- Main ---
[[ $# -gt 0 ]] || usage

case "$1" in
    init)    shift; cmd_init "$@" ;;
    push)    shift; cmd_push "$@" ;;
    update)  shift; cmd_update "$@" ;;
    devlog)  shift; cmd_devlog "$@" ;;
    status)  shift; cmd_status "$@" ;;
    --help|-h|help) usage ;;
    --version|-v) echo "playmore-deploy v${VERSION}" ;;
    *)       die "Unknown command: $1. Run 'playmore-deploy --help'" ;;
esac
