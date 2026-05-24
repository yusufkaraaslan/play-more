#!/usr/bin/env bash
# Dev helper: end-to-end verification of the chunked upload pipeline.
#
# Builds playmore into a temp dir, starts it on a free-ish port, provisions a
# verified test user via direct SQL (no SMTP needed), then exercises:
#
#   - Fix-3: GET /api/uploads/:id includes chunk_size in the response.
#   - Fix-2: PUT chunk returns 404 (not 200) when the session row was deleted
#            between handler lookup and the range update (GC vs PUT race).
#   - Fix-1: POST /api/games/:id/cover endpoint sets cover_path and serves the
#            image at /play/<id>/cover.<ext>.
#   - End-to-end: 70 MiB chunked upload (9 PUTs at 8 MiB each), finalize with
#            sha256, ZIP extraction, then cover upload on the chunked-created
#            game.
#
# Each run is self-contained: builds a fresh binary, fresh data dir, fresh user,
# fresh fixtures. Cleans up on exit. Re-running is safe.
#
# Requires: go, sqlite3, python3 (with bcrypt), curl, dd, zip, sha256sum.
#
# Usage:
#   ./scripts/verify-chunked-upload.sh             # default port 18099
#   PORT=18100 ./scripts/verify-chunked-upload.sh

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-18099}"
WORK="$(mktemp -d -t playmore-verify-XXXXXX)"
SERVER="http://localhost:$PORT"
DATA_DIR="$WORK/data"
FIXTURES="$WORK/fixtures"
BIN="$WORK/playmore-bin"
LOG="$WORK/playmore.log"
COOKIES="$WORK/cookies.txt"
PID=""

cleanup() {
    [[ -n "$PID" ]] && kill "$PID" 2>/dev/null || true
    rm -rf "$WORK"
}
trap cleanup EXIT

pass() { printf '  \033[32m\xe2\x9c\x93\033[0m %s\n' "$*"; }
fail() { printf '  \033[31m\xe2\x9c\x97\033[0m %s\n' "$*"; exit 1; }
section() { printf '\n%s\n%s\n' "================================================" "$*"; printf '%s\n' "================================================"; }

cd "$REPO_DIR"

section "Setup"
echo "  work dir:  $WORK"
echo "  port:      $PORT"
mkdir -p "$DATA_DIR" "$FIXTURES"

echo "  building playmore..."
go build -o "$BIN" .

echo "  starting server..."
"$BIN" --port "$PORT" --data "$DATA_DIR" > "$LOG" 2>&1 &
PID=$!
sleep 2
curl -s -o /dev/null -w "" --max-time 3 "$SERVER/" || fail "server didn't come up — see $LOG"
pass "server up at $SERVER (pid=$PID)"

echo "  provisioning verified test user (testuser / testpass123)..."
HASH=$(python3 -c "import bcrypt; print(bcrypt.hashpw(b'testpass123', bcrypt.gensalt(12)).decode())")
USER_ID=$(python3 -c "import uuid; print(uuid.uuid4())")
sqlite3 "$DATA_DIR/playmore.db" \
    "INSERT INTO users (id, username, email, password, email_verified) VALUES ('$USER_ID', 'testuser', 'test@test.local', '$HASH', 1)"
LOGIN=$(curl -s -c "$COOKIES" -X POST \
    -H "Origin: $SERVER" -H "Content-Type: application/json" \
    -d '{"email":"test@test.local","password":"testpass123"}' \
    "$SERVER/api/auth/login")
echo "$LOGIN" | grep -q '"username":"testuser"' || fail "login failed: $LOGIN"
pass "logged in (session cookie captured)"

echo "  building fixtures..."
cat > "$FIXTURES/smallgame.html" <<'EOF'
<!DOCTYPE html><html><body><h1>Small Game</h1></body></html>
EOF
mkdir "$FIXTURES/biggame"
cat > "$FIXTURES/biggame/index.html" <<'EOF'
<!DOCTYPE html><html><body><h1>Big Game</h1></body></html>
EOF
# 70 MiB random payload — .dat (not .bin which storage/files.go blocks)
head -c $((70*1024*1024)) /dev/urandom > "$FIXTURES/biggame/payload.dat"
(cd "$FIXTURES/biggame" && zip -q -0 -r "$FIXTURES/biggame.zip" .)
# Real 1x1 PNG (not a hand-typed byte blob — server validates by decoding)
python3 - <<PY
import struct, zlib
def chunk(typ, data):
    return struct.pack('>I', len(data)) + typ + data + struct.pack('>I', zlib.crc32(typ + data))
