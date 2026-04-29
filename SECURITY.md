# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in PlayMore, please report it privately. **Do not open a public issue.**

**Contact:** open a [GitHub security advisory](https://github.com/yusufkaraaslan/play-more/security/advisories/new) (preferred), or email the maintainer directly via the address listed on the GitHub profile.

Include:
- A clear description of the vulnerability
- Steps to reproduce
- Impact (what an attacker could do)
- Suggested fix if you have one

We aim to acknowledge reports within 72 hours and provide a fix or mitigation timeline within 14 days for confirmed issues.

## Supported Versions

PlayMore is in active alpha. Only the `main` branch receives security fixes. Pin your deployment to a tagged release if you need stability.

## Hardening Recommendations for Self-Hosters

### Required for production

- **Set `--trusted-proxies`** (or `PLAYMORE_TRUSTED_PROXIES`) to the CIDR of your reverse proxy. Default is to trust no `X-Forwarded-*` headers (prevents IP/proto spoofing). Example: `--trusted-proxies "127.0.0.1/32"`.
- **Set `--behind-tls-proxy`** when running behind a TLS-terminating reverse proxy (Caddy, nginx, Cloudflare). Forces the `Secure` flag on session cookies.
- **Configure SMTP** so email verification is enforced (uploads/reviews/devlogs require verified email when SMTP is configured).
- **Run as non-root** under a dedicated systemd user.
- **Use HTTPS** — either `--auto-tls`, `--tls-cert/key`, or a reverse proxy with TLS.

### Recommended for higher isolation

- **Set `--games-domain`** (e.g. `games.example.com`) to serve uploaded games from a separate origin. **This is strongly recommended for production** — see "Game iframe sandbox" below for the tradeoff.
- **Run behind a firewall** that only exposes the proxy port; do not expose port 8080 directly.
- **Mount the data directory as a separate filesystem** with quota limits.

### Defaults

The bundled defaults aim to be safe but not paranoid. Specifically:
- API keys cannot perform admin actions, delete accounts, change passwords, or manage other API keys.
- Rate limits apply per-IP per-endpoint (auth, upload, password reset, etc).
- CSP uses per-request nonce for `<script>` and `<style>` blocks.
- Iframe sandbox isolates uploaded games from the SPA.
- Email tokens (verify, reset) are stored as SHA-256 hashes; the raw token only exists in the email itself.
- Sessions are 32-byte crypto/rand tokens, 30-day expiry, invalidated on password change/reset.

## Game iframe sandbox

Uploaded games run in a sandboxed iframe. The sandbox attribute is set dynamically based on whether `--games-domain` is configured:

**Without `--games-domain`** (games served from main domain `/play/<id>/*`):
- Sandbox: `allow-scripts allow-pointer-lock allow-popups allow-popups-to-escape-sandbox`
- **No `allow-same-origin`** — iframe runs in an opaque origin
- ✅ Games CANNOT call `/api/*` with the user's cookies (no credentials cross opaque origin)
- ✅ Games CANNOT read `parent.document` or the SPA's localStorage
- ❌ Games CANNOT use their own `localStorage` / `IndexedDB` (storage is per-origin and the opaque origin storage is wiped each load)
- **Best for:** simple games that don't need persistence (puzzle, arcade, demos)

**With `--games-domain`** (games served from `games.example.com`):
- Sandbox includes `allow-same-origin`
- Games run on their own origin, scoped storage to that origin
- ✅ Games CANNOT call `/api/*` with the user's cookies (cookies are scoped to main domain)
- ✅ Games CANNOT read SPA DOM (iframe is cross-origin)
- ✅ Games CAN use `localStorage`, `IndexedDB`, etc. (scoped to `games.example.com`)
- **Best for:** save-game-heavy games, games that need persistence
- ⚠️ All games on a self-hosted instance share the `games.example.com` origin and therefore share storage — one game can read another game's localStorage. For full per-game isolation, use a wildcard subdomain like `<id>.games.example.com` (requires DNS + wildcard cert work).

## Known Limitations

- **Single-process rate limiting** — running multiple replicas behind a load balancer multiplies the effective limit by N. Use a sticky-session LB or single instance.
- **No 2FA** yet. Planned.
- **Admin = first registered user.** Stored in DB by `created_at` order. Register the admin account immediately after first start.
