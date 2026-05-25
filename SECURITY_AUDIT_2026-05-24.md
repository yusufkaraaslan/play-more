# PlayMore Security Audit Report
**Date:** 2026-05-24  
**Scope:** Full-stack review — Go backend, SQLite database, vanilla JS SPA frontend, Docker/infrastructure, email subsystem, and external integrations.  
**Methodology:** Parallel agent swarm covering 7 independent attack surfaces.

---

## Executive Summary

PlayMore demonstrates **above-average security discipline** for a self-hosted single-binary application. The codebase is fundamentally well-architected: parameterized queries eliminate SQL injection, path traversal is blocked at multiple layers, session tokens use high-entropy CSPRNG generation with hash-on-server storage, bcrypt cost-12 passwords with timing-equalized login paths, and a pragmatic CSRF defense combining Origin/Referer validation with SameSite=Lax cookies.

However, **6 critical/high-severity issues** require immediate attention, primarily around:
1. **Data integrity** — SQLite foreign keys are disabled, and migrations run dangerously without versioning.
2. **Information disclosure** — public collections and the activity feed can leak unpublished games.
3. **Injection surface** — several user input fields bypass sanitization, creating stored XSS and `javascript:` URI vectors.
4. **Header spoofing** — `IsSecure()` trusts raw `X-Forwarded-Proto` from any client.

**Severity Distribution:**
| Severity | Count |
|----------|-------|
| 🔴 Critical | 2 |
| 🟠 High | 5 |
| 🟡 Medium | 13 |
| 🟢 Low / Info | 18 |
| ✅ Positive | 16 |

---

## 🔴 Critical Severity

### C1. SQLite Foreign Keys Are Disabled
**Location:** `internal/storage/db.go:21`  
**Impact:** Data integrity loss, orphaned records, broken cascade semantics

The connection DSN opens SQLite without enabling foreign key enforcement:
```go
DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
```
SQLite defaults to `PRAGMA foreign_keys = OFF`. Every `ON DELETE CASCADE` in the schema is **dead code**. Deleting a user or game leaves orphaned rows in dependent tables unless manually cleaned up. The manual cleanup in `settings.go` and `admin.go` is error-prone and may miss tables over time.

**Fix:** Append `&_fk=1` to the DSN, or execute `DB.Exec("PRAGMA foreign_keys = ON")` after `sql.Open`.

---

### C2. Migrations Are Unversioned and Errors Are Silently Ignored
**Location:** `internal/storage/db.go:35-101`  
**Impact:** Database corruption, inconsistent schema state, masked failures

The migration runner has no `schema_migrations` tracking table. Every migration re-runs on every startup. Errors are explicitly swallowed:
```go
for _, m := range migrations {
    DB.Exec(m) // ignore errors (column already exists)
}
```
This masks real failures (disk full, corrupt DB, locked table). A partially failed migration leaves the database in an undefined state with no recovery path.

**Fix:** Add a `schema_migrations` table. Only run migrations not yet recorded. Wrap each migration in a transaction. Return errors instead of swallowing them.

---

## 🟠 High Severity

### H1. IDOR — Public Collections Expose Unpublished Games
**Location:** `internal/handlers/collections.go` (`GetCollection`)  
**Impact:** Information disclosure of unreleased titles, descriptions, screenshots, and file metadata

When resolving games in a collection, the handler iterates `GameIDs` and fetches each game **without checking `published`**:
```go
for _, gid := range col.GameIDs {
    g, err := models.GetGameByID(gid)  // no published check
    if err == nil { games = append(games, *g) }
}
```
A malicious developer can add an unpublished game to a public collection, and any viewer will see its full details.

**Fix:** Skip games where `!g.Published && g.DeveloperID != viewerID`.

---

### H2. Stored XSS via Unsanitized Game Update Fields
**Location:** `internal/handlers/games.go` (`UpdateGame`)  
**Impact:** Stored XSS if frontend renders these fields as HTML or with unsafe substitution

The following fields are written directly to the DB **without sanitization**:
- `CustomAbout`
- `SysReqMin`
- `SysReqRec`
- `Features` (JSON array of arbitrary strings)

The existing `SanitizePlain()` helper exists in the codebase but is not applied to these fields. If the frontend ever renders them in a context that processes HTML or fails to escape, this becomes stored XSS.

**Fix:** Apply `SanitizePlain()` to all four fields before persistence. Validate `Discount` is in `[0,100]` and `Price >= 0`.

---

