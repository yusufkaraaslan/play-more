# PlayMore

A self-hosted publishing platform for **modern browser games** — WebGPU, WebAssembly, WebGL2. Think itch.io but you own the server, and it doesn't choke on the modern stack.

**Built for what other platforms struggle with:**
- **WebGPU games** — first-class support, capability detection, per-game badges, analytics
- **Large WebAssembly builds** — gzip + Range request streaming, no 50 MB upload limit (cap is 500 MiB)
- **Modern engine exports** — Godot Web, Unity WebGL, Bevy, Babylon.js drop in directly
- **Sandboxed game origins** — optional `--games-domain` for full origin isolation, iframe sandbox tuned for gamepad/fullscreen/pointer-lock

## Features

- **Game Store** — search (FTS5), genre/sort filters, hero banner, discounts
- **Game Upload** — drag-and-drop `.html` or `.zip`, auto-extracts, WebGPU badge per title
- **Game Player** — fullscreen iframe with session timer, FPS overlay, gamepad + WebGPU
- **Developer Pages** — customizable storefront: banner, theme, custom CSS, links
- **Reviews + Devlogs + Comments** — full content layer
- **Library / Wishlist / Lists** — Steam-style sidebar with public + private collections
- **Activity Feed** — aggregated timeline from followed developers
- **Developer Platform** — API keys, CLI deploy script, automation-ready
- **Email + Auth** — verification, password reset, per-account brute-force protection, PoW CAPTCHA
- **Admin Panel** — first registered user becomes admin; moderation, analytics, audit log
- **Operations** — single-binary deploy, Docker, Let's Encrypt auto-TLS, ProtonMail Bridge support

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
- **Frontend**: Vanilla JS SPA (no framework, no build step, ~3500 lines)
- **Database**: SQLite (single file, zero config, WAL mode, FTS5 search)
- **Auth**: bcrypt (cost 12) + 256-bit session tokens (SHA-256 hashed at rest) + Bearer API keys + proof-of-work CAPTCHA
- **Deploy**: Single binary with embedded frontend (`go:embed`), no external assets at runtime

## Game Compatibility

Tested with games built using:

- **Godot 4** (Web export with WebGPU/WebGL2)
- **Unity 6** (WebGL build, including IL2CPP)
- **Bevy** (`wasm-bindgen` output)
- **Babylon.js** (WebGPU + WebXR)
- **Three.js** (WebGL2 + WebGPU experimental)
- Plain HTML/JS/Canvas games

Range requests + immutable cache headers mean even 100+ MB WASM builds load fast on second visit.

## v1

The original single-file HTML version is archived in `v1/`. Open `v1/index.html` in a browser — no server needed.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for code style, security checklist, and conventions. Security issues: see [SECURITY.md](SECURITY.md) — please use the private disclosure flow, not public issues.

## License

MIT — see [LICENSE](LICENSE). Third-party attributions in [NOTICE](NOTICE).
