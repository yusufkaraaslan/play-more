package turnserver

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // mandated by the TURN REST API spec; see note below
	"crypto/subtle"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// Ephemeral TURN credentials, per the "REST API For Access To TURN
// Services" scheme that coturn implements as `use-auth-secret`:
//
//	username   = <unix-expiry>:<user-id>
//	credential = base64(HMAC-SHA1(shared-secret, username))
//
// The point is that the server never stores per-user TURN passwords.
// Both sides derive the same credential from one shared secret, so
// /rtc-config can mint a short-lived credential for a logged-in user
// and the TURN server can verify it statelessly — no coordination, no
// credential table, nothing to leak at rest.
//
// HMAC-SHA1 is not a lapse: the scheme fixes it, and staying on it is
// what keeps these credentials interchangeable with a real coturn
// deployment (`static-auth-secret`) for operators who'd rather run
// their own. SHA-1's collision weaknesses don't carry into HMAC-SHA1,
// which has no practical forgery attack — but do not copy this
// construction anywhere else in the codebase.

// DefaultCredentialTTL is how long a minted credential stays valid.
// Long enough to cover joining a lobby and establishing the peer
// connection; short enough that a leaked credential is worthless
// almost immediately. The browser only needs it at ICE-gathering time.
const DefaultCredentialTTL = 10 * time.Minute

// Credentials is one minted TURN credential pair, shaped for direct
// inclusion in an RTCIceServer entry.
type Credentials struct {
	Username string
	Password string
	Expires  time.Time
}

// Mint derives a credential pair valid until now+ttl for the given
// user. userID is opaque to the TURN server — it exists so relay
// allocations can be attributed to an account in the TURN logs.
//
// Colons are stripped from userID because ':' is the field separator
// in the username; a userID containing one would let a caller forge
// an arbitrary expiry. PlayMore user IDs are UUIDs, so this never
// fires in practice — it's here so it can't start firing later.
func Mint(secret, userID string, ttl time.Duration, now time.Time) Credentials {
	if ttl <= 0 {
		ttl = DefaultCredentialTTL
	}
	expires := now.Add(ttl)
	username := strconv.FormatInt(expires.Unix(), 10) + ":" + strings.ReplaceAll(userID, ":", "")
	return Credentials{
		Username: username,
		Password: PasswordFor(secret, username),
		Expires:  expires,
	}
}

// PasswordFor derives the credential for a username. Exported so the
// TURN server's auth handler can recompute it on the verify side —
// the two sides must agree byte-for-byte.
func PasswordFor(secret, username string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// CheckUsername validates the expiry embedded in a TURN username and
// returns the user ID it was minted for.
//
// Note what this does NOT do: it does not authenticate. Anyone can
// present a well-formed username; only the HMAC proves possession of
// the shared secret, and pion verifies that itself by comparing the
// key we hand back. This is purely the expiry gate.
func CheckUsername(username string, now time.Time) (userID string, ok bool) {
	sep := strings.Index(username, ":")
	if sep <= 0 {
		return "", false
	}
	expiry, err := strconv.ParseInt(username[:sep], 10, 64)
	if err != nil {
		return "", false
	}
	if now.Unix() > expiry {
		return "", false
	}
	return username[sep+1:], true
}

// VerifyPassword compares a presented credential against the expected
// one in constant time. pion's long-term-credential path doesn't use
// this (it compares derived keys internally), but the chunked/manual
// verification paths and the tests do, and a plain == here would be a
// timing oracle on the HMAC.
func VerifyPassword(secret, username, presented string) bool {
	expected := PasswordFor(secret, username)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
}
