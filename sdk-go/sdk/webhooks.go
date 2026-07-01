package sdk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
)

// VerifySignature is the canonical webhook signature check.
// Returns nil if the signature is valid, an error otherwise.
//
// The signature is computed as HMAC-SHA256(secret, body),
// formatted as "sha256=<hex>". Use this in your server-side
// webhook handler to verify the request really came from
// PlayMore.
//
// The function uses hmac.Equal for constant-time comparison,
// so it's safe against timing side-channels.
func VerifySignature(secret string, body []byte, signatureHeader string) error {
	if secret == "" {
		return errors.New("verify: empty secret")
	}
	if signatureHeader == "" {
		return errors.New("verify: missing signature header")
	}
	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return errors.New("verify: signature must start with 'sha256='")
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, prefix))
	if err != nil {
		return errors.New("verify: signature is not valid hex")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return errors.New("verify: signature mismatch")
	}
	return nil
}

// VerifySignatureFromRequest is a convenience wrapper that
// reads the body off an http.Request and pulls the signature
// off the X-PlayMore-Signature header. Useful as a one-liner
// in a webhook handler:
//
//	if err := playmore.VerifySignatureFromRequest(secret, r); err != nil {
//	    http.Error(w, err.Error(), 400); return
//	}
func VerifySignatureFromRequest(secret string, r *http.Request) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return VerifySignature(secret, body, r.Header.Get("X-PlayMore-Signature"))
}

// SignBody is the inverse of VerifySignature: it produces the
// signature header value the server would send. Exposed so
// tests and replay-protection code can compute signatures
// without recreating the HMAC dance.
func SignBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
