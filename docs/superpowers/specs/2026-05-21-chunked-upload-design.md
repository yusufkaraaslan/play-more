# Chunked Upload Pipeline — Design

**Status:** Design approved, ready for implementation plan
**Date:** 2026-05-21
**Author:** Yusuf Karaaslan (via brainstorming session)

## Problem

The single multipart `POST /api/games` and `POST /api/games/:id/reupload` endpoints require the entire game ZIP in one HTTP request body. Cloudflare Free/Pro plans cap request bodies at 100 MiB, blocking the most common deployment topology (playmore behind Cloudflare). Server itself accepts up to 500 MiB (`storage.MaxFileSize`); the bottleneck is purely the reverse proxy in front.

Any indie dev shipping a game with embedded audio (~130 MiB of OGG tracks is typical) hits this immediately. Workarounds — strip audio, raise CF tier ($200/mo Business), DNS-grey-cloud the upload path — are all unsatisfying.

## Goal

Add a chunked upload protocol so games up to playmore's existing 500 MiB cap can be uploaded behind any reverse-proxy body limit ≥ 9 MiB, without losing the "single binary, no CGO, no object storage, minimal deps, self-hosted" philosophy. Keep the existing single-shot endpoints working for small uploads.

## Non-goals

- Increasing the 500 MiB total cap.
- Adding object-storage backends (S3, R2, MinIO).
- Implementing the full tus.io protocol.
- Per-chunk SHA verification — TLS in transit + whole-file SHA at finalize is sufficient.
- Parallel chunk upload from a single client — sequential is simpler and the bottleneck is upstream bandwidth anyway.

## High-level approach

**Approach 1 — upload-first, single sparse file.** Client `init`s an upload session (metadata bundled in), `PUT`s chunks at byte offsets into a pre-allocated sparse file via `os.File.WriteAt`, then `finalize`s. No game record exists until finalize succeeds (atomic, no orphan game rows). The partial file *is* the final file — no concat step.

Rejected alternatives:
- **Approach 2** (create game first, chunks scoped to game): extra round-trip; orphan-game state needs GC.
- **Approach 3** (tus.io subset): interop you don't need at the cost of a bigger surface area and odd HTTP semantics for a bash CLI.

## Protocol

### Endpoints

| Method   | Path                                            | Body                                                                                                | Returns                                                       |
| -------- | ----------------------------------------------- | --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `POST`   | `/api/uploads/init`                             | JSON `{filename, size, kind:"new_game"\|"reupload", game_id?, metadata?}`                            | `{upload_id, chunk_size: 8388608, expires_at}`                |
| `PUT`    | `/api/uploads/:upload_id/chunks?offset=<bytes>` | raw bytes, `Content-Type: application/octet-stream`, body ≤ `chunk_size + 1 MiB`                    | `{received_bytes}` — highest contiguous-from-zero byte count (fine for happy-path progress; for full range state during resume, call `GET /api/uploads/:upload_id`) |
| `GET`    | `/api/uploads/:upload_id`                       | —                                                                                                   | `{size, received_ranges:[[start,end),…], expires_at, status}` |
| `POST`   | `/api/uploads/:upload_id/finalize`              | JSON `{sha256?}` (optional; CLI sends, SPA omits)                                                   | `{game_id}` for `new_game`, `204` for `reupload`              |
| `DELETE` | `/api/uploads/:upload_id`                       | —                                                                                                   | `204` (partial file + row deleted)                            |

**Per-`kind` field requirements on `init`:**
- `kind:"new_game"` — `metadata` **required** (`{title, genre, description?, tags?:[…], is_webgpu?:bool}`, validated server-side same as the existing `POST /api/games` form fields); `game_id` must be absent (400 if present).
- `kind:"reupload"` — `game_id` **required** (must exist + be owned by the caller, else 404); `metadata` must be absent (400 if present).

### Happy-path flow (new game, 130 MiB ZIP)