sig = b'\x89PNG\r\n\x1a\n'
ihdr = chunk(b'IHDR', struct.pack('>IIBBBBB', 1, 1, 8, 6, 0, 0, 0))
idat = chunk(b'IDAT', zlib.compress(b'\x00\xff\x00\x00\xff'))
iend = chunk(b'IEND', b'')
open('$FIXTURES/cover.png','wb').write(sig+ihdr+idat+iend)
PY
pass "fixtures ready ($FIXTURES)"

api()    { curl -s -b "$COOKIES" -H "Origin: $SERVER" -X "$1" "${@:3}" "$SERVER$2"; }
jq_get() { python3 -c "import sys,json;d=json.load(sys.stdin);print(d.get('$1','MISSING'))"; }

section "Fix-3: GET /api/uploads/:id includes chunk_size"
INIT=$(api POST /api/uploads/init -H "Content-Type: application/json" \
    -d '{"filename":"x.zip","size":100,"kind":"new_game","metadata":{"title":"Fix3","genre":"action"}}')
UID3=$(echo "$INIT" | jq_get upload_id)
[[ "$UID3" == "MISSING" ]] && fail "init returned: $INIT"
echo "  upload_id=$UID3"
STATUS=$(api GET "/api/uploads/$UID3")
echo "  status=$STATUS"
CS=$(echo "$STATUS" | jq_get chunk_size)
[[ "$CS" == "8388608" ]] && pass "chunk_size present: $CS" || fail "chunk_size=$CS (expected 8388608)"
api DELETE "/api/uploads/$UID3" > /dev/null

section "Fix-2: PUT chunk returns 404 when row deleted mid-write"
INIT=$(api POST /api/uploads/init -H "Content-Type: application/json" \
    -d '{"filename":"x.zip","size":100,"kind":"new_game","metadata":{"title":"Fix2","genre":"action"}}')
UID2=$(echo "$INIT" | jq_get upload_id)
echo "  upload_id=$UID2"
sqlite3 "$DATA_DIR/playmore.db" "DELETE FROM upload_sessions WHERE id='$UID2'"
echo "  → deleted session row via SQL (simulating GC race)"
RESP=$(curl -s -b "$COOKIES" -H "Origin: $SERVER" \
    -H "Content-Type: application/octet-stream" --data-binary "X" \
    -w "\nHTTP:%{http_code}" -X PUT "$SERVER/api/uploads/$UID2/chunks?offset=0")
echo "  put-chunk response: $RESP"
echo "$RESP" | grep -q "HTTP:404" && pass "got 404 on PUT to vanished session" || fail "expected 404"

section "Fix-1: POST /api/games/:id/cover endpoint"
GAME=$(curl -s -b "$COOKIES" -H "Origin: $SERVER" \
    -F "game_file=@$FIXTURES/smallgame.html" -F "title=CoverTest" -F "genre=action" \
    "$SERVER/api/games")
GID=$(echo "$GAME" | python3 -c "import sys,json;d=json.load(sys.stdin);g=d.get('game') or d;print(g.get('id','MISSING'))")
[[ "$GID" == "MISSING" ]] && fail "game create: $GAME"
echo "  game_id=$GID"
COVER=$(curl -s -b "$COOKIES" -H "Origin: $SERVER" \
    -F "image=@$FIXTURES/cover.png" "$SERVER/api/games/$GID/cover")
