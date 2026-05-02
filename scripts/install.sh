#!/usr/bin/env bash
# One-time bootstrap: build playmore, install systemd units, set permissions.
#
# Prerequisites:
#   - go installed
#   - reverse proxy (e.g. nginx, caddy, cloudflared) configured if needed
#   - .env present in the repo root (copy .env.example)
#
# Safe to re-run. Uses the current user and this repo's location.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USER_NAME="$(id -un)"
GROUP_NAME="$(id -gn)"

cd "$REPO_DIR"

log() { printf '\n==> %s\n' "$*"; }

# --- Checks ---
command -v go >/dev/null || { echo "go not found"; exit 1; }
[[ -f .env ]] || { echo ".env missing — see docs/SETUP.md"; exit 1; }

# Extract hostname from PLAYMORE_BASE_URL
HOSTNAME="$(grep -E '^PLAYMORE_BASE_URL=' .env | head -1 | cut -d= -f2- | sed -E "s|^['\"]?https?://||; s|/.*||; s|['\"]?$||")"
[[ -n "$HOSTNAME" ]] || { echo "PLAYMORE_BASE_URL missing from .env"; exit 1; }

# Optional games domain for sandbox isolation
GAMES_DOMAIN="$(grep -E '^PLAYMORE_GAMES_DOMAIN=' .env | head -1 | cut -d= -f2- | sed -E "s|^['\"]?||; s|['\"]?$||")"

log "Repo:    $REPO_DIR"
log "User:    $USER_NAME"
log "Host:    $HOSTNAME"
[[ -n "$GAMES_DOMAIN" ]] && log "Games:   $GAMES_DOMAIN (separate origin for game sandbox)"

# --- Secrets ---
log "Tightening permissions on .env and data directory"
chmod 600 "$REPO_DIR/.env"
mkdir -p "$REPO_DIR/data"
chmod 700 "$REPO_DIR/data"

# --- Build ---
log "Building playmore binary"
go build -ldflags="-s -w" -o playmore .

# --- systemd unit for playmore ---
UNIT_FILE="$HOME/.config/systemd/user/playmore.service"
mkdir -p "$HOME/.config/systemd/user"

cat > "$UNIT_FILE" <<EOF
[Unit]
Description=PlayMore game publishing server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$REPO_DIR
ExecStart=$REPO_DIR/playmore
Restart=on-failure
RestartSec=5

# Hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=$REPO_DIR/data

[Install]
WantedBy=default.target
EOF

log "Installed user systemd unit: $UNIT_FILE"
log "To start: systemctl --user start playmore"
log "To enable at boot: systemctl --user enable playmore"

# --- nginx / reverse proxy example ---
NGINX_CONF="$REPO_DIR/scripts/nginx-example.conf"
cat > "$NGINX_CONF" <<'EOF'
# Example nginx reverse proxy configuration
# Adjust server_name and SSL paths for your domain.

server {
    listen 443 ssl http2;
    server_name example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
EOF

log "Example nginx config written to: $NGINX_CONF"
log "Done. Edit .env, then: systemctl --user start playmore"
