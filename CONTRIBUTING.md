# Contributing to PlayMore

Thanks for your interest in PlayMore. This is a small project — contributions are welcome but please read this first.

## Before opening a PR

1. **Open an issue first** for non-trivial changes. Describe the problem and the proposed approach. Drive-by PRs that change architecture or add dependencies will likely be closed.
2. **Run the build:** `go build -o playmore`
3. **Manual smoke test:** start the server, register a user, upload a game, post a review. There's no automated test suite yet.
4. **Match existing style.** Standard `go fmt`, no new dependencies without discussion, frontend rendering via `innerHTML` with `escapeHtml()` on user input.

## Code style

### Go

- Standard `go fmt`
- Handlers return JSON via `gin.H{}`
- Early-return error handling (no `else` after `return`)
- Use `sql.NullString` only when needed; prefer plain `string` with `DEFAULT ''`
- Log internal errors with `log.Printf`; return generic messages to clients
- Don't use `err.Error()` in HTTP responses — log the detail, return a stable message

### Frontend

- Single HTML file, inline CSS/JS, no build step
- All rendering via template strings + `innerHTML`
- **Always** call `escapeHtml()` on user-controlled data — usernames, titles, descriptions, etc.
- Avoid adding event handlers via `onclick="..."` attributes if you can use `addEventListener` (CSP `script-src-attr 'unsafe-inline'` is a known weakness)
- No JS libraries unless they're trivially auditable

### SQL

- Always parameterized queries (`?` placeholders, never string concat)
- Migrations go in `internal/storage/db.go` `migrations` slice

## Security

- All API key auth must be blocked from admin endpoints, account deletion, password change, and key management — see `middleware.IsAPIKeyAuth(c)`
- Rate-limit auth endpoints, email-sending endpoints, and any expensive operation
- **Don't** expose stack traces or DB errors to clients
- Email tokens, API keys: store SHA-256 hashes only

If you discover a security issue, see [SECURITY.md](SECURITY.md) — do **not** open a public issue.

## Adding routes

1. Handler in `internal/handlers/<area>.go`
2. Wire up in `internal/server/server.go` (within the `api` group for auth+CSRF, or outside for public)
3. Add to `internal/handlers/docs.go` so it shows in `/docs`
4. Apply `middleware.RateLimit(...)` if it's expensive or auth-related
5. Apply `middleware.AuthRequired()` if it needs auth
6. Apply `handlers.RequireVerifiedEmail()` if it needs verified email
7. Block API key auth (`middleware.IsAPIKeyAuth`) if it's account-level destructive

## Adding tables

1. Add to `schema` in `db.go`
2. Add an `ALTER TABLE` migration to the `migrations` slice for existing installs
3. Add a model file in `internal/models/`

## License

By contributing, you agree your contributions are licensed under the MIT License (see [LICENSE](LICENSE)).