echo "  cover response: $COVER"
CP=$(echo "$COVER" | jq_get cover_path)
[[ "$CP" == "/play/$GID/cover.png" ]] && pass "cover_path returned: $CP" || fail "cover_path=$CP"
GAME_AFTER=$(api GET "/api/games/$GID")
COVER_PERSISTED=$(echo "$GAME_AFTER" | python3 -c "
import sys,json
d=json.load(sys.stdin); g=d.get('game') or d
print(g.get('cover_path','MISSING'))
")
[[ "$COVER_PERSISTED" == "/play/$GID/cover.png" ]] && pass "cover_path persisted on games row" || fail "cover_path on row=$COVER_PERSISTED"
COVER_HTTP=$(curl -sL -o /dev/null -w "%{http_code}" "$SERVER/play/$GID/cover.png")
[[ "$COVER_HTTP" == "200" ]] && pass "cover file served: HTTP $COVER_HTTP" || fail "cover serve HTTP=$COVER_HTTP"

section "End-to-end: 70 MiB chunked upload → cover"
SIZE=$(stat -c%s "$FIXTURES/biggame.zip")
SHA=$(sha256sum "$FIXTURES/biggame.zip" | awk '{print $1}')
echo "  size=$SIZE bytes  sha=$SHA"
INIT=$(api POST /api/uploads/init -H "Content-Type: application/json" \
    -d "{\"filename\":\"biggame.zip\",\"size\":$SIZE,\"kind\":\"new_game\",\"metadata\":{\"title\":\"ChunkedBig\",\"genre\":\"adventure\",\"description\":\"e2e\",\"tags\":[\"test\"],\"is_webgpu\":false}}")
BID=$(echo "$INIT" | jq_get upload_id)
[[ "$BID" == "MISSING" ]] && fail "init: $INIT"
echo "  upload_id=$BID"
CHUNK=8388608
OFFSET=0
N=0
while [[ $OFFSET -lt $SIZE ]]; do
    N=$((N+1))
    REM=$((SIZE - OFFSET))
    if [[ $REM -ge $CHUNK ]]; then
        BYTES=$CHUNK
        dd if="$FIXTURES/biggame.zip" bs=$CHUNK skip=$((OFFSET / CHUNK)) count=1 status=none 2>/dev/null \
            | curl -s --fail -b "$COOKIES" -H "Origin: $SERVER" \
                -H "Content-Type: application/octet-stream" --data-binary @- \
                -X PUT "$SERVER/api/uploads/$BID/chunks?offset=$OFFSET" > /dev/null \
            || fail "chunk $N failed at offset $OFFSET"
    else
        BYTES=$REM
        dd if="$FIXTURES/biggame.zip" bs=1 skip=$OFFSET count=$REM status=none 2>/dev/null \
            | curl -s --fail -b "$COOKIES" -H "Origin: $SERVER" \
                -H "Content-Type: application/octet-stream" --data-binary @- \
                -X PUT "$SERVER/api/uploads/$BID/chunks?offset=$OFFSET" > /dev/null \
            || fail "tail chunk failed at offset $OFFSET"
    fi
    OFFSET=$((OFFSET + BYTES))
    printf "  chunk %2d at offset %d (%d bytes)\n" $N $((OFFSET - BYTES)) $BYTES
done
echo "  all $N chunks sent"
FIN=$(api POST "/api/uploads/$BID/finalize" -H "Content-Type: application/json" -d "{\"sha256\":\"$SHA\"}")
echo "  finalize: $FIN"
BIG_GID=$(echo "$FIN" | jq_get game_id)
[[ "$BIG_GID" == "MISSING" ]] && fail "finalize: $FIN"
pass "chunked game created: $BIG_GID"
COVER=$(curl -s -b "$COOKIES" -H "Origin: $SERVER" \
    -F "image=@$FIXTURES/cover.png" "$SERVER/api/games/$BIG_GID/cover")
CP=$(echo "$COVER" | jq_get cover_path)
[[ "$CP" == "/play/$BIG_GID/cover.png" ]] && pass "chunked-game cover set: $CP" || fail "cover_path=$CP"
ENTRY=$(curl -sL -o /dev/null -w "%{http_code}" "$SERVER/play/$BIG_GID/index.html")
[[ "$ENTRY" == "200" ]] && pass "chunked-game index.html served: HTTP $ENTRY" || fail "index HTTP=$ENTRY"
PAY=$(curl -sL -o /dev/null -w "%{http_code}" "$SERVER/play/$BIG_GID/payload.dat")
[[ "$PAY" == "200" ]] && pass "chunked-game payload.dat served: HTTP $PAY" || fail "payload HTTP=$PAY"

printf '\n\033[32m================================================\n'
printf   '\xe2\x9c\x93 ALL VERIFICATIONS PASSED\n'
printf   '================================================\033[0m\n'