1. Client `POST /api/uploads/init` with `{filename:"game.zip", size:136314880, kind:"new_game", metadata:{…}}` → gets `upload_id` + `chunk_size: 8 MiB`.
2. Client splits into 17 chunks (16 × 8 MiB + 1 × ~5.7 MiB), `PUT`s each sequentially with `?offset=` matching the file position.
3. Server writes each chunk via `f.WriteAt(buf, offset)` on a pre-allocated sparse file at `{dataDir}/uploads/.partial/{upload_id}.bin`, then coalesces the range `[offset, offset+len)` into the `received_ranges` JSON column under the session mutex.
4. Client `POST .../finalize {sha256}`. Server: (a) verifies `received_ranges == [[0, size)]` (one contiguous range), (b) hashes the partial file via streaming `sha256.New()`, compares to `sha256` if provided (400 on mismatch), (c) runs existing `ExtractZipFromReader` directly on the partial file (the partial *is* the temp file the existing code expects), (d) creates the `games` row from `metadata`, returns `game_id`, (e) deletes session row + partial file.

### Resume flow (chunk dropped)

1. Client `GET /api/uploads/:upload_id` → sees `received_ranges:[[0, 41943040), [50331648, …)]` — gap at `[41943040, 50331648)`.
2. Client computes missing range(s), re-PUTs just those bytes. `WriteAt` is idempotent for the same byte content at the same offset; a fully-redundant re-send is a no-op for the file and a coalesce-no-op for `received_ranges`.
3. Continues to finalize once gaps are filled.

### Why offset-based, not chunk-index

- `os.File.WriteAt` makes the partial file *be* the final file — no concat step.
- Out-of-order writes are natural (just coalesce ranges) instead of needing a chunk-N-vs-chunk-M map.
- Final chunk size is implicit (whatever bytes land at the tail offset), not a special case in the protocol.

## Server design

### Database migration

Appended to the existing idempotent migrations slice in `internal/storage/db.go`:

```sql
CREATE TABLE IF NOT EXISTS upload_sessions (
    id              TEXT PRIMARY KEY,                                    -- upload_id (uuid)
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    game_id         TEXT REFERENCES games(id) ON DELETE CASCADE,         -- NULL for new_game
    kind            TEXT NOT NULL,                                       -- 'new_game' | 'reupload'
    filename        TEXT NOT NULL,
    size            INTEGER NOT NULL,                                    -- expected total bytes
    received_ranges TEXT NOT NULL DEFAULT '[]',                          -- JSON [[start,end),...]
    metadata_json   TEXT NOT NULL DEFAULT '{}',                          -- title/genre/desc/tags/is_webgpu
    sha256_expected TEXT NOT NULL DEFAULT '',                            -- set on finalize
    status          TEXT NOT NULL DEFAULT 'open',                        -- open|finalizing|done|failed
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at      DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_user    ON upload_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires ON upload_sessions(expires_at);
```

### New Go files

