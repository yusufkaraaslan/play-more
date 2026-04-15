# PlayMore Setup Guide

## Quick start

```bash
go build -o playmore
./playmore setup        # interactive wizard — saves to .env
./playmore              # runs with the config from .env
```

The setup wizard asks about HTTPS, email, port, and analytics. That's it.

## Manual config (.env file)

Create a `.env` file in the same directory as the binary:

```ini
# Core
PLAYMORE_PORT=8080
PLAYMORE_DATA=data
PLAYMORE_BASE_URL=https://playmore.example.com

# HTTPS — pick ONE approach
PLAYMORE_AUTO_TLS=true
PLAYMORE_DOMAIN=playmore.example.com
# OR provide cert files directly:
# PLAYMORE_TLS_CERT=/path/to/fullchain.pem
# PLAYMORE_TLS_KEY=/path/to/privkey.pem

# Email (SMTP) — optional but recommended
PLAYMORE_SMTP_HOST=smtp.example.com
PLAYMORE_SMTP_PORT=587
PLAYMORE_SMTP_USER=noreply@example.com
PLAYMORE_SMTP_PASS=your-password
PLAYMORE_SMTP_FROM=PlayMore <noreply@example.com>

# Analytics — optional
PLAYMORE_GOATCOUNTER=https://mysite.goatcounter.com
```

All `PLAYMORE_*` env vars can also be passed as CLI flags (`--port`, `--smtp-host`, etc).

## SMTP health check

On startup PlayMore tests the SMTP connection if configured. You'll see:

- `✓  SMTP reachable at host:port` — good to go
- `⚠  SMTP health check failed` — can't connect; emails won't send

If the host is `127.0.0.1` (local bridge) and unreachable, PlayMore will try `systemctl start protonmail-bridge` / `proton-bridge` automatically and retry.

## Email providers

### ProtonMail Bridge (paid Proton plan required)
See [SETUP_PROTONMAIL_BRIDGE.md](SETUP_PROTONMAIL_BRIDGE.md).

### Free alternatives
| Provider | Free tier | Host |
|----------|-----------|------|
| Brevo | 300/day | `smtp-relay.brevo.com:587` |
| Resend | 100/day, 3000/mo | `smtp.resend.com:465` |
| SendGrid | 100/day | `smtp.sendgrid.net:587` |
| Mailgun | 100/day (EU) | `smtp.mailgun.org:587` |
| AWS SES | 62k/mo (from EC2) | `email-smtp.REGION.amazonaws.com:587` |

## Email verification gating

When SMTP is configured, the following actions require email verification:
- Upload games
- Post reviews
- Write devlogs
- Post comments

Unverified users see a banner on every page with a "Resend email" link. When SMTP is NOT configured, gating is automatically disabled so users can fully use the site without verification.

## HTTPS options

### Option 1 — Auto (Let's Encrypt)
```
PLAYMORE_AUTO_TLS=true
PLAYMORE_DOMAIN=playmore.example.com
```
Ports 80 and 443 must be reachable. Certs are cached in `data/certs/`.

### Option 2 — Manual cert files
```
PLAYMORE_TLS_CERT=/etc/letsencrypt/live/playmore/fullchain.pem
PLAYMORE_TLS_KEY=/etc/letsencrypt/live/playmore/privkey.pem
```

### Option 3 — Reverse proxy (recommended for most setups)
Run PlayMore on HTTP, use Caddy or nginx for TLS:
```
# Caddyfile
playmore.example.com {
    reverse_proxy localhost:8080
}
```

## Running as a systemd service

`/etc/systemd/system/playmore.service`:
```ini
[Unit]
Description=PlayMore
After=network.target

[Service]
Type=simple
User=playmore
WorkingDirectory=/srv/playmore
ExecStart=/srv/playmore/playmore
Restart=always

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now playmore
sudo journalctl -fu playmore   # follow logs
```

## First-run admin

The first registered user automatically becomes admin. Register immediately after starting for the first time.
