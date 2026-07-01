# PlayMore Developer Guide

## API Keys

API keys let you automate game uploads, updates, and devlog posts from the command line, CI/CD pipelines, or scripts — without storing your password.

### Creating an API key

1. Log in to PlayMore
2. Go to **Settings → API Keys**
3. Click **+ Generate Key**, give it a name (e.g. "CI Deploy", "My Laptop")
4. Copy the key immediately — it's shown only once

Keys look like: `pm_k_a1b2c3d4e5f6...` (69 characters total)

### Using API keys

Add the key as a Bearer token in the `Authorization` header:

```bash
curl -H "Authorization: Bearer pm_k_YOUR_KEY" https://playmore.example.com/api/auth/me
```

### API key permissions

API keys can:
- Upload, update, and delete your games
- Re-upload game files and manage screenshots
- Post and delete devlogs and comments
- Manage your library, wishlist, and collections
- Update your profile

API keys **cannot**:
- Access admin endpoints
- Delete your account
- Change your password
- Create or revoke other API keys

### Revoking keys

Go to **Settings → API Keys** and click **Revoke** next to the key. The key stops working immediately.

### Limits

- Maximum 10 API keys per account
- Rate limits apply the same as session auth
- Keys have no expiry — revoke them manually when no longer needed

---

## Deploy CLI

`playmore-deploy` is a standalone bash script for deploying games from the command line.

### Install

```bash
curl -fsSL https://YOUR_SERVER/deploy.sh -o playmore-deploy
chmod +x playmore-deploy
```

### Setup

```bash
./playmore-deploy init --server https://playmore.example.com --key pm_k_YOUR_KEY
```

Or run without flags for interactive prompts. Config is saved to `.playmore` in the current directory.

### Commands

#### `push` — Upload or re-upload game files

```bash
# First upload (creates the game)
./playmore-deploy push --file game.zip --title "My Game" --genre action --tags "2D, Pixel Art"

# Re-upload (updates existing game files)
./playmore-deploy push --file new-build.zip

# Auto-detect: looks for index.html or *.zip in current directory
./playmore-deploy push --title "My Game" --genre puzzle

# Upload a directory (auto-zips it)
./playmore-deploy push --file ./build/ --title "My Game" --genre action
```

Options:
- `--file PATH` — Game file (.html, .zip) or directory
- `--title TITLE` — Game title (required for first push)
- `--genre GENRE` — Genre: action, adventure, rpg, strategy, puzzle, racing, horror, experimental
- `--desc TEXT` — Description
- `--tags TAGS` — Comma-separated tags
- `--cover PATH` — Cover image file
- `--webgpu` — Mark as WebGPU game

#### `update` — Update game metadata

```bash
./playmore-deploy update --title "New Title" --desc "Updated description"
./playmore-deploy update --tags "3D, Multiplayer" --price 4.99
./playmore-deploy update --video "https://www.youtube.com/embed/VIDEO_ID"
```

#### `devlog` — Post a devlog entry

```bash
# Inline content
./playmore-deploy devlog --title "v1.2 Released" --content "Bug fixes and new features!"

# From a file
./playmore-deploy devlog --title "Update Notes" --file CHANGELOG.md

# From stdin
cat notes.md | ./playmore-deploy devlog --title "Release Notes"
```

#### `status` — Show current configuration

```bash
./playmore-deploy status
# PlayMore Deploy v1.0.0
#   Server:  https://playmore.example.com
#   Key:     pm_k_a1b2c3d4...
#   User:    myusername
#   Game:    My Game (abc-123-def)
#   URL:     https://playmore.example.com/#game/abc-123-def
```

### Config file

The `.playmore` file stores your configuration:

```
SERVER='https://playmore.example.com'
API_KEY='pm_k_your_key_here'
GAME_ID='abc-123-def'
```

- Project-local: `.playmore` in the current directory (checked first)
- Global fallback: `~/.config/playmore/config`

### Dependencies

- **Required:** `curl`
- **Optional:** `zip` (for auto-zipping directories), `jq` (for better JSON handling — falls back to sed/grep)

### CI/CD example

```yaml
# GitHub Actions
- name: Deploy to PlayMore
  run: |
    curl -fsSL https://playmore.example.com/deploy.sh -o playmore-deploy
    chmod +x playmore-deploy
    echo "SERVER='https://playmore.example.com'" > .playmore
    echo "API_KEY='${{ secrets.PLAYMORE_KEY }}'" >> .playmore
    echo "GAME_ID='${{ vars.PLAYMORE_GAME_ID }}'" >> .playmore
    ./playmore-deploy push --file ./dist/
```

---

## API Reference

