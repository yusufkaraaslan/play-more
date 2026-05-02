package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Proof-of-work CAPTCHA — privacy-preserving, no third-party dependency.
//
// Flow:
//  1. Client GETs /api/auth/captcha → receives {challenge, difficulty}
//  2. Client finds an integer nonce N such that the leading bits of
//     SHA-256(challenge + ":" + N) are all zero.
//  3. Client submits (challenge, nonce) along with registration.
//  4. Server verifies: hash matches difficulty, challenge is in the issued
//     set, and the challenge hasn't been redeemed yet (single-use).
//
// Difficulty 18 ≈ 1-3 seconds on a modern laptop CPU, ~10x more on phones.
// Verification on the server is O(1) — single hash + map check.

const (
	captchaDifficulty = 18                // bits of leading zeros required
	captchaTTL        = 10 * time.Minute  // challenge expires
	captchaMaxIssued  = 10000             // bound memory under flood
)

type captchaEntry struct {
	issuedAt time.Time
	used     bool
}

var (
	captchaMu      sync.Mutex
	captchaIssued  = map[string]*captchaEntry{}
	captchaCleaned time.Time
)

// IssueCaptcha — GET /api/auth/captcha
func IssueCaptcha(c *gin.Context) {
	challenge := newCaptchaChallenge()

	captchaMu.Lock()
	defer captchaMu.Unlock()

	// Lazy cleanup of expired challenges
	if time.Since(captchaCleaned) > 1*time.Minute {
		now := time.Now()
		for k, v := range captchaIssued {
			if now.Sub(v.issuedAt) > captchaTTL {
				delete(captchaIssued, k)
			}
		}
		captchaCleaned = now
	}

	// Bound memory — drop oldest if we somehow blow past the cap
	if len(captchaIssued) > captchaMaxIssued {
		// Just clear half — easier than tracking insertion order
		i := 0
		for k := range captchaIssued {
			delete(captchaIssued, k)
			i++
			if i > captchaMaxIssued/2 {
				break
			}
		}
	}

	captchaIssued[challenge] = &captchaEntry{issuedAt: time.Now()}

	c.JSON(http.StatusOK, gin.H{
		"challenge":  challenge,
		"difficulty": captchaDifficulty,
	})
}

// VerifyCaptcha checks (challenge, nonce) and burns the challenge so it
// cannot be reused. Returns nil on success, error otherwise.
func VerifyCaptcha(challenge, nonce string) error {
	if challenge == "" || nonce == "" {
		return errCaptchaMissing
	}
	if !verifyPoW(challenge, nonce, captchaDifficulty) {
		return errCaptchaInvalid
	}

	captchaMu.Lock()
	defer captchaMu.Unlock()

	e, ok := captchaIssued[challenge]
	if !ok {
		return errCaptchaUnknown
	}
	if e.used {
		return errCaptchaReused
	}
	if time.Since(e.issuedAt) > captchaTTL {
		delete(captchaIssued, challenge)
		return errCaptchaExpired
	}
	e.used = true
	// Keep the entry around (marked used) until cleanup so replays are caught
	return nil
}

func newCaptchaChallenge() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// verifyPoW checks that SHA-256(challenge + ":" + nonce) has at least
// `bits` leading zero bits.
func verifyPoW(challenge, nonce string, bits int) bool {
	h := sha256.Sum256([]byte(challenge + ":" + nonce))
	fullBytes := bits / 8
	for i := 0; i < fullBytes; i++ {
		if h[i] != 0 {
			return false
		}
	}
	if r := bits % 8; r > 0 {
		mask := byte(0xFF) << (8 - r)
		if h[fullBytes]&mask != 0 {
			return false
		}
	}
	return true
}

// Sentinel errors so handlers can distinguish reasons (used by Register).
var (
	errCaptchaMissing = &captchaError{"captcha required"}
	errCaptchaInvalid = &captchaError{"captcha solution invalid"}
	errCaptchaUnknown = &captchaError{"captcha challenge not recognized — request a new one"}
	errCaptchaReused  = &captchaError{"captcha already used — request a new one"}
	errCaptchaExpired = &captchaError{"captcha expired — request a new one"}
)

type captchaError struct{ msg string }

func (e *captchaError) Error() string { return e.msg }

func IsCaptchaError(err error) bool {
	_, ok := err.(*captchaError)
	return ok
}

// Trim trailing whitespace from solution candidates (clients sometimes send
// nonces with newlines)
func cleanCaptchaInput(s string) string { return strings.TrimSpace(s) }
