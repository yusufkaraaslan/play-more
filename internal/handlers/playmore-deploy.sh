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
    SERVER="" API_KEY="" GAME_ID=""
    if [[ -f "$CONFIG_FILE" ]]; then
        # shellcheck source=/dev/null
        source "$CONFIG_FILE"
    elif [[ -f "$GLOBAL_CONFIG" ]]; then
        # shellcheck source=/dev/null
        source "$GLOBAL_CONFIG"
    fi
}

save_config() {
    cat > "$CONFIG_FILE" <<EOF
SERVER=$SERVER
API_KEY=$API_KEY
GAME_ID=$GAME_ID
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

    if [[ -n "$GAME_ID" ]]; then
        # Re-upload existing game
        info "Re-uploading files to game $GAME_ID..."
        local result
        result=$(api_call POST "/api/games/$GAME_ID/reupload" \
            -F "game_file=@$file") || exit 1
        success "Game files updated!"
    else
        # New upload
        [[ -n "$title" ]] || read -rp "Game title: " title
        [[ -n "$genre" ]] || read -rp "Genre (action/adventure/rpg/strategy/puzzle/racing/horror/experimental): " genre
        [[ -n "$title" ]] || die "Title is required"
        [[ -n "$genre" ]] || die "Genre is required"

        info "Uploading new game: $title..."
        local curl_args=(-F "game_file=@$file" -F "title=$title" -F "genre=$genre" -F "is_webgpu=$webgpu")
        [[ -n "$desc" ]] && curl_args+=(-F "description=$desc")
        [[ -n "$tags" ]] && curl_args+=(-F "tags=$tags")
        [[ -n "$cover" && -f "$cover" ]] && curl_args+=(-F "cover=@$cover")

        local result
        result=$(api_call POST "/api/games" "${curl_args[@]}") || exit 1

        # Extract game ID from response
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

    echo ""
    echo "  View: ${SERVER}/#game/${GAME_ID:-unknown}"
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

    # Build JSON
    local json="{"
    local first=true
    add_field() {
        [[ -z "$2" ]] && return
        $first || json+=","
        first=false
        json+="\"$1\":\"$2\""
    }
    add_field "title" "$title"
    add_field "description" "$desc"
    add_field "genre" "$genre"
    if [[ -n "$price" ]]; then
        $first || json+=","
        first=false
        json+="\"price\":$price"
    fi
    if [[ -n "$tags" ]]; then
        $first || json+=","
        first=false
        local tags_json
        tags_json=$(echo "$tags" | sed 's/,/","/g')
        json+="\"tags\":[\"$tags_json\"]"
    fi
    if [[ -n "$video" ]]; then
        $first || json+=","
        first=false
        json+="\"videos\":[\"$video\"]"
    fi
    json+="}"

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

    # Escape content for JSON
    content=$(echo "$content" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\t/\\t/g' | tr '\n' '\\' | sed 's/\\/\\n/g')

    info "Posting devlog..."
    api_call POST "/api/games/$GAME_ID/devlogs" \
        -H "Content-Type: application/json" \
        -d "{\"title\":\"$title\",\"content\":\"$content\"}" > /dev/null || exit 1
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

load_config

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
