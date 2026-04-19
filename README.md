# PlayMore

A self-hosted game publishing platform for HTML5 games. Think Steam/itch.io but you own the server.

## Features

- **Game Store** — browse, search, filter, sort games with hero banner and special offers
- **Game Upload** — drag-and-drop .html or .zip files, auto-extracts multi-file games
- **Game Player** — fullscreen iframe player with session timer, WebGPU support
- **Developer Pages** — customizable storefront with banner, bio, links, game grid
- **Profile** — customizable with banner, theme color, bio, links, level badge
- **Reviews** — star ratings, review summary stats, reviewer avatars
- **Library & Wishlist** — add/remove games, search, sort by playtime/recent/A-Z
- **Feed** — aggregated timeline from followed developers with type filters
- **Devlogs** — blog posts tied to games
- **Follow System** — follow developers, see their activity in your feed
- **Collections** — user-created game groups
- **Dashboard** — manage uploaded games (view/edit/delete)
- **Admin Panel** — moderate users and games (first registered user = admin)
- **Settings** — change password, export/import backup, delete account
- **Docker** — multi-stage Dockerfile + docker-compose

## Quick Start

```bash
go build -o playmore
./playmore setup              # interactive wizard (HTTPS, email, etc.)
./playmore                    # starts with config from .env

# Or just run with defaults:
./playmore                    # http://localhost:8080

# Seed demo data (4 games with reviews)
curl -X POST http://localhost:8080/api/seed
```

**Guides:**
- [Setup Guide](docs/SETUP.md) — production config, HTTPS, email, systemd
- [Developer Guide](docs/DEVELOPER.md) — API keys, deploy CLI, API reference
- [ProtonMail Bridge](docs/SETUP_PROTONMAIL_BRIDGE.md) — email via Proton

## Docker

```bash
docker-compose up -d
```

## Production Deployment (HTTPS)

### Option 1: Reverse Proxy (Recommended)

**Caddy** (auto HTTPS, zero config):
```
playmore.example.com {
    reverse_proxy localhost:8080
}
```

**Nginx** with certbot:
```nginx
server {
    listen 443 ssl;
    server_name playmore.example.com;

    ssl_certificate /etc/letsencrypt/live/playmore.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/playmore.example.com/privkey.pem;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

Session cookies automatically get the `Secure` flag when `X-Forwarded-Proto: https` is set.

### Option 2: Direct TLS

```bash
./playmore --tls-cert cert.pem --tls-key key.pem --port 443
```

Docker with TLS:
```bash
docker run -d \
  -p 443:443 \
  -v /path/to/certs:/certs:ro \
  -v playmore-data:/app/data \
  playmore --tls-cert /certs/cert.pem --tls-key /certs/key.pem --port 443
```

## Tech Stack

- **Backend**: Go + Gin + SQLite (pure Go, no CGO)
- **Frontend**: Vanilla JS SPA (no framework)
- **Database**: SQLite (single file, zero config)
- **Auth**: bcrypt + session cookies
- **Deploy**: Single binary with embedded frontend (`go:embed`)

## v1

The original single-file HTML version is archived in `v1/`. Open `v1/index.html` in a browser — no server needed.