### H3. `javascript:` URLs Allowed in Profile Avatar/Banner
**Location:** `internal/handlers/settings.go` (`UpdateProfile`)  
**Impact:** XSS via image error handlers or browser-specific `javascript:` URI execution

`AvatarURL` and `BannerURL` are passed straight to `user.Update()` without URL scheme validation. If rendered in `<img src="">`, a `javascript:` URI can execute in certain browser behaviors.

**Fix:** Pass both through `SanitizeWebURL()` (or equivalent allowlist) before persistence.

---

### H4. Developer Page `DisplayName` Not Sanitized
**Location:** `internal/handlers/developer.go` (`UpdateDeveloperPage`)  
**Impact:** Stored XSS if rendered in HTML without escaping

`input.DisplayName` is passed to `models.UpsertDeveloperPage()` without `SanitizePlain()`. If the frontend renders this as raw HTML, it is a direct stored XSS vector.

**Fix:** Apply `SanitizePlain(input.DisplayName)` with a sensible length cap.

---

### H5. `IsSecure()` Trusts Raw `X-Forwarded-Proto` Unconditionally
**Location:** `internal/middleware/auth.go` (`IsSecure`)  
**Impact:** HSTS injection over HTTP, Secure cookie flag spoofing, mixed-content confusion

```go
func IsSecure(c *gin.Context) bool {
    ...
    return c.Request.Header.Get("X-Forwarded-Proto") == "https"
}
```
This reads the **raw HTTP header**, not a Gin-sanitized value. Gin does **not** strip or gate `X-Forwarded-Proto` based on `SetTrustedProxies`. A client connecting directly can send `X-Forwarded-Proto: https` over plain HTTP, causing:
- `Secure` cookie flag to be set (browsers won't send it back over HTTP, so impact is limited).
- HSTS header injection (modern browsers ignore HSTS over HTTP, but it's a spec violation).
- `window.PLAYMORE_GAMES_ORIGIN` scheme confusion in the SPA handler.

**Fix:** Only trust `X-Forwarded-Proto` when `hasTrustedProxy` is true:
```go
return ForceSecureCookies || c.Request.TLS != nil || (hasTrustedProxy && c.Request.Header.Get("X-Forwarded-Proto") == "https")
```

---

## 🟡 Medium Severity

### M1. Missing Rate Limiting on Destructive / State-Changing Endpoints
**Locations:** Multiple handlers  
**Impact:** Authenticated DoS, metadata spam, rapid delete/create cycles

Many authenticated mutating endpoints have **no rate limiting**:

| Endpoint | Handler |
|----------|---------|
| `PUT /api/games/:id` | `UpdateGame` |
| `DELETE /api/games/:id` | `DeleteGame` |
| `PUT /api/games/:id/visibility` | `ToggleVisibility` |
| `DELETE /api/games/:id/screenshots/:index` | `DeleteScreenshot` |
| `DELETE /api/reviews/:id` | `DeleteReview` |
| `DELETE /api/devlogs/:id` | `DeleteDevlog` |
| `DELETE /api/comments/:id` | `DeleteComment` |
| `DELETE /api/api-keys/:id` | `DeleteAPIKeyHandler` |
| Admin GET/PUT endpoints | All admin handlers |

**Fix:** Apply `middleware.RateLimit(...)` to all state-changing endpoints.

---

### M2. No Global Per-IP Rate Limit
**Location:** `internal/server/server.go`  
**Impact:** An attacker can rotate through different API endpoints to bypass per-path limits

Only per-path rate limits exist. There is no catch-all global limit (e.g., 200 req/min per IP).

**Fix:** Add a global per-IP middleware rate limit as a safety net.

---

### M3. No Maximum Length Validation on Free-Text Fields
**Locations:** `POST /api/games`, `PUT /api/games/:id`, `POST /api/devlogs`, `POST /api/comments`, `PUT /api/collections/:id`, etc.  
**Impact:** Storage exhaustion, DoS via giant payloads

Multiple endpoints accept unbounded-length strings. Examples:
- Game title, genre, description: only checked for `== ""` or `binding:"required"`
- Devlog title/content: `binding:"required"` only
- Comment text: `binding:"required,min=1"` only
- Collection name/description: none

**Fix:** Add `binding:"max=..."` or manual caps (title ≤ 100, description ≤ 5000, comment ≤ 2000).

---

### M4. `UpdateGame` Allows Negative Price / Invalid Discount
**Location:** `internal/handlers/games.go` (`UpdateGame`)  
**Impact:** Financial/logic corruption

`Price` accepts any `float64` (including negative). `Discount` accepts any `*int` (e.g., `-50` or `999`). No clamping to `[0, 100]`.

**Fix:** Validate `price >= 0` and `discount >= 0 && discount <= 100`.

---

### M5. Feed Leaks Unpublished Game Activity to Followers
**Location:** `internal/handlers/feed.go` (`GetFeed`)  
**Impact:** Pre-release information disclosure

Activity type `"upload"` is logged with the game title at upload time, before the game is published. The feed query does **not** filter out activity tied to unpublished games:
```go
WHERE a.user_id IN (SELECT followed_id FROM follows WHERE follower_id = ?)
```
Followers can see a developer uploaded "Secret Project X" even though the game is unpublished.

**Fix:** Join on `games` and filter `COALESCE(g.published, 1) = 1` for activity rows that have a `game_id`.

---

### M6. `TrackView` Creates Records for Non-Existent Games
**Location:** `internal/handlers/games.go` (`TrackView`)  
**Impact:** Analytics pollution/inflation

The handler does not verify `game_id` exists before inserting into `game_views`. An attacker can inflate analytics for arbitrary IDs. The rate limit (60/min) slows abuse but does not prevent it.

**Fix:** Check `SELECT 1 FROM games WHERE id = ?` before inserting.

---

### M7. No Collection Size Limit
**Location:** `internal/handlers/collections.go` (`AddToCollection`)  
**Impact:** N+1 query DoS when viewing collections

There is no maximum number of games per collection. `GetCollection` performs N+1 queries (`models.GetGameByID` in a loop), so a collection with thousands of games causes severe performance degradation.

**Fix:** Enforce a max collection size (e.g., 100 games).

---

### M8. `ChangePassword` Session Invalidation Is Broken
**Location:** `internal/handlers/settings.go` (`ChangePassword`)  
**Impact:** User is unexpectedly logged out after password change

```go
currentToken, _ := c.Cookie("session")
if currentToken != "" {
    storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ? AND token != ?`, user.ID, currentToken)
}
```
The DB stores **SHA-256 hashes** of tokens, but `currentToken` is the **raw** cookie value. The `!=` comparison will **never match**, so **all sessions are deleted**, including the current one. The user is immediately logged out.

**Fix:** Hash `currentToken` before comparison:
```go
storage.DB.Exec(`DELETE FROM sessions WHERE user_id = ? AND token != ?`, user.ID, models.HashSessionToken(currentToken))
```

---

### M9. Session Expiry Timezone Bug
**Location:** `internal/models/user.go` (`CreateSession`)  
**Impact:** Sessions expire early or live too long depending on server timezone

`CreateSession` stores `time.Now().Add(30 * 24 * time.Hour)` (local time), but `GetUserBySession` compares against SQLite `datetime('now')` (UTC). On a server with a positive UTC offset, sessions expire early; on negative offsets they live longer than intended.

**Fix:** Use `time.Now().UTC()` for session expiry.

---

### M10. Chunked Upload Init Lacks Extension Validation
**Location:** `internal/handlers/uploads_chunked.go` (`InitUpload`)  
**Impact:** Resource waste — attacker can initiate large uploads for blocked file types

`InitUpload` accepts any filename and size within bounds. Extension validation (`.zip`, `.html`, `.htm`) only happens at **finalize time**. A malicious client can waste server resources and bandwidth before discovering the rejection.

**Fix:** Reject non-allowed extensions in `InitUpload`.

---

### M11. Non-Atomic Database Backup
**Location:** `scripts/backup.sh:30-35`  
**Impact:** Potentially inconsistent backup

The script runs `sqlite3 .backup` (which handles WAL correctly), but then separately copies WAL and SHM files afterward. If writes occur between `.backup` and `cp`, the backup may be internally inconsistent.

**Fix:** Remove the separate WAL/SHM copy; `.backup` already handles this. Alternatively, use `VACUUM INTO`.

---

### M12. Error Information Disclosure in Uploads Handler
**Location:** `internal/handlers/uploads.go:93`  
**Impact:** Path disclosure, internal state leakage

```go
c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
```
This returns the raw error string from upload processing directly to the client, potentially disclosing filesystem paths or library internals.

**Fix:** Return sanitized user-facing messages. Log the raw error server-side.

---

### M13. Transaction Boundary Issue in Password Reset
**Location:** `internal/handlers/auth.go` (`ResetPassword`)  
**Impact:** Inconsistent state — password changed but token/sessions not invalidated

`user.SetPassword()` executes its UPDATE using `storage.DB` (the default connection), **not** the transaction `tx`. If `SetPassword` succeeds but `tx.Commit()` fails, the password is changed but the reset token is not deleted and sessions/API keys are not invalidated.

**Fix:** Pass the transaction `tx` into `SetPassword` or restructure so all operations use the same transaction.

---

## 🟢 Low Severity / Defense-in-Depth

| # | Issue | Location | Notes |
|---|-------|----------|-------|
| L1 | SQL concatenation in `AdminDeleteUser` | `internal/handlers/admin.go:146` | `table`/`col` are hardcoded, so not exploitable today. Dangerous pattern if refactored. |
| L2 | Password hash loaded for public queries | `internal/models/user.go` | `PublicUser()` relies on `json:"-"` to omit hash. Better to use separate structs. |
| L3 | Potential log injection in `adminLog` | `internal/handlers/admin.go:37` | `targetID` logged via `log.Printf` without control-character stripping. Currently UUID-only. |
| L4 | Docs list non-existent route | `internal/handlers/docs.go:38` | Documents `GET /api/auth/verify/:token` but actual endpoint is `POST /api/auth/verify`. |
| L5 | Frontend XSS architecture risk | `frontend/index.html` | ~80+ `innerHTML` assignments. No current exploit, but extremely fragile. One missed `escapeHtml()` = XSS. |
| L6 | CSP weakened by `unsafe-inline` | `internal/server/server.go` | `script-src-attr 'unsafe-inline'` required for hundreds of inline `onclick` handlers. Migrate to `addEventListener`. |
| L7 | Database file permissions world-readable | `internal/storage/db.go:15` | `os.MkdirAll(dataDir, 0755)` and DB inherits umask (often `0644`). Should be `0750`/`0640`. |
| L8 | Email tokens in URL fragments | `internal/email/email.go:121` | Raw token in `#verify/<token>` is visible in browser history and could leak via Referrer. 24h expiry limits window. |
| L9 | Log injection via control characters | `internal/server/server.go:44-53` | Only `\n` and `\r` stripped. Other terminal escape sequences could poison logs. |
| L10 | `SameSite=Lax` (not Strict) | `internal/handlers/auth.go` | Cross-site top-level navigation sends cookie. Combined with CSRF middleware this is acceptable. |
| L11 | Failed login emails logged as raw PII | `internal/handlers/auth.go:161` | `log.Printf("...email=%s...", emailKey)` accumulates PII in logs. Consider masking. |
| L12 | `rand.Read` error ignored | `internal/email/email.go` | `generateToken()` ignores `rand.Read` error. On Linux this is safe in practice, but formally risky. |
| L13 | Async email send DB errors ignored | `internal/handlers/auth.go` | `storage.DB.Exec` errors when inserting email tokens are unhandled. Reliability issue. |
| L14 | No `ReadTimeout`/`WriteTimeout` | `main.go:365-367` | Disabled to accommodate large uploads. `ReadHeaderTimeout: 10s` prevents slow headers. Consider proxy-level timeouts. |
| L15 | Implicit first-user admin model | `internal/models/user.go` | Admin is the earliest `created_at` user. No transfer mechanism. Self-deletion blocked. Dedicated `role` column would be more robust. |
| L16 | No `Cross-Origin-Opener-Policy` | `internal/server/server.go` | Missing COOP/CORP headers. Minor defense-in-depth gap. |
| L17 | No `HEALTHCHECK` in Dockerfile | `Dockerfile` | Docker image lacks health check. Hardening opportunity. |
| L18 | Registration validation leaks raw errors | `internal/handlers/auth.go:88` | `c.JSON(..., gin.H{"error": err.Error(), "captcha_failed": true})` exposes raw validation errors. |