| File                                       | Responsibility                                                                                                                                          |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/models/upload_session.go`        | CRUD + range coalescing (`addRange`, `isComplete`, `missingRanges`). All range math here; pure functions are unit-testable even though playmore has no auto suite today. |
| `internal/storage/partial.go`              | `CreatePartial(id, size)` (sparse file via `Truncate`), `WritePartialAt(id, offset, r)`, `OpenPartial(id) (*os.File, error)`, `DeletePartial(id)`. Path: `{dataDir}/uploads/.partial/{upload_id}.bin`. |
| `internal/handlers/uploads_chunked.go`     | HTTP handlers — `InitUpload`, `PutChunk`, `GetUploadStatus`, `FinalizeUpload`, `CancelUpload`. Thin; delegates to models + storage.                     |
| `internal/storage/partial_gc.go`           | `StartPartialGC(ctx)` goroutine. Every 10 min: delete sessions where `expires_at < now AND status='open'`, then delete matching partial files. Also sweeps orphan partial files on disk (no matching row). |

### Modifications to existing files

- **`internal/server/server.go`** — wire 5 new routes under `/api/uploads/...` with `AuthRequired()` + `RequireVerifiedEmail()` + rate-limit + per-route body cap. Chunk PUT cap = 9 MiB (chunk_size + 1 MiB headroom). Init/status/finalize/cancel = 1 MiB JSON cap.
- **`internal/middleware/csrf.go`** — extend content-type allowlist to permit `application/octet-stream` *only* on `PUT /api/uploads/:id/chunks` (Origin/Referer check intact; Bearer-auth bypasses CSRF as today).
- **`main.go`** — call `storage.StartPartialGC(ctx)` alongside `StartRateLimitCleanup` and `StartAnalyticsWriter`.
- **`internal/storage/db.go`** — append the migration block above to the migrations slice (3 lines: 1 CREATE TABLE + 2 CREATE INDEX).

### Security & limits

- **Auth**: `AuthRequired()` + `RequireVerifiedEmail()` on all 5 routes (matches existing upload routes).
- **Ownership**: every chunk/status/finalize/cancel verifies `session.user_id == GetUser(c).ID`. Returns 404 (not 403) on mismatch, per existing existence-hiding pattern.
- **Rate limits** (per-user when API-key authed, per-IP otherwise):
  - `init`: 20/hr
  - `PUT chunks`: 2000/hr
  - `status`: 600/hr
  - `finalize`: 20/hr
  - `cancel`: 60/hr
- **Size invariants** enforced on each PUT: `offset + len <= session.size`, `len <= chunk_size + headroom`, `session.size <= storage.MaxFileSize` (500 MiB) at init. Reject with 400/413; never silently resize.
- **`sha256_expected`** optional on finalize. If present, server streams the partial file through `sha256.New()` and rejects on mismatch. If absent, server skips hashing entirely.
- **Race protection on finalize**: `UPDATE upload_sessions SET status='finalizing' WHERE id=? AND status='open'`. Only the row that flipped wins; concurrent finalizes get 409.
- **Cleanup on failure**: any finalize error path → row marked `failed`, partial file deleted, no game record created.
- **`received_ranges` race**: per-session `sync.Mutex` keyed by `upload_id` (in-memory map) around the read-modify-write of the JSON column.

### Code touch summary

- 4 new files, ~600 LOC total estimate.
- 4 modified files: `server.go` (+~30 LOC), `csrf.go` (+~5 LOC), `main.go` (+1 LOC), `db.go` (+3 migration lines).
- No new Go dependencies — uses stdlib `crypto/sha256`, `encoding/json`, `os.File.WriteAt`.

## Client design

### Threshold

Both clients branch on file size:
- `size <= 64 MiB` → existing single-shot multipart `POST /api/games` or `.../reupload`, unchanged.
- `size > 64 MiB` → new chunked flow.

64 MiB sits comfortably under any plausible reverse-proxy body cap and keeps the small-file path simple.

### `sha256` is optional

Web Crypto's `crypto.subtle.digest` doesn't accept streams, and loading a 500 MiB file into an `ArrayBuffer` to hash it crashes mobile browsers. To avoid pulling in a streaming-SHA JS library (against playmore's vanilla-JS style):

- **CLI** always sends `sha256` (one-liner via `sha256sum` or `shasum -a 256`). Server streams the partial through `sha256.New()`, compares, rejects on mismatch.
- **SPA** omits it. Server skips hashing entirely in that case — no hash is computed or stored. SPA integrity relies on TLS in transit + the existing ZIP central-directory check (a truncated ZIP fails `ExtractZipFromReader` cleanly).

### Cover/screenshots go out-of-band for chunked uploads

The existing single-shot path bundles cover + screenshots in the same multipart as `game_file`. The chunked path doesn't. For a chunked new-game upload, after `finalize` returns `game_id`, the SPA does the existing flow: `POST /api/upload/image` for each → URL → `PUT /api/games/:id` with the URLs. Two extra small requests that already work today. The existing single-shot path keeps bundling them.

### Web SPA (`frontend/index.html`)

New helper added near existing upload code:

```js
async function uploadGameChunked(file, { kind, gameId, metadata }, onProgress) {
  const init = await api('/api/uploads/init', {
    method: 'POST',
    body: JSON.stringify({ filename: file.name, size: file.size, kind, game_id: gameId, metadata }),
  });
  const { upload_id, chunk_size } = init;

  for (let offset = 0; offset < file.size; offset += chunk_size) {
    const slice = file.slice(offset, Math.min(offset + chunk_size, file.size));
    await putChunkWithRetry(upload_id, offset, slice);   // retry-with-backoff
    onProgress((offset + slice.size) / file.size);
  }

  return api(`/api/uploads/${upload_id}/finalize`, {
    method: 'POST',
    body: JSON.stringify({}),                            // sha256 omitted from SPA
  });
}
```

Call sites:
- **New game** (`index.html:1275-1290`): if `pendingFile.size > 64 << 20`, call `uploadGameChunked(..., { kind:'new_game', metadata:{title,genre,description,tags,is_webgpu} })`, then on the returned `game_id` do the existing cover/screenshot upload flow. Else: existing FormData path.
- **Reupload** (`index.html:2620`): same threshold check, call `uploadGameChunked(..., { kind:'reupload', gameId })`.

**Progress UI**: existing upload modal gains a percent bar driven by `onProgress`.

**Resume on retry**: if a chunk PUT throws, helper shows a "Retry upload?" button. Retry calls `GET /api/uploads/:upload_id`, parses `received_ranges`, sends only the missing bytes, then finalizes. `upload_id` stashed in `sessionStorage` survives a page reload.

### Deploy CLI (`internal/handlers/playmore-deploy.sh`)

`cmd_push` branches:

```bash
local size sha
size=$(stat -c%s "$file" 2>/dev/null || stat -f%z "$file")    # GNU/BSD compat
if [[ $size -gt 67108864 ]]; then                             # >64 MiB → chunked
    if command -v sha256sum >/dev/null; then
        sha=$(sha256sum "$file" | awk '{print $1}')
    else
        sha=$(shasum -a 256 "$file" | awk '{print $1}')
    fi
    chunked_push "$file" "$size" "$sha"
