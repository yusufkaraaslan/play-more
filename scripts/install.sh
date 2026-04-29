#!/usr/bin/env bash
# One-time bootstrap: build playmore, install systemd units for playmore + cloudflared,
# enable linger so the ProtonMail Bridge user service starts at boot.
#
# Prerequisites (not automated — require interactive/browser steps):
#   - go, cloudflared installed
#   - .env present in the repo root (copy .env.example if one exists, or follow docs/SETUP.md)
#   - cloudflared tunnel login  (opens browser)
#   - cloudflared tunnel create playmore
#   - cloudflared tunnel route dns playmore <your-hostname>
#
# Safe to re-run. Uses the current user and this repo's location.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USER_NAME="$(id -un)"
GROUP_NAME="$(id -gn)"

cd "$REPO_DIR"

log() { printf '\n==> %s\n' "$*"; }

# --- Checks ---
command -v go >/dev/null          || { echo "go not found"; exit 1; }
command -v cloudflared >/dev/null || { echo "cloudflared not found"; exit 1; }
[[ -f .env ]]                     || { echo ".env missing — see docs/SETUP.md"; exit 1; }

# Extract hostname from PLAYMORE_BASE_URL (e.g. https://playmore.world → playmore.world)
HOSTNAME="$(grep -E '^PLAYMORE_BASE_URL=' .env | head -1 | cut -d= -f2- | sed -E "s|^['\"]?https?://||; s|/.*||; s|['\"]?$||")"
[[ -n "$HOSTNAME" ]] || { echo "PLAYMORE_BASE_URL missing from .env"; exit 1; }

# Optional games domain for sandbox isolation
GAMES_DOMAIN="$(grep -E '^PLAYMORE_GAMES_DOMAIN=' .env | head -1 | cut -d= -f2- | sed -E "s|^['\"]?||; s|['\"]?$||")"

# Find the tunnel credentials JSON (named <uuid>.json)
CRED_SRC="$(ls -1 "$HOME"/.cloudflared/*.json 2>/dev/null | head -1 || true)"
[[ -n "$CRED_SRC" ]] || { echo "No tunnel credentials in ~/.cloudflared — run: cloudflared tunnel create playmore"; exit 1; }
TUNNEL_ID="$(basename "$CRED_SRC" .json)"

log "Repo:     $REPO_DIR"
log "User:    $USER_NAME"
log "Host:    $HOSTNAME"
[[ -n "$GAMES_DOMAIN" ]] && log "Games:   $GAMES_DOMAIN (separate origin for game sandbox)"
log "Tunnel:  $TUNNEL_ID"

# --- Build ---
log "Building playmore binary"
go build -o playmore

# --- playmore.service ---
log "Installing /etc/systemd/system/playmore.service"
sudo tee /etc/systemd/system/playmore.service >/dev/null <<EOF
[Unit]
Description=PlayMore game publishing platform
After=network.target

[Service]
Type=simple
User=$USER_NAME
Group=$GROUP_NAME
WorkingDirectory=$REPO_DIR
EnvironmentFile=$REPO_DIR/.env
ExecStart=$REPO_DIR/playmore
Restart=on-failure
RestartSec=5s

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=read-only
ReadWritePaths=$REPO_DIR/data

[Install]
WantedBy=multi-user.target
EOF

# --- cloudflared config + service ---
log "Installing /etc/cloudflared/ (config + credentials)"
sudo mkdir -p /etc/cloudflared
sudo cp "$CRED_SRC" /etc/cloudflared/
sudo tee /etc/cloudflared/config.yml >/dev/null <<EOF
tunnel: $TUNNEL_ID
credentials-file: /etc/cloudflared/$TUNNEL_ID.json

ingress:
  - hostname: $HOSTNAME
    service: http://localhost:8080
  - hostname: www.$HOSTNAME
    service: http://localhost:8080
$([[ -n "$GAMES_DOMAIN" ]] && echo "  - hostname: $GAMES_DOMAIN
    service: http://localhost:8080")
  - service: http_status:404
EOF

# Auto-create DNS route for games subdomain (idempotent — silently no-ops if exists)
if [[ -n "$GAMES_DOMAIN" ]]; then
  log "Ensuring DNS route for $GAMES_DOMAIN"
  cloudflared tunnel route dns "$TUNNEL_ID" "$GAMES_DOMAIN" 2>&1 | grep -v "An A, AAAA, or CNAME record" || true
fi

log "Installing cloudflared systemd service"
if ! systemctl list-unit-files cloudflared.service >/dev/null 2>&1; then
  sudo cloudflared service install
fi

# Bump start timeout — initial tunnel handshake can exceed the 15s default
sudo mkdir -p /etc/systemd/system/cloudflared.service.d
sudo tee /etc/systemd/system/cloudflared.service.d/override.conf >/dev/null <<'EOF'
[Service]
TimeoutStartSec=60
EOF

# --- Enable + start ---
log "Enabling + starting services"
sudo systemctl daemon-reload
sudo systemctl enable --now playmore
sudo systemctl restart cloudflared

# --- Linger for user services (ProtonMail Bridge) ---
log "Enabling linger for $USER_NAME (so user services run at boot without GUI login)"
sudo loginctl enable-linger "$USER_NAME"

# --- Verify ---
log "Status"
systemctl --no-pager --lines=0 status playmore cloudflared || true

log "End-to-end check"
sleep 3
curl -sS -o /dev/null -w "https://$HOSTNAME → HTTP %{http_code} in %{time_total}s\n" --max-time 10 "https://$HOSTNAME/" || \
  echo "(public check failed — DNS may still be propagating; run it again in a minute)"

log "Done. Use scripts/deploy.sh for future code updates."