---

## ✅ Positive Security Findings

| Control | Implementation | Verdict |
|---------|---------------|---------|
| **SQL Injection** | 100% parameterized queries (`?` placeholders) throughout | ✅ Strong |
| **Path Traversal** | `http.Dir`, `filepath.Clean`, prefix checks, `SanitizeFileName`, regex-validated IDs | ✅ Strong |
| **Session Security** | 32-byte CSPRNG tokens, SHA-256 hashed in DB, 30-day expiry, HttpOnly, SameSite=Lax, fixation protection | ✅ Strong |
| **Password Storage** | bcrypt cost-12 with opportunistic rehashing | ✅ Strong |
| **Timing-Safe Login** | Dummy bcrypt hash for missing users equalizes timing | ✅ Strong |
| **API Keys** | `pm_k_` prefix, SHA-256 stored, prefix+hash lookup, 10-key limit, scoped restrictions | ✅ Strong |
| **CSRF Protection** | Origin/Referer validation + Content-Type enforcement. API keys correctly exempt. | ✅ Good |
| **File Upload Security** | Image validation via `image.Decode`, extension canonicalization, ZIP traversal blocking, ZIP bomb limits, executable blocklist | ✅ Strong |
| **Game File Serving** | Regex-validated game IDs, path-prefix containment, strict Content-Type mapping, `nosniff`, CSP `frame-ancestors` | ✅ Strong |
| **Rate Limiting** | Per-IP sliding window + per-account handler-level guards (`AllowByKey`) + PoW CAPTCHA | ✅ Good |
| **User Enumeration** | Generic errors on register/login/forgot-password; timing-equalized bcrypt | ✅ Strong |
| **Email Verification** | Hashed tokens, POST-based verification, 24h TTL, single-use, transactional consumption | ✅ Strong |
| **Password Reset** | Generic responses, short-lived tokens, invalidates all sessions + API keys on success | ✅ Strong |
| **Admin Required** | Rejects API key auth for admin endpoints; 404 (not 403) on failure | ✅ Good |
| **Trusted Proxies** | Rejects `0.0.0.0/0`; X-Forwarded-For walked from the right | ✅ Good |
| **Docker** | Multi-stage build, non-root user (`playmore` UID 1000), minimal `alpine:3.19` final image | ✅ Good |
| **No SSRF Surface** | Zero server-side HTTP clients fetching user-controlled URLs | ✅ Strong |
| **No Debug Exposure** | Defaults to Gin release mode; no pprof endpoints | ✅ Good |
| **TLS** | TLS 1.3 minimum; Auto-TLS with host whitelist | ✅ Good |
| **SMTP Security** | Mandatory STARTTLS, TLS 1.3, explicit plaintext fallback blocked | ✅ Strong |

