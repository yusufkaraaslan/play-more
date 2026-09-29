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

Every game HTML response has a server-enforced CSP sandbox without
`allow-same-origin`, including direct navigation and deployments using
`--games-domain`. Uploaded scripts cannot access the parent application's DOM
or storage. A separate games domain remains recommended for defense in depth.

Games have opaque origins, so native localStorage/IndexedDB access is unavailable.
The player supplies an in-memory localStorage/sessionStorage compatibility shim;
its contents are not durable. Use the per-player, per-game cloud saves API for
persistent data. The iframe's `allow-same-origin` attribute on a separate games
domain does not override the stricter response CSP.

## Multiplayer credentials

Game-session tokens bind a player to one game. Creation, matchmaking, joining,
and spectating enforce that boundary. Runtime operations require `session:write`
or `all`. Cross-origin sockets require an explicit valid runtime token; cookie
connections retain same-origin protection. Tokens are rechecked on commands and
once per second while idle. Renewal uses an `auth` frame with a fresh token for
the same identity and game. Application access logs omit query strings; reverse
proxy logs must do the same because browser WebSocket URLs carry runtime tokens.

Long-lived account/game secrets belong only on trusted backends, never in native
executables or browser bundles. See [external games](docs/sdk/external-games.md).
Game payloads are not validated by the relay, and P2P peers can continue exchanging
traffic independently of service credentials. This is not an anti-cheat system.

## Known Limitations

- **Single-process rate limiting** — running multiple replicas behind a load balancer multiplies the effective limit by N. Use a sticky-session LB or single instance.
- **No 2FA** yet. Planned.
- **Admin = first registered user.** Stored in DB by `created_at` order. Register the admin account immediately after first start.