Full interactive docs with try-it-out: `https://YOUR_SERVER/docs`

### Authentication

All endpoints accept two auth methods:
- **Session cookie** — set by `POST /api/v1/auth/login`
- **Bearer token** — `Authorization: Bearer pm_k_...`

### OpenAPI spec

The full machine-readable API contract is at `internal/handlers/openapi.yaml`
(also served live at `GET /openapi.yaml` and at `https://YOUR_SERVER/openapi.yaml`).
The interactive Swagger UI is at `https://YOUR_SERVER/docs`.

The spec is hand-written and kept in sync with the route table by a drift
test (`internal/server/routes_test.go::TestMountAPIRoutes_OpenAPIDrift`)
that runs on every build — adding a route without updating the YAML
fails CI.

### API version

All endpoints are mounted under `/api/v1/` (canonical). The unversioned
`/api/*` path is a permanent alias — both work identically and always
will. New integrations should use `/api/v1/`.

### Key endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/api/v1/auth/register` | - | Create account |
| POST | `/api/v1/auth/login` | - | Login (sets session cookie) |
| GET | `/api/v1/auth/me` | Yes | Get current user + stats |
| POST | `/api/v1/auth/forgot-password` | - | Request password reset email |
| POST | `/api/v1/auth/reset-password` | - | Reset password with token |
| GET | `/api/v1/auth/verify/:token` | - | Verify email address |
| POST | `/api/v1/auth/resend-verification` | Session | Resend verification email |

### Games

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/games` | - | List games (query: genre, search, sort, page, limit) |
| GET | `/api/v1/games/:id` | - | Get game detail (also accepts slug) |
| POST | `/api/v1/games` | Yes* | Upload new game (multipart: game_file, title, genre, ...) |
| PUT | `/api/v1/games/:id` | Yes | Update game (JSON: all fields) |
| DELETE | `/api/v1/games/:id` | Yes | Delete game |
| POST | `/api/v1/games/:id/reupload` | Yes | Replace game files (multipart: game_file) |
| PUT | `/api/v1/games/:id/visibility` | Yes | Publish/unpublish (JSON: {published: bool}) |
| POST | `/api/v1/games/:id/screenshots` | Yes | Add screenshots (multipart) |
| DELETE | `/api/v1/games/:id/screenshots/:index` | Yes | Remove screenshot |

*Requires verified email when SMTP is configured.

### Reviews, Library, Wishlist

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/games/:id/reviews` | - | List reviews |
| POST | `/api/v1/games/:id/reviews` | Yes* | Submit review |
| DELETE | `/api/v1/reviews/:id` | Yes | Delete your review |
| GET | `/api/v1/library` | Yes | Get library |
| POST | `/api/v1/library/:game_id` | Yes | Add to library |
| DELETE | `/api/v1/library/:game_id` | Yes | Remove from library |
| GET | `/api/v1/wishlist` | Yes | Get wishlist |
| POST | `/api/v1/wishlist/:game_id` | Yes | Add to wishlist |
| DELETE | `/api/v1/wishlist/:game_id` | Yes | Remove from wishlist |

### Collections / Lists

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/collections` | Yes | List your collections |
| GET | `/api/v1/collections/public` | - | Browse public lists |
| GET | `/api/v1/collections/:id` | - | Get collection detail + games |
| POST | `/api/v1/collections` | Yes | Create collection |
| PUT | `/api/v1/collections/:id` | Yes | Update name/description/visibility |
| DELETE | `/api/v1/collections/:id` | Yes | Delete collection |
| POST | `/api/v1/collections/:id/games` | Yes | Add game to collection |
| DELETE | `/api/v1/collections/:id/games/:game_id` | Yes | Remove from collection |

### API Keys

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/api-keys` | Session | List your API keys (masked) |
| POST | `/api/v1/api-keys` | Session | Generate new key (returns raw key once) |
| DELETE | `/api/v1/api-keys/:id` | Session | Revoke a key |