---

## Prioritized Remediation Roadmap

### Immediate (Fix This Week)
1. **Enable SQLite foreign keys** — `&_fk=1` in DSN or `PRAGMA foreign_keys = ON`.
2. **Add migration tracking** — Create `schema_migrations` table; stop swallowing migration errors.
3. **Fix `GetCollection` unpublished game disclosure** — Filter unpublished games the viewer doesn't own.
4. **Sanitize `UpdateGame` fields** — Apply `SanitizePlain()` to `CustomAbout`, `SysReqMin`, `SysReqRec`, and `Features` elements.
5. **Validate `AvatarURL`/`BannerURL`** — Reject `javascript:` and non-HTTP(S) schemes.
6. **Sanitize `DisplayName`** — Apply `SanitizePlain()` in `UpdateDeveloperPage`.
7. **Fix `IsSecure()` header spoofing** — Gate `X-Forwarded-Proto` on `hasTrustedProxy`.

### Short-Term (Fix This Month)
8. Add rate limiting to all authenticated mutating endpoints.
9. Add a global per-IP rate limit (e.g., 200 req/min).
10. Add maximum length validation to all free-text inputs.
11. Validate `price >= 0` and `discount` in `[0, 100]`.
12. Filter unpublished games from the activity feed.
13. Verify game existence in `TrackView` before inserting.
14. Cap collection size (e.g., 100 games).
15. Fix `ChangePassword` session invalidation (hash token before comparison).
16. Fix session expiry timezone bug (use UTC).
17. Fix non-atomic backup script (remove separate WAL/SHM copy).
18. Sanitize error responses in `uploads.go` and `auth.go`.
19. Fix `ResetPassword` transaction boundary (use `tx` for password update).

### Hardening (Ongoing)
20. Refactor `AdminDeleteUser` to avoid dynamic SQL strings.
21. Split user query structs to avoid loading password hashes for public lookups.
22. Migrate frontend from inline `onclick` to `addEventListener` to tighten CSP.
23. Restrict data directory and DB file permissions (`0750`/`0640`).
24. Add `Cross-Origin-Opener-Policy` and `Cross-Origin-Resource-Policy` headers.
25. Consider masking logged email addresses.
26. Add request timeouts to the frontend `api()` fetch helper.
27. Add a `role` column to `users` instead of implicit first-user admin.
28. Validate extensions early in chunked upload initialization.
29. Handle `rand.Read` and DB write errors explicitly in email flows.
30. Add a `HEALTHCHECK` to the Dockerfile.

---

*Report generated by parallel agent swarm analysis of the PlayMore codebase.*
