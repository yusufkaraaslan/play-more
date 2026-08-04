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

## Reverse proxy body limits

**Game uploads use chunked PUTs (≤ 9 MiB per request) above 64 MiB**, so any reverse proxy with a body cap ≥ 9 MiB will work — including Cloudflare Free/Pro (100 MiB). Set `client_max_body_size 16m` (nginx) or equivalent. Below 64 MiB, the legacy single-shot path is used and the proxy must allow the full file in one request.

## TURN relay (multiplayer)

Optional. Only relevant if you host multiplayer games.

### Do you need it?

WebRTC connects players directly. When it can't — symmetric NAT, carrier-grade
NAT, corporate/school firewalls that block UDP — the SDK transparently falls
back to relaying through the WebSocket. That fallback always works, but it is
**capped at 30 messages/sec with 8 KiB frames, and is always reliable+ordered**
because it rides TCP.

So the decision is about your game type, not your bandwidth:

| Game type | Without TURN |
|---|---|
| Turn-based (chess, cards, word games) | Fine. The relay is more than enough. |
| Real-time (action, physics, anything ticking >30 Hz) | **Broken** for affected players — the rate cap throttles them and the unreliable data channel silently becomes reliable+ordered, so late packets queue instead of being dropped. |

TURN relays UDP at the transport layer, so data-channel semantics and latency
survive. Note it does **not** reduce server bandwidth — traffic still passes
through your machine either way.

### Requirements — read before enabling

TURN needs **inbound UDP**: the control port (default 3478) plus one port per
concurrent relayed connection (default range 49152–49251).

**HTTP-only ingress cannot carry this.** If your server is reached through
Cloudflare Tunnel, ngrok, or a typical PaaS, TURN traffic will not arrive no
matter how it's configured — those paths proxy HTTP(S) only. You need a direct
public IP with those UDP ports open, or an external TURN service.

### Embedded relay

Enable it in `./playmore setup`, or in `.env`:

```bash
PLAYMORE_TURN=true
PLAYMORE_TURN_PUBLIC_IP=203.0.113.10   # your server's public IPv4 — required
PLAYMORE_TURN_LISTEN=0.0.0.0:3478
PLAYMORE_TURN_MIN_PORT=49152
PLAYMORE_TURN_MAX_PORT=49251
PLAYMORE_TURN_SECRET=<32-random-bytes> # generated by the wizard
```

Then open the firewall:

```bash
sudo ufw allow 3478/udp
sudo ufw allow 49152:49251/udp
```

`PLAYMORE_TURN_PUBLIC_IP` cannot be auto-detected — behind NAT the server only
sees its private address, and guessing wrong produces candidates that gather
successfully and then never connect. If you set a private address, startup
warns you.

Each concurrent relayed peer uses one port from the range, so 100 ports means
100 simultaneous relayed peers. Widen it (and the firewall rule) for larger
deployments.

Set `PLAYMORE_TURN_SECRET` explicitly. If you leave it empty a random one is
generated per process, which means credentials stop working across a restart
and multiple instances won't agree on them.

Clients receive **ephemeral credentials** (10-minute TTL, HMAC-derived, minted
per user at `GET /rtc-config`). No TURN passwords are stored anywhere.

Enabling the embedded relay also gives you self-hosted STUN on the same port,
so you no longer depend on Google's public STUN server.

### External coturn instead

The credential scheme is coturn's `use-auth-secret`, so an existing coturn
works unchanged — leave `PLAYMORE_TURN` off and point at it:

```bash
PLAYMORE_TURN_SERVERS=turn:user:pass@turn.example.com:3478
```

### Verifying

Startup logs the bound port and range. External reachability can't be checked
from the server itself — confirm it from outside with
[Trickle ICE](https://webrtc.github.io/samples/src/content/peerconnection/trickle-ice/):
enter your TURN URL and credentials, and look for a candidate of type `relay`.
If only `host` and `srflx` appear, the UDP ports aren't reaching you.

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