### Other

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/profile/:username` | - | Get user profile |
| PUT | `/api/v1/profile` | Yes | Update profile |
| GET | `/api/v1/developer/:username` | - | Get developer page |
| PUT | `/api/v1/developer` | Yes | Update developer page |
| GET | `/api/v1/feed` | Yes | Activity feed |
| POST | `/api/v1/games/:id/devlogs` | Yes* | Create devlog |
| GET | `/api/v1/notifications` | Yes | Get notifications |
| GET | `/avatar/:username` | - | Generated avatar image |
| GET | `/docs` | - | Interactive API docs |
| GET | `/deploy.sh` | - | Download deploy CLI script |

---

## Security

### API key storage
- Keys are hashed with SHA-256 before storage (never stored in plain text)
- Only the key prefix (`pm_k_` + 8 chars) is stored for identification
- The raw key is shown exactly once at creation time

### CSRF protection
- Browser requests: validated via Origin/Referer headers
- API key requests: CSRF is skipped (non-browser clients don't send Origin)
- Invalid Bearer tokens are rejected immediately (prevents CSRF bypass)

### Rate limiting
- Per-IP, per-endpoint limits
- Applied to auth, uploads, key creation, and sensitive endpoints
- Returns HTTP 429 when exceeded

### Email verification

A verified email is required for write actions (uploads, reviews,
devlogs, comments, API key creation, chunked uploads). This applies
**whether or not SMTP is configured**:

- **SMTP configured**: the user must click the verification link
  in the email they receive at registration.
- **SMTP not configured**: the user can register and log in, but
  every write action returns `403 Forbidden` with
  `{"email_verification":"required","smtp_required":true}` until
  the operator sets up SMTP. This is the safe default for a
  self-hosted single-user app — it prevents the out-of-the-box
  install from being an open spam vector.

The startup log shows `⚠ SMTP not configured` whenever the SMTP
gate is enforced, so operators see the implication immediately.
Configure SMTP via the `--smtp-*` CLI flags or `PLAYMORE_SMTP_*`
env vars to allow user content. See `docs/SETUP.md`.

## Chunked uploads

For files larger than 64 MiB (or behind a reverse proxy with a smaller body cap, like Cloudflare Free/Pro at 100 MiB), use the chunked upload pipeline instead of the single-shot `POST /api/games`.

### Endpoints

- `POST /api/v1/uploads/init` — create an upload session
- `PUT /api/v1/uploads/:upload_id/chunks?offset=N` — write bytes at a byte offset
- `GET /api/v1/uploads/:upload_id` — check progress / find missing bytes (for resume)
- `POST /api/v1/uploads/:upload_id/finalize` — assemble + extract + create or update the game
- `DELETE /api/v1/uploads/:upload_id` — cancel and clean up

(The unversioned `/api/*` paths are a permanent alias and work identically.)

### Full curl example (new game)

```bash
FILE=/path/to/game.zip
SIZE=$(stat -c%s "$FILE" 2>/dev/null || stat -f%z "$FILE")
SHA=$(sha256sum "$FILE" | awk '{print $1}')

# 1. Init
INIT=$(curl -s -X POST "$SERVER/api/v1/uploads/init" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d "{\"filename\":\"game.zip\",\"size\":$SIZE,\"kind\":\"new_game\",
       \"metadata\":{\"title\":\"My Game\",\"genre\":\"action\",
                    \"description\":\"Hi\",\"tags\":[\"foo\"],\"is_webgpu\":false}}")
UPLOAD_ID=$(echo "$INIT" | jq -r .upload_id)
CHUNK=$(echo "$INIT" | jq -r .chunk_size)

# 2. PUT chunks
OFFSET=0
while [ $OFFSET -lt $SIZE ]; do
    dd if="$FILE" bs="$CHUNK" skip=$((OFFSET/CHUNK)) count=1 status=none | \
      curl -s -X PUT --data-binary @- \
        -H "Authorization: Bearer $KEY" \
        -H "Content-Type: application/octet-stream" \
        "$SERVER/api/v1/uploads/$UPLOAD_ID/chunks?offset=$OFFSET"
    OFFSET=$((OFFSET + CHUNK))
done

# 3. Finalize
curl -s -X POST "$SERVER/api/v1/uploads/$UPLOAD_ID/finalize" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d "{\"sha256\":\"$SHA\"}"
# → {"game_id":"<uuid>"}
```

### Resume

If a PUT fails or the client disconnects:

```bash
STATUS=$(curl -s -H "Authorization: Bearer $KEY" "$SERVER/api/v1/uploads/$UPLOAD_ID")
# STATUS includes received_ranges; compute the gaps and re-PUT those bytes only.
```

### Limits

| Endpoint     | Rate limit (per user) | Body cap |
| ------------ | --------------------- | -------- |
| `init`       | 20/hr                 | 1 MiB    |
| `PUT chunks` | 2000/hr               | 9 MiB    |
| `GET status` | 600/hr                | n/a      |
| `finalize`   | 20/hr                 | 1 MiB    |
| `cancel`     | 60/hr                 | n/a      |

- `sha256` field on finalize is optional; if present, server verifies and rejects on mismatch.
- Upload sessions expire 24 h from creation; expired sessions and partial files are GC'd every 10 minutes.
- Max session size: 500 MiB (same as the existing single-shot limit).
- Below 64 MiB, prefer the existing single-shot `POST /api/games` for fewer round-trips.