else
    # existing single-shot path, unchanged
fi
```

`chunked_push` flow:

1. `api_call POST /api/uploads/init` with JSON `{filename, size, kind, game_id?, metadata?}` → parse `upload_id`, `chunk_size`.
2. Loop `for ((offset=0; offset < size; offset += chunk_size))`:
   ```bash
   dd if="$file" bs="$chunk_size" skip=$((offset/chunk_size)) count=1 status=none 2>/dev/null \
     | curl --fail -s -X PUT --data-binary @- \
            -H "Authorization: Bearer $API_KEY" \
            -H "Content-Type: application/octet-stream" \
            "${SERVER}/api/uploads/${upload_id}/chunks?offset=${offset}"
   ```
   With `printf "[%2d/%2d] %s\n"` progress.
3. On PUT failure: 3 retries with backoff (1s, 2s, 4s). If still failing, exit non-zero. Re-running `playmore-deploy push` notices the stashed `UPLOAD_ID` in `.playmore`, calls `GET /api/uploads/$UPLOAD_ID`, computes missing ranges, resumes.
4. `api_call POST /api/uploads/$upload_id/finalize` with `{"sha256":"$sha"}` → parse `game_id`, save to `.playmore`.

CLI deps remain `bash + curl + zip + dd + (sha256sum | shasum)` — all POSIX-or-coreutils.

### Client code touch summary

- `frontend/index.html`: +~120 LOC (helper + branching + progress UI + retry UI). No new JS deps.
- `internal/handlers/playmore-deploy.sh`: +~80 LOC (`chunked_push` + size detection + sha helper + resume).

## Test plan

playmore has no automated test suite. The following 18 manual cases must pass before release:

| # | Case                                  | Steps                                                                                         | Expected                                                                                                  |
| - | ------------------------------------- | --------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| 1 | Threshold — small file                | Upload 5 MiB HTML via SPA                                                                     | Single-shot path. DevTools shows one request.                                                              |
| 2 | Threshold — large file                | Upload 130 MiB ZIP via SPA                                                                    | Chunked: 1 init + ~17 PUTs + 1 finalize. Progress bar advances. Game playable after.                       |
| 3 | CLI happy path                        | `playmore-deploy push --file game.zip` with 250 MiB file                                       | `[01/32] … [32/32]` progress, `Game uploaded! ID:` shown. `sha256` sent on finalize.                       |
| 4 | Reupload chunked (SPA)                | Existing game, drag-drop new 150 MiB ZIP                                                       | `kind:"reupload"`, no new game row, files replaced atomically.                                             |
| 5 | Resume — SPA tab close                | Start 200 MiB upload, close tab at ~50%, reopen, click "Resume"                                | `GET /api/uploads/:id` returns partial `received_ranges`; only missing bytes sent.                          |
| 6 | Resume — CLI Ctrl-C                   | Ctrl-C mid-upload, re-run `playmore-deploy push`                                               | CLI reads `UPLOAD_ID` from `.playmore`, calls status, resumes from missing offset.                          |
| 7 | SHA mismatch                          | CLI: pass wrong `--sha256` or modify file between hash + upload                                | Finalize returns 400 `sha256 mismatch`. Session `failed`. Partial file deleted.                            |
| 8 | Concurrent finalize                   | Two terminals POST finalize same `upload_id`                                                   | One returns `{game_id}`, the other 409. One `games` row created.                                            |
| 9 | Body cap                              | `curl PUT --data-binary @big.bin …/chunks?offset=0` where big=20 MiB                          | 413.                                                                                                       |
| 10 | Offset overrun                        | PUT with `offset=499000000, total=500MiB`, body=10 MiB                                         | 400 `chunk exceeds declared size`. No write.                                                                |
| 11 | Cross-user attempt                    | User A inits; User B `PUT chunks` with same upload_id                                          | 404 (existence-hiding). Nothing written.                                                                    |
| 12 | Unverified email                      | Fresh signup, try to init                                                                      | 403 from `RequireVerifiedEmail()`.                                                                          |
| 13 | CSRF: octet-stream scoped             | `PUT /api/games/:id` (existing) with `Content-Type: application/octet-stream` from a browser   | Rejected by CSRF (octet-stream not allowed on this path).                                                   |
| 14 | GC sweep                              | Init, kill client, `UPDATE upload_sessions SET expires_at=datetime('now','-1 hour')`, wait 10m | Row gone, partial file gone, no orphan on disk.                                                             |
| 15 | Orphan file sweep                     | Drop file in `uploads/.partial/` with no matching row; wait for GC                            | File removed by orphan sweep.                                                                              |
| 16 | Migration on existing DB              | Build, run against a pre-feature `playmore.db`                                                 | `upload_sessions` table + indexes created idempotently. Restart is no-op.                                  |
| 17 | End-to-end through Cloudflare         | Deploy to a CF-fronted instance, push 130 MiB via CLI                                          | Succeeds. Previously died with CF error 413.                                                                |
| 18 | Cover/screenshot out-of-band (SPA)    | Chunked new-game with cover + 3 screenshots                                                    | Finalize returns `game_id`; SPA uploads images via `/api/upload/image`, PUTs URLs. All visible on game page.|

## Rollout

- **Single binary release.** Migration auto-runs on startup (idempotent, fits existing `db.go:35-86` pattern).
- **Existing clients keep working.** Single-shot `POST /api/games` and `.../reupload` are untouched; old `playmore-deploy.sh` continues uploading small games.
- **Web SPA ships embedded** via `go:embed`; deploys atomically.
- **Deploy CLI** at `GET /deploy.sh` gets new version on next `curl …/deploy.sh -o playmore-deploy`. The size-threshold branch is purely client-side; the new script is forward-compatible with any server, and the old script keeps working against the new server.
- **Docs updates:**
  - `docs/DEVELOPER.md` — append "Chunked uploads" section with 5 endpoints + curl examples + resume flow.
  - `docs/SETUP.md` — soften/remove reverse-proxy body-cap warnings for game uploads.
  - `README.md` — bullet "uploads work behind Cloudflare Free/Pro".

## Acceptance criteria

- [ ] All 18 test cases pass on Linux + Chrome + Firefox.
- [ ] 130 MiB ZIP uploads end-to-end through a Cloudflare-fronted instance via both SPA and CLI.
- [ ] Resume works after SPA tab close *and* after CLI Ctrl-C.
- [ ] Small (<64 MiB) uploads still use the single-shot path — verified by network inspector showing one multipart POST.
- [ ] No regression on cover/screenshot upload (SPA new-game flow).

## Risks & mitigations

| Risk                                                | Mitigation                                                                                                                  |
| --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| Race on `received_ranges` JSON during parallel PUTs | Per-session `sync.Mutex` keyed by `upload_id` around the read-modify-write of the ranges column.                            |
| Server restart mid-upload                           | Session row + partial file are persisted. Client resumes via `GET status` on next attempt; no in-memory state to recover.   |
| Disk fills with abandoned partials                  | GC every 10 min on TTL=24h sessions + orphan-file sweep (files with no row deleted).                                        |
| `sha256` mismatch leaves orphan partial             | Finalize error path sets `status='failed'` and deletes partial file inside the same handler.                                |
| Migration fails on existing prod DB                 | Idempotent `CREATE … IF NOT EXISTS`; errors swallowed per existing convention; manual rerun is safe.                        |
| SPA loses `upload_id` (sessionStorage cleared)      | User loses resume for that session; restart required. Acceptable — partial gets GC'd after 24h.                             |

## Open decisions (locked)

- Coexist with single-shot, threshold 64 MiB.
- Full-resume model with `received_ranges` tracked in DB.
- GC interval 10 min, session TTL 24 h.
- `sha256` required from CLI, optional from SPA.
- Cover/screenshots out-of-band for chunked uploads.
- Approach 1 (sparse-file `WriteAt`, no concat step).
